package netem

import (
	"context"
	"testing"
	"time"
)

// A path that delivers some packets twice costs nothing: duplicate data is
// discarded, and a duplicated acknowledgement is not taken as a loss report.
// Nothing is lost here, so the gate is that the transfer verifies and that no
// packet is fast retransmitted, the only resend a duplicate could cause.
//
// The loss probe and the retransmission timeout are driven by time, not by
// duplicates. A host that stalls the whole process for a few round trips, as
// a busy CI runner does, can let a probe fire before the reader has drained
// the acknowledgements that arrived meanwhile, so those resends are allowed;
// without them nothing may be resent at all.
func TestDuplicatedPacketsCauseNoRetransmission(t *testing.T) {
	n := NewNetwork(613)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	cfg := Config{Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 256 * 1024, DuplicateRate: 0.10}
	n.Connect(a, b, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, Quiet())
	data := make([]byte, 2<<20)
	for i := range data {
		data[i] = byte(i*13 + i/1021)
	}
	res, err := pair.RunTransfer(ctx, data, FlowOptions{InitiatorCid: 1500, MetricsInterval: 100 * time.Millisecond, Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Verified {
		t.Fatal("the bytes that arrived are not the ones sent")
	}
	fwd, back := n.Link("a", "b").Stats(), n.Link("b", "a").Stats()
	if fwd.PacketsDuplicated == 0 || back.PacketsDuplicated == 0 {
		t.Fatalf("duplicated %d data and %d return packets; the link is not duplicating",
			fwd.PacketsDuplicated, back.PacketsDuplicated)
	}
	last, _ := res.Sender.Last()
	t.Logf("%s; %d data and %d return packets duplicated; %d retransmitted: %d fast, %d by loss probe, %d by timeout",
		res, fwd.PacketsDuplicated, back.PacketsDuplicated, last.PacketsRetransmitted,
		last.FastRetransmits, last.LossProbes, last.Timeouts)
	if fwd.PacketsDropped != 0 {
		t.Fatalf("the link dropped %d packets; this test needs a lossless path", fwd.PacketsDropped)
	}
	if last.FastRetransmits > 0 {
		t.Errorf("%d packets fast retransmitted on a path that lost none: a duplicate was taken for a loss",
			last.FastRetransmits)
	}
	if last.LossProbes == 0 && last.Timeouts == 0 && last.PacketsRetransmitted > 0 {
		t.Errorf("%d packets retransmitted on a path that lost none, with no timer to account for them",
			last.PacketsRetransmitted)
	}
}
