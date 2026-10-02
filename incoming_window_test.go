package utp_go

import (
	"testing"
	"time"
)

// incomingWindowConn is a connected acceptor with one packet outstanding.
func incomingWindowConn(t *testing.T) (*connection, time.Time) {
	t.Helper()
	const syn = uint16(100)
	const synAck = uint16(101)
	conn := CreateTestConnection(Endpoint{Type: Acceptor, SynNum: syn, SynAck: synAck})
	sentPackets := newSentPacketsWithoutLogger(synAck, newDefaultController(fromConnConfig(conn.config)))
	now := time.Now()
	sentPackets.OnTransmit(synAck+1, st_data, []byte{0xef}, 64, now)
	conn.state = &ConnState{
		stateType:   ConnConnected,
		SentPackets: sentPackets,
		SendBuf:     newSendBuffer(TEST_BUFFER_SIZE),
		RecvBuf:     newReceiveBuffer(TEST_BUFFER_SIZE, syn),
	}
	conn.peerRecvWindow = 1 << 20
	return conn, now
}

// A packet libutp discards does not change what we think the peer's window
// is. libutp reads the window only after the acknowledgement-number and
// reorder-window checks (utp_internal.cpp:2144); this used to take it from
// every packet, first thing, so a forged packet with a bad acknowledgement
// could close the window.
func TestDiscardedPacketDoesNotSetThePeerWindow(t *testing.T) {
	conn, now := incomingWindowConn(t)
	const syn = uint16(100)
	// Acknowledges a packet never sent: libutp drops it (:1794-1807).
	forged := NewPacketBuilder(st_state, conn.cid.Send, uint32(now.UnixMicro()), 0, syn+1).
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
	good := NewPacketBuilder(st_state, conn.cid.Send, uint32(now.UnixMicro()), 0, syn+1).
		WithAckNum(101).Build()
	conn.onPacket(good, now)
	if conn.peerRecvWindow != 0 {
		t.Errorf("a valid packet advertising a zero window left the peer window at %d", conn.peerRecvWindow)
	}
}

// Every packet reporting a zero window restarts the probe's deadline: libutp
// sets `zerowindow_time = current_ms + 15000` on each (utp_internal.cpp:
// 2148-2151), so the probe goes 15 seconds after the last, not the first.
func TestZeroWindowProbeDeadlineRestartsOnEachZeroWindow(t *testing.T) {
	conn, now := incomingWindowConn(t)
	const syn = uint16(100)
	zero := func(at time.Time) {
		conn.onPacket(NewPacketBuilder(st_state, conn.cid.Send, uint32(at.UnixMicro()), 0, syn+1).
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

// A FIN waits for room in the window like any packet. libutp queues it in its
// outgoing buffer and sends it only while !is_full() (utp_internal.cpp:
// 931-985). This library sent it the moment the send buffer was empty; with
// more in flight than a loss had just left the window, the controller refused
// even its zero bytes and transmit panicked. Found by a two-way transfer at 5%
// loss, one run in six.
func TestFinWaitsForRoomInTheWindow(t *testing.T) {
	const syn = uint16(100)
	const synAck = uint16(101)
	conn := CreateTestConnection(Endpoint{Type: Acceptor, SynNum: syn, SynAck: synAck})
	ctrl := newDefaultController(fromConnConfig(conn.config))
	ctrl.maxWindowSizeBytes = 1 << 20
	sentPackets := newSentPacketsWithoutLogger(synAck, ctrl)
	now := time.Now()
	for i := uint16(1); i <= 3; i++ {
		sentPackets.OnTransmit(synAck+i, st_data, make([]byte, 1000), 1000, now)
	}
	conn.state = &ConnState{
		stateType:   ConnConnected,
		SentPackets: sentPackets,
		SendBuf:     newSendBuffer(TEST_BUFFER_SIZE),
		RecvBuf:     newReceiveBuffer(TEST_BUFFER_SIZE, syn),
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

// An acknowledgement wakes the writer whichever packet carries it. libutp
// makes the socket writable after any incoming packet that leaves the window
// below full (utp_internal.cpp:2302-2308). This woke only for ST_STATE, so on
// a connection carrying data both ways -- where the peer's acknowledgements
// ride on its data packets -- the window opened and nothing used it until a
// timer ran: about 36 s instead of 5 for 1 MB each way at 5% loss, in 3 runs
// of 15.
func TestAcknowledgementOnADataPacketWakesTheWriter(t *testing.T) {
	for _, pt := range []PacketType{st_state, st_data} {
		t.Run(pt.String(), func(t *testing.T) {
			conn, now := incomingWindowConn(t)
			const syn = uint16(100)
			// Clear any wake left over from setting up.
			conn.wantWrite = false
			b := NewPacketBuilder(pt, conn.cid.Send, uint32(now.UnixMicro()), 1<<20, syn+1).WithAckNum(102)
			if pt == st_data {
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

// No more than 1023 packets in flight, however small they are. libutp's
// is_full refuses at `cur_window_packets >= OUTGOING_BUFFER_MAX_SIZE - 1`
// before it counts bytes (utp_internal.cpp:939-947). There was no packet
// bound here at all.
func TestOutstandingPacketsAreCappedAt1023(t *testing.T) {
	for _, already := range []int{1021, 1022} {
		conn := drainTestConn(t, 0, TEST_BUFFER_SIZE)
		// Room for three whole packets, so that none is a short tail the
		// Nagle rule would hold back.
		conn.state.SendBuf = newSendBuffer(64 * 1024)
		conn.mtu = newMtuSearch(uint32(conn.config.MaxPacketSize), time.Now())
		p := conn.mtu.payloadSize()
		conn.testWidenCongestionWindow(1 << 30)
		conn.peerRecvWindow = 1 << 30
		now := time.Now()
		for i := 1; i <= already; i++ {
			conn.state.SentPackets.OnTransmit(101+uint16(i), st_data, []byte{1}, 1, now)
		}
		sent := sendAndCount(t, conn, make([]byte, 3*p))
		if want := 1023 - already; len(sent) != want {
			t.Errorf("with %d tiny packets in flight and a vast window, %d more went out; "+
				"libutp stops at 1023 in flight, which leaves room for %d",
				already, len(sent), want)
		}
	}
}
