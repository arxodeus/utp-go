package netem

import (
	"context"
	"testing"
	"time"
)

// TestAckCoalescingUnderLoad measures how many acknowledgements the receiver
// puts on the return path per data packet it receives, on a real transfer.
//
// The connection defers acks — connection.ackPending is set when data arrives
// and flushAck sends at most one at the end of the event-loop pass, matching
// libutp's schedule_ack / utp_issue_deferred_acks (utp_internal.cpp:2377,
// :3796-3808). How much that saves is load-dependent, not structural: when
// packets arrive faster than the connection drains them, several are
// acknowledged together; when they arrive one at a time there is nothing to
// coalesce, and acknowledging each is both correct and what libutp does when
// its embedder reads one datagram per batch.
//
// So this measures the ratio across a range of packet rates rather than
// asserting a fixed count. Before deferring was implemented the ratio was
// exactly 1.000 at every rate — every data packet drew its own ack.
//
// Measured on the reference machine, once each:
//
//	              plain    -race
//	20Mbps/10ms   0.823    0.844-0.919
//	100Mbps/1ms   0.368    0.670-0.721
//	1Gbps/1ms     0.167    0.390-0.486
//
// Only the highest rate is asserted, and loosely. At the lowest rate
// coalescing barely engages by design, so a slower machine could legitimately
// reach 1.000 there and the assertion would be measuring the machine.
func TestAckCoalescingUnderLoad(t *testing.T) {
	cases := []struct {
		name     string
		bps      uint64
		delay    time.Duration
		maxRatio float64 // 0 means "report only, do not assert"
	}{
		{name: "20Mbps/10ms", bps: 20_000_000, delay: 10 * time.Millisecond},
		{name: "100Mbps/1ms", bps: 100_000_000, delay: time.Millisecond},
		{name: "1Gbps/1ms", bps: 1_000_000_000, delay: time.Millisecond, maxRatio: 0.75},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, acks := ackRatioTransfer(t, tc.bps, tc.delay)
			ratio := float64(acks) / float64(data)
			t.Logf("%d data packets drew %d acks (%.3f per data packet)", data, acks, ratio)

			if acks == 0 {
				t.Fatal("no acks on the return path at all; the measurement is not measuring what it thinks")
			}
			if tc.maxRatio == 0 {
				return
			}
			if ratio > tc.maxRatio {
				t.Errorf("%.3f acks per data packet, want at most %.2f; deferred acks are "+
					"not coalescing under load", ratio, tc.maxRatio)
			}
		})
	}
}

// ackRatioTransfer runs one verified transfer and returns the number of packets
// offered to the forward link and to the return link. The forward link carries
// data, the return link carries acks; neither link loses anything here, so
// "offered" is what was sent.
func ackRatioTransfer(t *testing.T, bps uint64, delay time.Duration) (dataPackets, ackPackets uint64) {
	t.Helper()

	n := NewNetwork(21)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	n.Connect(a, b, Config{
		Delay:        delay,
		BandwidthBps: bps,
		QueueBytes:   64 * 1024,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, quiet())

	payload := make([]byte, 512*1024)
	for i := range payload {
		payload[i] = byte(i * 31)
	}
	res, err := pair.RunTransfer(ctx, payload, FlowOptions{
		InitiatorCid:    100,
		MetricsInterval: 2 * time.Millisecond,
		Verify:          true,
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if !res.Verified {
		t.Fatalf("payload mismatch: got %d bytes, want %d", res.BytesTransferred, len(payload))
	}

	return n.Link("sender", "receiver").Stats().PacketsOffered,
		n.Link("receiver", "sender").Stats().PacketsOffered
}
