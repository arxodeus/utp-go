//go:build cgo

package netem

import (
	"context"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
	"github.com/zen-eth/utp-go/native/libutp"
)

// What a retransmission timeout with data in flight does to the window.
//
// This is the other half of libutp's timeout branch. The idle half -- decay by
// a third when nothing is outstanding -- is measured by
// TestLibutpIdleWindowDecay. This is what happens when something *is*:
//
//	} else {
//	    // our delay was so high that our congestion window
//	    // was shrunk below one packet, preventing us from
//	    // sending anything for one time-out period. Now, reset
//	    // the congestion window to fit one packet, to start over
//	    // again
//	    max_window = packet_size;
//	    slow_start = true;
//	}
//	                                        (utp_internal.cpp:1223-1228)
//
// One packet, and back into slow start. Against the idle branch's two thirds
// that is a difference of more than an order of magnitude, which is what makes
// it measurable from outside without reading anyone's window.
//
// Two measurements, doing different jobs, and it is worth being precise about
// which does which.
//
// Our side is read directly: the window and the slow-start flag after the
// timeout. That is the assertion with teeth -- forcing the idle branch instead
// leaves the window at 25569 bytes with slow start off, and both assertions
// fail.
//
// Both sides are also measured by one shared observable, at the receiver: how
// many bytes arrive in the quarter second after recovery starts. That is what
// makes this a comparison against the reference rather than against a reading
// of its source, and the two land within 1.5% of each other -- libutp 43560
// bytes against our 44233, where slow start from one packet doubling over the
// ~5.7 round trips in 250 ms predicts 43.4 KB.
//
// But it is a weak discriminator, and pretending otherwise would be the
// mistake this file is meant to avoid. Under the forced idle branch it read
// 33091 against libutp's 43560 -- *lower*, and well within any tolerance worth
// setting, because a 25 KB window outside slow start ramps more slowly over
// 250 ms than a one-packet window inside it. The agreement is evidence; the
// direct assertions are the test.
func TestTimeoutWithDataInFlightCollapsesWindow(t *testing.T) {
	const blackout = 1600 * time.Millisecond

	theirs := libutpRecoveryAfterBlackout(t, blackout)
	ours, ourCwnd, ourSlowStart, ourTimeouts := ourRecoveryAfterBlackout(t, blackout)

	t.Logf("bytes delivered in the 250ms after a %v blackout: libutp %d, ours %d",
		blackout, theirs, ours)
	t.Logf("our window immediately after the timeout: %d bytes, slow start %v, %d timeout(s)",
		ourCwnd, ourSlowStart, ourTimeouts)

	// Our side can be checked directly, and is the sharper assertion: libutp
	// sets max_window to one packet and slow_start to true, and both are
	// visible here.
	if ourTimeouts == 0 {
		t.Fatalf("no retransmission timeout fired during a %v blackout; this run did not reach "+
			"the state it measures", blackout)
	}
	if ourCwnd > 4000 {
		t.Errorf("a timeout with data in flight left our window at %d bytes; libutp sets it to "+
			"one packet (utp_internal.cpp:1223-1228)", ourCwnd)
	}
	if !ourSlowStart {
		t.Errorf("a timeout with data in flight did not re-enter slow start; libutp sets " +
			"slow_start = true in the same branch (utp_internal.cpp:1227)")
	}

	// And libutp has to behave the same way, or the assertions above are
	// pinning our behaviour to a reading of the source rather than to the
	// reference. 60 KB in 250 ms is well above any one-packet ramp and well
	// below what an intact window would deliver.
	if theirs > 60_000 {
		t.Errorf("libutp delivered %d bytes in the 250ms after the blackout, too many for a "+
			"sender restarting from one packet; the reading of utp_internal.cpp:1223-1228 "+
			"behind this test may be wrong", theirs)
	}
	// The two should land within a factor of three of each other. Loose on
	// purpose: this bound is a sanity check on the comparison, not the thing
	// that catches a wrong branch -- see the note above on why this observable
	// cannot do that job.
	if lo, hi := float64(theirs)/3, float64(theirs)*3; float64(ours) < lo || float64(ours) > hi {
		t.Errorf("after the same blackout libutp delivered %d bytes and we delivered %d; "+
			"more than a factor of three apart is a different recovery, not a different "+
			"schedule", theirs, ours)
	}
}

