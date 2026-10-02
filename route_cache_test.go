package utp_go

import (
	"context"
	"testing"
	"time"
)

// The reader remembers the last datagram's route. Every change to the routing
// tables must invalidate it: a connection registered, removed, or attached for
// inline handling. A stale route would hand a removed connection's packets to
// it, or keep sending a connection's packets through its channel after it was
// attached.
func TestRouteCacheFollowsTheTables(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := newScriptedConn()
	defer conn.Close()
	sock := WithSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	const id = 700
	pkt, err := DecodePacket(NewPacketBuilder(st_data, id, 0, 1<<20, 1).
		WithPayload([]byte("x")).Build().Encode())
	if err != nil {
		t.Fatal(err)
	}
	peerKey := conn.peer.Hash()
	key := string(appendConnKey(nil, id-1, id, peerKey))
	route := func() (chan *streamEvent, *connection) {
		sock.dispatchMu.Lock()
		defer sock.dispatchMu.Unlock()
		_, _, ch, c := sock.route(pkt, peerKey)
		return ch, c
	}

	first := make(chan *streamEvent, 1)
	sock.putConnStream(key, first)
	if ch, _ := route(); ch != first {
		t.Fatal("a registered connection was not found")
	}

	sock.removeConnStream(key)
	if ch, _ := route(); ch != nil {
		t.Fatal("a removed connection was still routed to")
	}

	second := make(chan *streamEvent, 1)
	sock.putConnStream(key, second)
	if ch, _ := route(); ch != second {
		t.Fatal("a connection registered under a key just freed was not found")
	}

	// Registered over a route still remembered: a key reused before the
	// old entry's removal reached the tables.
	third := make(chan *streamEvent, 1)
	sock.putConnStream(key, third)
	if ch, _ := route(); ch != third {
		t.Fatal("a connection registered over a remembered route was not found")
	}

	inline := &connection{}
	sock.dispatchMu.Lock()
	sock.attachInline(key, third, inline)
	sock.dispatchMu.Unlock()
	if _, c := route(); c != inline {
		t.Fatal("a connection attached for inline handling was still reached through its channel")
	}
}

// An initiator's SYN keeps its retransmission timer until an acknowledgement
// after the handshake retires it. On a connection that only receives, every
// such acknowledgement names the SYN and nothing new -- the case the
// acknowledgement fast path takes -- so the fast path must still retire it.
func TestReceiverRetiresTheSynTimer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	defer pinRandom(4200)()

	conn := newScriptedConn()
	defer conn.Close()
	sock := WithSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	cid := NewConnectionId(conn.peer, 4200, 4201)
	streamCh := make(chan *UtpStream, 1)
	go func() {
		if stream, err := sock.ConnectWithCid(ctx, cid, NewConnectionConfig()); err == nil {
			streamCh <- stream
		}
	}()
	waitFor(t, 2*time.Second, func() bool { return conn.emittedCount() > 0 })
	conn.inject(NewPacketBuilder(st_state, 4200, 150000, 1<<20, 900).
		WithAckNum(4200).Build().Encode())
	var stream *UtpStream
	select {
	case stream = <-streamCh:
	case <-time.After(5 * time.Second):
		t.Fatal("connect never completed")
	}

	conn.inject(NewPacketBuilder(st_data, 4200, 160000, 1<<20, 900).
		WithAckNum(4200).WithPayload([]byte("data")).Build().Encode())
	conn.settle()

	c := stream.conn
	c.mu.Lock()
	armed := len(c.armed)
	c.mu.Unlock()
	if armed != 0 {
		t.Fatalf("%d retransmission timer(s) still armed on a connection that has sent no data; "+
			"the SYN's timer was not retired", armed)
	}
}
