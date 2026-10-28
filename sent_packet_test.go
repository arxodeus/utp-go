package utp

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNextSeqNum(t *testing.T) {

}

func TestOnTransmitInitial(t *testing.T) {
	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))

	initSeqNum := uint16(math.MaxUint16)
	sentPackets := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	seqNum := sentPackets.NextSeqNum()
	data := []byte{0}
	length := uint32(len(data))
	now := time.Now()
	sentPackets.OnTransmit(seqNum, stData, data, length, now)

	if len(sentPackets.packets) != 1 {
		t.Errorf("expected 1 packet, got %d", len(sentPackets.packets))
	}

	packetInst := sentPackets.packets[0]
	if packetInst.seqNum != seqNum {
		t.Errorf("expected seq num %d, got %d", seqNum, packetInst.seqNum)
	}
	if !packetInst.transmission.Equal(now) {
		t.Errorf("expected transmission time %v, got %v", now, packetInst.transmission)
	}
	if packetInst.acked {
		t.Errorf("expected the packet not acknowledged")
	}
	if packetInst.retransmission != packetInst.transmission {
		t.Errorf("expected initial retransmission, got %v", packetInst.retransmission)
	}
}

func TestOnTransmitRetransmit(t *testing.T) {
	initSeqNum := uint16(math.MaxUint16)
	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))
	sentPackets := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	seqNum := sentPackets.NextSeqNum()
	data := []byte{0}
	length := uint32(len(data))
	// Distinct by construction: two consecutive clock readings can be
	// equal.
	first := time.Now()
	second := first.Add(time.Millisecond)

	sentPackets.OnTransmit(seqNum, stData, data, length, first)
	sentPackets.OnTransmit(seqNum, stData, data, length, second)

	if len(sentPackets.packets) != 1 {
		t.Errorf("expected 1 packet, got %d", len(sentPackets.packets))
	}

	packetInst := sentPackets.packets[0]
	if packetInst.seqNum != seqNum {
		t.Errorf("expected seq num %d, got %d", seqNum, packetInst.seqNum)
	}
	if !packetInst.transmission.Equal(first) {
		t.Errorf("expected transmission time %v, got %v", first, packetInst.transmission)
	}
	if packetInst.acked {
		t.Errorf("expected the packet not acknowledged")
	}
	if packetInst.retransmission == packetInst.transmission {
		t.Errorf("expected retransmission was updated, got %v", packetInst.retransmission)
	}
	if packetInst.retransmission != second {
		t.Errorf("expected retransmission time %v, got %v", second, packetInst.retransmission)
	}
}

func TestOnTransmitOutOfOrder(t *testing.T) {
	initSeqNum := uint16(math.MaxUint16)

	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))
	sentPackets := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	outOfOrderSeqNum := initSeqNum + 2 // wrapping add
	data := []byte{0}
	length := uint32(len(data))
	now := time.Now()

	err := sentPackets.OnTransmit(outOfOrderSeqNum, stData, data, length, now)
	require.ErrorIs(t, err, errOutOfOrderTransmit)
}

func TestOnSelectiveAck(t *testing.T) {
	const (
		numPackets = 10
		sackLen    = numPackets - 2
		ackDelay   = time.Millisecond * 100 // Assume this value.
	)

	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))

	initSeqNum := uint16(math.MaxUint16)
	sentPackets := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	data := []byte{0}
	length := uint32(len(data))

	// Send count packets.
	for i := 0; i < numPackets; i++ {
		now := time.Now()
		seqNum := sentPackets.NextSeqNum()
		sentPackets.OnTransmit(seqNum, stData, data, length, now)
	}

	// Create a selective ack.
	acked := make([]bool, sackLen)
	for i := range acked {
		if i%2 == 0 {
			acked[i] = true
		}
	}
	selectiveAck := newSelectiveAck(acked)

	// Process the ack.
	now := time.Now()
	_, _, err := sentPackets.onAck(initSeqNum+1, selectiveAck, ackDelay, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the acks.
	if !sentPackets.packets[0].acked {
		t.Errorf("expected the first packet acknowledged")
	}
	if sentPackets.packets[1].acked {
		t.Errorf("expected the second packet not acknowledged")
	}
	for i := 2; i < numPackets; i++ {
		isEmpty := i%2 != 0
		require.Equal(t, isEmpty, !sentPackets.packets[i].acked, fmt.Sprintf("packet %d: expected unacknowledged=%v", i, isEmpty))
	}
}

