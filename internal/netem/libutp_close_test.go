//go:build cgo

package netem

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// A transfer that completed must not end in a connection reset for the peer.
//
// libutp finishes sending and closes, sending a FIN. We acknowledge it,
// deliver the end of stream and tear down. If that acknowledgement is lost,
// libutp retransmits the FIN to a connection this socket no longer has,
// which must be acknowledged again rather than answered with a RESET; a
// RESET would make libutp report UTP_ECONNRESET for a transfer that arrived
// whole. Run on a 3%-loss path with reordering and jitter.
func TestLibutpCleanCloseSurvivesLostFinAck(t *testing.T) {
	payload := make([]byte, 128*1024)
	for i := range payload {
		payload[i] = byte(i * 31)
	}

	// Seed 3 is not arbitrary: it loses the acknowledgement of libutp's FIN
	// reproducibly, which is what makes this a gate rather than a lottery.
	const seed = 3
	n := NewNetwork(seed)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{
		Delay: 40 * time.Millisecond, Jitter: 10 * time.Millisecond,
		BandwidthBps: 5_000_000, QueueBytes: 16 * 1024,
		LossRate: 0.03, ReorderRate: 0.02,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_, got, err := libutpToGo(ctx, n, a, b, payload, 7400)
	resets := lastReceiverResets.Load()

	if err != nil {
		t.Fatalf("libutp reported the transfer failed: %v (this side sent %d RESETs; "+
			"receiver read: %v)", err, resets, lastReceiverReadErr.Load())
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("%d of %d bytes and they do not match", len(got), len(payload))
	}
	if resets != 0 {
		t.Errorf("this socket sent %d RESETs during a transfer that completed; a peer "+
			"retransmitting a FIN whose acknowledgement was lost must be acknowledged "+
			"again, not told the connection is gone", resets)
	}
}
