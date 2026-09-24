package netem

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// driftResult is what one drifted transfer observed from the sender's side.
type driftResult struct {
	ppm           float64
	delivered     int
	elapsed       time.Duration
	meanQueueSeen time.Duration
	maxQueueSeen  time.Duration
	// lateQueueMean is the mean over samples taken after the delay window has
	// had time to fill twice over. The steady state is what the w x r rule
	// predicts; a mean over the whole transfer includes the ramp towards it
	// and is necessarily lower.
	lateQueueMean time.Duration
	lateSamples   int
	sent          int
	skewFinal     time.Duration
	skewMax       time.Duration
	driftFinal    int64
	penaltyFinal  time.Duration
	penaltyMax    time.Duration
	meanCwnd      uint32
	minCwnd       uint32
	finalCwnd     uint32
	linkQueueMean time.Duration
	samples       int
	throughputBps float64
}

func (r driftResult) String() string {
	return fmt.Sprintf(
		"%+.0fppm: %d bytes in %v (%.2f Mb/s); settled queue %v max %v "+
			"(link actually queued %v); skew correction final %v max %v; drift %d penalty final %v max %v; cwnd mean %d min %d final %d; %d samples",
		r.ppm, r.delivered, r.elapsed.Round(time.Millisecond), r.throughputBps/1e6,
		r.lateQueueMean.Round(time.Microsecond), r.maxQueueSeen.Round(time.Microsecond),
		r.linkQueueMean.Round(time.Microsecond),
		r.skewFinal.Round(time.Microsecond), r.skewMax.Round(time.Microsecond),
		r.driftFinal, r.penaltyFinal.Round(time.Microsecond), r.penaltyMax.Round(time.Microsecond),
		r.meanCwnd, r.minCwnd, r.finalCwnd, r.samples)
}

// runDrifted sends payloadLen bytes with the sender's clock drifting at ppm,
// over a link with plenty of capacity and no bottleneck queue worth speaking
// of, so that any queue the sender *believes* in is the drift.
func runDrifted(t *testing.T, ppm float64, runFor time.Duration, cid uint16, delayWindow time.Duration) driftResult {
	t.Helper()
	return runDriftedWith(t, ppm, runFor, cid, delayWindow, 5*time.Millisecond)
}

