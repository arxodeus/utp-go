package utp

import (
	"testing"
	"time"
)

// incomingWindowConn is a connected acceptor with one packet outstanding.
func incomingWindowConn(t *testing.T) (*connection, time.Time) {
	t.Helper()
	const syn = uint16(100)
	const synAck = uint16(101)
	conn := CreateTestConnection(endpointInfo{Type: acceptor, SynNum: syn, SynAck: synAck})
	sentPackets := newSentPacketsWithoutLogger(synAck, newDefaultController(fromConnConfig(conn.config)))
	now := time.Now()
	sentPackets.OnTransmit(synAck+1, stData, []byte{0xef}, 64, now)
	conn.state = &connState{
		stateType:   connConnected,
		SentPackets: sentPackets,
		SendBuf:     newSendBuffer(testBufferSize),
		RecvBuf:     newReceiveBuffer(testBufferSize, syn),
	}
	conn.peerRecvWindow = 1 << 20
	return conn, now
}

// A discarded packet does not change what we think the peer's window is. The
// window is read only after the acknowledgement-number and reorder-window
// checks, so a forged packet cannot close it.
func TestDiscardedPacketDoesNotSetThePeerWindow(t *testing.T) {
	conn, now := incomingWindowConn(t)
	const syn = uint16(100)
	// Acknowledges a packet never sent, so it is dropped.
	forged := newPacketBuilder(stState, conn.cid.Send, uint32(now.UnixMicro()), 0, syn+1).
		WithAckNum(101 + 50).Build()
	if !conn.invalidAckNum(forged) {
		t.Fatal("the pre-filter accepts this packet, so the test does not reach the case")
	}
	conn.onPacket(forged, now)
	if conn.peerRecvWindow != 1<<20 {
		t.Errorf("a packet dropped for its acknowledgement number set the peer window to %d", conn.peerRecvWindow)
	}
	if !conn.zeroWindowProbeDue.IsZero() {
		t.Error("a packet dropped for its acknowledgement number armed the zero-window probe")
	}

	// The same window on a packet that passes is taken.
	good := newPacketBuilder(stState, conn.cid.Send, uint32(now.UnixMicro()), 0, syn+1).
		WithAckNum(101).Build()
	conn.onPacket(good, now)
	if conn.peerRecvWindow != 0 {
		t.Errorf("a valid packet advertising a zero window left the peer window at %d", conn.peerRecvWindow)
	}
}

// Every packet reporting a zero window restarts the probe's deadline, so the
// probe goes one interval after the last such packet, not the first.
func TestZeroWindowProbeDeadlineRestartsOnEachZeroWindow(t *testing.T) {
	conn, now := incomingWindowConn(t)
	const syn = uint16(100)
	zero := func(at time.Time) {
		conn.onPacket(newPacketBuilder(stState, conn.cid.Send, uint32(at.UnixMicro()), 0, syn+1).
			WithAckNum(101).Build(), at)
	}
	zero(now)
	later := now.Add(10 * time.Second)
	zero(later)
	want := later.Add(conn.zeroWindowProbeIntervalOrDefault())
	if !conn.zeroWindowProbeDue.Equal(want) {
		t.Errorf("probe due %v after zero-window packets at 0s and 10s; libutp's is 15s after the "+
			"second, %v", conn.zeroWindowProbeDue.Sub(now), want.Sub(now))
	}
}

