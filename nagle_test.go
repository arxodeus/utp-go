package utp_go

import (
	"bytes"
	"testing"
	"time"
)

// nagleTestConn is a connected connection with room in both windows for far
// more than these tests send, so that only the Nagle rule can hold a packet.
func nagleTestConn(t *testing.T) (*connection, uint32) {
	t.Helper()
	conn := drainTestConn(t, 0, TEST_BUFFER_SIZE)
	conn.state.SendBuf = newSendBuffer(64 * 1024)
	conn.mtu = newMtuSearch(uint32(conn.config.MaxPacketSize), time.Now())
	p := conn.mtu.payloadSize()
	conn.testWidenCongestionWindow(100 * p)
	conn.peerRecvWindow = 100 * p
	return conn, p
}

// libutp's Nagle rule (flush_packets, utp_internal.cpp:974-982), with both
// windows open: a short packet waits while another is unacknowledged, and
// what is written next joins it until it is a full packet.
func TestNagleHoldsAShortPacketBehindAnother(t *testing.T) {
	conn, p := nagleTestConn(t)

	if sent := sendAndCount(t, conn, []byte("first")); len(sent) != 1 {
		t.Fatalf("a short write with nothing in flight: sent %v, want one packet", sent)
	}
	if sent := sendAndCount(t, conn, bytes.Repeat([]byte("s"), 100)); len(sent) != 0 {
		t.Errorf("a short write behind an unacknowledged packet: sent %v, want nothing", sent)
	}
	// The rest of a packet's worth joins the held bytes and fills it.
	if sent := sendAndCount(t, conn, bytes.Repeat([]byte("t"), int(p))); len(sent) != 1 || sent[0] != int(p) {
		t.Errorf("filling the held packet: sent %v, want one packet of %d", sent, p)
	}
}

// The control: the same short write with NoDelay leaves at once.
func TestNoDelaySendsAShortPacketAtOnce(t *testing.T) {
	conn, _ := nagleTestConn(t)
	conn.config.NoDelay = true

	sendAndCount(t, conn, []byte("first"))
	if sent := sendAndCount(t, conn, bytes.Repeat([]byte("s"), 100)); len(sent) != 1 || sent[0] != 100 {
		t.Errorf("NoDelay, a short write behind an unacknowledged packet: sent %v, want 100 bytes at once", sent)
	}
}

// Closing the write side releases a held packet: nothing more will join it.
// libutp queues its FIN behind the held packet, which is then no longer the
// last and goes out (utp_close, utp_internal.cpp:3358-3380; flush_packets).
func TestNagleReleasedWhenTheWriteSideCloses(t *testing.T) {
	conn, _ := nagleTestConn(t)

	sendAndCount(t, conn, []byte("first"))
	if sent := sendAndCount(t, conn, bytes.Repeat([]byte("s"), 100)); len(sent) != 0 {
		t.Fatalf("precondition: the short write was not held: sent %v", sent)
	}
	conn.writeShut = true
	if sent := sendAndCount(t, conn, nil); len(sent) != 1 || sent[0] != 100 {
		t.Errorf("after the write side closed: sent %v, want the held 100 bytes", sent)
	}
}
