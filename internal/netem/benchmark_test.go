package netem

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	utp "github.com/arxodeus/utp"
)

// The benchmark suite: one fixed set of link profiles, run the same way every
// time, so a congestion-control change can be judged.
//
// Every profile is deterministic, with a fixed seed per link from which the
// emulator draws its loss, reordering and jitter. Our side's timing is not:
// it runs on the real clock with real goroutines, so single runs vary by a
// few percent. Treat differences under about 5% as noise.
//
// Each profile runs benchmarkRepeats times and the median is reported with
// the spread, since a single retransmission timeout can halve a short
// transfer's goodput.
//
// Run with:
//
//	go test ./internal/netem -run TestBenchmarkSuite -v
//
// It is skipped under -short.

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
			// Long enough that a single retransmission timeout is not a
			// large fraction of the run.
			//
			// At 512 KB these took about a second, and one RTO -- a second on
			// its own at the default floor -- made the result bimodal: nine
			// runs still landed in two clusters a factor of three apart, and
			// the median reported whichever cluster held five of them. A
			// benchmark row that swings on which mode the majority fell into
			// is not measuring the congestion controller.
			name:    "Broadband, 1% loss",
			seed:    103,
			cfg:     Config{Delay: 20 * time.Millisecond, LossRate: 0.01, BandwidthBps: 10_000_000, QueueBytes: 64 * 1024},
			payload: 4 * 1024 * 1024,
		},
		{
			name:    "Broadband, 5% loss",
			seed:    104,
			cfg:     Config{Delay: 20 * time.Millisecond, LossRate: 0.05, BandwidthBps: 10_000_000, QueueBytes: 64 * 1024},
			payload: 2 * 1024 * 1024,
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
					for run := 0; run < MeasurementRepeats(); run++ {
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
					lo, hi := MinMaxFloat(mbps)
					rows = append(rows, fmt.Sprintf("| %s | %.2f Mbps | %.2f-%.2f | %v | %.2f%% | %.0fB | %.0fB | %.1f%% | %v | %v | %v |",
						profile.name, MedianFloat(mbps), lo, hi,
						medianDuration(elapsed).Round(time.Millisecond),
						MedianFloat(retx), MedianFloat(cwndMean), MedianFloat(cwndMax),
						MedianFloat(atFloor),
						medianDuration(qdelayP50).Round(time.Microsecond),
						medianDuration(qdelayP95).Round(time.Microsecond),
						medianDuration(queueP50).Round(time.Microsecond)))
				})
			}

			// Two flows over one bottleneck: the fairness number LEDBAT is
			// judged on. See twoFlowProfiles for why there are two.
			for _, tp := range twoFlowProfiles() {
				tp := tp
				t.Run(tp.name, func(t *testing.T) {
					var totals, fairnesses, shared, drops []float64
					for run := 0; run < MeasurementRepeats(); run++ {
						r := runTwoFlowBottleneck(t, benchmarkSeed(108), tp, alg.algo)
						total := r.throughputs[0].Mbps() + r.throughputs[1].Mbps()
						totals = append(totals, total)
						fairnesses = append(fairnesses, r.fairness)
						shared = append(shared, r.shared)
						drops = append(drops, float64(r.queueDrops))
						t.Logf("run %d: %.2f + %.2f Mbps, total %.2f, Jain fairness %.3f, "+
							"split while both ran %.1f/%.1f, %d queue drops",
							run+1, r.throughputs[0].Mbps(), r.throughputs[1].Mbps(), total, r.fairness,
							100*r.shared, 100*(1-r.shared), r.queueDrops)
					}
					lo, hi := MinMaxFloat(totals)
					slo, _ := MinMaxFloat(shared)
					rows = append(rows, fmt.Sprintf("| %s | %.2f Mbps total | %.2f-%.2f | | | | | | | | "+
						"Jain %.3f; split while both ran %.0f/%.0f (worst %.0f/%.0f); %.0f queue drops |",
						tp.name, MedianFloat(totals), lo, hi, MedianFloat(fairnesses),
						100*MedianFloat(shared), 100*(1-MedianFloat(shared)), 100*slo, 100*(1-slo),
						MedianFloat(drops)))
				})
			}

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
	n := NewNetwork(benchmarkSeed(profile.seed))
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	n.ConnectAsymmetric(a, b, profile.cfg, profile.cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, Quiet())

	data := make([]byte, profile.payload)
	for i := range data {
		data[i] = byte(i * 31)
	}

	cfg := utp.NewConnectionConfig()
	cfg.CongestionAlgorithm = algo
	cfg.RACK = benchmarkRACK()

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

