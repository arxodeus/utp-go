//go:build cgo

package netem

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// Transfers against real libutp on paths that damage them.
//
// The interop gate in native/libutp runs over loopback, which loses nothing,
// reorders nothing and delays nothing: it proves the two implementations can
// complete a transfer, not that they can recover one. Everything that makes
// loss recovery interesting -- selective acks, fast retransmit, the
// retransmission timeout, and the window collapsing and reopening -- is
// exactly what a clean path never exercises.
//
// These are the conditions where two implementations most plausibly disagree,
// because each side is reading the other's acknowledgements to decide what to
// resend. A selective-ack bitfield read from the wrong end, an off-by-one in
// the window it covers, or a different rule for what counts as lost, all
// produce a transfer that completes on loopback and stalls or corrupts here.
//
// This is a correctness gate, not a measurement: what it asserts is that every
// byte arrives, in both directions, on every profile. Goodput on these paths is
// in TestLibutpOverEmulatedNetwork.
func TestLibutpInteropUnderAdverseConditions(t *testing.T) {
	// Seed 83 is chosen, not arbitrary: it damages every profile below in
	// both directions. The guards at the end of each case check that it still
	// does, because a profile that quietly stopped losing or reordering
	// packets would pass while testing a clean path.
	profiles := []struct {
		name string
		cfg  Config
	}{
		{"5% loss", Config{
			Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000,
			QueueBytes: 64 * 1024, LossRate: 0.05,
		}},
		{"2% reordering", Config{
			Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000,
			QueueBytes: 64 * 1024, ReorderRate: 0.05,
		}},
		{"jitter half the delay", Config{
			Delay: 20 * time.Millisecond, Jitter: 10 * time.Millisecond,
			BandwidthBps: 10_000_000, QueueBytes: 64 * 1024,
		}},
		// Everything at once, on a queue too shallow to absorb it. If the two
		// implementations are going to disagree about recovery, this is where.
		{"bad path", Config{
			Delay: 40 * time.Millisecond, Jitter: 10 * time.Millisecond,
			BandwidthBps: 5_000_000, QueueBytes: 16 * 1024,
			LossRate: 0.03, ReorderRate: 0.02,
		}},
	}

	payload := make([]byte, 128*1024)
	for i := range payload {
		payload[i] = byte(i * 31)
	}

	for _, p := range profiles {
		for _, mode := range []string{"libutp->go", "go->libutp"} {
			t.Run(p.name+" "+mode, func(t *testing.T) {
				n := NewNetwork(83)
				defer n.Close()
				a := n.MustAddEndpoint("a")
				b := n.MustAddEndpoint("b")
				n.Connect(a, b, p.cfg)

				ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
				defer cancel()

				var (
					elapsed time.Duration
					got     []byte
					err     error
				)
				start := time.Now()
				if mode == "libutp->go" {
					elapsed, got, err = libutpToGo(ctx, n, a, b, payload, 7100)
				} else {
					elapsed, got, err = goToLibutp(ctx, n, a, b, payload, 7100)
				}
				fwd := n.Link("a", "b").Stats()

				if err != nil {
					t.Fatalf("%s over a %s path: %v (after %v); link %s",
						mode, p.name, err, time.Since(start).Round(time.Millisecond), fwd)
				}
				if !bytes.Equal(got, payload) {
					t.Fatalf("%s over a %s path: %d of %d bytes and they do not match; link %s",
						mode, p.name, len(got), len(payload), fwd)
				}
				t.Logf("%s over a %s path: %d bytes verified in %v (%.2f Mbps); link %s",
					mode, p.name, len(got), elapsed.Round(time.Millisecond),
					float64(len(got))*8/elapsed.Seconds()/1e6, fwd)

				// A profile that stopped damaging packets would pass while
				// testing nothing.
				if p.cfg.LossRate > 0 && fwd.DroppedByLoss == 0 {
					t.Errorf("no packet was lost on a path configured to drop %.0f%%; "+
						"this profile tested a clean path", p.cfg.LossRate*100)
				}
				if p.cfg.ReorderRate > 0 && fwd.PacketsReordered == 0 {
					t.Errorf("no packet was reordered on a path configured to reorder %.0f%%",
						p.cfg.ReorderRate*100)
				}
			})
		}
	}
}
