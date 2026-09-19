package utp_go

import (
	"testing"
)

// utp_read_drained, at the level where its rules can be stated exactly.
//
// The end-to-end behaviour of this mechanism resisted three attempts at a
// stable network measurement, for reasons written up in KNOWN-LIMITATIONS.md.
// What is testable without that is the rule itself: when handing bytes up
// reopens a window narrower than what is now free, the peer is owed an
// acknowledgement -- and when it does not, no extra acknowledgement is sent,
// because that doubles the reverse traffic.

func drainTestConn(t *testing.T, lastAdvertised uint32, bufferSize int) *connection {
	t.Helper()
	const syn = uint16(100)
	const synAck = uint16(101)
	conn := CreateTestConnection(Endpoint{Type: Acceptor, SynNum: syn, SynAck: synAck})
	conn.state = &ConnState{
		stateType:   ConnConnected,
		SentPackets: newSentPacketsWithoutLogger(synAck, newDefaultController(fromConnConfig(conn.config))),
		SendBuf:     newSendBuffer(TEST_BUFFER_SIZE),
		RecvBuf:     newReceiveBuffer(bufferSize, syn),
	}
	conn.lastAdvertisedWindow = lastAdvertised
	conn.ackPending = false
	return conn
}

// A window larger than the one last advertised is owed an acknowledgement.
func TestReadDrainedOwesAnAckWhenTheWindowGrew(t *testing.T) {
	conn := drainTestConn(t, 0, TEST_BUFFER_SIZE)
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

// A window no larger than the one last advertised owes nothing.
//
// This is the guard against doubling the reverse traffic. The acknowledgement
// owed for incoming data already carries a current window, because the drain
// happens before the acknowledgement in the same pass of the event loop; an
// extra one here would be a second acknowledgement for the same packet.
func TestReadDrainedIsSilentWhenTheWindowDidNotGrow(t *testing.T) {
	buf := newReceiveBuffer(TEST_BUFFER_SIZE, 100)
	full := uint32(buf.Available())

	for _, advertised := range []uint32{full, full + 1, full * 2} {
		conn := drainTestConn(t, advertised, TEST_BUFFER_SIZE)
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

// The test is growth, not growth from zero, and the difference is a stall.
//
// An earlier version fired only when the window last advertised was zero, on
// the reasoning that an ordinary drain is already covered. It left a
// connection wedged: the window fell to 861 bytes, which is not zero but is
// less than a packet, so the sender could not fit one and went quiet; nothing
// arrived, so nothing was acknowledged; the application read and freed the
// buffer, and the zero-only test declined to mention it. A window too small to
// use is as blocking as a closed one.
func TestReadDrainedCoversASubPacketWindow(t *testing.T) {
	// 861 bytes: the figure from the trace that found this.
	conn := drainTestConn(t, 861, TEST_BUFFER_SIZE)
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
	conn := drainTestConn(t, 0, TEST_BUFFER_SIZE)
	conn.state.stateType = ConnConnecting
	conn.onReadDrained()
	if conn.ackPending {
		t.Error("owed an acknowledgement on a connection that is not established")
	}

	conn = drainTestConn(t, 0, TEST_BUFFER_SIZE)
	conn.state.RecvBuf = nil
	conn.onReadDrained()
	if conn.ackPending {
		t.Error("owed an acknowledgement with no receive buffer")
	}
}

// The window this connection last put on the wire is what the comparison is
// against, and it is recorded from the packet itself rather than tracked
// alongside it.
func TestLastAdvertisedWindowFollowsWhatWasSent(t *testing.T) {
	conn := drainTestConn(t, 0, TEST_BUFFER_SIZE)
	conn.socketEvents = make(chan *socketEvent, 4)

	pkt := NewPacketBuilder(st_state, 1, 0, 4242, 1).Build()
	conn.emit(pkt)

	if conn.lastAdvertisedWindow != 4242 {
		t.Errorf("recorded %d after emitting a packet advertising 4242",
			conn.lastAdvertisedWindow)
	}
}
