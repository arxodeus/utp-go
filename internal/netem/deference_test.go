package netem

import (
	"testing"

	utp "github.com/arxodeus/utp"
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
	solo := RunRenoSolo(t)
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
			utpMbps, renoMbps, queue, utpShare := RunDeference(t, alg.algo)

			// The number that matters: how much of the competitor's
			// bandwidth uTP took. A protocol that yields leaves the
			// competitor close to what it had alone.
			retained := renoMbps / solo
			t.Logf("%s: uTP %.2f Mbps, competitor %.2f Mbps", alg.name, utpMbps, renoMbps)
			t.Logf("%s: the competitor kept %.0f%% of what it got alone (%.2f of %.2f Mbps)",
				alg.name, retained*100, renoMbps, solo)
			t.Logf("%s: uTP took %.0f%% of the shared link", alg.name, 100*utpMbps/(utpMbps+renoMbps))
			// The share above is from each flow's whole-transfer goodput,
			// which counts the competitor's head start and whichever flow
			// finishes alone. This one is the split while both were sending.
			t.Logf("%s: uTP's share while both ran %.1f%%", alg.name, 100*utpShare)
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