// libutpRecoveryAfterBlackout runs real libutp as the sender, blackholes the
// link mid-transfer for long enough to force a timeout with data in flight,
// and reports how many bytes reach the receiver in the 250 ms after the link
// returns.
func libutpRecoveryAfterBlackout(t *testing.T, blackout time.Duration) int {
	t.Helper()

	n := NewNetwork(8383)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	clean := Config{Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 512 * 1024}
	n.Connect(a, b, clean)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	drv, err := libutp.NewDriver(uint64(time.Now().UnixMicro()))
	if err != nil {
		t.Fatal(err)
	}
	drv.PushRandom(8300)

	stopPump := make(chan struct{})
	pumped := make(chan struct{})
	defer func() {
		close(stopPump)
		<-pumped
		drv.Close()
	}()

	inbox := make(chan []byte, 8192)
	go func() {
		buf := make([]byte, 65536)
		for {
			nb, _, err := a.ReadFrom(buf)
			if err != nil {
				return
			}
			p := make([]byte, nb)
			copy(p, buf[:nb])
			select {
			case inbox <- p:
			case <-ctx.Done():
				return
			}
		}
	}()

	recv := newReceivingEndpoint(t, ctx, b)
	defer recv.close()

	go func() {
		defer close(pumped)
		tick := time.NewTicker(200 * time.Microsecond)
		defer tick.Stop()
		readBuf := make([]byte, 64*1024)
		for {
			select {
			case <-stopPump:
				return
			case <-ctx.Done():
				return
			case pkt := <-inbox:
				drv.SetTime(uint64(time.Now().UnixMicro()))
				drv.Inject(pkt)
				drv.IssueAcks()
			case <-tick.C:
				drv.SetTime(uint64(time.Now().UnixMicro()))
				drv.CheckTimeouts()
			}
			for {
				if nr := drv.Read(readBuf); nr == 0 {
					break
				}
			}
			for _, pkt := range drv.Emitted() {
				if _, err := a.WriteTo(pkt, b.Addr()); err != nil {
					return
				}
			}
			drv.ClearEmitted()
		}
	}()

	if err := drv.Connect(); err != nil {
		t.Fatalf("libutp connect: %v", err)
	}
	payload := make([]byte, 8<<20)
	rand.New(rand.NewSource(29)).Read(payload)
	if _, err := drv.Write(payload); err != nil {
		t.Fatalf("libutp write: %v", err)
	}

	// Let the window grow, with the transfer still running.
	time.Sleep(1200 * time.Millisecond)
	if recv.count() == 0 {
		t.Fatal("libutp delivered nothing before the blackout")
	}

	return blackoutAndMeasure(t, n, clean, blackout, recv.count)
}

// ourRecoveryAfterBlackout does the same with this library as the sender, and
// additionally reports the window, the slow-start flag and the timeout count
// straight after the timeout, which can be read directly on this side.
func ourRecoveryAfterBlackout(t *testing.T, blackout time.Duration) (
	delivered int, cwnd uint32, slowStart bool, timeouts uint64) {
	t.Helper()

	n := NewNetwork(8484)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	clean := Config{Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 512 * 1024}
	n.Connect(a, b, clean)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	sockA := utp.WithSocket(ctx, a, quiet())
	defer sockA.Close()
	sockB := utp.WithSocket(ctx, b, quiet())
	defer sockB.Close()

	var (
		mu      sync.Mutex
		samples []utp.ConnectionMetrics
	)
	cfg := utp.NewConnectionConfig()
	cfg.MetricsInterval = 20 * time.Millisecond
	cfg.Metrics = func(m utp.ConnectionMetrics) {
		mu.Lock()
		samples = append(samples, m)
		mu.Unlock()
	}

	var received atomic.Int64
	go func() {
		stream, err := sockB.Accept(ctx, utp.NewConnectionConfig())
		if err != nil {
			return
		}
		defer stream.Close()
		buf := make([]byte, 64*1024)
		for ctx.Err() == nil {
			nb, err := stream.Read(ctx, buf)
			if err != nil {
				return
			}
			received.Add(int64(nb))
		}
	}()

	time.Sleep(100 * time.Millisecond)
	cid := utp.NewConnectionId(b.Addr(), 9800, 9801)
	stream, err := sockA.ConnectWithCid(ctx, cid, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer stream.Close()

	payload := make([]byte, 8<<20)
	rand.New(rand.NewSource(31)).Read(payload)
	go func() { _, _ = stream.Write(ctx, payload) }()

	time.Sleep(1200 * time.Millisecond)
	if received.Load() == 0 {
		t.Fatal("nothing was delivered before the blackout")
	}
	mu.Lock()
	beforeTimeouts := lastSample(samples).Timeouts
	mu.Unlock()

	delivered = blackoutAndMeasure(t, n, clean, blackout, func() int { return int(received.Load()) })

	// The window straight after the timeout: the first sample taken once the
	// blackout ended, before the ramp has had time to move it.
	mu.Lock()
	defer mu.Unlock()
	final := lastSample(samples)
	lowest := final.CwndBytes
	sawSlowStart := final.SlowStart
	for _, m := range samples {
		if m.Timeouts > beforeTimeouts && m.CwndBytes < lowest {
			lowest = m.CwndBytes
			sawSlowStart = m.SlowStart
		}
	}
	return delivered, lowest, sawSlowStart, final.Timeouts - beforeTimeouts
}

// blackoutAndMeasure drops everything on the a->b link for the given duration,
// then reports how many further bytes the receiver takes in the next 250 ms.
func blackoutAndMeasure(t *testing.T, n *Network, clean Config,
	blackout time.Duration, count func() int) int {
	t.Helper()

	dark := clean
	dark.LossRate = 1
	if err := n.SetConfig("a", "b", dark); err != nil {
		t.Fatal(err)
	}
	time.Sleep(blackout)

	if err := n.SetConfig("a", "b", clean); err != nil {
		t.Fatal(err)
	}

	// Measure from when recovery actually starts, not from when the link
	// returns. Those are not the same instant: the retransmission timeout
	// doubles on each expiry, so after a 1.6s blackout the next attempt is not
	// due for another second and a half. Measuring the 250ms after the link
	// came back read zero bytes for both implementations -- a comparison of
	// two zeroes, which passes and says nothing.
	deadline := time.Now().Add(20 * time.Second)
	start := count()
	for count() == start {
		if time.Now().After(deadline) {
			t.Fatalf("nothing was delivered in the 20s after the link returned; the sender " +
				"never resumed, so there is no recovery to measure")
		}
		time.Sleep(5 * time.Millisecond)
	}

	before := count()
	time.Sleep(250 * time.Millisecond)
	return count() - before
}
