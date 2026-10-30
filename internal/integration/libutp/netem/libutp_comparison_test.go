//go:build cgo

package netem_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/arxodeus/utp/internal/netem"
)

// TestLibutpOverEmulatedNetwork runs real libutp over the emulated network and
// compares it with this library on identical links. Four flows go over each
// link, every sender against every receiver:
//
//	go->go          both ends this library
//	libutp->go      libutp's congestion controller, our receiver
//	go->libutp      our congestion controller, libutp's receiver
//	libutp->libutp  the reference against itself
//
// The first two compare senders on the same link, payload and clock; the
// other two are controls, so a difference can be attributed to one end.
// Every flow is timed from just before the SYN to the receiver holding the
// last byte.
//
// This is not a claim that either implementation is better: the links are
// emulated, it is one machine, and libutp is driven by the loop in
// libutp_flow_test.go rather than a production embedder.
func TestLibutpOverEmulatedNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("a measurement of 20 transfers; TestLibutpTransfersOverEmulatedNetwork covers interop")
	}
	profiles := []struct {
		name  string
		cfg   Config
		bytes int
	}{
		{"LAN (1ms, 100Mbps)", Config{Delay: time.Millisecond, BandwidthBps: 100_000_000, QueueBytes: 64 * 1024}, 4 << 20},
		{"Broadband (20ms, 10Mbps)", Config{Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 64 * 1024}, 1 << 20},
		{"Broadband, 1% loss", Config{Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 64 * 1024, LossRate: 0.01}, 1 << 20},
		{"High BDP (100ms, 20Mbps)", Config{Delay: 100 * time.Millisecond, BandwidthBps: 20_000_000, QueueBytes: 256 * 1024}, 2 << 20},
		{"Shallow queue (16KB)", Config{Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 16 * 1024}, 1 << 20},
	}
	modes := []string{"go->go", "libutp->go", "go->libutp", "libutp->libutp"}

	repeats := MeasurementRepeats()
	var table strings.Builder
	table.WriteString("| Profile | go->go | libutp->go | go->libutp | libutp->libutp |\n| --- | --- | --- | --- | --- |\n")

	for _, p := range profiles {
		payload := make([]byte, p.bytes)
		for i := range payload {
			payload[i] = byte(i * 31)
		}

		cells := make([]string, 0, len(modes))
		for _, mode := range modes {
			samples := make([]float64, 0, repeats)
			for r := 0; r < repeats; r++ {
				mbps, err := runComparisonFlow(t, mode, p.cfg, payload)
				if err != nil {
					t.Errorf("%s %s: %v", p.name, mode, err)
					break
				}
				samples = append(samples, mbps)
			}
			if len(samples) == 0 {
				cells = append(cells, "failed")
				continue
			}
			lo, hi := MinMaxFloat(samples)
			cells = append(cells, fmt.Sprintf("%.2f (%.2f-%.2f)", MedianFloat(samples), lo, hi))
		}
		t.Logf("%-26s go->go %-22s libutp->go %-22s go->libutp %-22s libutp->libutp %s", p.name, cells[0], cells[1], cells[2], cells[3])
		fmt.Fprintf(&table, "| %s | %s | %s | %s | %s |\n", p.name, cells[0], cells[1], cells[2], cells[3])
	}

	if dir := os.Getenv("UTP_BENCHMARK_OUT_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			_ = os.WriteFile(dir+"/LIBUTP-COMPARISON.md", []byte(table.String()), 0o644)
		}
	}
}

// runComparisonFlow runs one transfer in one direction and returns its goodput
// in Mbps. The payload is verified every time: a fast transfer of the wrong
// bytes is not a result.
func runComparisonFlow(t *testing.T, mode string, cfg Config, payload []byte) (float64, error) {
	t.Helper()

	n := NewNetwork(21)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	var (
		elapsed time.Duration
		got     []byte
		err     error
	)
	switch mode {
	case "go->go":
		pair := NewUtpPair(ctx, n, a, b, Quiet())
		var res *TransferResult
		res, err = pair.RunTransfer(ctx, payload, FlowOptions{InitiatorCid: 100, Verify: true})
		if err == nil {
			elapsed = res.Elapsed
			if !res.Verified {
				return 0, fmt.Errorf("go->go payload mismatch")
			}
			got = payload
		}
	case "libutp->go":
		elapsed, got, err = libutpToGo(ctx, n, a, b, payload, 7000)
	case "go->libutp":
		elapsed, got, err = goToLibutp(ctx, n, a, b, payload, 7000)
	case "libutp->libutp":
		elapsed, got, err = libutpToLibutp(ctx, n, a, b, payload, 7000)
	default:
		return 0, fmt.Errorf("unknown mode %q", mode)
	}
	if err != nil {
		return 0, err
	}
	if !bytes.Equal(got, payload) {
		return 0, fmt.Errorf("payload mismatch: got %d of %d bytes", len(got), len(payload))
	}
	if elapsed <= 0 {
		return 0, fmt.Errorf("no elapsed time recorded")
	}
	return float64(len(payload)) * 8 / elapsed.Seconds() / 1e6, nil
}

// TestLibutpTransfersOverEmulatedNetwork is the cheap gate: libutp completes
// a verified transfer over the emulated network in both directions. It checks
// that the harness driving libutp (clock, packet delivery, read draining,
// MTU) is wired correctly; it runs on every `go test ./internal/netem`.
func TestLibutpTransfersOverEmulatedNetwork(t *testing.T) {
	payload := make([]byte, 256*1024)
	for i := range payload {
		payload[i] = byte(i * 31)
	}
	cfg := Config{Delay: 10 * time.Millisecond, BandwidthBps: 20_000_000, QueueBytes: 64 * 1024}

	for _, mode := range []string{"libutp->go", "go->libutp", "libutp->libutp"} {
		t.Run(mode, func(t *testing.T) {
			mbps, err := runComparisonFlow(t, mode, cfg, payload)
			if err != nil {
				t.Fatalf("%s: %v", mode, err)
			}
			t.Logf("%s: %d bytes verified, %.2f Mbps", mode, len(payload), mbps)
		})
	}
}
