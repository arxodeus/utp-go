package netem

import (
	"context"
	"testing"
	"time"

	utp "github.com/arxodeus/utp"
)

// rackRetransmissions runs one transfer over a reordering link and returns
// how many packets the sender resent.
func rackRetransmissions(t *testing.T, rack bool) uint64 {
	t.Helper()
	n := NewNetwork(103)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{
		Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 256 * 1024,
		ReorderRate: 0.02, ReorderDelay: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, Quiet())
	cfg := utp.NewConnectionConfig()
	cfg.RACK = rack
	res, err := pair.RunTransfer(ctx, make([]byte, 2<<20), FlowOptions{InitiatorCid: 100, Config: cfg, Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Verified {
		t.Fatal("payload mismatch")
	}
	s := res.Sender.Samples()
	return s[len(s)-1].PacketsRetransmitted
}

// A packet displaced by 5 ms on a 40 ms round trip is reordered, not lost.
// libutp's rule resends it once three later packets arrive first; RACK
// waits a quarter of the minimum round trip, 10 ms here, once the path has
// been seen to reorder. Nothing is lost on this link, so every resend is
// spurious. Measured over 2 MB: 23-24 resends with libutp's rule, 3 with
// RACK, three runs each.
func TestRackResendsLessUnderReordering(t *testing.T) {
	if testing.Short() {
		t.Skip("not a -short test")
	}
	off, on := rackRetransmissions(t, false), rackRetransmissions(t, true)
	t.Logf("resends over a reordering link with no loss: %d with libutp's rule, %d with RACK", off, on)
	if off < 10 {
		t.Fatalf("libutp's rule resent %d packets; the link is not reordering enough to compare", off)
	}
	if on*2 > off {
		t.Errorf("RACK resent %d packets where libutp's rule resent %d; want at most half", on, off)
	}
}
