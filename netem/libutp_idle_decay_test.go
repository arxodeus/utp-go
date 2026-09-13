//go:build cgo

package netem

import (
	"context"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
	"github.com/zen-eth/utp-go/native/libutp"
)

// What real libutp does to its congestion window while a connection sits idle.
//
// libutp checks one retransmission deadline per socket rather than one timer
// per packet, and `rto_timeout` is never cleared when the window empties: it
// is set when a packet goes into an empty window (utp_internal.cpp:997), reset
// on every acknowledged packet (:1389), and re-armed on every expiry (:1204).
// So once the last packet is acknowledged the deadline keeps ticking, and when
// it passes with nothing outstanding the idle branch runs:
//
//	if ((cur_window_packets == 0) && ((int)max_window > packet_size)) {
//	    // we don't have any packets in-flight, even though
//	    // we could. This implies that the connection is just
//	    // idling. No need to be aggressive about resetting the
//	    // congestion window. Just let it decay by a 3:rd.
//	    max_window = max(max_window * 2 / 3, size_t(packet_size));
//	                                        (utp_internal.cpp:1216-1222)
//
// `retransmit_count` is only incremented when something *is* outstanding
// (:1240), so this repeats without ever killing the connection: the window
// decays by a third at each expiry while the RTO doubles, so the decays land
// at one, three, seven, fifteen RTOs and the window falls geometrically.
//
// This library cannot do that. Its retransmission timers are armed per packet
// and there is none to arm when nothing is outstanding, so an idle connection
// keeps whatever window it last had. Whether that is worth changing depends on
// what libutp actually does rather than on what its source appears to say,
// which is what this measures.
//
// The window is not readable from outside libutp -- `max_window` is private
// and `utp_socket_stats` does not report it -- so it is measured the way a
// peer would see it: idle for a while, then write a burst, and count what
// comes out before anything is acknowledged. That first flight is the window.
func TestLibutpIdleWindowDecay(t *testing.T) {
	const idle = 8 * time.Second

	busy := libutpFirstFlightAfterIdle(t, 0)
	idled := libutpFirstFlightAfterIdle(t, idle)
	ourBusy, ourIdled := ourWindowAcrossIdle(t, idle)

	t.Logf("libutp first flight: %d bytes with no idle, %d after %v (%.0f%% of it)",
		busy, idled, idle, 100*float64(idled)/float64(busy))
	t.Logf("ours, congestion window: %d bytes with no idle, %d after %v (%.0f%% of it)",
		ourBusy, ourIdled, idle, 100*float64(ourIdled)/float64(ourBusy))

	// libutp has to have decayed, or there is nothing here to compare against
	// and this test is measuring the harness rather than the reference.
	if busy == 0 || idled == 0 {
		t.Fatalf("libutp's first flight measured %d bytes busy and %d idle; a zero is a "+
			"measurement that did not happen, not a window that was empty", busy, idled)
	}
	if float64(idled) > 0.6*float64(busy) {
		t.Fatalf("libutp kept %d of %d bytes across %v idle; the reference did not decay, so "+
			"this run says nothing about the divergence it exists to measure", idled, busy, idle)
	}
	// Three decays by a third, at one, three and seven RTOs, predicts
	// (2/3)^3 = 30%. Loose, because which decays land inside the window
	// depends on where the RTO floor sits.
	if lo, hi := 0.15*float64(busy), 0.55*float64(busy); float64(idled) < lo || float64(idled) > hi {
		t.Errorf("libutp's window after %v idle was %d bytes, outside the %0.f-%0.f the "+
			"decay-by-a-third schedule predicts; the reading of utp_internal.cpp:1216-1222 "+
			"behind this test may be wrong", idle, idled, lo, hi)
	}

	// And ours has to decay the same way. This is the assertion the test was
	// built for: it began by pinning the *divergence* -- ours held 100% of its
	// window where libutp kept 29% -- and failed, as intended, the moment the
	// decay was implemented.
	// Compared as a decay *rule*, not as a ratio.
	//
	// Comparing the ratios directly is what a first version did, and it fails
	// one run in three for a reason that is not a divergence: how many
	// expiries fit in the idle period depends on where the RTO floor lands,
	// so a run may take two decays where another takes three -- 44% against
	// 30% -- while applying exactly the same rule. Converting each ratio to
	// the number of thirds it represents compares the rule and tolerates the
	// count, and still fails on a genuinely different rule: three halvings
	// would read as 5.1 steps against libutp's 3.
	steps := func(ratio float64) float64 { return math.Log(ratio) / math.Log(2.0/3.0) }
	ourRatio := float64(ourIdled) / float64(ourBusy)
	theirRatio := float64(idled) / float64(busy)
	ourSteps, theirSteps := steps(ourRatio), steps(theirRatio)
	t.Logf("decays by a third: libutp %.2f, ours %.2f", theirSteps, ourSteps)
	if d := ourSteps - theirSteps; d > 1.1 || d < -1.1 {
		t.Errorf("across %v idle libutp kept %.0f%% of its window (%d of %d, %.2f decays by a "+
			"third) and we kept %.0f%% (%d of %d, %.2f). More than one expiry apart is a "+
			"different rule, not a different schedule (utp_internal.cpp:1216-1222, :1204)",
			idle, 100*theirRatio, idled, busy, theirSteps,
			100*ourRatio, ourIdled, ourBusy, ourSteps)
	}
	// Neither should fall below one packet, which is the floor libutp clamps
	// to in the same expression.
	if ourIdled < 1000 {
		t.Errorf("our window decayed to %d bytes, below the one-packet floor", ourIdled)
	}
}

