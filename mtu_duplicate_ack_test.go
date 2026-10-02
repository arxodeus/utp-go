package utp_go

import (
	"testing"
	"time"
)

// The duplicate-acknowledgement route to lowering the MTU ceiling
// (libutp utp_internal.cpp:1921-1941), rule by rule.
//
// The netem case shows what it is worth end to end. These pin the rules it
// has to follow, which that measurement cannot distinguish: a version that
// counted every acknowledgement rather than only bare ones, or that fired on
// the first duplicate rather than the third, would still bring the search
// down on a path that drops probes.

// dupAckConn builds a connected connection with three packets outstanding and
// an MTU probe on the first of them.
func dupAckConn(t *testing.T) (*connection, uint16) {
	t.Helper()
	return dupAckConnN(t, 3)
}

// dupAckConnN is dupAckConn with n packets outstanding.
func dupAckConnN(t *testing.T, n uint16) (*connection, uint16) {
	t.Helper()
	const syn = uint16(100)
	const synAck = uint16(101)

	conn := CreateTestConnection(Endpoint{Type: Acceptor, SynNum: syn, SynAck: synAck})
	congestionCtrl := newDefaultController(fromConnConfig(conn.config))
	sentPackets := newSentPacketsWithoutLogger(synAck, congestionCtrl)

	now := time.Now()
	for i := uint16(1); i <= n; i++ {
		sentPackets.OnTransmit(synAck+i, st_data, []byte{byte(i)}, 64, now)
	}
	conn.state = &ConnState{
		stateType:   ConnConnected,
		SentPackets: sentPackets,
		SendBuf:     newSendBuffer(TEST_BUFFER_SIZE),
		RecvBuf:     newReceiveBuffer(TEST_BUFFER_SIZE, syn),
	}

	conn.mtu = newMtuSearch(1400, now)
	// The probe is the first outstanding packet, so the acknowledgement a peer
	// repeats while the hole is open is the one just before it.
	probeSeq := synAck + 1
	conn.mtu.beginProbe(probeSeq, 1200)

	// The number a peer repeats: just before the oldest outstanding packet.
	return conn, conn.state.SentPackets.LastAckedSeqNum()
}

// The third bare acknowledgement repeating the number before the probe lowers
// the ceiling below the probe's size. The first two do not.
func TestDuplicateAckLowersTheMtuCeiling(t *testing.T) {
	conn, dup := dupAckConn(t)
	if dup != conn.mtu.probeSeq-1 {
		t.Fatalf("the repeated ack %d is not the one before the probe %d; this test "+
			"would exercise the wrong branch", dup, conn.mtu.probeSeq)
	}
	ceilingBefore := conn.mtu.ceiling

	for i := 1; i <= 2; i++ {
		conn.noteDuplicateAck(st_state, dup)
		if conn.mtu.ceiling != ceilingBefore {
			t.Fatalf("the ceiling moved on duplicate %d; libutp acts on the third", i)
		}
		if !conn.mtu.probing {
			t.Fatalf("the probe was forgotten on duplicate %d", i)
		}
	}

	conn.noteDuplicateAck(st_state, dup)
	if conn.mtu.ceiling != 1200-1 {
		t.Errorf("after three duplicates the ceiling is %d; the probe was 1200 bytes, "+
			"so libutp sets it to 1199", conn.mtu.ceiling)
	}
	if conn.mtuProbesLostToDuplicateAcks != 1 {
		t.Errorf("the mechanism ran %d times, expected 1", conn.mtuProbesLostToDuplicateAcks)
	}
}

// Only bare ST_STATE packets count.
//
// libutp is emphatic (utp_internal.cpp:1911-1920): an ST_DATA carrying an
// acknowledgement was most likely sent because the peer had data of its own.
// Counting those would give three "duplicates" immediately after every payload
// packet on a bidirectional connection.
func TestDuplicateAckIgnoresDataPackets(t *testing.T) {
	for _, pt := range []PacketType{st_data, st_fin} {
		t.Run(pt.String(), func(t *testing.T) {
			conn, dup := dupAckConn(t)
			ceilingBefore := conn.mtu.ceiling
			for i := 0; i < 5; i++ {
				conn.noteDuplicateAck(pt, dup)
			}
			if conn.mtu.ceiling != ceilingBefore {
				t.Errorf("five %s packets repeating the same ack moved the ceiling to %d; "+
					"only ST_STATE counts", pt.String(), conn.mtu.ceiling)
			}
			if conn.mtuProbesLostToDuplicateAcks != 0 {
				t.Error("a data packet was counted as a duplicate acknowledgement")
			}
		})
	}
}

