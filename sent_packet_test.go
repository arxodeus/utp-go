package utp_go

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const TEST_DELAY = time.Duration(time.Millisecond * 100)

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
	sentPackets.OnTransmit(seqNum, st_data, data, length, now)

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
	if len(packetInst.acks) != 0 {
		t.Errorf("expected empty acks, got %d", len(packetInst.acks))
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
	first := time.Now()
	second := time.Now()

	sentPackets.OnTransmit(seqNum, st_data, data, length, first)
	sentPackets.OnTransmit(seqNum, st_data, data, length, second)

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
	if len(packetInst.acks) != 0 {
		t.Errorf("expected empty acks, got %d", len(packetInst.acks))
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

	outOfOrderSeqNum := initSeqNum + 2 // wrapping addition for uint16
	data := []byte{0}
	length := uint32(len(data))
	now := time.Now()

	// This should panic due to out of order transmit
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for out of order transmit")
		}
	}()

	sentPackets.OnTransmit(outOfOrderSeqNum, st_data, data, length, now)
}

func TestOnSelectiveAck(t *testing.T) {
	const (
		COUNT    = 10
		SACK_LEN = COUNT - 2
		DELAY    = time.Millisecond * 100 // Assuming this value
	)

	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))

	initSeqNum := uint16(math.MaxUint16)
	sentPackets := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	data := []byte{0}
	length := uint32(len(data))

	// Send COUNT packets
	for i := 0; i < COUNT; i++ {
		now := time.Now()
		seqNum := sentPackets.NextSeqNum()
		sentPackets.OnTransmit(seqNum, st_data, data, length, now)
	}

	// Create selective ACK
	acked := make([]bool, SACK_LEN)
	for i := range acked {
		if i%2 == 0 {
			acked[i] = true
		}
	}
	selectiveAck := NewSelectiveAck(acked)

	// Process ACK
	now := time.Now()
	_, _, err := sentPackets.onAck(initSeqNum+1, selectiveAck, DELAY, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify ACKs
	if len(sentPackets.packets[0].acks) != 1 {
		t.Errorf("expected 1 ack for first packet, got %d", len(sentPackets.packets[0].acks))
	}
	if len(sentPackets.packets[1].acks) != 0 {
		t.Errorf("expected no acks for second packet, got %d", len(sentPackets.packets[1].acks))
	}
	for i := 2; i < COUNT; i++ {
		isEmpty := i%2 != 0
		require.Equal(t, isEmpty, len(sentPackets.packets[i].acks) == 0, fmt.Sprintf("packet %d: expected acks empty=%v, got %v", i, isEmpty, len(sentPackets.packets[i].acks)))
	}
}

func TestDetectLostPackets(t *testing.T) {
	const (
		COUNT = 10
		START = COUNT - LossThreshold
		DELAY = time.Millisecond * 100
	)

	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))

	initSeqNum := uint16(math.MaxUint16)
	sentPackets := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	data := []byte{0}
	length := uint32(len(data))

	// Send packets and selectively acknowledge them
	for i := 0; i < COUNT; i++ {
		now := time.Now()
		seqNum := sentPackets.NextSeqNum()
		sentPackets.OnTransmit(seqNum, st_data, data, length, now)

		if i >= START {
			err := sentPackets.Ack(seqNum, DELAY, now)
			require.NoError(t, err)
		}
	}

	// Detect lost packets
	firstUnacked, err := sentPackets.FirstUnackedSeqNum()
	require.NoError(t, err)
	lost := sentPackets.DetectLostPackets(firstUnacked)

	// Verify lost packets
	for i := 0; i < START; i++ {
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
	const DELAY = time.Millisecond * 100

	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))

	initSeqNum := uint16(math.MaxUint16)
	sentPackets := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	seqNum := sentPackets.NextSeqNum()
	data := []byte{0}
	length := uint32(len(data))
	now := time.Now()
	sentPackets.OnTransmit(seqNum, st_data, data, length, now)

	// Artificially insert packet into lost packets
	sentPackets.lostPackets.ReplaceOrInsert(seqNum)

	// Acknowledge the packet
	now = time.Now()
	err := sentPackets.Ack(seqNum, DELAY, now)
	require.NoError(t, err)

	index := sentPackets.SeqNumIndex(seqNum)
	packetInst := sentPackets.packets[index]

	if len(packetInst.acks) != 1 {
		t.Errorf("expected 1 ack, got %d", len(packetInst.acks))
	}
	if !packetInst.acks[0].Equal(now) {
		t.Errorf("expected ack time %v, got %v", now, packetInst.acks[0])
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
		COUNT   = 10
		ACK_NUM = uint16(3)
		DELAY   = time.Millisecond * 100
	)

	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))

	initSeqNum := uint16(math.MaxUint16)
	sentPackets := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	data := []byte{0}
	length := uint32(len(data))

	// Send COUNT packets
	for i := 0; i < COUNT; i++ {
		now := time.Now()
		seqNum := sentPackets.NextSeqNum()
		sentPackets.OnTransmit(seqNum, st_data, data, length, now)
	}

	// Verify test preconditions
	if int(ACK_NUM) >= COUNT {
		t.Fatal("ACK_NUM must be less than COUNT")
	}
	if COUNT-int(ACK_NUM) <= 2 {
		t.Fatal("COUNT minus ACK_NUM must be greater than 2")
	}

	// Acknowledge prior innerMap packets
	now := time.Now()
	firstUnacked, err := sentPackets.FirstUnackedSeqNum()
	require.NoError(t, err)
	err = sentPackets.AckPriorUnacked(ACK_NUM, firstUnacked, DELAY, now)
	require.NoError(t, err)

	// Verify acknowledgments
	for i := 0; i < int(ACK_NUM); i++ {
		if len(sentPackets.packets[i].acks) != 1 {
			t.Errorf("packet %d: expected 1 ack, got %d", i, len(sentPackets.packets[i].acks))
		}
	}
}

