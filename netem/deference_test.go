package netem

import (
	"context"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// The experiment uTP is actually judged on: does it get out of the way of a
// loss-based flow sharing the same bottleneck?
//
// Everything else in this harness measures uTP against uTP, which is the easy
// case. "Less than best effort" is a claim about what happens when something
// greedy shows up, and until now nothing here could produce something greedy.
// netem.RunRenoFlow is that competitor -- Reno's congestion control exactly,
// with none of TCP's protocol; see reno.go for precisely what it does and does
// not model.
//
// Both flows run over one shared bottleneck (Network.ConnectShared), so they
// contend for one queue at one rate. Without that they would each get their
// own link and neither could crowd the other out, which is what made this
// measurement impossible before.
func TestDeferenceToLossBasedFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("not a -short test")
	}

	// The competitor alone, to establish what the link gives one greedy flow
	// with nothing to share it with.
	solo := runRenoSolo(t)
	t.Logf("baseline: the competitor alone gets %.2f Mbps", solo)

	for _, alg := range []struct {
		name string
		algo utp.CongestionAlgorithm
	}{
		{"LEDBAT", utp.AlgorithmLEDBAT},
		{"LEDBAT++", utp.AlgorithmLEDBATPP},
	} {
		alg := alg
		t.Run(alg.name, func(t *testing.T) {
			utpMbps, renoMbps, queue := runDeference(t, alg.algo)

			// The number that matters: how much of the competitor's
			// bandwidth uTP took. A protocol that yields leaves the
			// competitor close to what it had alone.
			retained := renoMbps / solo
			t.Logf("%s: uTP %.2f Mbps, competitor %.2f Mbps", alg.name, utpMbps, renoMbps)
			t.Logf("%s: the competitor kept %.0f%% of what it got alone (%.2f of %.2f Mbps)",
				alg.name, retained*100, renoMbps, solo)
			t.Logf("%s: uTP took %.0f%% of the shared link", alg.name, 100*utpMbps/(utpMbps+renoMbps))
			t.Logf("%s: standing queue at the bottleneck p50 %v, p95 %v, max %v",
				alg.name, queue.p50, queue.p95, queue.max)

			if utpMbps <= 0 {
				t.Errorf("the uTP flow got no bandwidth at all; nothing can be concluded")
			}
			if renoMbps <= 0 {
				t.Errorf("the competing flow got no bandwidth at all; nothing can be concluded")
			}
		})
	}
}

type queueStats struct {
	p50, p95, max time.Duration
}

// deferenceLinkConfig is the shared bottleneck both experiments run over.
//
// The queue is 256 KB, about 200ms at this rate. It has to be deeper than
// either controller's delay target for delay-based control to operate at all:
// on a queue shallower than the target, the queue overflows before the target
// is reached and a delay-based controller degenerates into a loss-based one,
// which would make this experiment measure nothing.
func deferenceLinkConfig() Config {
	return Config{
		Delay:        20 * time.Millisecond,
		BandwidthBps: 10_000_000,
		QueueBytes:   256 * 1024,
	}
}

const deferencePayload = 4 * 1024 * 1024

// runRenoSolo measures the competitor with the link to itself.
func runRenoSolo(t *testing.T) float64 {
	t.Helper()
	n := NewNetwork(310)
	defer n.Close()
	a := n.MustAddEndpoint("reno-sender")
	b := n.MustAddEndpoint("reno-receiver")
	cfg := deferenceLinkConfig()
	n.ConnectShared(cfg, [2]*Endpoint{a, b}, [2]*Endpoint{b, a})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	res, err := RunRenoFlow(ctx, a, b, deferencePayload, 1024)
	if err != nil {
		t.Fatalf("competitor alone: %v", err)
	}
	t.Logf("competitor alone: %s, cwnd mean %dB max %dB, retx %d, timeouts %d, fast retransmits %d",
		res.Goodput, res.CwndMeanBytes, res.CwndMaxBytes, res.Retransmits, res.Timeouts, res.FastRetransmits)
	return res.Goodput.Mbps()
}

// runDeference runs a uTP flow and the competitor over one shared bottleneck
// and returns what each achieved, plus the queue they left at the bottleneck.
func runDeference(t *testing.T, algo utp.CongestionAlgorithm) (utpMbps, renoMbps float64, queue queueStats) {
	t.Helper()

	n := NewNetwork(311)
	defer n.Close()
	utpSender := n.MustAddEndpoint("utp-sender")
	utpReceiver := n.MustAddEndpoint("utp-receiver")
	renoSender := n.MustAddEndpoint("reno-sender")
	renoReceiver := n.MustAddEndpoint("reno-receiver")

	// One bottleneck, four directed pairs through it.
	cfg := deferenceLinkConfig()
	n.ConnectShared(cfg,
		[2]*Endpoint{utpSender, utpReceiver},
		[2]*Endpoint{utpReceiver, utpSender},
		[2]*Endpoint{renoSender, renoReceiver},
		[2]*Endpoint{renoReceiver, renoSender},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, utpSender, utpReceiver, quiet())

	data := make([]byte, deferencePayload)
	for i := range data {
		data[i] = byte(i)
	}

	type outcome struct {
		which string
		mbps  float64
		err   error
	}
	results := make(chan outcome, 2)

	// The competitor starts first and is given time to fill the queue, so the
	// uTP flow arrives at a bottleneck that is already busy -- which is the
	// situation it is supposed to detect and defer to.
	go func() {
		res, err := RunRenoFlow(ctx, renoSender, renoReceiver, deferencePayload, 1024)
		if err != nil {
			results <- outcome{"reno", 0, err}
			return
		}
		results <- outcome{"reno", res.Goodput.Mbps(), nil}
	}()

	time.Sleep(1500 * time.Millisecond)

	go func() {
		cfgConn := utp.NewConnectionConfig()
		cfgConn.CongestionAlgorithm = algo
		res, err := pair.RunTransfer(ctx, data, FlowOptions{
			InitiatorCid:    800,
			Config:          cfgConn,
			MetricsInterval: 10 * time.Millisecond,
		})
		if err != nil {
			results <- outcome{"utp", 0, err}
			return
		}
		results <- outcome{"utp", res.Goodput.Mbps(), nil}
	}()

	for i := 0; i < 2; i++ {
		out := <-results
		if out.err != nil {
			t.Fatalf("%s flow: %v", out.which, out.err)
		}
		if out.which == "utp" {
			utpMbps = out.mbps
		} else {
			renoMbps = out.mbps
		}
	}

	// The queue at the bottleneck itself, which is what a third party sharing
	// this link would experience.
	link := n.Link("utp-sender", "utp-receiver")
	if link != nil {
		st := link.Stats()
		queue.max = st.QueueDelayMax
		queue.p50 = link.QueueDelayPercentile(0.50)
		queue.p95 = link.QueueDelayPercentile(0.95)
		_ = st
	}
	return utpMbps, renoMbps, queue
}