// A FIN waits for room in the window like any packet.
func TestFinWaitsForRoomInTheWindow(t *testing.T) {
	const syn = uint16(100)
	const synAck = uint16(101)
	conn := CreateTestConnection(endpointInfo{Type: acceptor, SynNum: syn, SynAck: synAck})
	ctrl := newDefaultController(fromConnConfig(conn.config))
	ctrl.maxWindowSizeBytes = 1 << 20
	sentPackets := newSentPacketsWithoutLogger(synAck, ctrl)
	now := time.Now()
	for i := uint16(1); i <= 3; i++ {
		sentPackets.OnTransmit(synAck+i, stData, make([]byte, 1000), 1000, now)
	}
	conn.state = &connState{
		stateType:   connConnected,
		SentPackets: sentPackets,
		SendBuf:     newSendBuffer(testBufferSize),
		RecvBuf:     newReceiveBuffer(testBufferSize, syn),
	}
	conn.peerRecvWindow = 1 << 20
	// A loss halves the window below what is in flight.
	ctrl.maxWindowSizeBytes = 1500

	conn.shutdown()
	if conn.state.closing == nil {
		t.Fatal("shutdown did not start closing")
	}
	if conn.state.closing.LocalFin != nil {
		t.Fatalf("the FIN went with 3000 bytes in flight against a 1500-byte window")
	}

	// The window reopens; the next pass sends it.
	ctrl.maxWindowSizeBytes = 1 << 20
	conn.shutdown()
	if conn.state.closing.LocalFin == nil {
		t.Fatal("the FIN did not go once the window had room")
	}
}

// An acknowledgement wakes the writer whichever packet carries it: on a
// connection carrying data both ways, acknowledgements ride on the peer's
// data packets.
func TestAcknowledgementOnADataPacketWakesTheWriter(t *testing.T) {
	for _, pt := range []packetType{stState, stData} {
		t.Run(pt.String(), func(t *testing.T) {
			conn, now := incomingWindowConn(t)
			const syn = uint16(100)
			// Clear any wake left over from setting up.
			conn.wantWrite = false
			b := newPacketBuilder(pt, conn.cid.Send, uint32(now.UnixMicro()), 1<<20, syn+1).WithAckNum(102)
			if pt == stData {
				b = b.WithPayload([]byte("the peer's own data"))
			}
			conn.onPacket(b.Build(), now)
			if conn.state.SentPackets.HasUnackedPackets() {
				t.Fatal("the acknowledgement did not retire the outstanding packet")
			}
			if !conn.wantWrite {
				t.Errorf("an acknowledgement on %s retired a packet and did not wake the writer", pt.String())
			}
		})
	}
}

// No more than 1023 packets in flight, however small.
func TestOutstandingPacketsAreCappedAt1023(t *testing.T) {
	for _, already := range []int{1021, 1022} {
		conn := drainTestConn(t, 0, testBufferSize)
		// Room for three whole packets, so that none is a short tail the
		// Nagle rule would hold back.
		conn.state.SendBuf = newSendBuffer(64 * 1024)
		conn.mtu = newMtuSearch(uint32(conn.config.MaxPacketSize), time.Now())
		p := conn.mtu.payloadSize()
		conn.testWidenCongestionWindow(1 << 30)
		conn.peerRecvWindow = 1 << 30
		now := time.Now()
		for i := 1; i <= already; i++ {
			conn.state.SentPackets.OnTransmit(101+uint16(i), stData, []byte{1}, 1, now)
		}
		sent := sendAndCount(t, conn, make([]byte, 3*p))
		if want := 1023 - already; len(sent) != want {
			t.Errorf("with %d tiny packets in flight and a vast window, %d more went out; "+
				"libutp stops at 1023 in flight, which leaves room for %d",
				already, len(sent), want)
		}
	}
}

// A closed connection answers each queued write once, however many passes
// examine it.
func TestClosedConnectionAnswersEachWriteOnce(t *testing.T) {
	conn, now := incomingWindowConn(t)
	resultCh := make(chan *readOrWriteResult, 1)
	conn.pendingWrites = []*queuedWrite{{data: []byte("unsent"), resultCh: resultCh}}
	conn.state.stateType = connClosed
	conn.state.Err = ErrReset

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn.processWrites(now)
		conn.processWrites(now)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a second pass over a closed connection blocked answering a write already answered")
	}
	if res := <-resultCh; res.Err != ErrReset {
		t.Fatalf("the write was answered with %v, want %v", res.Err, ErrReset)
	}
}
