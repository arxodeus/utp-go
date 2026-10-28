package netem

import (
	"context"
	"testing"
	"time"
)

// A path that delivers some packets twice costs nothing: duplicate data is
// discarded, and a duplicated acknowledgement is not taken as a loss report.
// The gate is that the transfer verifies and no packet is resent: nothing is
// lost here, so any retransmission would be one a duplicate caused.
func TestDuplicatedPacketsCauseNoRetransmission(t *testing.T) {
	n := NewNetwork(613)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	cfg := Config{Delay: 20 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 256 * 1024, DuplicateRate: 0.10}
	n.Connect(a, b, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, quiet())
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
	t.Logf("%s; %d data and %d return packets duplicated; %d retransmitted, %d by timeout",
		res, fwd.PacketsDuplicated, back.PacketsDuplicated, last.PacketsRetransmitted, last.Timeouts)
	if fwd.PacketsDropped == 0 && last.PacketsRetransmitted > 0 {
		t.Errorf("%d packets retransmitted on a path that lost none", last.PacketsRetransmitted)
	}
}
