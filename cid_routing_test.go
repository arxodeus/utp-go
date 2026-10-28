package utp

import (
	"context"
	"sync"
	"testing"
	"time"
)

// awaitSyn waits for our socket to emit a SYN on conn and returns it.
func awaitSyn(t *testing.T, conn *scriptedConn) *packet {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, raw := range conn.takeEmitted() {
			if p, err := decodePacket(raw, false); err == nil && p.Header.PacketType == stSyn {
				return p
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no SYN was sent")
	return nil
}

// A packet is delivered to the connection whose receive id it carries, and
// only to that one. Each side sends with its send id, the other's receive
// id. Two connections to one peer with adjacent ids, one receiving on R and
// sending on R+1, the next receiving on R+1, share a number; a packet for the
// second must not match the first. Here that packet is the second
// connection's SYN-ACK.
func TestPacketReachesTheConnectionItIsFor(t *testing.T) {
	const (
		recv     = 7000
		peerConn = 300
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn := newScriptedConn()
	sock := NewSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	connect := func(cid *ConnectionID) chan error {
		done := make(chan error, 1)
		go func() {
			_, err := sock.ConnectWithCID(ctx, cid, NewConnectionConfig())
			done <- err
		}()
		return done
	}
	answer := func(syn *packet) {
		// The SYN carries our receive id; the answer comes back on it.
		conn.inject(newPacketBuilder(stState, syn.Header.ConnectionID,
			uint32(time.Now().UnixMicro()), 1<<20, peerConn).
			WithAckNum(syn.Header.SeqNum).Build().Encode())
	}

	first := connect(NewConnectionID(conn.peer, recv, recv+1))
	answer(awaitSyn(t, conn))
	if err := <-first; err != nil {
		t.Fatalf("first connection: %v", err)
	}

	second := connect(NewConnectionID(conn.peer, recv+1, recv+2))
	answer(awaitSyn(t, conn))
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("second connection: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the second connection's SYN-ACK did not reach it")
	}
}

// Receive ids are unique per peer, whichever side opened the connection, so
// that a packet's id names one connection. Two rules keep them so.

// A SYN whose connection would receive on an id that a connection we dialed
// already receives on is refused without an answer.
func TestSynForATakenReceiveIDIsRefused(t *testing.T) {
	const recv = 7000
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn := newScriptedConn()
	sock := NewSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	go sock.ConnectWithCID(ctx, NewConnectionID(conn.peer, recv, recv+1), NewConnectionConfig())
	awaitSyn(t, conn)
	before := sock.NumConnections()

	// The peer dials us with id recv-1: the connection would receive on recv.
	conn.inject(newPacketBuilder(stSyn, recv-1, uint32(time.Now().UnixMicro()), 1<<20, 500).Build().Encode())
	deadline := time.Now().Add(2 * time.Second)
	for sock.ConnectionsRefusedIDInUse() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := sock.ConnectionsRefusedIDInUse(); got != 1 {
		t.Fatalf("%d SYNs refused for a receive id in use, expected 1", got)
	}
	if n := sock.NumConnections(); n != before {
		t.Fatalf("%d connections after the refused SYN, %d before", n, before)
	}
	for _, raw := range conn.takeEmitted() {
		if p, err := decodePacket(raw, false); err == nil && p.Header.ConnectionID == recv-1 {
			t.Fatalf("the refused SYN was answered: %s", describePackets([][]byte{raw}))
		}
	}
}

// A connection we dial does not take a receive id an accepted one already
// receives on.
func TestDialledConnectionAvoidsAReceiveIDInUse(t *testing.T) {
	const taken = 7000
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn := newScriptedConn()
	sock := NewSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	// An accepted connection receiving on taken: its SYN carried taken-1.
	accepted := make(chan error, 1)
	go func() {
		_, err := sock.Accept(ctx, NewConnectionConfig())
		accepted <- err
	}()
	conn.inject(newPacketBuilder(stSyn, taken-1, uint32(time.Now().UnixMicro()), 1<<20, 500).Build().Encode())
	if err := <-accepted; err != nil {
		t.Fatalf("accept: %v", err)
	}

	// The first draw is the taken id, the second a free one.
	draws := []uint16{taken, taken + 100}
	var mu sync.Mutex
	next := func() uint16 {
		mu.Lock()
		defer mu.Unlock()
		v := draws[0]
		if len(draws) > 1 {
			draws = draws[1:]
		}
		return v
	}
	prev := randomUint16Source.Load()
	randomUint16Source.Store(&next)
	cid, err := sock.generateCid(conn.peer, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	randomUint16Source.Store(prev)
	if cid.Recv == taken {
		t.Fatalf("a dialled connection was given receive id %d, which an accepted one already has", taken)
	}
}

// A connection that has just ended does not lend its ids to the next one to
// the same peer, or whatever was still in flight for it, here a RESET from
// the peer, would reach the new connection.
func TestClosedConnectionIdsAreNotReusedAtOnce(t *testing.T) {
	const recv = 7000
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn := newScriptedConn()
	sock := NewSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	done := make(chan *Stream, 1)
	go func() {
		s, _ := sock.ConnectWithCID(ctx, NewConnectionID(conn.peer, recv, recv+1), NewConnectionConfig())
		done <- s
	}()
	syn := awaitSyn(t, conn)
	conn.inject(newPacketBuilder(stState, syn.Header.ConnectionID, uint32(time.Now().UnixMicro()), 1<<20, 300).
		WithAckNum(syn.Header.SeqNum).Build().Encode())
	if <-done == nil {
		t.Fatal("did not connect")
	}
	// The peer resets it, and it is gone.
	conn.inject(newPacketBuilder(stReset, recv, 0, 0, 301).WithAckNum(syn.Header.SeqNum).Build().Encode())
	deadline := time.Now().Add(5 * time.Second)
	for sock.NumConnections() > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n := sock.NumConnections(); n != 0 {
		t.Fatalf("%d connections after the reset", n)
	}

	draws := []uint16{recv, recv + 100}
	var mu sync.Mutex
	next := func() uint16 {
		mu.Lock()
		defer mu.Unlock()
		v := draws[0]
		if len(draws) > 1 {
			draws = draws[1:]
		}
		return v
	}
	prev := randomUint16Source.Load()
	randomUint16Source.Store(&next)
	cid, err := sock.generateCid(conn.peer, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	randomUint16Source.Store(prev)
	if cid.Recv == recv {
		t.Fatalf("receive id %d reused the moment the connection holding it ended", recv)
	}
}

// A dialed connection does not send on an id another connection to the peer
// receives on: a RESET is matched on the receive id first, so the other
// connection would take its RESETs.
func TestDialledConnectionDoesNotSendOnAnIDInUse(t *testing.T) {
	const recv = 7000
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn := newScriptedConn()
	sock := NewSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	// A connection receiving on recv, sending on recv+1.
	go sock.ConnectWithCID(ctx, NewConnectionID(conn.peer, recv, recv+1), NewConnectionConfig())
	awaitSyn(t, conn)

	// The first draw would receive on recv-1 and send on recv.
	draws := []uint16{recv - 1, recv + 100}
	var mu sync.Mutex
	next := func() uint16 {
		mu.Lock()
		defer mu.Unlock()
		v := draws[0]
		if len(draws) > 1 {
			draws = draws[1:]
		}
		return v
	}
	prev := randomUint16Source.Load()
	randomUint16Source.Store(&next)
	cid, err := sock.generateCid(conn.peer, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	randomUint16Source.Store(prev)
	if cid.Send == recv {
		t.Fatalf("a dialled connection sends on %d, which another connection to the peer receives on", recv)
	}
}
