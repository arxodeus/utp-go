package utp

import (
	"testing"
	"time"
)

// An acknowledgement number this connection cannot match acknowledges
// nothing, and that is all it does: it does not reset the connection. The
// acknowledgement-number check already drops anything acknowledging a packet
// never sent, so what reaches here is a stale acknowledgement, within the
// three-packet tolerance below the last sequence number sent but below
// everything being tracked. A delayed or duplicated ST_STATE early in a
// connection is exactly that. Both behaviors answer with silence, so the
// test checks what the connection does next.
func TestStaleAckIsIgnoredNotFatal(t *testing.T) {
	const syn = uint16(100)
	const synAck = uint16(101)

	conn := CreateTestConnection(endpointInfo{Type: acceptor, SynNum: syn, SynAck: synAck})

	congestionCtrl := newDefaultController(fromConnConfig(conn.config))
	sentPackets := newSentPacketsWithoutLogger(synAck, congestionCtrl)
	now := time.Now()
	sentPackets.OnTransmit(synAck+1, st_data, []byte{0xef}, 64, now)

	conn.state = &connState{
		stateType:   connConnected,
		SentPackets: sentPackets,
		SendBuf:     newSendBuffer(TEST_BUFFER_SIZE),
		RecvBuf:     newReceiveBuffer(TEST_BUFFER_SIZE, syn),
	}

	// Two sequence numbers behind the connection's first: inside the window
	// invalidAckNum tolerates, outside everything sentPackets tracks. That
	// gap is the only way to reach this branch.
	stale := newPacketBuilder(st_state, conn.cid.Send, uint32(now.UnixMicro()),
		DefaultWindowSize, syn+1).WithAckNum(synAck - 2).Build()
	if conn.invalidAckNum(stale) {
		t.Fatal("the pre-filter rejected this packet, so the branch under test is never reached")
	}

	conn.onPacket(stale, now)

	if conn.state.stateType != connConnected {
		t.Errorf("the connection is %v after a stale acknowledgement, want %v",
			connStateName(conn.state.stateType), connStateName(connConnected))
	}
	if conn.state.Err != nil {
		t.Errorf("the connection recorded %v; libutp records nothing at all", conn.state.Err)
	}
	if !conn.state.SentPackets.HasUnackedPackets() {
		t.Error("the outstanding packet was treated as acknowledged; a stale " +
			"acknowledgement acknowledges nothing")
	}

	// A real acknowledgement for the outstanding packet is still acted on.
	good := newPacketBuilder(st_state, conn.cid.Send, uint32(now.UnixMicro()),
		DefaultWindowSize, syn+2).WithAckNum(synAck + 1).Build()
	conn.onPacket(good, now.Add(10*time.Millisecond))

	if conn.state.SentPackets.HasUnackedPackets() {
		t.Error("the following acknowledgement was not acted on; the connection " +
			"did not survive the stale one intact")
	}
}

// Anything acknowledging a packet never sent is dropped before reaching the
// branch above, which is why only stale acknowledgements get there.
func TestAckForAPacketNeverSentIsDropped(t *testing.T) {
	const syn = uint16(100)
	const synAck = uint16(101)

	conn := CreateTestConnection(endpointInfo{Type: acceptor, SynNum: syn, SynAck: synAck})

	congestionCtrl := newDefaultController(fromConnConfig(conn.config))
	sentPackets := newSentPacketsWithoutLogger(synAck, congestionCtrl)
	now := time.Now()
	sentPackets.OnTransmit(synAck+1, st_data, []byte{0xef}, 64, now)

	conn.state = &connState{
		stateType:   connConnected,
		SentPackets: sentPackets,
		SendBuf:     newSendBuffer(TEST_BUFFER_SIZE),
		RecvBuf:     newReceiveBuffer(TEST_BUFFER_SIZE, syn),
	}

	forged := newPacketBuilder(st_state, conn.cid.Send, uint32(now.UnixMicro()),
		DefaultWindowSize, syn+1).WithAckNum(synAck + 30000).Build()

	if !conn.invalidAckNum(forged) {
		t.Fatal("an acknowledgement 30000 packets ahead of anything sent was accepted")
	}

	conn.onPacket(forged, now)

	if conn.state.stateType != connConnected {
		t.Errorf("the connection is %v after a forged acknowledgement, want %v",
			connStateName(conn.state.stateType), connStateName(connConnected))
	}
	if !conn.state.SentPackets.HasUnackedPackets() {
		t.Error("a forged acknowledgement acknowledged a packet")
	}
}

