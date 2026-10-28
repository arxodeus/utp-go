//go:build cgo

package netem

import (
	"context"
	"testing"
	"time"
)

// How each implementation's congestion control responds to a queue it caused.
//
// The bottleneck is cut to 40% of its rate partway through a transfer, so a
// sender filling the old capacity suddenly overdrives the new one and a queue
// builds. A delay-based controller should notice it in the one-way delay and
// back off until the standing queue is small; a loss-based one would push
// until the queue overflowed. The measurement is the queuing delay the link
// reports, the quantity LEDBAT controls, measured the same way for both.
//
// Stepping the propagation delay instead would measure nothing: tripling the
// RTT cuts the rate of any fixed window, whatever the controller does.
//
// The two controllers need not agree on how much queue to leave, so the
// numbers are reported and the assertion is only that each keeps the queue
// bounded.
func TestCongestionRespondsToSelfInflictedQueue(t *testing.T) {
	const (
		fullRate    = 10_000_000
		steppedRate = 4_000_000
		sampleEvery = 50 * time.Millisecond
		stepAt      = 1500 * time.Millisecond
		runFor      = 4500 * time.Millisecond
	)

	type result struct {
		queueBefore, queueAfter time.Duration
		dropped                 uint64
	}
	results := map[string]result{}

	for _, mode := range []string{"libutp->go", "go->libutp"} {
		n := NewNetwork(93)
		a := n.MustAddEndpoint("a")
		b := n.MustAddEndpoint("b")
		cfg := Config{Delay: 20 * time.Millisecond, BandwidthBps: fullRate, QueueBytes: 256 * 1024}
		n.Connect(a, b, cfg)

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

		payload := make([]byte, 8<<20)
		for i := range payload {
			payload[i] = byte(i * 31)
		}

		done := make(chan struct{})
		go func() {
			defer close(done)
			if mode == "libutp->go" {
				_, _, _ = libutpToGo(ctx, n, a, b, payload, 7600)
			} else {
				_, _, _ = goToLibutp(ctx, n, a, b, payload, 7600)
			}
		}()

		// Let it settle at the full rate, then read the queue it is holding.
		time.Sleep(stepAt)
		beforeStats := n.Link("a", "b").Stats()
		queueBefore := beforeStats.MeanQueueDelay()

		stepCfg := cfg
		stepCfg.BandwidthBps = steppedRate
		if err := n.SetConfig("a", "b", stepCfg); err != nil {
			t.Fatalf("stepping the bandwidth: %v", err)
		}
		// Reset so the delay measured afterwards is only the new regime's.
		n.Link("a", "b").ResetStats()

		time.Sleep(runFor - stepAt)
		afterStats := n.Link("a", "b").Stats()

		cancel()
		<-done
		n.Close()

		results[mode] = result{
			queueBefore: queueBefore,
			queueAfter:  afterStats.MeanQueueDelay(),
			dropped:     afterStats.DroppedByQueue,
		}
		t.Logf("%s: queueing delay %v at %d Mbps, %v after the bottleneck fell to %d Mbps "+
			"(max %v, %d packets dropped by the queue)",
			mode, queueBefore.Round(time.Microsecond), fullRate/1_000_000,
			results[mode].queueAfter.Round(time.Microsecond), steppedRate/1_000_000,
			afterStats.QueueDelayMax.Round(time.Microsecond), results[mode].dropped)
	}

	for _, mode := range []string{"libutp->go", "go->libutp"} {
		r := results[mode]
		// The queue is 256KB against a 4 Mbps bottleneck: filling it is half a
		// second of delay. A controller reading the delay signal settles well
		// short of that. One that ignored it would sit at the drop tail.
		if r.queueAfter > 200*time.Millisecond {
			t.Errorf("%s left %v of standing queue after the bottleneck fell; a delay-based "+
				"controller is supposed to back off well before the queue is full",
				mode, r.queueAfter)
		}
	}

	t.Logf("standing queue after the step: libutp %v, ours %v",
		results["libutp->go"].queueAfter.Round(time.Microsecond),
		results["go->libutp"].queueAfter.Round(time.Microsecond))
}

// How fast each implementation ramps up from a standing start. Slow start
// doubles the window each round trip, so a sender reaches the bottleneck in a
// time that grows with the logarithm of the window.
//
// Measured as the time to first carry 90% of the link rate in a sampling
// interval, on an idle path with no loss. Both numbers are reported; the
// assertion is only that neither takes absurdly long, since LEDBAT++'s slow
// start is gain-scaled and the two need not agree exactly.
func TestCongestionSlowStartRamp(t *testing.T) {
	const (
		rate        = 10_000_000
		sampleEvery = 25 * time.Millisecond
		observeFor  = 2500 * time.Millisecond
		target      = 0.9
	)

	reached := map[string]time.Duration{}

	for _, mode := range []string{"libutp->go", "go->libutp"} {
		n := NewNetwork(94)
		a := n.MustAddEndpoint("a")
		b := n.MustAddEndpoint("b")
		n.Connect(a, b, Config{Delay: 20 * time.Millisecond, BandwidthBps: rate, QueueBytes: 256 * 1024})

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		payload := make([]byte, 8<<20)
		for i := range payload {
			payload[i] = byte(i * 31)
		}

		done := make(chan struct{})
		go func() {
			defer close(done)
			if mode == "libutp->go" {
				_, _, _ = libutpToGo(ctx, n, a, b, payload, 7700)
			} else {
				_, _, _ = goToLibutp(ctx, n, a, b, payload, 7700)
			}
		}()

		start := time.Now()
		last := uint64(0)
		var hit time.Duration
		for time.Since(start) < observeFor {
			time.Sleep(sampleEvery)
			now := n.Link("a", "b").Stats().BytesOffered
			bps := float64(now-last) * 8 / sampleEvery.Seconds()
			last = now
			if hit == 0 && bps >= target*rate {
				hit = time.Since(start)
			}
		}
		cancel()
		<-done
		n.Close()

		reached[mode] = hit
		if hit == 0 {
			t.Errorf("%s never reached %.0f%% of a %d Mbps link in %v", mode, target*100, rate/1_000_000, observeFor)
			continue
		}
		t.Logf("%s reached %.0f%% of the link in %v", mode, target*100, hit.Round(time.Millisecond))
	}

	t.Logf("time to %.0f%% of link: libutp %v, ours %v", target*100,
		reached["libutp->go"].Round(time.Millisecond), reached["go->libutp"].Round(time.Millisecond))
}