func TestDetectLostPackets(t *testing.T) {
	const (
		numPackets = 10
		firstLost  = numPackets - lossThreshold
		ackDelay   = time.Millisecond * 100
	)

	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))

	initSeqNum := uint16(math.MaxUint16)
	sentPackets := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	data := []byte{0}
	length := uint32(len(data))

	// Send packets and selectively acknowledge them.
	for i := 0; i < numPackets; i++ {
		now := time.Now()
		seqNum := sentPackets.NextSeqNum()
		sentPackets.OnTransmit(seqNum, stData, data, length, now)

		if i >= firstLost {
			err := sentPackets.Ack(seqNum, ackDelay, now)
			require.NoError(t, err)
		}
	}

	// Detect lost packets.
	firstUnacked, err := sentPackets.FirstUnackedSeqNum()
	require.NoError(t, err)
	lost := sentPackets.DetectLostPackets(firstUnacked)

	// Verify the lost packets.
	for i := 0; i < firstLost; i++ {
		packetInst := sentPackets.packets[i]
		var found bool
		for _, lostSeq := range lost {
			if found = packetInst.seqNum == lostSeq; found {
				break
			}
		}
		require.True(t, found, fmt.Sprintf("packet %d (seq=%d) should be marked as lost", i, packetInst.seqNum))
	}
}

func TestAck(t *testing.T) {
	const ackDelay = time.Millisecond * 100

	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))

	initSeqNum := uint16(math.MaxUint16)
	sentPackets := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	seqNum := sentPackets.NextSeqNum()
	data := []byte{0}
	length := uint32(len(data))
	now := time.Now()
	sentPackets.OnTransmit(seqNum, stData, data, length, now)

	// Insert a packet into the lost packets directly.
	sentPackets.lostPackets.ReplaceOrInsert(seqNum)

	// Acknowledge the packet.
	now = time.Now()
	err := sentPackets.Ack(seqNum, ackDelay, now)
	require.NoError(t, err)

	index := sentPackets.SeqNumIndex(seqNum)
	packetInst := sentPackets.packets[index]

	if !packetInst.acked {
		t.Errorf("expected the packet acknowledged")
	}

	sentPackets.lostPackets.Ascend(func(lostSeq uint16) bool {
		if lostSeq == packetInst.seqNum {
			t.Error("packet should not be marked as lost")
			return false
		}
		return true
	})
}

func TestAckPriorUnacked(t *testing.T) {
	const (
		numPackets = 10
		lastAcked  = uint16(3)
		ackDelay   = time.Millisecond * 100
	)

	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))

	initSeqNum := uint16(math.MaxUint16)
	sentPackets := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	data := []byte{0}
	length := uint32(len(data))

	// Send count packets.
	for i := 0; i < numPackets; i++ {
		now := time.Now()
		seqNum := sentPackets.NextSeqNum()
		sentPackets.OnTransmit(seqNum, stData, data, length, now)
	}

	// Verify the preconditions.
	if int(lastAcked) >= numPackets {
		t.Fatal("lastAcked must be less than numPackets")
	}
	if numPackets-int(lastAcked) <= 2 {
		t.Fatal("numPackets minus lastAcked must be greater than 2")
	}

	// Acknowledge the earlier packets.
	now := time.Now()
	firstUnacked, err := sentPackets.FirstUnackedSeqNum()
	require.NoError(t, err)
	err = sentPackets.AckPriorUnacked(lastAcked, firstUnacked, ackDelay, now)
	require.NoError(t, err)

	// Verify the acknowledgments.
	for i := 0; i < int(lastAcked); i++ {
		if !sentPackets.packets[i].acked {
			t.Errorf("packet %d: expected it acknowledged", i)
		}
	}
}

func TestAckUnsent(t *testing.T) {
	const ackDelay = time.Millisecond * 100

	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))

	initSeqNum := uint16(math.MaxUint16)
	sentPackets := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	unsentAckNum := initSeqNum + 2 // wrapping add
	now := time.Now()

	err := sentPackets.Ack(unsentAckNum, ackDelay, now)
	require.ErrorIs(t, err, errAckOfUnsent)
}

func TestSeqNumIndex(t *testing.T) {
	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))

	initSeqNum := uint16(math.MaxUint16)
	sentPackets := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	if sentPackets.SeqNumIndex(initSeqNum) != math.MaxUint16 {
		t.Errorf("expected index %d, got %d", math.MaxUint16, sentPackets.SeqNumIndex(initSeqNum))
	}

	zero := initSeqNum + 1 // wrapping add
	if sentPackets.SeqNumIndex(zero) != 0 {
		t.Errorf("expected index 0, got %d", sentPackets.SeqNumIndex(zero))
	}
}

