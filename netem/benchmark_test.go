package netem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// The M5 benchmark suite: one fixed set of link profiles, run the same way
// every time, so a congestion-control change can be judged rather than
// guessed at.
//
// Every profile is deterministic -- a fixed seed per link, and the emulator's
// loss, reordering and jitter all draw from it -- so two runs of the same
// binary produce the same numbers, and a difference between two binaries is
// the change and not the weather. What is *not* deterministic is our side's
// timing: it runs on the real clock with real goroutines, so single runs vary
// by a few percent. Numbers below are one run each unless stated; treat
// differences under about 5% as noise.
//
// Each profile is run benchmarkRepeats times and the median reported, because
// our side's run-to-run variance is real: a single retransmission timeout
// costs a full RTO and can halve a short transfer's goodput. The spread is
// reported alongside the median so a reader can see how much to trust it.
//
// Run with:
//
//	go test ./netem -run TestBenchmarkSuite -v
//
// It is skipped under -short.

// benchmarkRepeats is how many times each profile runs. The median is
// reported. Three is enough to reject a single outlier and cheap enough to
// run on every congestion-control change.
const benchmarkRepeats = 3

// benchmarkAlgorithms is the set of congestion controllers the suite runs.
// Every profile runs under each, so the tables can be compared directly.
func benchmarkAlgorithms() []struct {
	name string
	algo utp.CongestionAlgorithm
} {
	return []struct {
		name string
		algo utp.CongestionAlgorithm
	}{
		{"LEDBAT", utp.AlgorithmLEDBAT},
		{"LEDBAT++", utp.AlgorithmLEDBATPP},
	}
}

type linkProfile struct {
	name    string
	seed    int64
	cfg     Config
	payload int
}

func benchmarkProfiles() []linkProfile {
	return []linkProfile{
		{
			name:    "LAN (1ms, 100Mbps, no loss)",
			seed:    101,
			cfg:     Config{Delay: time.Millisecond, BandwidthBps: 100_000_000, QueueBytes: 256 * 1024},
			payload: 4 * 1024 * 1024,
		},
		{
			name:    "Broadband (20ms, 10Mbps, no loss)",
			seed:    102,
			cfg:     Config{Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 64 * 1024},
			payload: 1024 * 1024,
		},
		{
			name:    "Broadband, 1% loss",
			seed:    103,
			cfg:     Config{Delay: 20 * time.Millisecond, LossRate: 0.01, BandwidthBps: 10_000_000, QueueBytes: 64 * 1024},
			payload: 512 * 1024,
		},
		{
			name:    "Broadband, 5% loss",
			seed:    104,
			cfg:     Config{Delay: 20 * time.Millisecond, LossRate: 0.05, BandwidthBps: 10_000_000, QueueBytes: 64 * 1024},
			payload: 256 * 1024,
		},
		{
			name:    "High BDP (100ms, 20Mbps, no loss)",
			seed:    105,
			cfg:     Config{Delay: 100 * time.Millisecond, BandwidthBps: 20_000_000, QueueBytes: 512 * 1024},
			payload: 2 * 1024 * 1024,
		},
		{
			name:    "Shallow queue (20ms, 10Mbps, 16KB queue)",
			seed:    106,
			cfg:     Config{Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 16 * 1024},
			payload: 512 * 1024,
		},
		{
			// Long enough for several LEDBAT++ slowdown cycles. Everything
			// above finishes in one or two, where a slowdown is a large
			// fraction of the whole transfer and the measurement says more
			// about the transfer's length than about the controller.
			name:    "Long transfer (20ms, 10Mbps, 8MB)",
			seed:    109,
			cfg:     Config{Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 64 * 1024},
			payload: 8 * 1024 * 1024,
		},
		{
			name:    "Reordering (20ms, 10Mbps, 2% reordered)",
			seed:    107,
			cfg:     Config{Delay: 20 * time.Millisecond, ReorderRate: 0.02, BandwidthBps: 10_000_000, QueueBytes: 64 * 1024},
			payload: 512 * 1024,
		},
	}
}

func TestBenchmarkSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("benchmark suite is not a -short test")
	}

	for _, alg := range benchmarkAlgorithms() {
		alg := alg
		t.Run(alg.name, func(t *testing.T) {
			var rows []string
			rows = append(rows, "| Profile | Goodput (median) | Range | Elapsed | Retx | cwnd mean | cwnd max | at floor | qdelay p50 | qdelay p95 | standing queue p50 |")
			rows = append(rows, "| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |")

			for _, profile := range benchmarkProfiles() {
				profile := profile
				t.Run(profile.name, func(t *testing.T) {
					var (
						mbps      []float64
						elapsed   []time.Duration
						retx      []float64
						cwndMean  []float64
						cwndMax   []float64
						atFloor   []float64
						qdelayP50 []time.Duration
						qdelayP95 []time.Duration
						queueP50  []time.Duration
					)
					for run := 0; run < benchmarkRepeats; run++ {
						res, sum := runProfileOnce(t, profile, alg.algo)
						mbps = append(mbps, res.Goodput.Mbps())
						elapsed = append(elapsed, res.Elapsed)
						retx = append(retx, sum.RetransmitRate*100)
						cwndMean = append(cwndMean, float64(sum.CwndMeanBytes))
						cwndMax = append(cwndMax, float64(sum.CwndMaxBytes))
						atFloor = append(atFloor, sum.CwndPinnedAtMin*100)
						qdelayP50 = append(qdelayP50, sum.QueueingDelayP50)
						qdelayP95 = append(qdelayP95, sum.QueueingDelayP95)
						queueP50 = append(queueP50, sum.StandingQueueP50)
						t.Logf("run %d: %s | %s", run+1, res, sum)
					}
					lo, hi := minMaxFloat(mbps)
					rows = append(rows, fmt.Sprintf("| %s | %.2f Mbps | %.2f-%.2f | %v | %.2f%% | %.0fB | %.0fB | %.1f%% | %v | %v | %v |",
						profile.name, medianFloat(mbps), lo, hi,
						medianDuration(elapsed).Round(time.Millisecond),
						medianFloat(retx), medianFloat(cwndMean), medianFloat(cwndMax),
						medianFloat(atFloor),
						medianDuration(qdelayP50).Round(time.Microsecond),
						medianDuration(qdelayP95).Round(time.Microsecond),
						medianDuration(queueP50).Round(time.Microsecond)))
				})
			}

			// Two flows over one bottleneck: the fairness number LEDBAT is
			// judged on.
			t.Run("Two flows, 8Mbps bottleneck", func(t *testing.T) {
				var totals, fairnesses []float64
				for run := 0; run < benchmarkRepeats; run++ {
					fairness, throughputs := runTwoFlowBottleneck(t, 108, alg.algo)
					total := throughputs[0].Mbps() + throughputs[1].Mbps()
					totals = append(totals, total)
					fairnesses = append(fairnesses, fairness)
					t.Logf("run %d: %.2f + %.2f Mbps, total %.2f, Jain fairness %.3f",
						run+1, throughputs[0].Mbps(), throughputs[1].Mbps(), total, fairness)
				}
				lo, hi := minMaxFloat(totals)
				rows = append(rows, fmt.Sprintf("| Two flows, 8Mbps bottleneck | %.2f Mbps total | %.2f-%.2f | | | | | | | | Jain %.3f |",
					medianFloat(totals), lo, hi, medianFloat(fairnesses)))
			})

			t.Logf("%s:\n%s", alg.name, strings.Join(rows, "\n"))
			if dir := os.Getenv("UTP_BENCHMARK_OUT_DIR"); dir != "" {
				path := filepath.Join(dir, strings.ReplaceAll(alg.name, "+", "p")+".md")
				if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
					t.Fatalf("writing %s: %v", path, err)
				}
				t.Logf("wrote %s", path)
			}
		})
	}
}

// runProfileOnce runs one transfer over one profile's link and returns what
// it achieved.
func runProfileOnce(t *testing.T, profile linkProfile, algo utp.CongestionAlgorithm) (*TransferResult, Summary) {
	t.Helper()
	n := NewNetwork(profile.seed)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	n.ConnectAsymmetric(a, b, profile.cfg, profile.cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, quiet())

	data := make([]byte, profile.payload)
	for i := range data {
		data[i] = byte(i * 31)
	}

	cfg := utp.NewConnectionConfig()
	cfg.CongestionAlgorithm = algo

	res, err := pair.RunTransfer(ctx, data, FlowOptions{
		InitiatorCid:    400,
		Config:          cfg,
		MetricsInterval: 2 * time.Millisecond,
		Verify:          true,
	})
	if err != nil {
		t.Fatalf("%s: %v", profile.name, err)
	}
	if !res.Verified {
		t.Fatalf("%s: payload corrupted", profile.name)
	}
	return res, res.Sender.Summary()
}

func medianFloat(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}

func minMaxFloat(values []float64) (float64, float64) {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return sorted[0], sorted[len(sorted)-1]
}

func medianDuration(values []time.Duration) time.Duration {
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

func runTwoFlowBottleneck(t *testing.T, seed int64, algo utp.CongestionAlgorithm) (fairness float64, throughputs []Throughput) {
	t.Helper()
	n := NewNetwork(seed)
	defer n.Close()
	sender := n.MustAddEndpoint("sender")
	receiver := n.MustAddEndpoint("receiver")
	cfg := Config{Delay: 20 * time.Millisecond, BandwidthBps: 8_000_000, QueueBytes: 64 * 1024}
	n.ConnectAsymmetric(sender, receiver, cfg, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, sender, receiver, quiet())

	data := make([]byte, 512*1024)
	for i := range data {
		data[i] = byte(i)
	}

	type outcome struct {
		res *TransferResult
		err error
	}
	results := make(chan outcome, 2)
	for i, cid := range []uint16{500, 520} {
		go func(i int, cid uint16) {
			cfg := utp.NewConnectionConfig()
			cfg.CongestionAlgorithm = algo
			res, err := pair.RunTransfer(ctx, data, FlowOptions{
				InitiatorCid:    cid,
				Config:          cfg,
				MetricsInterval: 5 * time.Millisecond,
			})
			results <- outcome{res, err}
		}(i, cid)
	}

	var rates []float64
	for i := 0; i < 2; i++ {
		out := <-results
		if out.err != nil {
			t.Fatalf("flow %d: %v", i, out.err)
		}
		throughputs = append(throughputs, out.res.Goodput)
		rates = append(rates, float64(out.res.Goodput.Bps()))
	}
	return FairnessIndex(rates), throughputs
}