// ourWindowAcrossIdle reports this library's congestion window after a bulk
// transfer and again after `idle` of silence.
//
// Ours is read directly from the metrics rather than inferred from a first
// flight, because it can be: libutp's `max_window` is private and
// `utp_socket_stats` does not report it, which is the only reason that side is
// measured the roundabout way.
func ourWindowAcrossIdle(t *testing.T, idle time.Duration) (busy, idled uint32) {
	t.Helper()

	n := NewNetwork(7272)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{
		Delay:        20 * time.Millisecond,
		BandwidthBps: 10_000_000,
		QueueBytes:   512 * 1024,
	})

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

	go func() {
		stream, err := sockB.Accept(ctx, utp.NewConnectionConfig())
		if err != nil {
			return
		}
		defer stream.Close()
		buf := make([]byte, 64*1024)
		for ctx.Err() == nil {
			if _, err := stream.Read(ctx, buf); err != nil {
				return
			}
		}
	}()

	time.Sleep(100 * time.Millisecond)
	cid := utp.NewConnectionId(b.Addr(), 9600, 9601)
	stream, err := sockA.ConnectWithCid(ctx, cid, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer stream.Close()

	bulk := make([]byte, 512*1024)
	rand.New(rand.NewSource(19)).Read(bulk)
	start := time.Now()
	if _, err := stream.Write(ctx, bulk); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForIdle(t, &mu, &samples, start, 120*time.Second)

	mu.Lock()
	busy = lastSample(samples).CwndBytes
	mu.Unlock()

	// Idle. An idle connection produces no metrics samples at all, because
	// nothing wakes its event loop -- which is the mechanism behind the
	// divergence as much as the missing timer is. So the window afterwards is
	// read from the first sample the next write produces.
	time.Sleep(idle)

	mu.Lock()
	before := len(samples)
	mu.Unlock()
	if _, err := stream.Write(ctx, []byte("probe")); err != nil {
		t.Fatalf("probe write: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(samples)
		if n > before {
			idled = samples[before].CwndBytes
			mu.Unlock()
			return busy, idled
		}
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no metrics sample arrived after the idle period")
	return 0, 0
}

// libutpFirstFlightAfterIdle drives real libutp as the sender: a bulk transfer
// to grow its window, then `idle` of silence, then a burst. It reports how
// many bytes libutp put on the wire in the burst's first flight.
func libutpFirstFlightAfterIdle(t *testing.T, idle time.Duration) int {
	t.Helper()

	n := NewNetwork(7171)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{
		Delay:        20 * time.Millisecond,
		BandwidthBps: 10_000_000,
		QueueBytes:   512 * 1024,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	const connSeed = 7700
	drv, err := libutp.NewDriver(uint64(time.Now().UnixMicro()))
	if err != nil {
		t.Fatal(err)
	}
	drv.PushRandom(connSeed)

	// Stop and join the pump before the C driver is freed.
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

	// The receiver is one of ours, reading continuously so its advertised
	// window never becomes the limit.
	recv := newReceivingEndpoint(t, ctx, b)
	defer recv.close()

	type burst struct {
		at    time.Time
		bytes int
	}
	sent := make(chan burst, 8192)

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
			out := drv.Emitted()
			drv.ClearEmitted()
			for _, pkt := range out {
				if _, err := a.WriteTo(pkt, b.Addr()); err != nil {
					return
				}
				// Only data packets count towards a flight.
				if len(pkt) > 20 {
					select {
					case sent <- burst{time.Now(), len(pkt) - 20}:
					default:
					}
				}
			}
		}
	}()

	if err := drv.Connect(); err != nil {
		t.Fatalf("libutp connect: %v", err)
	}

	bulk := make([]byte, 512*1024)
	rand.New(rand.NewSource(17)).Read(bulk)
	if _, err := drv.Write(bulk); err != nil {
		t.Fatalf("libutp write: %v", err)
	}

	// Wait for the bulk transfer to be delivered and quiet down.
	if !recv.waitForBytes(len(bulk), 120*time.Second) {
		t.Fatalf("libutp delivered only %d of %d bytes", recv.count(), len(bulk))
	}
	drainBursts(sent)

	if idle > 0 {
		time.Sleep(idle)
		drainBursts(sent)
	}

	// The burst. Everything libutp emits before the first acknowledgement can
	// come back -- one propagation delay each way plus slack -- is its window.
	probe := make([]byte, 512*1024)
	if _, err := drv.Write(probe); err != nil {
		t.Fatalf("libutp probe write: %v", err)
	}
	// Measure the flight from its first packet, not from the call that asked
	// for it.
	//
	// This used to open a 40ms window at the moment of the write. Run on its
	// own that was fine; run inside the full package it returned zero, because
	// under load the driver's pump goroutine can be descheduled past the whole
	// window and the test then compared a real number against nothing. The
	// window now starts when the first packet actually goes out, and a flight
	// that never starts is a failure rather than a zero.
	var first burst
	select {
	case first = <-sent:
	case <-time.After(20 * time.Second):
		t.Fatal("libutp emitted nothing after the probe write; there is no first flight to measure")
	}

	total := first.bytes
	deadline := time.After(40 * time.Millisecond)
	for {
		select {
		case s := <-sent:
			if s.at.Sub(first.at) > 40*time.Millisecond {
				return total
			}
			total += s.bytes
		case <-deadline:
			return total
		}
	}
}

func drainBursts[T any](ch chan T) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// receivingEndpoint is one of our sockets accepting a connection and reading
// continuously, used as the far end for a libutp sender.
type receivingEndpoint struct {
	sock   *utp.UtpSocket
	got    atomic.Int64
	cancel context.CancelFunc
}

func newReceivingEndpoint(t *testing.T, ctx context.Context, ep *Endpoint) *receivingEndpoint {
	t.Helper()
	r := &receivingEndpoint{}
	r.sock = utp.WithSocket(ctx, ep, quiet())
	go func() {
		stream, err := r.sock.Accept(ctx, utp.NewConnectionConfig())
		if err != nil {
			return
		}
		defer stream.Close()
		buf := make([]byte, 64*1024)
		for ctx.Err() == nil {
			n, err := stream.Read(ctx, buf)
			if err != nil {
				return
			}
			r.got.Add(int64(n))
		}
	}()
	return r
}

func (r *receivingEndpoint) count() int { return int(r.got.Load()) }

func (r *receivingEndpoint) waitForBytes(n int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if r.count() >= n {
			// Let the last acknowledgements settle.
			time.Sleep(300 * time.Millisecond)
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func (r *receivingEndpoint) close() { r.sock.Close() }