// An acknowledgement that is not the repeated one resets the count, so
// duplicates have to be consecutive.
func TestDuplicateAckCountResets(t *testing.T) {
	conn, dup := dupAckConn(t)
	ceilingBefore := conn.mtu.ceiling

	conn.noteDuplicateAck(st_state, dup)
	conn.noteDuplicateAck(st_state, dup)
	// Something else in between.
	conn.noteDuplicateAck(st_state, dup+7)
	conn.noteDuplicateAck(st_state, dup)

	if conn.mtu.ceiling != ceilingBefore {
		t.Errorf("the ceiling moved to %d after 2 duplicates, an unrelated ack and 1 more; "+
			"the count must restart", conn.mtu.ceiling)
	}

	// Two more reach three consecutive.
	conn.noteDuplicateAck(st_state, dup)
	conn.noteDuplicateAck(st_state, dup)
	if conn.mtu.ceiling != 1200-1 {
		t.Errorf("three consecutive duplicates after the reset left the ceiling at %d",
			conn.mtu.ceiling)
	}
}

// A hole ahead of the probe says nothing about size. libutp forgets the probe
// so another can be sent, and leaves the ceiling alone.
func TestDuplicateAckForANonProbeHoleForgetsTheProbe(t *testing.T) {
	conn, _ := dupAckConn(t)
	ceilingBefore := conn.mtu.ceiling

	// A repeated acknowledgement pointing somewhere other than just before the
	// probe. It still has to be the number the window says is repeated, or it
	// would simply reset the count -- so move the probe instead.
	conn.mtu.clearProbe()
	conn.mtu.beginProbe(conn.state.SentPackets.LastAckedSeqNum()+3, 1200)
	dup := conn.state.SentPackets.LastAckedSeqNum()

	for i := 0; i < 3; i++ {
		conn.noteDuplicateAck(st_state, dup)
	}

	if conn.mtu.ceiling != ceilingBefore {
		t.Errorf("the ceiling moved to %d for a hole that was not the probe", conn.mtu.ceiling)
	}
	if conn.mtu.probing {
		t.Error("the probe is still outstanding; libutp clears it so another can be sent")
	}
	if conn.mtuProbesLostToDuplicateAcks != 0 {
		t.Error("a hole that was not the probe was counted as a probe loss")
	}
}

// Nothing is counted while nothing is outstanding, and libutp does not reset
// the count there either: the whole block sits inside
// `if (cur_window_packets > 0)`.
func TestDuplicateAckIgnoredWithNothingOutstanding(t *testing.T) {
	conn, dup := dupAckConn(t)

	// Two duplicates with packets outstanding.
	conn.noteDuplicateAck(st_state, dup)
	conn.noteDuplicateAck(st_state, dup)

	// Now nothing is outstanding.
	empty := newSentPacketsWithoutLogger(101, newDefaultController(fromConnConfig(conn.config)))
	conn.state.SentPackets = empty
	if empty.UnackedCount() != 0 {
		t.Fatalf("the replacement tracker has %d unacked; this test needs none", empty.UnackedCount())
	}
	conn.noteDuplicateAck(st_state, dup)
	if conn.duplicateAcks != 2 {
		t.Errorf("the count is %d; an acknowledgement arriving with nothing outstanding "+
			"is neither counted nor a reset", conn.duplicateAcks)
	}
}

// A selective ack sets the duplicate count to the number of packets it names
// inside the window (libutp: `duplicate_ack = count`, utp_internal.cpp:1612),
// and the next duplicate counts on from there. With the two packets after the
// probe named, the second duplicate is libutp's third.
func TestSelectiveAckSetsTheDuplicateCount(t *testing.T) {
	conn, dup := dupAckConn(t)
	ceilingBefore := conn.mtu.ceiling
	// Bits 0 and 1 are dup+2 and dup+3: the two packets after the probe.
	sack := NewSelectiveAck([]bool{true, true})

	conn.noteDuplicateAck(st_state, dup)
	conn.noteSelectiveAckCount(dup, sack)
	if conn.duplicateAcks != 2 {
		t.Fatalf("a selective ack naming two packets in the window set the count to %d", conn.duplicateAcks)
	}
	if conn.mtu.ceiling != ceilingBefore {
		t.Fatalf("the ceiling moved before the count reached three")
	}
	conn.noteDuplicateAck(st_state, dup)
	if conn.mtu.ceiling != 1200-1 {
		t.Errorf("after a selective ack naming two packets and one more duplicate the ceiling is "+
			"%d; libutp's count is three there and it sets the ceiling to 1199", conn.mtu.ceiling)
	}

	// Bits outside the window, and the oldest packet's, are not counted.
	conn2, dup2 := dupAckConn(t)
	far := make([]bool, 32)
	far[5] = true // dup2+7: never sent
	conn2.noteSelectiveAckCount(dup2, NewSelectiveAck(far))
	if conn2.duplicateAcks != 0 {
		t.Errorf("a bit for a packet never sent was counted: %d", conn2.duplicateAcks)
	}
}