// A packet is fast retransmitted once. Further acks that still show it
// missing leave it alone; only the retransmission timer sends it again.
func TestFastRetransmitHappensOncePerPacket(t *testing.T) {
	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))
	initSeqNum := uint16(100)
	sent := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	data := []byte{0}
	for i := 0; i < 8; i++ {
		sent.OnTransmit(sent.NextSeqNum(), stData, data, uint32(len(data)), time.Now())
	}

	// 101 is missing; 102 through 105 arrive. That is four acks past the
	// gap, so 101 is declared lost.
	acked := make([]bool, 4)
	for i := range acked {
		acked[i] = true
	}
	sack := newSelectiveAck(acked)

	if _, _, err := sent.onAck(initSeqNum, sack, 10*time.Millisecond, time.Now()); err != nil {
		t.Fatalf("first ack: %v", err)
	}
	first := sent.TakeLostPackets()
	if len(first) != 1 || first[0].SeqNum != 101 {
		t.Fatalf("first ack should have declared 101 lost, got %v", describeLost(first))
	}

	// The same ack again, twice. 101 is still missing and must not be
	// resent.
	for round := 0; round < 3; round++ {
		if _, _, err := sent.onAck(initSeqNum, sack, 10*time.Millisecond, time.Now()); err != nil {
			t.Fatalf("repeat ack %d: %v", round, err)
		}
		if again := sent.TakeLostPackets(); len(again) != 0 {
			t.Fatalf("repeat ack %d fast retransmitted %v again; it was already resent once",
				round, describeLost(again))
		}
	}
}

// Fast retransmission works whatever the first sequence number is: the
// fast-retransmit floor starts at the first sequence number in both roles.
// If it started at a fixed value, a random start more than half the
// sequence space away would make the floor look ahead, and nothing could be
// fast retransmitted until the numbers wrapped.
func TestFastRetransmitFromAnyInitialSequenceNumber(t *testing.T) {
	for _, initSeqNum := range []uint16{100, 32768, 40000, 65000, 65534} {
		ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))
		sent := newSentPacketsWithoutLogger(initSeqNum, ctrl)
		data := []byte{0}
		for i := 0; i < 8; i++ {
			sent.OnTransmit(sent.NextSeqNum(), stData, data, uint32(len(data)), time.Now())
		}
		// The first packet is missing; the next four arrive.
		sack := newSelectiveAck([]bool{true, true, true, true})
		if _, _, err := sent.onAck(initSeqNum, sack, 10*time.Millisecond, time.Now()); err != nil {
			t.Fatalf("init %d: %v", initSeqNum, err)
		}
		lost := sent.TakeLostPackets()
		if len(lost) != 1 || lost[0].SeqNum != initSeqNum+1 {
			t.Errorf("first sequence number %d: the hole at %d should be fast retransmitted, got %v",
				initSeqNum, initSeqNum+1, describeLost(lost))
		}
	}
}

// One ack fast retransmits at most four packets, however many it reveals as
// lost.
func TestFastRetransmitIsCappedPerAck(t *testing.T) {
	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))
	ctrl.maxWindowSizeBytes = 1 << 20
	initSeqNum := uint16(100)
	sent := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	data := []byte{0}
	for i := 0; i < 40; i++ {
		sent.OnTransmit(sent.NextSeqNum(), stData, data, uint32(len(data)), time.Now())
	}

	// Ten missing packets with acknowledged packets above all of them:
	// 102, 104, 106 and so on are acknowledged, the odd ones are not.
	acked := make([]bool, 30)
	for i := range acked {
		acked[i] = i%2 == 0
	}
	sack := newSelectiveAck(acked)

	if _, _, err := sent.onAck(initSeqNum, sack, 10*time.Millisecond, time.Now()); err != nil {
		t.Fatalf("ack: %v", err)
	}

	batch := sent.TakeLostPackets()
	if len(batch) > maxFastResendsPerAck {
		t.Fatalf("one ack fast retransmitted %d packets, cap is %d: %v",
			len(batch), maxFastResendsPerAck, describeLost(batch))
	}
	if len(batch) != maxFastResendsPerAck {
		t.Fatalf("expected the cap of %d packets to be reached with ten losses, got %d",
			maxFastResendsPerAck, len(batch))
	}

	// The rest are still pending and come out on the next round.
	next := sent.TakeLostPackets()
	if len(next) == 0 {
		t.Error("packets held back by the cap were dropped instead of resent on the next round")
	}
}

func describeLost(packets []*lostPacket) []uint16 {
	seqNums := make([]uint16, 0, len(packets))
	for _, p := range packets {
		seqNums = append(seqNums, p.SeqNum)
	}
	return seqNums
}