// After CloseRead the acknowledgement number still advances and the
// advertised window does not shrink, so a peer whose data is being discarded
// can finish sending and closing.
func TestReadShutdownAcksWithoutBuffering(t *testing.T) {
	const syn = uint16(100)
	const synAck = uint16(101)

	conn := CreateTestConnection(endpointInfo{Type: acceptor, SynNum: syn, SynAck: synAck})
	congestionCtrl := newDefaultController(fromConnConfig(conn.config))
	conn.state = &connState{
		stateType:   connConnected,
		SentPackets: newSentPacketsWithoutLogger(synAck, congestionCtrl),
		SendBuf:     newSendBuffer(TEST_BUFFER_SIZE),
		RecvBuf:     newReceiveBuffer(TEST_BUFFER_SIZE, syn),
	}

	availableBefore := conn.state.RecvBuf.Available()
	conn.onCloseRead()

	payload := make([]byte, 512)
	for i := 0; i < 8; i++ {
		if err := conn.onData(syn+1+uint16(i), payload); err != nil {
			t.Fatalf("packet %d: %v", i, err)
		}
	}

	if got, want := conn.state.RecvBuf.AckNum(), syn+8; got != want {
		t.Errorf("acknowledgement number is %d after eight packets, want %d: "+
			"a discarded packet still has to be acknowledged", got, want)
	}
	if got := conn.state.RecvBuf.Available(); got != availableBefore {
		t.Errorf("the receive window shrank from %d to %d; discarded data must not "+
			"occupy it, or the peer is throttled and eventually stalled",
			availableBefore, got)
	}
	if got := conn.state.RecvBuf.Readable(); got != 0 {
		t.Errorf("%d bytes are readable after the read side closed", got)
	}
}

// Out-of-order packets still reorder correctly when discarded: the sequence
// number is recorded even though the bytes are not.
func TestReadShutdownStillReorders(t *testing.T) {
	const syn = uint16(100)
	const synAck = uint16(101)

	conn := CreateTestConnection(endpointInfo{Type: acceptor, SynNum: syn, SynAck: synAck})
	congestionCtrl := newDefaultController(fromConnConfig(conn.config))
	conn.state = &connState{
		stateType:   connConnected,
		SentPackets: newSentPacketsWithoutLogger(synAck, congestionCtrl),
		SendBuf:     newSendBuffer(TEST_BUFFER_SIZE),
		RecvBuf:     newReceiveBuffer(TEST_BUFFER_SIZE, syn),
	}
	conn.onCloseRead()

	payload := make([]byte, 128)
	// The gap first.
	if err := conn.onData(syn+2, payload); err != nil {
		t.Fatal(err)
	}
	if got, want := conn.state.RecvBuf.AckNum(), syn; got != want {
		t.Fatalf("acknowledgement number moved to %d on an out-of-order packet, want %d",
			got, want)
	}
	// Then what fills it: both should now be acknowledged.
	if err := conn.onData(syn+1, payload); err != nil {
		t.Fatal(err)
	}
	if got, want := conn.state.RecvBuf.AckNum(), syn+2; got != want {
		t.Errorf("acknowledgement number is %d after the gap was filled, want %d", got, want)
	}
	if got := conn.state.RecvBuf.Readable(); got != 0 {
		t.Errorf("%d bytes are readable after the read side closed", got)
	}
}
