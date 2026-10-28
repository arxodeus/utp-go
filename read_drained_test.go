package utp

import (
	"testing"
)

// When delivering bytes to the reader opens a receive window larger than the
// one last advertised, the peer is owed an acknowledgement; otherwise no
// extra acknowledgement is sent, since that would double the reverse traffic.

func drainTestConn(t *testing.T, lastAdvertised uint32, bufferSize int) *connection {
	t.Helper()
	const syn = uint16(100)
	const synAck = uint16(101)
	conn := CreateTestConnection(endpointInfo{Type: acceptor, SynNum: syn, SynAck: synAck})
	conn.state = &connState{
		stateType:   connConnected,
		SentPackets: newSentPacketsWithoutLogger(synAck, newDefaultController(fromConnConfig(conn.config))),
		SendBuf:     newSendBuffer(testBufferSize),
		RecvBuf:     newReceiveBuffer(bufferSize, syn),
	}
	conn.lastAdvertisedWindow = lastAdvertised
	conn.ackPending = false
	return conn
}

// A window larger than the one last advertised is owed an acknowledgement.
func TestReadDrainedOwesAnAckWhenTheWindowGrew(t *testing.T) {
	conn := drainTestConn(t, 0, testBufferSize)
	if conn.state.RecvBuf.Available() == 0 {
		t.Fatal("the receive buffer starts with no room; this case would assert nothing")
	}

	conn.onReadDrained()

	if !conn.ackPending {
		t.Error("a window that grew past the one last advertised owes an acknowledgement")
	}
	if conn.readDrainedAcks != 1 {
		t.Errorf("counted %d, expected 1", conn.readDrainedAcks)
	}
}

// A window no larger than the one last advertised owes nothing. The
// acknowledgement for incoming data already carries a current window, since
// the drain happens before it in the same pass.
func TestReadDrainedIsSilentWhenTheWindowDidNotGrow(t *testing.T) {
	buf := newReceiveBuffer(testBufferSize, 100)
	full := uint32(buf.Available())

	for _, advertised := range []uint32{full, full + 1, full * 2} {
		conn := drainTestConn(t, advertised, testBufferSize)
		conn.onReadDrained()
		if conn.ackPending {
			t.Errorf("last advertised %d against %d now free: owed an acknowledgement "+
				"for a window that did not grow", advertised, full)
		}
		if conn.readDrainedAcks != 0 {
			t.Errorf("last advertised %d: counted %d, expected 0",
				advertised, conn.readDrainedAcks)
		}
	}
}

// The test is growth, not growth from zero. A window too small to fit a
// packet blocks the sender as a closed one does, so its reopening must be
// reported too.
func TestReadDrainedCoversASubPacketWindow(t *testing.T) {
	// 861 bytes: less than a packet.
	conn := drainTestConn(t, 861, testBufferSize)
	if conn.state.RecvBuf.Available() <= 861 {
		t.Fatal("the receive buffer is too small for this case to exercise anything")
	}

	conn.onReadDrained()

	if !conn.ackPending {
		t.Error("a window that grew from 861 bytes owes an acknowledgement; a window " +
			"too small to carry a packet blocks the sender exactly as a closed one does")
	}
}

// Nothing is owed on a connection that is not established, where there is no
// peer to tell and no buffer to report.
func TestReadDrainedIgnoresAnUnestablishedConnection(t *testing.T) {
	conn := drainTestConn(t, 0, testBufferSize)
	conn.state.stateType = connConnecting
	conn.onReadDrained()
	if conn.ackPending {
		t.Error("owed an acknowledgement on a connection that is not established")
	}

	conn = drainTestConn(t, 0, testBufferSize)
	conn.state.RecvBuf = nil
	conn.onReadDrained()
	if conn.ackPending {
		t.Error("owed an acknowledgement with no receive buffer")
	}
}

// The comparison is against the window this connection last sent, recorded
// from the packet itself.
func TestLastAdvertisedWindowFollowsWhatWasSent(t *testing.T) {
	conn := drainTestConn(t, 0, testBufferSize)
	conn.socketEvents = make(chan *socketEvent, 4)

	pkt := newPacketBuilder(stState, 1, 0, 4242, 1).Build()
	conn.emit(pkt)

	if conn.lastAdvertisedWindow != 4242 {
		t.Errorf("recorded %d after emitting a packet advertising 4242",
			conn.lastAdvertisedWindow)
	}
}
