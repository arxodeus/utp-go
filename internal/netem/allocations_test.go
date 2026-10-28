package netem

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// TestAllocationsPerPacket bounds how much a bulk transfer allocates for each
// packet that crosses the link, data and acknowledgement alike.
//
// It was 13.2, everything in the process counted: the sender, the receiver,
// the emulated network and this harness. The library encoded every packet
// into a new slice, boxed every delay sample, gave each packet's
// retransmission timer three objects, copied the application's bytes twice
// on the way out and every datagram whole on the way in, and handed received
// data to the reader in a new buffer and result each time. Now about 5.3,
// nearly half of it the emulated network's own (profiled). In a process this
// small the collector's cost follows these: see KNOWN-LIMITATIONS.md, "An idle
// connection held a megabyte". The bound sits between the two.
func TestAllocationsPerPacket(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector allocates on its own account")
	}
	const perPacketMax = 8.0
	n := NewNetwork(101)
	defer n.Close()
	a := n.MustAddEndpoint("sender")
	b := n.MustAddEndpoint("receiver")
	n.Connect(a, b, Config{Delay: time.Millisecond, BandwidthBps: 100_000_000, QueueBytes: 256 * 1024})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, quiet())
	payload := make([]byte, 4<<20)

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := pair.RunTransfer(ctx, payload, FlowOptions{InitiatorCid: 100, Verify: true}); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)

	packets := n.Link("sender", "receiver").Stats().PacketsOffered +
		n.Link("receiver", "sender").Stats().PacketsOffered
	perPacket := float64(after.Mallocs-before.Mallocs) / float64(packets)
	t.Logf("%.2f allocations per packet over %d packets, %.0f bytes",
		perPacket, packets, float64(after.TotalAlloc-before.TotalAlloc)/float64(packets))
	if perPacket > perPacketMax {
		t.Fatalf("%.2f allocations per packet, expected at most %.0f", perPacket, perPacketMax)
	}
}
