package netem

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// The application-limited guard, measured rather than read.
//
// libutp refuses to grow the congestion window when the application has not
// been filling it:
//
//	if (scaled_gain > 0 && ctx->current_ms - last_maxed_out_window > 1000) {
//	    // if it was more than 1 second since we tried to send a packet
//	    // and stopped because we hit the max window, we're most likely rate
//	    // limited (which prevents us from ever hitting the window size)
//	    // if this is the case, we cannot let the max_window grow indefinitely
//	    scaled_gain = 0;
//	}
//	                                        (utp_internal.cpp:1681-1686)
//
// `last_maxed_out_window` is stamped by `is_full` every time the sender has
// something to send and no window to send it in (:945, :957). The reason the
// guard exists: a window grown while nothing is filling it is a measurement of
// nothing, and the sender dumps it all at once the moment the application
// speeds up.
//
// This library has the same rule (congestion.go, lastMaxedOutWindow) and has
// had it only as a citation. This measures it.
//
// One detail decides whether the test measures anything at all. The guard
// zeroes `scaled_gain`, which is the LEDBAT term; in slow start libutp takes
// `max_window = max(ss_cwnd, ledbat_cwnd)` (:1699), and `ss_cwnd` is not
// gated. So a connection still in slow start grows whatever the application
// does, in both implementations, and a test that goes idle before slow start
// ends would see growth and call it a defect. Phase A exists to end slow
// start before the measurement begins.
func TestApplicationLimitedWindowDoesNotGrow(t *testing.T) {
	n := NewNetwork(5150)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{
		Delay:        20 * time.Millisecond,
		BandwidthBps: 3_000_000,
		QueueBytes:   8 * 1024,
	})

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
	sendCfg := utp.NewConnectionConfig()
	sendCfg.MetricsInterval = 20 * time.Millisecond
	sendCfg.Metrics = func(m utp.ConnectionMetrics) {
		mu.Lock()
		samples = append(samples, m)
		mu.Unlock()
	}

	// The receiver reads continuously, so the peer's advertised window never
	// becomes what limits the sender -- the point is to observe congestion
	// control, not flow control.
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
	cid := utp.NewConnectionId(b.Addr(), 9300, 9301)
	stream, err := sockA.ConnectWithCid(ctx, cid, sendCfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer stream.Close()

	// --- phase A: fill the pipe, so slow start is over and the window is
	// large enough that holding it flat means something.
	bulk := make([]byte, 512*1024)
	rand.New(rand.NewSource(7)).Read(bulk)
	// Repeated until slow start is actually over, rather than once and hope.
	// Slow start ends on a loss or on the delay crossing 0.9 of target
	// (utp_internal.cpp:1692-1697), and on a link this clean one 512 KB
	// transfer does not reliably produce either: one run in four left the
	// connection still in slow start, where the window grows whatever the
	// application does and the guard cannot be measured at all.
	var phaseAEnd time.Time
	for attempt := 0; ; attempt++ {
		if attempt == 8 {
			t.Fatalf("slow start had not ended after %d bulk transfers; the link is too clean "+
				"for this test to reach the state it measures", attempt)
		}
		bulkStart := time.Now()
		if _, err := stream.Write(ctx, bulk); err != nil {
			t.Fatalf("phase A write: %v", err)
		}
		phaseAEnd = waitForIdle(t, &mu, &samples, bulkStart, 60*time.Second)
		mu.Lock()
		last := samples[len(samples)-1]
		mu.Unlock()
		if !last.SlowStart {
			break
		}
	}

	// --- phase B: application-limited. Small writes, far apart: the sender
	// always has window to spare, so is_full never fires and libutp's guard
	// would hold the window where phase A left it.
	const phaseB = 5 * time.Second
	trickle := make([]byte, 1024)
	deadline := time.Now().Add(phaseB)
	for time.Now().Before(deadline) {
		if _, err := stream.Write(ctx, trickle); err != nil {
			t.Fatalf("phase B write: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	phaseBEnd := time.Now()

	mu.Lock()
	got := append([]utp.ConnectionMetrics(nil), samples...)
	mu.Unlock()
	if len(got) == 0 {
		t.Fatal("no metrics samples; the observer never fired and this test measured nothing")
	}

	startCwnd, peakA, ok := cwndRange(got, time.Time{}, phaseAEnd)
	if !ok {
		t.Fatal("no samples during phase A")
	}
	// The measurement starts where the guard does, not where phase B does.
	// libutp suppresses growth only once it has been more than a second since
	// the window was last filled (utp_internal.cpp:1681), so the first second
	// after a phase of hard sending is allowed to grow -- in both
	// implementations. Measuring from the start of phase B counted that
	// second's legitimate growth as a failure, on two runs in three: 16724 to
	// 23031 bytes, all of it before the guard's precondition held.
	guardStart, ok := firstGuarded(got, phaseAEnd, phaseBEnd)
	if !ok {
		t.Fatal("the guard's precondition never held during phase B")
	}
	atBStart, peakB, ok := cwndRange(got, guardStart.Add(-time.Nanosecond), phaseBEnd)
	if !ok {
		t.Fatal("no samples during phase B")
	}

	// Three things have to be true before the measurement means anything, and
	// each of them was false at some point while this test was being written.
	//
	// The window has to have grown in phase A, or holding it flat afterwards
	// is not evidence of anything.
	if peakA < 4*startCwnd {
		t.Fatalf("phase A grew the window only from %d to %d bytes; without a window that "+
			"grew, phase B holding flat says nothing", startCwnd, peakA)
	}
	// Slow start has to be over. Its term is ungated in both implementations
	// (utp_internal.cpp:1699), so a connection still in slow start grows
	// whatever the application does -- measured here at ~112 bytes per 83ms
	// before the link was given a queue small enough to end slow start with a
	// loss.
	if inSlowStart(got, phaseAEnd, phaseBEnd) {
		t.Fatalf("the connection was still in slow start during the application-limited " +
			"phase, where the window grows whether or not anything fills it; this run " +
			"measured nothing about the guard")
	}
	// And the guard's own precondition has to hold: libutp suppresses growth
	// only once it has been more than a second since the window was last
	// filled.
	if left := phaseBEnd.Sub(guardStart); left < 3*time.Second {
		t.Fatalf("only %v of the application-limited phase ran with the guard engaged; too "+
			"little for its absence to be visible", left.Round(time.Millisecond))
	}
	if maxAppLimited(got, phaseAEnd, phaseBEnd) <= time.Second {
		t.Fatalf("the sender never went more than a second without filling its window, so " +
			"the guard's precondition (utp_internal.cpp:1681) never held")
	}

	// The measurement. The window is expected to be flat, not merely bounded:
	// the guard zeroes the gain outright.
	limit := atBStart + atBStart/50
	if peakB > limit {
		t.Errorf("the window grew from %d to %d bytes over %v in which the application wrote "+
			"1 KB every 20ms and never once filled it; libutp holds the window where it was "+
			"(utp_internal.cpp:1681-1686) because a window grown while nothing fills it is a "+
			"measurement of nothing", atBStart, peakB, phaseB)
	}
	t.Logf("phase A grew the window %d -> %d bytes; %v application-limited (slow start over, "+
		"window unfilled for up to %v) left it %d -> %d, peak %d against a bound of %d",
		startCwnd, peakA, phaseB, maxAppLimited(got, phaseAEnd, phaseBEnd).Round(time.Second),
		atBStart, lastCwnd(got, phaseBEnd), peakB, limit)

	if testing.Verbose() {
		base := got[0].At
		for i, m := range got {
			if m.At.Before(phaseAEnd) && i%10 != 0 {
				continue
			}
			t.Logf("  t=%6dms cwnd=%7d inflight=%7d pending=%6d ss=%v applim=%v timeouts=%d rtt=%v",
				m.At.Sub(base).Milliseconds(), m.CwndBytes, m.InFlightBytes,
				m.SendBufferPending, m.SlowStart, m.AppLimitedSince.Round(time.Millisecond),
				m.Timeouts, m.RTT.Round(time.Millisecond))
		}
	}
}

// cwndRange reports the first and the largest congestion window observed in
// (after, until].
func cwndRange(samples []utp.ConnectionMetrics, after, until time.Time) (first, peak uint32, ok bool) {
	for _, m := range samples {
		if !m.At.After(after) || m.At.After(until) {
			continue
		}
		if !ok {
			first = m.CwndBytes
			ok = true
		}
		if m.CwndBytes > peak {
			peak = m.CwndBytes
		}
	}
	return first, peak, ok
}

func lastCwnd(samples []utp.ConnectionMetrics, until time.Time) uint32 {
	var last uint32
	for _, m := range samples {
		if m.At.After(until) {
			break
		}
		last = m.CwndBytes
	}
	return last
}

// waitForIdle blocks until the connection has nothing buffered and nothing in
// flight, and reports when that happened.
// waitForIdle blocks until the connection has nothing buffered and nothing in
// flight, counting only samples taken after since, and reports when that
// happened.
//
// The `since` argument is not decoration. Without it this returned on the
// stale pre-transfer sample -- pending, in-flight and queued writes all
// legitimately zero before anything had been written -- so phase A ended
// before it began and the window it was supposed to grow never appeared.
func waitForIdle(t *testing.T, mu *sync.Mutex, samples *[]utp.ConnectionMetrics,
	since time.Time, limit time.Duration) time.Time {
	t.Helper()
	deadline := time.Now().Add(limit)
	started := false
	for time.Now().Before(deadline) {
		mu.Lock()
		var last utp.ConnectionMetrics
		if n := len(*samples); n > 0 {
			last = (*samples)[n-1]
		}
		mu.Unlock()
		if last.At.After(since) {
			busy := last.SendBufferPending > 0 || last.InFlightBytes > 0 || last.PendingWrites > 0
			if busy {
				started = true
			} else if started {
				return time.Now()
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the bulk transfer never drained within %v", limit)
	return time.Time{}
}

// inSlowStart reports whether any sample in (after, until] was still in slow
// start.
func inSlowStart(samples []utp.ConnectionMetrics, after, until time.Time) bool {
	for _, m := range samples {
		if !m.At.After(after) || m.At.After(until) {
			continue
		}
		if m.SlowStart {
			return true
		}
	}
	return false
}

// maxAppLimited reports the longest the sender went without filling its window
// during (after, until].
func maxAppLimited(samples []utp.ConnectionMetrics, after, until time.Time) time.Duration {
	var longest time.Duration
	for _, m := range samples {
		if !m.At.After(after) || m.At.After(until) {
			continue
		}
		if m.AppLimitedSince > longest {
			longest = m.AppLimitedSince
		}
	}
	return longest
}

// firstGuarded reports when the application-limited guard's precondition first
// held: the first sample in (after, until] where the sender had gone more than
// a second without filling its window.
func firstGuarded(samples []utp.ConnectionMetrics, after, until time.Time) (time.Time, bool) {
	for _, m := range samples {
		if !m.At.After(after) || m.At.After(until) {
			continue
		}
		if m.AppLimitedSince > time.Second {
			return m.At, true
		}
	}
	return time.Time{}, false
}
