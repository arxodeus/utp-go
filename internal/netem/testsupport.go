package netem

import (
	"context"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	utp "github.com/arxodeus/utp"
)

// Helpers shared by the tests in this package and by the interoperability
// tests in internal/integration/libutp.

// Quiet is a logger that drops everything a test would not act on.
func Quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 4}))
}

// benchmarkRepeats is how many times each profile runs by default: once.
// One run is a gate, not a measurement: everything asserted is per run (the
// transfer completed and the bytes are correct), which catches a congestion
// controller that has stopped working, and keeps `go test ./internal/netem`
// within Go's default timeout.
//
// Set UTP_BENCHMARK_REPEATS to take a measurement. The loss profiles need
// about seven: their results are bimodal, one retransmission timeout
// dominating a short transfer.
const benchmarkRepeats = 1

// MeasurementRepeats is the repeat count, overridable for measurement runs.
func MeasurementRepeats() int {
	if v := os.Getenv("UTP_BENCHMARK_REPEATS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return benchmarkRepeats
}

func MedianFloat(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}

func MinMaxFloat(values []float64) (float64, float64) {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return sorted[0], sorted[len(sorted)-1]
}

// benchmarkRACK is whether UTP_BENCHMARK_RACK asks for ConnectionConfig.RACK,
// so that the suite, deference and latecomer measurements can compare the
// two loss detectors in one binary, interleaved.
func benchmarkRACK() bool { return os.Getenv("UTP_BENCHMARK_RACK") == "1" }

type QueueStats struct {
	p50, p95, max time.Duration
}

// DeferenceLinkConfig is the shared bottleneck both experiments run over.
//
// The queue is 256 KB, about 200ms at this rate. It has to be deeper than
// either controller's delay target for delay-based control to operate at all:
// on a queue shallower than the target, the queue overflows before the target
// is reached and a delay-based controller degenerates into a loss-based one,
// which would make this experiment measure nothing.
func DeferenceLinkConfig() Config {
	return Config{
		Delay:        20 * time.Millisecond,
		BandwidthBps: 10_000_000,
		QueueBytes:   256 * 1024,
	}
}

const DeferencePayload = 4 * 1024 * 1024

// RunRenoSolo measures the competitor with the link to itself.
func RunRenoSolo(t *testing.T) float64 {
	t.Helper()
	n := NewNetwork(310)
	defer n.Close()
	a := n.MustAddEndpoint("reno-sender")
	b := n.MustAddEndpoint("reno-receiver")
	cfg := DeferenceLinkConfig()
	n.ConnectShared(cfg, [2]*Endpoint{a, b}, [2]*Endpoint{b, a})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	res, err := RunRenoFlow(ctx, a, b, DeferencePayload, 1024)
	if err != nil {
		t.Fatalf("competitor alone: %v", err)
	}
	t.Logf("competitor alone: %s, cwnd mean %dB max %dB, retx %d, timeouts %d, fast retransmits %d",
		res.Goodput, res.CwndMeanBytes, res.CwndMaxBytes, res.Retransmits, res.Timeouts, res.FastRetransmits)
	return res.Goodput.Mbps()
}

// RunDeference runs a uTP flow and the competitor over one shared bottleneck
// and returns what each achieved, plus the queue they left at the bottleneck.
func RunDeference(t *testing.T, algo utp.CongestionAlgorithm) (utpMbps, renoMbps float64, queue QueueStats, utpShare float64) {
	t.Helper()

	n := NewNetwork(311)
	defer n.Close()
	utpSender := n.MustAddEndpoint("utp-sender")
	utpReceiver := n.MustAddEndpoint("utp-receiver")
	renoSender := n.MustAddEndpoint("reno-sender")
	renoReceiver := n.MustAddEndpoint("reno-receiver")

	// One bottleneck, four directed pairs through it.
	cfg := DeferenceLinkConfig()
	n.ConnectShared(cfg,
		[2]*Endpoint{utpSender, utpReceiver},
		[2]*Endpoint{utpReceiver, utpSender},
		[2]*Endpoint{renoSender, renoReceiver},
		[2]*Endpoint{renoReceiver, renoSender},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, utpSender, utpReceiver, Quiet())

	data := make([]byte, DeferencePayload)
	for i := range data {
		data[i] = byte(i)
	}

	type outcome struct {
		which     string
		mbps      float64
		delivered []DeliverySample
		err       error
	}
	results := make(chan outcome, 2)

	// The competitor starts first and is given time to fill the queue, so the
	// uTP flow arrives at a bottleneck that is already busy -- which is the
	// situation it is supposed to detect and defer to.
	go func() {
		res, err := RunRenoFlow(ctx, renoSender, renoReceiver, DeferencePayload, 1024)
		if err != nil {
			results <- outcome{"reno", 0, nil, err}
			return
		}
		results <- outcome{"reno", res.Goodput.Mbps(), res.Delivered, nil}
	}()

	time.Sleep(1500 * time.Millisecond)

	go func() {
		cfgConn := utp.NewConnectionConfig()
		cfgConn.CongestionAlgorithm = algo
		cfgConn.RACK = benchmarkRACK()
		res, err := pair.RunTransfer(ctx, data, FlowOptions{
			InitiatorCid:    800,
			Config:          cfgConn,
			MetricsInterval: 10 * time.Millisecond,
		})
		if err != nil {
			results <- outcome{"utp", 0, nil, err}
			return
		}
		results <- outcome{"utp", res.Goodput.Mbps(), ReceivedSeries(res.Receiver.Samples()), nil}
	}()

	var utpDelivered, renoDelivered []DeliverySample
	for i := 0; i < 2; i++ {
		out := <-results
		if out.err != nil {
			t.Fatalf("%s flow: %v", out.which, out.err)
		}
		if out.which == "utp" {
			utpMbps, utpDelivered = out.mbps, out.delivered
		} else {
			renoMbps, renoDelivered = out.mbps, out.delivered
		}
	}
	if share, ok := SplitWhileBothRan(utpDelivered, renoDelivered); ok {
		utpShare = share
	} else {
		t.Errorf("the two flows never overlapped; there is no split to report")
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
	return utpMbps, renoMbps, queue, utpShare
}

// BatchedReader tells the socket how many datagrams are already waiting, so
// it reads them all before acknowledging, as it does from a real socket's
// batched read (utp.QueuedReader). Endpoint itself does not; see there.
type BatchedReader struct{ *Endpoint }

func (b BatchedReader) Queued() int { return len(b.inbox) }

// WaitForIdle blocks until the connection has nothing buffered and nothing in
// flight, and reports when that happened.
// WaitForIdle blocks until the connection has nothing buffered and nothing in
// flight, counting only samples taken after since, and reports when that
// happened.
//
// The `since` argument is not decoration. Without it this returned on the
// stale pre-transfer sample -- pending, in-flight and queued writes all
// legitimately zero before anything had been written -- so phase A ended
// before it began and the window it was supposed to grow never appeared.
func WaitForIdle(t *testing.T, mu *sync.Mutex, samples *[]utp.ConnectionMetrics,
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

// LastSample is the latest of samples, or the zero value if there are none.
func LastSample(samples []utp.ConnectionMetrics) utp.ConnectionMetrics {
	if len(samples) == 0 {
		return utp.ConnectionMetrics{}
	}
	return samples[len(samples)-1]
}
