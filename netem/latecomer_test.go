package netem

import (
	"context"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// The latecomer experiment: the failure classic LEDBAT is known for, and the
// one LEDBAT++ exists to fix.
//
// A delay-based controller decides how much queue it is causing by comparing
// the delay it measures now against the lowest delay it has ever seen. That
// only works if it ever saw the path empty. A flow that starts while another
// flow already has the bottleneck queue full measures that queue as if it
// were the path itself: its "base delay" is wrong by exactly the standing
// queue, it concludes it is causing no delay at all, and it stops yielding.
//
// The result is backwards. The flow that arrives *later* is the one that
// misbehaves, and it takes throughput from the flow that was already there.
// RFC 6817 acknowledges this; LEDBAT++ §4.4 answers it with periodic
// slowdowns, which drop the window to two packets for two round trips so the
// queue drains and the base delay can be measured against an empty path.
//
// This test does not assert that LEDBAT++ wins. It measures the split both
// ways and reports it, and fails only on the thing that would make the
// measurement meaningless -- a flow that got no bandwidth at all.
func TestLatecomerShare(t *testing.T) {
	if testing.Short() {
		t.Skip("latecomer experiment is not a -short test")
	}

	for _, alg := range []struct {
		name string
		algo utp.CongestionAlgorithm
	}{
		{"LEDBAT", utp.AlgorithmLEDBAT},
		{"LEDBAT++", utp.AlgorithmLEDBATPP},
	} {
		alg := alg
		t.Run(alg.name, func(t *testing.T) {
			incumbent, latecomer := runLatecomer(t, alg.algo)

			ratio := latecomer / incumbent
			t.Logf("%s: incumbent %.2f Mbps, latecomer %.2f Mbps, latecomer/incumbent %.2f",
				alg.name, incumbent, latecomer, ratio)
			t.Logf("%s: Jain fairness over the shared period %.3f",
				alg.name, FairnessIndex([]float64{incumbent, latecomer}))

			if incumbent <= 0 {
				t.Errorf("the incumbent flow was starved to nothing by the latecomer")
			}
			if latecomer <= 0 {
				t.Errorf("the latecomer got no bandwidth at all")
			}
		})
	}
}

// runLatecomer starts one flow, lets it fill the bottleneck queue, then starts
// a second, and returns each flow's goodput in Mbps.
//
// Both flows carry the same payload, so the comparison is of how long each
// took, not of how much each was asked to move.
func runLatecomer(t *testing.T, algo utp.CongestionAlgorithm) (incumbentMbps, latecomerMbps float64) {
	t.Helper()

	n := NewNetwork(210)
	defer n.Close()
	sender := n.MustAddEndpoint("sender")
	receiver := n.MustAddEndpoint("receiver")
	// A deep queue, so a standing queue can actually build and be measured.
	// 256 KB at 10 Mbps is about 200 ms of buffering -- ordinary for
	// consumer broadband, and deeper than either algorithm's delay target,
	// which is what makes delay-based control operative at all here.
	cfg := Config{Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 256 * 1024}
	n.ConnectAsymmetric(sender, receiver, cfg, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, sender, receiver, quiet())

	data := make([]byte, 4*1024*1024)
	for i := range data {
		data[i] = byte(i)
	}

	newConfig := func() *utp.ConnectionConfig {
		c := utp.NewConnectionConfig()
		c.CongestionAlgorithm = algo
		return c
	}

	type outcome struct {
		which string
		mbps  float64
		err   error
	}
	results := make(chan outcome, 2)

	go func() {
		res, err := pair.RunTransfer(ctx, data, FlowOptions{
			InitiatorCid:    700,
			Config:          newConfig(),
			MetricsInterval: 10 * time.Millisecond,
		})
		if err != nil {
			results <- outcome{"incumbent", 0, err}
			return
		}
		results <- outcome{"incumbent", res.Goodput.Mbps(), nil}
	}()

	// Long enough for the incumbent to fill the queue, so the latecomer's
	// very first delay samples are taken against a full one. That is the
	// whole point: the latecomer's base delay is wrong from its first packet.
	time.Sleep(1500 * time.Millisecond)

	go func() {
		res, err := pair.RunTransfer(ctx, data, FlowOptions{
			InitiatorCid:    720,
			Config:          newConfig(),
			MetricsInterval: 10 * time.Millisecond,
		})
		if err != nil {
			results <- outcome{"latecomer", 0, err}
			return
		}
		results <- outcome{"latecomer", res.Goodput.Mbps(), nil}
	}()

	for i := 0; i < 2; i++ {
		out := <-results
		if out.err != nil {
			t.Fatalf("%s flow: %v", out.which, out.err)
		}
		if out.which == "incumbent" {
			incumbentMbps = out.mbps
		} else {
			latecomerMbps = out.mbps
		}
	}
	return incumbentMbps, latecomerMbps
}

// The base-delay estimate is the thing the latecomer problem corrupts, so
// measure it directly rather than only through throughput: run a flow on a
// path with a standing queue and watch what it believes the empty path costs.
//
// A LEDBAT++ flow drops to two packets periodically, which drains the queue,
// so its base delay should track the real one. A classic LEDBAT flow has no
// such mechanism and can only ever revise its estimate downwards by luck.
func TestBaseDelayTracking(t *testing.T) {
	if testing.Short() {
		t.Skip("not a -short test")
	}

	for _, alg := range []struct {
		name string
		algo utp.CongestionAlgorithm
	}{
		{"LEDBAT", utp.AlgorithmLEDBAT},
		{"LEDBAT++", utp.AlgorithmLEDBATPP},
	} {
		alg := alg
		t.Run(alg.name, func(t *testing.T) {
			n := NewNetwork(211)
			defer n.Close()
			a := n.MustAddEndpoint("sender")
			b := n.MustAddEndpoint("receiver")
			cfg := Config{Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 256 * 1024}
			n.ConnectAsymmetric(a, b, cfg, cfg)

			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
			defer cancel()
			pair := NewUtpPair(ctx, n, a, b, quiet())

			data := make([]byte, 4*1024*1024)
			cfgConn := utp.NewConnectionConfig()
			cfgConn.CongestionAlgorithm = alg.algo

			res, err := pair.RunTransfer(ctx, data, FlowOptions{
				InitiatorCid:    740,
				Config:          cfgConn,
				MetricsInterval: 10 * time.Millisecond,
			})
			if err != nil {
				t.Fatal(err)
			}
			sum := res.Sender.Summary()
			t.Logf("%s: %s", alg.name, res)
			t.Logf("%s: %s", alg.name, sum)

			// The standing queue this flow left behind. Lower is better: it
			// is the delay every other user of this path pays because of us.
			//
			// Measured from the round trip, not from the controller's own
			// base-delay estimate. The controller's estimate is exactly what
			// goes wrong here, so asking it how much queue it is causing is
			// asking the defendant for the verdict -- see Summary.
			t.Logf("%s: standing queue p50 %v, p95 %v (controller believed %v)",
				alg.name, sum.StandingQueueP50, sum.StandingQueueP95, sum.QueueingDelayP50)
			t.Logf("%s: round trip min %v, p50 %v -- the path itself is %v",
				alg.name, sum.RTTMin, sum.RTTP50, 2*cfg.Delay)
		})
	}
}
