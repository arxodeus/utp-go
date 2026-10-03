package goutp

import (
	"context"
	"testing"
	"time"

	"github.com/anacrolix/go-utp/purego"
	utp "github.com/zen-eth/utp-go"
	"github.com/zen-eth/utp-go/netem"
)

// netemPair is a pair on an emulated network whose link, both ways, is cfg:
// ours on a netem endpoint directly, purego through netemPacketConn.
func netemPair(t *testing.T, ctx context.Context, cfg netem.Config, seed int64) *pair {
	t.Helper()
	n := netem.NewNetwork(seed)
	a, b := n.MustAddEndpoint("ours"), n.MustAddEndpoint("purego")
	n.Connect(a, b, cfg)
	t.Cleanup(n.Close)

	ours := utp.WithSocket(ctx, a, quietLogger())
	t.Cleanup(ours.Close)
	pc := newNetemPacketConn(b, a)
	theirs, err := purego.NewSocketFromPacketConn(pc,
		purego.WithLogger(puregoLogger()), purego.WithAddrResolver(pc.resolve))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = theirs.Close() })
	return &pair{ours: ours, theirs: theirs, theirPeer: b.Addr(), ourAddr: a.Name(), network: "netem"}
}

// Transfers complete intact over links that lose, delay, reorder and
// duplicate packets, in every combination of who dials and who sends. These
// are the conditions under which the two engines' differences show: purego
// reads a selective ack one bit short of where libutp reads it, so a
// disagreement about which packets arrived would leave one of them waiting
// for a retransmission that never comes, and the transfer would not finish.
func TestPuregoTransferUnderAdverseConditions(t *testing.T) {
	const ms = time.Millisecond
	profiles := []struct {
		name string
		cfg  netem.Config
		size int
	}{
		{"20 ms, 3 ms jitter", netem.Config{Delay: 20 * ms, Jitter: 3 * ms}, 1 << 20},
		{"10 ms, 1% loss", netem.Config{Delay: 10 * ms, LossRate: 0.01}, 1 << 20},
		{"10 ms, 5% loss", netem.Config{Delay: 10 * ms, LossRate: 0.05}, 256 << 10},
		{"10 ms, 5% reordered", netem.Config{Delay: 10 * ms, ReorderRate: 0.05, ReorderDelay: 5 * ms}, 1 << 20},
		{"10 ms, 5% duplicated", netem.Config{Delay: 10 * ms, DuplicateRate: 0.05}, 1 << 20},
		{"2 Mb/s, 32 KB queue", netem.Config{Delay: 10 * ms, BandwidthBps: 2_000_000, QueueBytes: 32 << 10}, 512 << 10},
		{"all at once", netem.Config{Delay: 15 * ms, Jitter: 2 * ms, LossRate: 0.02, ReorderRate: 0.02,
			ReorderDelay: 5 * ms, DuplicateRate: 0.02}, 512 << 10},
	}
	for pi, prof := range profiles {
		t.Run(prof.name, func(t *testing.T) {
			for _, oursDials := range []bool{true, false} {
				for _, oursSends := range []bool{true, false} {
					t.Run(roleName(oursDials)+", "+sender(oursSends), func(t *testing.T) {
						ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
						defer cancel()
						p := netemPair(t, ctx, prof.cfg, int64(1000+pi))
						ours, theirs := p.connect(t, ctx, oursDials)
						payload := makePayload(t, prof.size)
						start := time.Now()
						if err := transfer(ctx, ours, theirs, oursDials, oursSends, payload); err != nil {
							t.Fatal(err)
						}
						el := time.Since(start)
						t.Logf("%d bytes in %v (%.2f Mbps)", len(payload), el.Round(time.Millisecond),
							float64(len(payload))*8/el.Seconds()/1e6)
					})
				}
			}
		})
	}
}
