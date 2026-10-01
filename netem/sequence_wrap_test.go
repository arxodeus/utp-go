package netem

import (
	"context"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// A connection that sends more than 65,536 packets, so that its sequence
// numbers wrap, goes on sending at the rate of the path.
//
// It did not. The sender kept every packet it had ever sent and found each by
// its distance from the first sequence number, modulo 65536; past 65,535
// packets a new one landed on an old, acknowledged entry, was booked as a
// retransmission of it, and stopped counting as in flight. The sender then
// ran away at the rate of its event loop -- measured at 22,000 packets a
// second into a 10 Mbps link, sharing it with a Reno flow, from about 90 MB
// into the connection. No test had sent that much on one connection.
//
// 600-byte datagrams get past the wrap in about 40 MB. The gate is on what
// the sender put on the wire against what the payload needed: a sender that
// has lost count of what is in flight sends many times more.
func TestTransferPastSequenceWrap(t *testing.T) {
	if testing.Short() {
		t.Skip("not a -short test")
	}
	t.Run("clean", func(t *testing.T) { transferPastWrap(t, 0, 70_000, 11) })
	// Twice round, with selective acks and fast retransmission working
	// across the boundary.
	t.Run("1% loss", func(t *testing.T) { transferPastWrap(t, 0.01, 140_000, 12) })
}

// transferPastWrap sends packets packets' worth of payload over a 100 Mbps
// link losing lossRate of datagrams, and fails if the sender sent more than
// limitTenths/10 times that.
func transferPastWrap(t *testing.T, lossRate float64, packets, limitTenths int) {
	n := NewNetwork(512)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{Delay: time.Millisecond, BandwidthBps: 100_000_000, QueueBytes: 256 * 1024, LossRate: lossRate})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, quiet())

	cfg := utp.NewConnectionConfig()
	cfg.MaxPacketSize = 600
	payload := 600 - 20
	data := make([]byte, packets*payload)
	for i := range data {
		data[i] = byte(i*7 + i/4099)
	}
	res, err := pair.RunTransfer(ctx, data, FlowOptions{
		InitiatorCid:    1300,
		Config:          cfg,
		MetricsInterval: 100 * time.Millisecond,
		Verify:          true,
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if !res.Verified {
		t.Fatalf("%d bytes arrived, not the ones sent", res.BytesTransferred)
	}
	last, ok := res.Sender.Last()
	if !ok {
		t.Fatal("no metrics from the sender")
	}
	t.Logf("%s; the sender sent %d packets for %d packets' worth of payload (%d retransmitted)",
		res, last.PacketsSent, packets, last.PacketsRetransmitted)
	if last.PacketsSent < uint64(packets) {
		t.Fatalf("the sender reports %d packets sent for %d packets' worth of payload; the "+
			"sequence numbers did not wrap and this measures nothing", last.PacketsSent, packets)
	}
	if limit := uint64(packets * limitTenths / 10); last.PacketsSent > limit {
		t.Errorf("the sender sent %d packets for %d packets' worth of payload, more than %d",
			last.PacketsSent, packets, limit)
	}
}
