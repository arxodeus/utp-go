package utp

import (
	"context"
	"testing"
	"time"
)

// Once everything a sender sent is acknowledged, no loss probe stays armed,
// and the connection rests without waiting for a timer. A probe left armed
// fired to find nothing outstanding, and until it did the connection could
// not rest: about 10 ms here, and past 400 ms on Windows CI, failing
// TestRestingConnectionIdleTimeout. Stopping it alone left a sender whose
// last acknowledgement the socket's reader handled with no reason to wake
// its loop at all, and it never rested.
func TestSenderRestsOnceEverythingIsAcknowledged(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, client, server := restingPair(t, ctx, NewConnectionConfig())
	defer client.Close()
	defer server.Close()

	exchange(t, ctx, client, server, 1024)
	c := client.conn
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		acked := len(c.armed) == 0 && !c.state.SentPackets.HasUnackedPackets()
		probe := c.loopTimers.lossProbe.pending
		c.mu.Unlock()
		if acked {
			if probe {
				t.Fatal("a loss probe is armed with nothing outstanding")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the sender's data was never acknowledged")
		}
		time.Sleep(100 * time.Microsecond)
	}
	// Promptly: without the wake it took about a second, until an
	// unrelated timer happened to run a pass.
	awaitRest(t, 300*time.Millisecond, client)
}