func TestAckUnsent(t *testing.T) {
	const DELAY = time.Millisecond * 100

	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))

	initSeqNum := uint16(math.MaxUint16)
	sentPackets := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	unsentAckNum := initSeqNum + 2 // wrapping addition for uint16
	now := time.Now()

	// This should panic when trying to acknowledge an unsent packet
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when acknowledging unsent packet")
		}
	}()

	err := sentPackets.Ack(unsentAckNum, DELAY, now)
	require.Error(t, err, "expected error when acknowledging unsent packet")
}

func TestSeqNumIndex(t *testing.T) {
	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))

	initSeqNum := uint16(math.MaxUint16)
	sentPackets := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	if sentPackets.SeqNumIndex(initSeqNum) != math.MaxUint16 {
		t.Errorf("expected index %d, got %d", math.MaxUint16, sentPackets.SeqNumIndex(initSeqNum))
	}

	zero := initSeqNum + 1 // wrapping addition for uint16
	if sentPackets.SeqNumIndex(zero) != 0 {
		t.Errorf("expected index 0, got %d", sentPackets.SeqNumIndex(zero))
	}
}

// A packet is fast retransmitted once. Every further ack that still shows it
// missing leaves it alone; only the retransmission timer sends it again.
//
// libutp advances fast_resend_seq_nr past each packet as it resends it
// (utp_internal.cpp:1603) and refuses to fast resend anything below it
// (:1537). Without that, a packet declared lost was resent on every ack until
// its own ack came back: measured on a 2%-loss link, 52 fast retransmissions
// for 7 distinct lost packets, one packet sent 16 extra times.
func TestFastRetransmitHappensOncePerPacket(t *testing.T) {
	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))
	initSeqNum := uint16(100)
	sent := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	data := []byte{0}
	for i := 0; i < 8; i++ {
		sent.OnTransmit(sent.NextSeqNum(), st_data, data, uint32(len(data)), time.Now())
	}

	// 101 is missing; 102 through 105 arrive. That is four acks past the
	// gap, so 101 is declared lost.
	acked := make([]bool, 4)
	for i := range acked {
		acked[i] = true
	}
	sack := NewSelectiveAck(acked)

	if _, _, err := sent.onAck(initSeqNum, sack, 10*time.Millisecond, time.Now()); err != nil {
		t.Fatalf("first ack: %v", err)
	}
	first := sent.TakeLostPackets()
	if len(first) != 1 || first[0].SeqNum != 101 {
		t.Fatalf("first ack should have declared 101 lost, got %v", describeLost(first))
	}

	// The same ack again, and again. 101 is still missing, and must not be
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

// One ack fast retransmits at most four packets, however many it reveals as
// lost. libutp: "Re-send max 4 packets" (utp_internal.cpp:1605-1606).
func TestFastRetransmitIsCappedPerAck(t *testing.T) {
	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))
	ctrl.maxWindowSizeBytes = 1 << 20
	initSeqNum := uint16(100)
	sent := newSentPacketsWithoutLogger(initSeqNum, ctrl)

	data := []byte{0}
	for i := 0; i < 40; i++ {
		sent.OnTransmit(sent.NextSeqNum(), st_data, data, uint32(len(data)), time.Now())
	}

	// Ten missing packets with acked packets above all of them: 102, 104,
	// 106 ... are acked, the odd ones are not.
	acked := make([]bool, 30)
	for i := range acked {
		acked[i] = i%2 == 0
	}
	sack := NewSelectiveAck(acked)

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

	// The rest are still pending, and come out on the next round rather than
	// being forgotten.
	next := sent.TakeLostPackets()
	if len(next) == 0 {
		t.Error("packets held back by the cap were dropped instead of resent on the next round")
	}
}

func describeLost(packets []*LostPacket) []uint16 {
	seqNums := make([]uint16, 0, len(packets))
	for _, p := range packets {
		seqNums = append(seqNums, p.SeqNum)
	}
	return seqNums
}
