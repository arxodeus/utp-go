package utp_go

import (
	"testing"
	"time"
)

// An acknowledgement number this connection cannot match acknowledges
// nothing, and that is all it does.
//
// libutp computes how many packets an acknowledgement covers and discards the
// answer when it exceeds what is outstanding:
//
//	int acks = (pk_ack_nr - (conn->seq_nr - 1 - conn->cur_window_packets)) & ACK_NR_MASK;
//	// this happens when we receive an old ack nr
//	if (acks > conn->cur_window_packets) acks = 0;
//	                                        (utp_internal.cpp:1904-1907)
//
// This implementation used to reset the connection instead. CONFORMANCE.md
// recorded it as a divergence the packet-injection corpus cannot see: both
// sides answer such a packet with silence, so the transcripts agree while one
// connection is dead and the other is alive. That is why this test looks at
// what the connection does next rather than at what it emitted.
//
// The case is ordinary, not exotic. `invalidAckNum` already implements
// libutp's own pre-filter (utp_internal.cpp:1794-1807), which drops anything
// acking a packet never sent, so what reaches here is a *stale*
// acknowledgement -- one still inside libutp's three-packet tolerance below
// the last sequence number sent, but below everything this connection is
// tracking. A delayed or duplicated ST_STATE early in a connection is exactly
// that, and it used to be fatal.
func TestStaleAckIsIgnoredNotFatal(t *testing.T) {
	const syn = uint16(100)
	const synAck = uint16(101)

	conn := CreateTestConnection(Endpoint{Type: Acceptor, SynNum: syn, SynAck: synAck})

	congestionCtrl := newDefaultController(fromConnConfig(conn.config))
	sentPackets := newSentPacketsWithoutLogger(synAck, congestionCtrl)
	now := time.Now()
	sentPackets.OnTransmit(synAck+1, st_data, []byte{0xef}, 64, now)

	conn.state = &ConnState{
		stateType:   ConnConnected,
		SentPackets: sentPackets,
		SendBuf:     newSendBuffer(TEST_BUFFER_SIZE),
		RecvBuf:     newReceiveBuffer(TEST_BUFFER_SIZE, syn),
	}

	// Two sequence numbers behind the connection's first: inside the window
	// invalidAckNum tolerates, outside everything sentPackets tracks. That
	// gap is the only way to reach this branch at all, which is worth
	// stating -- a test that could not reach it would prove nothing.
	stale := NewPacketBuilder(st_state, conn.cid.Send, uint32(now.UnixMicro()),
		DefaultWindowSize, syn+1).WithAckNum(synAck - 2).Build()
	if conn.invalidAckNum(stale) {
		t.Fatal("the pre-filter rejected this packet, so the branch under test is never reached")
	}

	conn.onPacket(stale, now)

	if conn.state.stateType != ConnConnected {
		t.Errorf("the connection is %v after a stale acknowledgement, want %v",
			connStateName(conn.state.stateType), connStateName(ConnConnected))
	}
	if conn.state.Err != nil {
		t.Errorf("the connection recorded %v; libutp records nothing at all", conn.state.Err)
	}
	if !conn.state.SentPackets.HasUnackedPackets() {
		t.Error("the outstanding packet was treated as acknowledged; a stale " +
			"acknowledgement acknowledges nothing")
	}

	// And it still works: a real acknowledgement for the packet that is
	// genuinely outstanding is still acted on.
	good := NewPacketBuilder(st_state, conn.cid.Send, uint32(now.UnixMicro()),
		DefaultWindowSize, syn+2).WithAckNum(synAck + 1).Build()
	conn.onPacket(good, now.Add(10*time.Millisecond))

	if conn.state.SentPackets.HasUnackedPackets() {
		t.Error("the following acknowledgement was not acted on; the connection " +
			"did not survive the stale one intact")
	}
}

// The other half of the rule, and the reason the branch above is only ever
// reached by stale acknowledgements: anything acking a packet that was never
// sent is dropped before it gets there.
//
// libutp is unusually explicit about why -- "This would imply a spoofed
// address or a malicious attempt to attach the uTP implementation"
// (utp_internal.cpp:1794-1807) -- and dropping is all it does. Pinned here so
// that relaxing the reset above cannot quietly become an open door.
func TestAckForAPacketNeverSentIsDropped(t *testing.T) {
	const syn = uint16(100)
	const synAck = uint16(101)

	conn := CreateTestConnection(Endpoint{Type: Acceptor, SynNum: syn, SynAck: synAck})

	congestionCtrl := newDefaultController(fromConnConfig(conn.config))
	sentPackets := newSentPacketsWithoutLogger(synAck, congestionCtrl)
	now := time.Now()
	sentPackets.OnTransmit(synAck+1, st_data, []byte{0xef}, 64, now)

	conn.state = &ConnState{
		stateType:   ConnConnected,
		SentPackets: sentPackets,
		SendBuf:     newSendBuffer(TEST_BUFFER_SIZE),
		RecvBuf:     newReceiveBuffer(TEST_BUFFER_SIZE, syn),
	}

	forged := NewPacketBuilder(st_state, conn.cid.Send, uint32(now.UnixMicro()),
		DefaultWindowSize, syn+1).WithAckNum(synAck + 30000).Build()

	if !conn.invalidAckNum(forged) {
		t.Fatal("an acknowledgement 30000 packets ahead of anything sent was accepted")
	}

	conn.onPacket(forged, now)

	if conn.state.stateType != ConnConnected {
		t.Errorf("the connection is %v after a forged acknowledgement, want %v",
			connStateName(conn.state.stateType), connStateName(ConnConnected))
	}
	if !conn.state.SentPackets.HasUnackedPackets() {
		t.Error("a forged acknowledgement acknowledged a packet")
	}
}
