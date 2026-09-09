//go:build cgo

package netem

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLibutpOverEmulatedNetwork runs real libutp over the emulated network and
// compares it against this library on identical links.
//
// This is the gap COMPATIBILITY.md named as its largest: every congestion
// control row there was *cited* — code read against utp_internal.cpp and
// matched by hand — and every number in BENCHMARKS.md compared this library
// against a previous version of itself, because libutp had never been run over
// the emulated network. Three flows go over each link:
//
//	go->go        both ends this library, the BENCHMARKS.md configuration
//	libutp->go    libutp's congestion controller, our receiver
//	go->libutp    our congestion controller, libutp's receiver
//
// Comparing the first two is the point: same link, same payload, same clock,
// different sender.
//
// What this is not: a claim that either implementation is better. The links
// are emulated, the machine is one machine, and libutp is being driven by the
// loop in libutp_flow_test.go rather than by a production embedder.
func TestLibutpOverEmulatedNetwork(t *testing.T) {
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
	modes := []string{"go->go", "libutp->go", "go->libutp"}

	repeats := benchmarkRepeatCount()
	var table strings.Builder
	table.WriteString("| Profile | go->go | libutp->go | go->libutp |\n| --- | --- | --- | --- |\n")

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
			lo, hi := minMaxFloat(samples)
			cells = append(cells, fmt.Sprintf("%.2f (%.2f-%.2f)", medianFloat(samples), lo, hi))
		}
		t.Logf("%-26s go->go %-22s libutp->go %-22s go->libutp %s", p.name, cells[0], cells[1], cells[2])
		fmt.Fprintf(&table, "| %s | %s | %s | %s |\n", p.name, cells[0], cells[1], cells[2])
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
		pair := NewUtpPair(ctx, n, a, b, quiet())
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

// TestLibutpTransfersOverEmulatedNetwork is the cheap gate: libutp completes a
// verified transfer over the emulated network in both directions. It runs on
// every `go test ./netem`, where the comparison table above is the expensive
// part.
//
// Its value is not the throughput number. It is that the harness driving
// libutp — clock, packet delivery, read draining, MTU — is wired up correctly,
// which is not something to take on trust: two defects in it were found by
// measuring, and both made libutp look worse than it is. See
// KNOWN-LIMITATIONS.md.
func TestLibutpTransfersOverEmulatedNetwork(t *testing.T) {
	payload := make([]byte, 256*1024)
	for i := range payload {
		payload[i] = byte(i * 31)
	}
	cfg := Config{Delay: 10 * time.Millisecond, BandwidthBps: 20_000_000, QueueBytes: 64 * 1024}

	for _, mode := range []string{"libutp->go", "go->libutp"} {
		t.Run(mode, func(t *testing.T) {
			mbps, err := runComparisonFlow(t, mode, cfg, payload)
			if err != nil {
				t.Fatalf("%s: %v", mode, err)
			}
			t.Logf("%s: %d bytes verified, %.2f Mbps", mode, len(payload), mbps)
		})
	}
}