func medianDuration(values []time.Duration) time.Duration {
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

// twoFlowProfile is a bottleneck shared by two identical flows that start
// together.
type twoFlowProfile struct {
	name       string
	queueBytes int
	payload    int
}

// twoFlowProfiles are the two-flow rows.
//
// The first is the original row and is kept as it was, so it stays
// comparable with every figure recorded against it. Its queue, 64KB at
// 8Mb/s, is 65ms, below LEDBAT's 100ms target, so whether it overflows
// depends on the controller: classic LEDBAT's two flows keep the queue short
// enough never to drop a packet, LEDBAT++'s drop 8-10 a run.
//
// The second takes the queue out of the question. It holds 2MB, about two
// seconds at 8Mb/s, which no controller here fills, so nothing is dropped
// under either and the flows can only yield to each other on delay. The
// payload is larger so the shared period outlasts slow start.
func twoFlowProfiles() []twoFlowProfile {
	return []twoFlowProfile{
		{"Two flows, 8Mbps bottleneck", 64 * 1024, 512 * 1024},
		{"Two flows, 8Mbps, 2MB queue", 2 << 20, 4 << 20},
	}
}

// twoFlowResult is one run of a two-flow profile.
type twoFlowResult struct {
	throughputs []Throughput
	// fairness is Jain's index over the two flows' goodputs. Each flow's
	// goodput covers its whole transfer, including any time it had the link
	// to itself after the other finished, which evens the two out: a 60/40
	// split while both ran still scores about 0.99.
	fairness float64
	// shared is the smaller flow's share of the bytes delivered while both
	// were running, which is the split itself.
	shared float64
	// queueDrops counts packets the bottleneck discarded because its queue
	// was full.
	queueDrops uint64
}

func runTwoFlowBottleneck(t *testing.T, seed int64, profile twoFlowProfile, algo utp.CongestionAlgorithm) twoFlowResult {
	t.Helper()
	n := NewNetwork(seed)
	defer n.Close()
	sender := n.MustAddEndpoint("sender")
	receiver := n.MustAddEndpoint("receiver")
	cfg := Config{Delay: 20 * time.Millisecond, BandwidthBps: 8_000_000, QueueBytes: profile.queueBytes}
	n.ConnectAsymmetric(sender, receiver, cfg, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, sender, receiver, Quiet())

	data := make([]byte, profile.payload)
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
			cfg.RACK = benchmarkRACK()
			res, err := pair.RunTransfer(ctx, data, FlowOptions{
				InitiatorCid:    cid,
				Config:          cfg,
				MetricsInterval: 5 * time.Millisecond,
			})
			results <- outcome{res, err}
		}(i, cid)
	}

	var out twoFlowResult
	var rates []float64
	var received [][]utp.ConnectionMetrics
	for i := 0; i < 2; i++ {
		o := <-results
		if o.err != nil {
			t.Fatalf("flow %d: %v", i, o.err)
		}
		out.throughputs = append(out.throughputs, o.res.Goodput)
		rates = append(rates, float64(o.res.Goodput.Bps()))
		received = append(received, o.res.Receiver.Samples())
	}
	out.fairness = FairnessIndex(rates)
	out.shared = sharedPeriodSplit(received[0], received[1])
	out.queueDrops = n.Link("sender", "receiver").Stats().DroppedByQueue
	return out
}

// sharedPeriodSplit is the smaller of two flows' shares of the bytes their
// receivers took in while both were running. It is 0 if the two never
// overlap. See SplitWhileBothRan.
func sharedPeriodSplit(a, b []utp.ConnectionMetrics) float64 {
	share, ok := SplitWhileBothRan(ReceivedSeries(a), ReceivedSeries(b))
	if !ok {
		return 0
	}
	return math.Min(share, 1-share)
}

// benchmarkSeed is the profile's seed, or UTP_BENCHMARK_SEED when set.
//
// A profile's seed fixes which packets the emulator drops, so every repeat of
// a profile sees the same loss pattern -- as long as the sender offers the
// same packets in the same order. A change to what the sender puts on the
// wire moves every later loss onto different packets, and a comparison of
// two implementations on one seed is then partly a comparison of two loss
// patterns. Sweeping the seed is how to separate them.
func benchmarkSeed(profileSeed int64) int64 {
	if v := os.Getenv("UTP_BENCHMARK_SEED"); v != "" {
		if seed, err := strconv.ParseInt(v, 10, 64); err == nil {
			return seed
		}
	}
	return profileSeed
}
