package utp

import (
	"context"
	"testing"
	"time"
)

// Once everything a sender sent is acknowledged, no loss probe stays armed,
// and the connection rests without waiting for a timer, including when the
// socket's reader handled the last acknowledgement.
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
	// Promptly, not when some unrelated timer runs a pass.
	awaitRest(t, 300*time.Millisecond, client)
}
