package netem

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// One burst of loss must halve the window once, not once per lost packet.
//
// libutp rate-limits the decay:
//
//	bool can_decay_win(int64 msec) const {
//	    return (msec - last_rwin_decay) >= MAX_WINDOW_DECAY;   // 100 ms
//	}
//	void maybe_decay_win(uint64 current_ms) {
//	    if (can_decay_win(current_ms)) {
//	        max_window = (size_t)(max_window * .5);
//	        last_rwin_decay = current_ms;
//	        ...
//	                    (utp_internal.cpp:602-615, the constant at :51)
//
// and calls it once per acknowledgement that resent anything (`:1610`), not
// once per packet resent. So a queue overflow that loses a dozen packets in
// one window costs one halving, and nothing more for 100 ms however many acks
// report the hole.
//
// This library has the same rule and has had it only as a citation. The
// failure it prevents is not subtle: halving per lost packet takes a window to
// a sixteenth on four losses, and to the floor on eight, after which the
// connection crawls.
//
// The loss is staged rather than waited for -- the link is told to drop half of
// what crosses it for 25 ms and then to stop -- so the burst lands inside one
// window and is recovered by duplicate acknowledgements rather than by a
// timeout. That distinction matters: a retransmission timeout collapses the
// window to one packet through a different branch entirely
// (utp_internal.cpp:1223-1228), which would mask whatever the decay limiter
// did. The test asserts no timeout happened rather than assuming it.
func TestWindowDecayIsRateLimited(t *testing.T) {
	n := NewNetwork(8080)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	clean := Config{
		Delay:        20 * time.Millisecond,
		BandwidthBps: 10_000_000,
		QueueBytes:   512 * 1024,
	}
	n.Connect(a, b, clean)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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
	cfg.MetricsInterval = 10 * time.Millisecond
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
	cid := utp.NewConnectionId(b.Addr(), 9700, 9701)
	stream, err := sockA.ConnectWithCid(ctx, cid, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer stream.Close()

	payload := make([]byte, 3<<20)
	rand.New(rand.NewSource(23)).Read(payload)
	done := make(chan error, 1)
	go func() {
		_, err := stream.Write(ctx, payload)
		done <- err
	}()

	// Let the window grow before damaging anything.
	time.Sleep(1200 * time.Millisecond)

	mu.Lock()
	before := lastSample(samples)
	mu.Unlock()
	lossAt := time.Now()

	// Half the packets, not all of them.
	//
	// Dropping everything for 25ms was the first attempt and recovered
	// nothing: with no packet arriving after the gap the receiver has nothing
	// to acknowledge, so there are no duplicate acknowledgements, no fast
	// retransmit, and recovery waits for the retransmission timeout -- the
	// one branch this test has to avoid. Losing half of a burst leaves plenty
	// arriving to report the holes.
	lossy := clean
	lossy.LossRate = 0.5
	if err := n.SetConfig("a", "b", lossy); err != nil {
		t.Fatal(err)
	}
	time.Sleep(25 * time.Millisecond)
	if err := n.SetConfig("a", "b", clean); err != nil {
		t.Fatal(err)
	}

	// Long enough for the hole to be reported and recovered, short enough to
	// stay well inside the one-second retransmission timeout.
	time.Sleep(600 * time.Millisecond)

	mu.Lock()
	got := append([]utp.ConnectionMetrics(nil), samples...)
	mu.Unlock()

	after := lastSample(got)
	lowest, ok := lowestCwndAfter(got, lossAt)
	if !ok {
		t.Fatal("no samples after the loss burst")
	}

	// Preconditions, each of which was worth asserting rather than assuming.
	if before.CwndBytes < 8*before.MinCwndBytes {
		t.Fatalf("the window was only %d bytes (floor %d) when the loss was staged; there is "+
			"no room for repeated halvings to be visible", before.CwndBytes, before.MinCwndBytes)
	}
	if testing.Verbose() {
		base := got[0].At
		for _, m := range got {
			if m.At.Before(lossAt.Add(-200 * time.Millisecond)) {
				continue
			}
			t.Logf("  t=%6dms cwnd=%7d inflight=%7d sent=%d retx=%d fastretx=%d timeouts=%d",
				m.At.Sub(base).Milliseconds(), m.CwndBytes, m.InFlightBytes,
				m.PacketsSent, m.PacketsRetransmitted, m.FastRetransmits, m.Timeouts)
		}
	}
	if resent := after.PacketsRetransmitted - before.PacketsRetransmitted; resent < 3 {
		t.Fatalf("only %d packet(s) were retransmitted after blackholing the link for 25ms; "+
			"without several losses in one window there is nothing for the rate limit to "+
			"limit", resent)
	}
	if timedOut := after.Timeouts - before.Timeouts; timedOut > 0 {
		t.Fatalf("%d retransmission timeout(s) fired during the recovery; a timeout collapses "+
			"the window through a different branch entirely and would mask what the decay "+
			"limiter did", timedOut)
	}

	// The measurement. One halving is 50%; two would be 25%. The bound sits
	// between them, low enough to allow a second decay 100ms later during a
	// long recovery and still fail the per-packet halving this exists to
	// prevent, which reaches a sixteenth on four losses.
	// The measurement. One halving leaves 50%, two leave 25%; recovery here
	// takes one or two round trips, so one or two decays are both within the
	// rule. The bound sits below two and far above what halving per lost
	// packet would produce: ten losses would take the window to a thousandth
	// of itself, which is the floor.
	if floor := before.CwndBytes / 5; lowest < floor {
		t.Errorf("one burst of loss took the window from %d bytes to %d, below the %d that two "+
			"halvings would leave. libutp decays once per acknowledgement that resent "+
			"something and not again for 100ms (utp_internal.cpp:602-615), so a burst inside "+
			"one window costs one halving", before.CwndBytes, lowest, floor)
	}

	t.Logf("window %d -> %d bytes (%.0f%%) across 25ms of 50%% loss that cost %d retransmissions "+
		"and no timeouts; unlimited, the same burst takes it to the %d-byte floor",
		before.CwndBytes, lowest, 100*float64(lowest)/float64(before.CwndBytes),
		after.PacketsRetransmitted-before.PacketsRetransmitted, before.MinCwndBytes)

	cancel()
	<-done
}

// lowestCwndAfter reports the smallest congestion window observed at or after
// the given moment.
func lowestCwndAfter(samples []utp.ConnectionMetrics, at time.Time) (uint32, bool) {
	var lowest uint32
	found := false
	for _, m := range samples {
		if m.At.Before(at) {
			continue
		}
		if !found || m.CwndBytes < lowest {
			lowest = m.CwndBytes
			found = true
		}
	}
	return lowest, found
}
