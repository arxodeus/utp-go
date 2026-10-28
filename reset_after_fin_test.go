package utp

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

// A RESET that arrives after everything the peer sent, its FIN included,
// takes nothing back: the reader still gets every byte and a clean end of
// stream. A writer is told the connection was reset. The side that closed
// first destroys its connection once its FIN is acknowledged and answers the
// next packet with a RESET, so this is routine.
func TestResetAfterTheFinTakesNothingBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const cid = 4400
	client, server := writevPair(t, ctx, cid)

	// More than the read queue holds, so some is still in the receive
	// buffer when the reset arrives, as with a reader that has fallen
	// behind.
	payload := bytes.Repeat([]byte("after the fin "), 64*1024)
	if _, err := client.Write(ctx, payload); err != nil {
		t.Fatal(err)
	}
	client.Close()

	// Wait until the server has everything, FIN included, unread.
	deadline := time.Now().Add(10 * time.Second)
	for {
		server.conn.mu.Lock()
		through := server.conn.receivedThroughFin()
		server.conn.mu.Unlock()
		if through {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server never received through the FIN")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The client's socket resets the server's connection, as when the
	// server's next packet reaches a connection already gone. Its ack
	// number is the packet that drew it, the server's latest.
	server.conn.mu.Lock()
	lastSent := server.conn.state.SentPackets.NextSeqNum() - 1
	server.conn.mu.Unlock()
	reset := newPacketBuilder(stReset, cid+1, 0, 0, randomUint16()).WithAckNum(lastSent).Build()
	client.conn.out(newOutgoingSocketEvent(reset, client.conn.cid.Peer))
	// The connection ends once its reader has taken what it holds; wait for
	// the reset to land first.
	deadline = time.Now().Add(10 * time.Second)
	for {
		server.conn.mu.Lock()
		closed := server.conn.state.stateType == connClosed
		server.conn.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the reset did not close the server's connection")
		}
		time.Sleep(5 * time.Millisecond)
	}

	var got []byte
	n, err := server.ReadToEOF(ctx, &got)
	if err != nil {
		t.Fatalf("read %d of %d bytes, then %v", n, len(payload), err)
	}
	if !bytes.Equal(got[:n], payload) {
		t.Fatalf("read %d bytes, not the %d sent", n, len(payload))
	}
	if _, err := server.Write(ctx, []byte("x")); !errors.Is(err, ErrReset) {
		t.Fatalf("a write after the reset returned %v, want ErrReset", err)
	}
}