// runDriftedWith runs one transfer with the sender's clock drifting at ppm.
//
// chunkInterval paces the writes; zero writes as fast as the connection will
// take them, which is what makes the congestion window the limit.
func runDriftedWith(t *testing.T, ppm float64, runFor time.Duration, cid uint16, delayWindow, chunkInterval time.Duration) driftResult {
	t.Helper()

	n := NewNetwork(91)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	n.Connect(a, b, Config{
		Delay:        10 * time.Millisecond,
		BandwidthBps: 50_000_000,
		QueueBytes:   512 * 1024,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Only the sender's clock drifts. The receiver keeps the real one, so ppm
	// is the *relative* rate error, which is all that can matter.
	sendConn := utp.Conn(a)
	if ppm != 0 {
		sendConn = NewDriftingClock(a, ppm)
	}
	sendSock := utp.WithSocket(ctx, sendConn, quiet())
	defer sendSock.Close()
	recvSock := utp.WithSocket(ctx, b, quiet())
	defer recvSock.Close()

	acceptCid := utp.NewConnectionId(a.Addr(), cid+1, cid)
	connectCid := utp.NewConnectionId(b.Addr(), cid, cid+1)

	var (
		mu          sync.Mutex
		res         driftResult
		queueSum    time.Duration
		lateSum     time.Duration
		cwndSum     uint64
		delivered   int
		sent        int
		firstSample time.Time
		settleAfter = 2 * delayWindow
	)
	res.ppm = ppm
	res.minCwnd = ^uint32(0)

	sendCfg := utp.NewConnectionConfig()
	sendCfg.DelayWindow = delayWindow
	sendCfg.MetricsInterval = 20 * time.Millisecond
	sendCfg.Metrics = func(m utp.ConnectionMetrics) {
		if m.BaseDelay <= 0 {
			return
		}
		q := m.QueueingDelay()
		mu.Lock()
		defer mu.Unlock()
		if firstSample.IsZero() {
			firstSample = m.At
		}
		res.samples++
		queueSum += q
		if m.At.Sub(firstSample) >= settleAfter {
			res.lateSamples++
			lateSum += q
		}
		if q > res.maxQueueSeen {
			res.maxQueueSeen = q
		}
		res.skewFinal = m.ClockSkewCorrection
		if m.ClockSkewCorrection > res.skewMax {
			res.skewMax = m.ClockSkewCorrection
		}
		res.driftFinal = m.ClockDrift
		res.penaltyFinal = m.ClockDriftPenalty
		if m.ClockDriftPenalty > res.penaltyMax {
			res.penaltyMax = m.ClockDriftPenalty
		}
		cwndSum += uint64(m.CwndBytes)
		if m.CwndBytes < res.minCwnd {
			res.minCwnd = m.CwndBytes
		}
		res.finalCwnd = m.CwndBytes
	}

	// Paced well below the link rate, so no real queue ever forms.
	//
	// A bulk transfer cannot isolate drift: LEDBAT fills the bottleneck queue
	// to its target by design, so the "uncongested" control run sees tens of
	// milliseconds of perfectly real queueing delay and subtracting it from a
	// drifted run subtracts noise. An application-limited flow leaves the
	// queue empty, and then every millisecond the sender believes in is the
	// drift. Measured first the other way, which is how that was found.
	chunk := 4 << 10
	if chunkInterval == 0 {
		chunk = 64 << 10
	}
	payload := make([]byte, chunk)
	for i := range payload {
		payload[i] = byte(i * 13)
	}

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		stream, err := recvSock.AcceptWithCid(ctx, acceptCid, utp.NewConnectionConfig())
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer stream.Close()
		buf := make([]byte, 64<<10)
		for {
			n, err := stream.Read(ctx, buf)
			if err != nil {
				return
			}
			mu.Lock()
			delivered += n
			mu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		stream, err := sendSock.ConnectWithCid(ctx, connectCid, sendCfg)
		if err != nil {
			t.Errorf("connect: %v", err)
			return
		}
		defer stream.Close()
		deadline := time.Now().Add(runFor)
		var tick <-chan time.Time
		if chunkInterval > 0 {
			ticker := time.NewTicker(chunkInterval)
			defer ticker.Stop()
			tick = ticker.C
		}
		for time.Now().Before(deadline) {
			if tick != nil {
				select {
				case <-tick:
				case <-ctx.Done():
					return
				}
			}
			if _, err := stream.Write(ctx, payload); err != nil {
				t.Errorf("write: %v", err)
				return
			}
			mu.Lock()
			sent += chunk
			mu.Unlock()
		}
	}()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	res.elapsed = time.Since(start)
	res.delivered = delivered
	res.sent = sent
	res.linkQueueMean = n.Link("sender", "receiver").Stats().MeanQueueDelay()
	if res.samples > 0 {
		res.meanQueueSeen = queueSum / time.Duration(res.samples)
		res.meanCwnd = uint32(cwndSum / uint64(res.samples))
	}
	if res.lateSamples > 0 {
		res.lateQueueMean = lateSum / time.Duration(res.lateSamples)
	}
	if res.elapsed > 0 {
		res.throughputBps = float64(delivered) * 8 / res.elapsed.Seconds()
	}
	if res.minCwnd == ^uint32(0) {
		res.minCwnd = 0
	}
	return res
}

// The clock-skew correction, measured against the error it exists to cancel.
//
// LEDBAT's signal is a one-way delay, which is a difference between two
// clocks, so it carries their relative rate error. Without a correction the
// reported queueing delay settles at **the delay window multiplied by the
// drift rate** -- the base is a sliding-window minimum, so it follows the
// drift but lags by the window. That was measured before the correction
// existed: 0.99x the prediction at 5000, 10000 and 20000ppm.
//
// libutp cancels it by watching the *other* direction. The same slow clock
// that inflates the peer's view of this sender makes packets from the peer
// appear to arrive sooner, so a falling base delay in that direction is the
// visible half of the bias inflating the invisible half
// (utp_internal.cpp:2002-2014). This is that, and what it should now leave
// behind is nothing.
//
// Measured with a short window and large rates rather than the real window
// and real rates, because only the product matters and the real combination
// takes minutes per point. A 2-second window at 20000ppm is the same 40ms of
// error as the default 120-second window at 333ppm.
func TestClockSkewCorrectionCancelsThePhantomQueue(t *testing.T) {
	const runFor = 14 * time.Second
	const window = 2 * time.Second

	type point struct {
		ppm         float64
		uncorrected time.Duration
		got         driftResult
	}
	var points []point

	// Back to back, because cross-run comparisons in this harness are not
	// worth having -- see BENCHMARKS.md.
	for i, ppm := range []float64{0, -5000, -10000, -20000} {
		r := runDrifted(t, ppm, runFor, uint16(900+i*10), window)
		points = append(points, point{
			ppm:         ppm,
			uncorrected: time.Duration(float64(window) * -ppm / 1e6),
			got:         r,
		})
		t.Logf("%s  (would be %v uncorrected)", r.String(),
			time.Duration(float64(window)*-ppm/1e6).Round(time.Microsecond))
	}

	baseline := points[0].got
	if baseline.samples == 0 {
		t.Fatal("the undrifted run produced no samples; the test observed nothing")
	}
	if baseline.lateSamples == 0 {
		t.Fatal("no samples were taken after the window had filled; the transfer is " +
			"too short for a steady state")
	}
	for _, p := range points {
		if p.got.delivered < p.got.sent/2 {
			t.Errorf("%+.0fppm delivered %d of the %d bytes written; this run measures "+
				"a stall, not drift", p.ppm, p.got.delivered, p.got.sent)
		}
	}
	// Paced far below the link rate, so there is no real queue to confuse
	// with a phantom one.
	if baseline.lateQueueMean > 5*time.Millisecond {
		t.Fatalf("the undrifted run saw %v of queueing delay on a link it is using a "+
			"fraction of; drift cannot be isolated against that", baseline.lateQueueMean)
	}

	for _, p := range points[1:] {
		// The correction has to have actually fired, and by roughly the whole
		// drift accumulated over the run. Without this the test would pass
		// for a build where drift never reached the signal at all.
		wantSkew := time.Duration(float64(p.got.elapsed) * -p.ppm / 1e6)
		if p.got.skewFinal < wantSkew/2 {
			t.Errorf("%+.0fppm: the correction reached only %v against the %v of drift "+
				"accumulated over the run; it is not tracking",
				p.ppm, p.got.skewFinal, wantSkew)
		}

		excess := p.got.lateQueueMean - baseline.lateQueueMean
		if excess < 0 {
			excess = 0
		}
		left := float64(excess) / float64(p.uncorrected)
		t.Logf("%+.0fppm: %v of phantom queue left, against %v uncorrected (%.1f%%); "+
			"correction accumulated %v",
			p.ppm, excess.Round(time.Microsecond),
			p.uncorrected.Round(time.Microsecond), left*100,
			p.got.skewFinal.Round(time.Microsecond))

		// A tenth of what it would otherwise be. The residual is the lag
		// between a fall in the peer's base and the shift that answers it,
		// and it scales with the rate rather than vanishing.
		if left > 0.10 {
			t.Errorf("%+.0fppm: %v of phantom queue survives the correction, %.0f%% of "+
				"the %v it would be uncorrected", p.ppm, excess, left*100, p.uncorrected)
		}
	}
}

// Fairness at a shared bottleneck, which is what the correction is for.
//
// TestClockDriftInflatesTheDelaySignal establishes the rule -- the reported
// queueing delay settles at the delay window multiplied by the drift rate.
// The question that decides whether libutp's correction is worth building
// here is what that costs, and the answer is not what it looks like.
//
// Measured first on a single flow with the link to itself: an above-target
// phantom queue of 139ms cost about **1%** of throughput. The controller does
// back off -- the real queue it left at the bottleneck fell from 29ms to 24ms
// -- but a delay-based controller that backs off while still filling the pipe
// loses almost nothing. That run is not kept, because a 1% difference on a
// saturated link is not distinguishable from noise and asserting on it would
// be asserting on noise.
//
// Where it has to show is in competition. LEDBAT exists to yield, and a flow
// that believes it is above target when it is not yields more than its share.
// So: two flows through one bottleneck, one with a drifting clock and one
// without, and the question is what fraction of the link each ends up with.
//
// Scaled the same way as the rule test and for the same reason -- only the
// product of window and rate matters. A 2-second window at 60000ppm is the
// 120ms of phantom queue that the default 120-second window reaches at
// 1000ppm, a rate an oversubscribed virtual machine's clock really does hit.
func TestClockDriftYieldsShareAtASharedBottleneck(t *testing.T) {
	const runFor = 14 * time.Second
	const window = 2 * time.Second
	const driftPPM = -60000.0

	n := NewNetwork(93)
	defer n.Close()
	driftedSrc := n.MustAddEndpoint("drifted-src")
	driftedDst := n.MustAddEndpoint("drifted-dst")
	cleanSrc := n.MustAddEndpoint("clean-src")
	cleanDst := n.MustAddEndpoint("clean-dst")

	// One bottleneck, both flows through it, both directions so that
	// acknowledgements travel too.
	//
	// The queue is large enough that it never overflows, and the test depends
	// on that. It was 256KB, 105ms at this rate, barely above one flow's
	// 100ms target, so two flows overflowed it, and whichever took the tail
	// drop halved its window and spent seconds catching up. That is loss
	// recovery, not delay-based yielding, and it swamped what this test
	// measures: with *neither* flow drifted, the split ranged 44-58% over
	// six runs, 26-76% within single seconds; with the correction on, 38.5-
	// 57.5% over twenty; with it off, 28.5-43.4% over eight. The two
	// overlapped, so no threshold separated them, and the test failed about
	// one run in ten. At 1MB (420ms) no packet is dropped and the flows yield
	// to each other on delay alone: undrifted 49.8-50.2%, corrected 50.2-
	// 50.7%, uncorrected 32.0-33.3%, six runs each.
	cfg := Config{
		Delay:        10 * time.Millisecond,
		BandwidthBps: 20_000_000,
		QueueBytes:   1024 * 1024,
	}
	n.ConnectShared(cfg,
		[2]*Endpoint{driftedSrc, driftedDst},
		[2]*Endpoint{cleanSrc, cleanDst},
	)
	n.ConnectShared(cfg,
		[2]*Endpoint{driftedDst, driftedSrc},
		[2]*Endpoint{cleanDst, cleanSrc},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	type flow struct {
		name      string
		delivered int
		queueSeen time.Duration
	}
	var mu sync.Mutex
	results := map[string]*flow{
		"drifted": {name: "drifted"},
		"clean":   {name: "clean"},
	}

	run := func(wg *sync.WaitGroup, label string, src, dst *Endpoint, ppm float64, cid uint16) {
		defer wg.Done()

		sendConn := utp.Conn(src)
		if ppm != 0 {
			sendConn = NewDriftingClock(src, ppm)
		}
		sendSock := utp.WithSocket(ctx, sendConn, quiet())
		defer sendSock.Close()
		recvSock := utp.WithSocket(ctx, dst, quiet())
		defer recvSock.Close()

		var (
			queueSum time.Duration
			samples  int
		)
		sendCfg := utp.NewConnectionConfig()
		sendCfg.DelayWindow = window
		sendCfg.MetricsInterval = 20 * time.Millisecond
		sendCfg.Metrics = func(m utp.ConnectionMetrics) {
			if m.BaseDelay <= 0 {
				return
			}
			mu.Lock()
			queueSum += m.QueueingDelay()
			samples++
			mu.Unlock()
		}

		acceptCid := utp.NewConnectionId(src.Addr(), cid+1, cid)
		connectCid := utp.NewConnectionId(dst.Addr(), cid, cid+1)

		var inner sync.WaitGroup
		inner.Add(2)
		go func() {
			defer inner.Done()
			stream, err := recvSock.AcceptWithCid(ctx, acceptCid, utp.NewConnectionConfig())
			if err != nil {
				t.Errorf("%s accept: %v", label, err)
				return
			}
			defer stream.Close()
			buf := make([]byte, 64<<10)
			for {
				k, err := stream.Read(ctx, buf)
				if err != nil {
					return
				}
				mu.Lock()
				results[label].delivered += k
				mu.Unlock()
			}
		}()
		go func() {
			defer inner.Done()
			stream, err := sendSock.ConnectWithCid(ctx, connectCid, sendCfg)
			if err != nil {
				t.Errorf("%s connect: %v", label, err)
				return
			}
			defer stream.Close()
			payload := make([]byte, 64<<10)
			deadline := time.Now().Add(runFor)
			for time.Now().Before(deadline) {
				if _, err := stream.Write(ctx, payload); err != nil {
					return
				}
			}
		}()
		inner.Wait()

		mu.Lock()
		if samples > 0 {
			results[label].queueSeen = queueSum / time.Duration(samples)
		}
		mu.Unlock()
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go run(&wg, "drifted", driftedSrc, driftedDst, driftPPM, 960)
	go run(&wg, "clean", cleanSrc, cleanDst, 0, 970)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	drifted := results["drifted"]
	clean := results["clean"]
	total := drifted.delivered + clean.delivered
	if total == 0 {
		t.Fatal("neither flow delivered anything")
	}
	share := float64(drifted.delivered) / float64(total)
	t.Logf("shared 20Mb/s bottleneck over %v: drifted %d bytes (%.1f%% of the link, "+
		"queue seen %v), clean %d bytes (%.1f%%, queue seen %v)",
		runFor, drifted.delivered, share*100, drifted.queueSeen.Round(time.Millisecond),
		clean.delivered, (1-share)*100, clean.queueSeen.Round(time.Millisecond))

	if clean.delivered == 0 {
		t.Fatal("the undrifted flow delivered nothing; there is no comparison")
	}
	// Both flows must see the bottleneck. A drifted flow that sees no queue
	// at all is not being corrected, it is being blinded -- which is what an
	// unbounded correction does, and it wins the link by ignoring congestion
	// rather than by being treated fairly. Measured at 4ms against 40ms
	// before the correction was made to age out.
	if drifted.queueSeen < clean.queueSeen/2 {
		t.Errorf("the drifted flow saw %v of queue against the clean flow's %v: it is "+
			"blind to the bottleneck, not corrected for drift",
			drifted.queueSeen, clean.queueSeen)
	}
	// Uncorrected it takes 32.0-33.3%, corrected 50.2-50.7% (see the queue
	// above). The bounds sit well clear of both.
	if share < 0.45 {
		t.Errorf("the drifted flow took %.1f%% of the link; uncorrected it takes 32-33%%, "+
			"so the correction has not recovered its share", share*100)
	}
	if share > 0.55 {
		t.Errorf("the drifted flow took %.1f%% of the link, more than its share; the "+
			"correction is over-cancelling and leaving it delay-blind", share*100)
	}
}
