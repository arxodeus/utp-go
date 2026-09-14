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
			"(link actually queued %v); cwnd mean %d min %d final %d; %d samples",
		r.ppm, r.delivered, r.elapsed.Round(time.Millisecond), r.throughputBps/1e6,
		r.lateQueueMean.Round(time.Microsecond), r.maxQueueSeen.Round(time.Microsecond),
		r.linkQueueMean.Round(time.Microsecond),
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

// What clock drift costs the delay signal, and the rule that governs it.
//
// Nothing here could produce skew until DriftingClock existed, so this is the
// first measurement of a question the design has always had an answer to on
// paper: LEDBAT's signal is a difference between two clocks, and if they run
// at different rates that difference grows without bound.
//
// The base delay is a sliding-window minimum, so it does absorb drift -- but
// only with a lag. If the measured delay grows at rate r, the lowest sample
// still inside a window of length w is the one from w ago, so the reported
// queueing delay settles at **w x r** whether or not there is a queue. That
// product is the prediction this measures.
//
// It is measured with a short window and a large rate rather than the real
// window and a real rate, because only the product matters and the real
// combination takes minutes per data point. A 2-second window at 20000ppm is
// the same 40ms of phantom queue as the default 120-second window at 333ppm,
// and the second would need a three-minute transfer to reach steady state.
// The extrapolation is stated rather than assumed: the test checks the
// product across three rates, so the linearity it rests on is itself measured.
//
// For reference at the default 120-second window:
//
//	 100ppm (a good crystal)          12ms of phantom queue
//	 500ppm                           60ms
//	1000ppm (a loaded VM's clock)    120ms -- above the 100ms target
//
// libutp is exposed to this far more: its base is the minimum over thirteen
// one-minute buckets (DELAY_BASE_HISTORY, utp_internal.cpp:50), so about 780
// seconds, which is 6.5x this window. That is why it also shifts its own base
// whenever the peer's base drops (:2002-2014) -- a correction this library
// does not have, and needs proportionally less.
func TestClockDriftInflatesTheDelaySignal(t *testing.T) {
	// Long enough that the window fills and the signal settles: the rule
	// predicts a steady state, and a transfer shorter than the window can
	// only show the ramp towards it.
	const runFor = 14 * time.Second
	const window = 2 * time.Second

	type point struct {
		ppm       float64
		predicted time.Duration
		got       driftResult
	}
	var points []point

	// Back to back, because cross-run comparisons in this harness are not
	// worth having -- see BENCHMARKS.md.
	for i, ppm := range []float64{0, -5000, -10000, -20000} {
		r := runDrifted(t, ppm, runFor, uint16(900+i*10), window)
		predicted := time.Duration(float64(window) * -ppm / 1e6)
		points = append(points, point{ppm: ppm, predicted: predicted, got: r})
		t.Logf("%s  (predicted phantom queue %v)", r.String(), predicted.Round(time.Microsecond))
	}

	baseline := points[0].got
	if baseline.samples == 0 {
		t.Fatal("the undrifted run produced no samples; the test observed nothing")
	}
	for _, p := range points {
		// A paced flow is not a bulk transfer, so the check is that it kept
		// flowing rather than that a fixed total arrived.
		if p.got.delivered < p.got.sent/2 {
			t.Errorf("%+.0fppm delivered %d of the %d bytes written; the connection "+
				"did not keep up and this run measures a stall, not drift",
				p.ppm, p.got.delivered, p.got.sent)
		}
	}
	// The link has capacity to spare, so the undrifted run must see almost no
	// queue -- otherwise there is nothing to attribute to drift.
	// With the flow paced far below the link rate there should be no real
	// queue at all, so the control run must sit near zero. If it does not,
	// the drifted runs are being compared against noise.
	if baseline.lateQueueMean > 5*time.Millisecond {
		t.Fatalf("the undrifted run saw %v of queueing delay on a link it is using "+
			"a fraction of; drift cannot be isolated against that", baseline.lateQueueMean)
	}

	// The rule: the excess over the undrifted run tracks window x rate.
	if baseline.lateSamples == 0 {
		t.Fatal("no samples were taken after the window had filled; the transfer is " +
			"too short to show a steady state, and the rule is about one")
	}
	for _, p := range points[1:] {
		excess := p.got.lateQueueMean - baseline.lateQueueMean
		if excess <= 0 {
			t.Errorf("%+.0fppm saw no more queueing delay than no drift at all "+
				"(%v against %v); the drift is not reaching the signal",
				p.ppm, p.got.lateQueueMean, baseline.lateQueueMean)
			continue
		}
		ratio := float64(excess) / float64(p.predicted)
		t.Logf("%+.0fppm: %v of phantom queue against %v predicted (%.2fx)",
			p.ppm, excess.Round(time.Microsecond), p.predicted.Round(time.Microsecond), ratio)
		// Once settled the figure should be the product itself. Still a
		// range rather than a number: the sample is a mean over a live
		// transfer that also carries a little real queue.
		if ratio < 0.6 || ratio > 1.6 {
			t.Errorf("%+.0fppm: phantom queue %v is %.2fx the predicted %v, outside "+
				"the range this rule would explain", p.ppm, excess, ratio, p.predicted)
		}
	}
}

// What the phantom queue actually costs, at a shared bottleneck.
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
	cfg := Config{
		Delay:        10 * time.Millisecond,
		BandwidthBps: 20_000_000,
		QueueBytes:   256 * 1024,
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
	// The drifted flow must see a substantially larger queue than the clean
	// one -- otherwise the drift is not reaching the signal and whatever the
	// shares turn out to be says nothing about drift.
	if drifted.queueSeen < clean.queueSeen+50*time.Millisecond {
		t.Fatalf("the drifted flow saw %v of queue against the clean flow's %v; the "+
			"drift is not reaching the signal", drifted.queueSeen, clean.queueSeen)
	}
	// The finding itself is the share, logged above and recorded in
	// KNOWN-LIMITATIONS.md. Asserted loosely, as a tripwire: a drifted flow
	// should not be taking *more* than its half.
	if share > 0.5 {
		t.Errorf("the drifted flow took %.1f%% of the link despite believing it was "+
			"%v above target; the delay signal is not driving the window",
			share*100, drifted.queueSeen)
	}
}
