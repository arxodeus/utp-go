package utp_go

import (
	"bytes"
	"testing"
	"time"
)

// The peer's receive window, as libutp's sender spends it.
//
// flush_packets sends the next queued packet only while !is_full(), and
// is_full with no argument refuses when cur_window + packet_size >
// min(max_window, opt_sndbuf, max_window_user) (utp_internal.cpp:933-936,
// :956, :974). The conformance corpus cannot isolate this against libutp at
// connection start -- libutp's congestion window is one packet there (:2567),
// which holds a second packet back whatever the peer advertises, and its first
// packet is 1452 bytes where ours is 962 (KNOWN-LIMITATIONS.md, M6) -- so the
// rule is asserted here, on our side alone.

// sendAndCount queues data and returns the payload sizes that went out.
func sendAndCount(t *testing.T, conn *connection, data []byte) []int {
	t.Helper()
	conn.state.SendBuf.Write(data)
	conn.processWrites(time.Now())
	var sizes []int
	for {
		select {
		case ev := <-conn.socketEvents:
			if ev.Packet != nil && ev.Packet.Header.PacketType == st_data {
				sizes = append(sizes, len(ev.Packet.Body))
			}
		default:
			return sizes
		}
	}
}

func (c *connection) testWidenCongestionWindow(bytes uint32) {
	c.state.SentPackets.congestionCtrl.(*defaultController).maxWindowSizeBytes = bytes
}

// Bytes already in flight spend the peer's window.
func TestInFlightBytesSpendThePeersWindow(t *testing.T) {
	conn := drainTestConn(t, 0, TEST_BUFFER_SIZE)
	conn.mtu = newMtuSearch(uint32(conn.config.MaxPacketSize), time.Now())
	p := conn.mtu.payloadSize()
	conn.testWidenCongestionWindow(100 * p)
	conn.peerRecvWindow = 2*p + p/2

	first := sendAndCount(t, conn, bytes.Repeat([]byte("a"), int(p)))
	if len(first) != 1 {
		t.Fatalf("one packet into a window of 2.5 packets: sent %v", first)
	}
	second := sendAndCount(t, conn, bytes.Repeat([]byte("b"), int(p)))
	if len(second) != 1 {
		t.Fatalf("a second packet beside one in flight, 2.5 packets advertised: sent %v", second)
	}
	third := sendAndCount(t, conn, bytes.Repeat([]byte("c"), int(p)))
	if len(third) != 0 {
		t.Errorf("a third packet beside two in flight overruns a window of 2.5 packets: "+
			"sent %v (libutp: cur_window + packet_size > max_window_user, utp_internal.cpp:956)",
			third)
	}
}

// Nothing goes out into less than a full packet of room, and nothing is cut
// down to fit it -- even a write smaller than the room.
func TestNothingIsSentIntoLessThanAPacketOfRoom(t *testing.T) {
	conn := drainTestConn(t, 0, TEST_BUFFER_SIZE)
	conn.mtu = newMtuSearch(uint32(conn.config.MaxPacketSize), time.Now())
	p := conn.mtu.payloadSize()
	conn.testWidenCongestionWindow(100 * p)
	conn.peerRecvWindow = p - 1

	if sent := sendAndCount(t, conn, bytes.Repeat([]byte("d"), 100)); len(sent) != 0 {
		t.Errorf("100 bytes into %d of room, a packet being %d: sent %v "+
			"(libutp's flush_packets charges a whole packet_size, utp_internal.cpp:934, :974)",
			p-1, p, sent)
	}

	conn.peerRecvWindow = p
	if sent := sendAndCount(t, conn, nil); len(sent) != 1 || sent[0] != 100 {
		t.Errorf("a full packet of room: expected the queued 100 bytes as one packet, sent %v", sent)
	}
}

// The control for both: a window with room for everything sends everything.
func TestAWidePeerWindowSendsEveryPacket(t *testing.T) {
	conn := drainTestConn(t, 0, TEST_BUFFER_SIZE)
	conn.mtu = newMtuSearch(uint32(conn.config.MaxPacketSize), time.Now())
	p := conn.mtu.payloadSize()
	conn.testWidenCongestionWindow(100 * p)
	conn.peerRecvWindow = 3 * p

	sent := sendAndCount(t, conn, bytes.Repeat([]byte("e"), int(3*p)))
	if len(sent) != 3 {
		t.Errorf("three packets into a window of three: sent %v", sent)
	}
}

// RecvBufferDrops counts a packet refused for want of room, and not a
// duplicate of one already held.
func TestRecvBufferDropsCountsRefusalsOnly(t *testing.T) {
	conn := drainTestConn(t, 0, 1000)
	first := conn.state.RecvBuf.AckNum() + 1

	if err := conn.onData(first, bytes.Repeat([]byte("f"), 600)); err != nil {
		t.Fatalf("600 bytes into an empty 1000-byte buffer: %v", err)
	}
	if conn.recvBufferDrops != 0 {
		t.Fatalf("an admitted packet counted as a drop")
	}
	if err := conn.onData(first, bytes.Repeat([]byte("f"), 600)); err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	if conn.recvBufferDrops != 0 {
		t.Errorf("a duplicate of a packet already held counted as a drop")
	}
	_ = conn.onData(first+1, bytes.Repeat([]byte("g"), 600))
	if conn.recvBufferDrops != 1 {
		t.Errorf("600 bytes into 400 of room: counted %d drops, expected 1", conn.recvBufferDrops)
	}
}