// The same, through the packet path: the count is taken from a selective ack
// on an arriving packet, after its acknowledgement is applied.
func TestSelectiveAckSetsTheDuplicateCountOnArrival(t *testing.T) {
	conn, dup := dupAckConn(t)
	const syn = uint16(100)
	now := time.Now()
	bits := make([]bool, 32)
	bits[0], bits[1] = true, true // dup+2 and dup+3, the packets after the probe

	withSack := NewPacketBuilder(st_state, conn.cid.Send, uint32(now.UnixMicro()),
		DefaultWindowSize, syn+1).WithAckNum(dup).WithSelectiveAck(NewSelectiveAck(bits)).Build()
	conn.onPacket(withSack, now)
	if conn.duplicateAcks != 2 {
		t.Fatalf("after a duplicate carrying a selective ack for two packets the count is %d; "+
			"libutp's is 2 (utp_internal.cpp:1612)", conn.duplicateAcks)
	}
	bare := NewPacketBuilder(st_state, conn.cid.Send, uint32(now.UnixMicro()),
		DefaultWindowSize, syn+1).WithAckNum(dup).Build()
	conn.onPacket(bare, now.Add(time.Millisecond))
	if conn.mtu.ceiling != 1200-1 {
		t.Errorf("two duplicates, the first naming two packets, left the ceiling at %d; "+
			"libutp's count reaches three on the second and sets it to 1199", conn.mtu.ceiling)
	}
}

// A selective ack that names more than three packets after the probe judges
// it refused there and then. libutp's test is equality with three, so a count
// that jumps from below three to above it -- an acknowledgement covering a
// whole read batch -- concludes nothing in libutp, ever, for that run. It is
// the same evidence as three duplicates: at least three packets after the
// probe arrived and the probe did not.
func TestSelectiveAckPastThreeJudgesTheProbe(t *testing.T) {
	conn, dup := dupAckConnN(t, 8)
	// dup+2 .. dup+6: five packets after the probe.
	sack := NewSelectiveAck([]bool{true, true, true, true, true})
	conn.noteSelectiveAckCount(dup, sack)
	if conn.duplicateAcks != 5 {
		t.Fatalf("a selective ack naming five packets in the window set the count to %d", conn.duplicateAcks)
	}
	if conn.mtu.ceiling != 1200-1 {
		t.Errorf("five packets after the probe arrived and it did not, and the ceiling is %d; "+
			"want 1199", conn.mtu.ceiling)
	}
	if conn.mtuProbesLostToDuplicateAcks != 1 {
		t.Errorf("the probe was judged %d times, want 1", conn.mtuProbesLostToDuplicateAcks)
	}
}

// One run of duplicates judges once, however long it goes on and whichever
// route took the count past three; an acknowledgement that moves starts a new
// run.
func TestDuplicateRunJudgesOnce(t *testing.T) {
	conn, dup := dupAckConnN(t, 8)
	probeSeq := conn.mtu.probeSeq
	for i := 0; i < 6; i++ {
		conn.noteDuplicateAck(st_state, dup)
	}
	conn.noteSelectiveAckCount(dup, NewSelectiveAck([]bool{true, true, true, true}))
	conn.noteDuplicateAck(st_state, dup)
	if conn.mtuProbesLostToDuplicateAcks != 1 {
		t.Fatalf("one run of duplicates judged the probe %d times", conn.mtuProbesLostToDuplicateAcks)
	}

	// A new probe, and an acknowledgement that is not the repeated one, ends
	// the run: the next three judge again.
	conn.mtu.beginProbe(probeSeq, 1100)
	conn.noteDuplicateAck(st_state, dup+5)
	for i := 0; i < 3; i++ {
		conn.noteDuplicateAck(st_state, dup)
	}
	if conn.mtuProbesLostToDuplicateAcks != 2 {
		t.Errorf("a second run judged %d probes in all, want 2", conn.mtuProbesLostToDuplicateAcks)
	}
}
