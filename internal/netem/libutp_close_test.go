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
// The failure this pins: libutp finishes sending and closes, so it sends a
// FIN. We receive it, acknowledge it, hand the application its end of stream,
// and tear the connection down. If that acknowledgement is lost -- ordinary on
// a lossy path -- libutp retransmits the FIN, and by then the connection is
// gone from this socket's table, so the retransmission is answered with a
// RESET. libutp reports UTP_ECONNRESET on a transfer that in fact arrived
// whole.
//
// libutp cannot do this to us. It holds the socket in CS_GOT_FIN until its own
// application closes, so a retransmitted FIN is simply acknowledged again.
// This is the concrete cost of the half-close deviation in DEVIATIONS.md,
// which until now had none recorded.
//
// Measured before the fix, on a 3%-loss path with reordering and jitter: 4 of
// 20 seeds ended this way, and seed 3 ended this way 5 times out of 5. Nothing
// was lost from the transfer itself -- the receiving side read all 131072
// bytes without error every time -- so this is purely a failure of the close,
// and it is the peer that is told the connection broke.
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
