package netem

import (
	"context"
	"testing"
	"time"
)

// TestAckCoalescingUnderLoad measures how many acknowledgements the receiver
// puts on the return path per data packet it receives, on a real transfer.
//
// The connection defers acks -- connection.ackPending is set when data arrives
// and flushAck sends one once the read that brought the data has been handed
// out, matching libutp's schedule_ack / utp_issue_deferred_acks
// (utp_internal.cpp:2377, :3796-3808) -- and lets one wait to cover a few
// packets only once its acknowledgements have been seen to queue on the way
// back (connection.ackEvery). Where the return path keeps up, it acknowledges
// as libutp does; this network hands each datagram to the receiver on its
// own, so that is about one acknowledgement per data packet.
//
// So the assertion is on the path that cannot keep up: 20 Mb/s of data
// draws about 1,750 acknowledgements a second, and 160 kb/s carries about
// 1,000. Measured 0.51-0.52 acknowledgements per data packet there, three
// runs, and 1.00 with ackEvery returning 1. The symmetric rows are reported
// only: 0.84, 1.00 and 0.94 at 20 Mb/s, 100 Mb/s and 1 Gb/s.
func TestAckCoalescingUnderLoad(t *testing.T) {
	cases := []struct {
		name     string
		bps      uint64
		backBps  uint64 // 0: the same as bps
		delay    time.Duration
		maxRatio float64 // 0 means "report only, do not assert"
	}{
		{name: "20Mbps/10ms, 160kbps back", bps: 20_000_000, backBps: 160_000, delay: 10 * time.Millisecond, maxRatio: 0.75},
		{name: "20Mbps/10ms", bps: 20_000_000, delay: 10 * time.Millisecond},
		{name: "100Mbps/1ms", bps: 100_000_000, delay: time.Millisecond},
		{name: "1Gbps/1ms", bps: 1_000_000_000, delay: time.Millisecond},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, acks := ackRatioTransfer(t, tc.bps, tc.backBps, tc.delay)
			ratio := float64(acks) / float64(data)
			t.Logf("%d data packets drew %d acks (%.3f per data packet)", data, acks, ratio)

			if acks == 0 {
				t.Fatal("no acks on the return path at all; the measurement is not measuring what it thinks")
			}
			if tc.maxRatio == 0 {
				return
			}
			if ratio > tc.maxRatio {
				t.Errorf("%.3f acks per data packet, want at most %.2f; acknowledgements are "+
					"not coalescing on a return path that cannot carry them", ratio, tc.maxRatio)
			}
		})
	}
}

// ackRatioTransfer runs one verified transfer and returns the number of packets
// offered to the forward link and to the return link. The forward link carries
// data, the return link carries acks; neither link loses anything here, so
// "offered" is what was sent.
func ackRatioTransfer(t *testing.T, bps, backBps uint64, delay time.Duration) (dataPackets, ackPackets uint64) {
	t.Helper()

	n := NewNetwork(21)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	fwd := Config{Delay: delay, BandwidthBps: bps, QueueBytes: 64 * 1024}
	back := fwd
	if backBps != 0 {
		back.BandwidthBps, back.QueueBytes = backBps, 8*1024
	}
	n.ConnectAsymmetric(a, b, fwd, back)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, quiet())

	payload := make([]byte, 2<<20)
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
