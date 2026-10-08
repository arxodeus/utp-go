package utp_go

import (
	"errors"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/google/btree"
)

// lostFreeList is the node free list every connection's lost-packet tree
// shares, as the receive buffers' reorder trees do (pendingFreeList).
var lostFreeList = btree.NewFreeListG[uint16](btree.DefaultFreeListSize)

const LossThreshold = 3

var ErrInvalidAckNum = errors.New("invalid ack number")
var ErrNoneAckNum = errors.New("none ack number")
var ErrCannotFindLostPacket = errors.New("cannot mark unsent packet lost")
var ErrSentPacketMarkLost = errors.New("lost packet was previously sent")

type LostPacket struct {
	SeqNum     uint16
	PacketType PacketType
	Data       []byte
}

type sentPacket struct {
	seqNum         uint16
	packetType     PacketType
	data           []byte
	transmission   time.Time
	retransmission time.Time
	// acked is whether the packet has been acknowledged. It was a slice of
	// the times it was, appended to on the first and only ever asked
	// whether it was empty: an allocation per packet for a boolean.
	acked bool
	// needResend is libutp's need_resend: given up as lost by a
	// retransmission timeout and waiting to be sent again. See
	// MarkAllForResend.
	needResend bool
}

func (s *sentPacket) rtt(now time.Time) time.Duration {
	var lastTransmission time.Time
	if s.retransmission == s.transmission {
		lastTransmission = s.transmission
	} else {
		lastTransmission = s.retransmission
	}
	return now.Sub(lastTransmission)
}

type LostPacketSeqNums []uint16

func (l LostPacketSeqNums) Remove(seq uint16) LostPacketSeqNums {
	var n []uint16
	for i, seqNum := range l {
		if seqNum == seq {
			n = append((l)[:i], (l)[i+1:]...)
			break
		}
	}
	return n
}

type sentPackets struct {
	logger log.Logger
	// packets is the send window: packets[0] is sequence number base, and
	// each one after it the next. Acknowledged packets at the front are
	// dropped when a new one is added (trimAcked), so this holds what is
	// outstanding and little else -- libutp's outbuf, which holds
	// cur_window_packets (utp_internal.cpp:1071-1077).
	//
	// It used to hold every packet the connection had ever sent, indexed by
	// distance from the initial sequence number modulo 65536. Past 65,535
	// packets -- about 90 MB -- a new sequence number landed on an old,
	// acknowledged entry: the new packet was booked as a retransmission of
	// it, bytes in flight stopped counting it, and the sender ran away at
	// the full rate of its event loop, measured at 22,000 packets a second
	// into a 10 Mbps link. Nothing acknowledged was ever freed either.
	packets []*sentPacket
	// fullAcked is what onAck last returned as cumulatively acknowledged.
	fullAcked circularRangeInclusive
	// spare holds records of acknowledged packets, for the next sent. A
	// record is looked at only while its packet is in the window, so one
	// trimmed from the front is free.
	spare []*sentPacket
	// base is the sequence number of packets[0], or the next to be sent
	// when packets is empty.
	base uint16
	// lastAck is the highest sequence number acknowledged in order from the
	// first, among those already dropped from packets; hasAck says whether
	// there is one. See LastAckNum.
	lastAck     uint16
	hasAck      bool
	initSeqNum  uint16
	lostPackets *btree.BTreeG[uint16]
	// fastResendSeqNum is the lowest sequence number still eligible for fast
	// retransmission. Everything below it has either been acked or been fast
	// retransmitted once already, and only the retransmission timer will send
	// it again.
	//
	// This is libutp's `fast_resend_seq_nr` (utp_internal.cpp:470), advanced
	// past each packet as it is resent (:1603) and forward with the
	// cumulative ack (:2186-2188), and tested before any fast retransmit
	// (:1537, :1560).
	//
	// Without it a packet declared lost stayed lost until its own ack came
	// back, and every ack that arrived in the meantime resent it again and
	// halved the window again. Measured on a 2%-loss link: 52 fast
	// retransmissions for 7 distinct lost packets, one packet sent 16 extra
	// times.
	fastResendSeqNum uint16
	congestionCtrl   Controller
}

func newSentPackets(initSeqNum uint16, congestionCtrl Controller, logger log.Logger) *sentPackets {
	return &sentPackets{
		logger:      logger,
		packets:     make([]*sentPacket, 0),
		base:        initSeqNum + 1,
		lastAck:     initSeqNum,
		initSeqNum:  initSeqNum,
		lostPackets: btree.NewWithFreeListG(2, btree.Less[uint16](), lostFreeList),
		// The number the next packet will take, in both roles. libutp's
		// accepting side does the same (utp_internal.cpp:2988-2989); its
		// initiator does not -- fast_resend_seq_nr stays at 1 when
		// utp_connect randomises seq_nr (:2615, :2768), and for half of all
		// starting numbers never catches up. Not copied; see
		// TestFastRetransmitFromAnyInitialSequenceNumber.
		fastResendSeqNum: initSeqNum + 1,
		congestionCtrl:   congestionCtrl,
	}
}

func newSentPacketsWithoutLogger(initSeqNum uint16, congestionCtrl Controller) *sentPackets {
	return newSentPackets(initSeqNum, congestionCtrl, nil)
}

func (s *sentPackets) OnTimeout() {
	s.congestionCtrl.OnTimeout(s.HasUnackedPackets())
}

func (s *sentPackets) NextSeqNum() uint16 {
	return s.base + uint16(len(s.packets))
}

// transmissionForgetter is a controller that can drop its record of a packet
// once the sender is done with it. See defaultController.forgetTransmission.
type transmissionForgetter interface {
	forgetTransmission(seqNum uint16)
}

// trimAcked drops the acknowledged packets at the front of the window, and
// the controller's records of them: nothing asks about a packet behind the
// window (Ack, OnLost and MarkAllForResend all stop at it).
func (s *sentPackets) trimAcked() {
	n := 0
	for n < len(s.packets) && s.packets[n].acked {
		n++
	}
	if n == 0 {
		return
	}
	s.lastAck, s.hasAck = s.packets[n-1].seqNum, true
	forget, _ := s.congestionCtrl.(transmissionForgetter)
	for i := 0; i < n; i++ {
		if forget != nil {
			forget.forgetTransmission(s.packets[i].seqNum)
		}
		*s.packets[i] = sentPacket{}
		s.spare = append(s.spare, s.packets[i])
		s.packets[i] = nil
	}
	s.packets = s.packets[n:]
	s.base += uint16(n)
}

func (s *sentPackets) AckNum() uint16 {
	num, isNone := s.LastAckNum()
	if isNone {
		return 0
	}
	return num
}

// SeqNumRange is the sequence numbers an acknowledgement may name: the one
// before the window, which acknowledges nothing new, through the last sent.
func (s *sentPackets) SeqNumRange() *circularRangeInclusive {
	end := s.NextSeqNum() - uint16(1)
	return newCircularRangeInclusive(s.base-1, end)
}

func (s *sentPackets) Timeout() time.Duration {
	return s.congestionCtrl.Timeout()
}

// ControllerStats returns a snapshot of the congestion controller's state.
func (s *sentPackets) ControllerStats() ControllerStats {
	return s.congestionCtrl.Stats()
}

// QueueingDelay is ControllerStats().FilteredQueueingDelay, from the
// controller directly when it offers it.
func (s *sentPackets) QueueingDelay() time.Duration {
	if q, ok := s.congestionCtrl.(interface{ QueueingDelay() time.Duration }); ok {
		return q.QueueingDelay()
	}
	return s.congestionCtrl.Stats().FilteredQueueingDelay
}

func (s *sentPackets) Window() uint32 {
	return s.congestionCtrl.BytesAvailableInWindow()
}

func (s *sentPackets) CongestionWindow() uint32 {
	return s.congestionCtrl.CongestionWindow()
}

func (s *sentPackets) BytesInFlight() uint32 {
	return s.congestionCtrl.BytesInFlight()
}

// OnWindowFull records that the sender was blocked by the congestion window
// rather than by having nothing to send.
func (s *sentPackets) OnWindowFull(now time.Time) {
	s.congestionCtrl.OnWindowFull(now)
}

// OnPeerDelay passes the delay measured on an inbound packet to the
// controller, which uses it to detect clock drift. See
// defaultController.OnPeerDelay.
func (s *sentPackets) OnPeerDelay(sample uint32, now time.Time) {
	s.congestionCtrl.OnPeerDelay(sample, now)
}

// OnTick lets a time-driven controller advance without an ack.
func (s *sentPackets) OnTick(now time.Time) {
	s.congestionCtrl.OnTick(now)
}

// UnackedCount is how many packets have been sent and not yet acknowledged --
// libutp's `cur_window_packets`.
func (s *sentPackets) UnackedCount() uint16 {
	if len(s.packets) == 0 {
		return 0
	}
	lastAck, none := s.LastAckNum()
	if none {
		return uint16(len(s.packets))
	}
	return s.NextSeqNum() - 1 - lastAck
}

// LastAckedSeqNum is the sequence number just before the oldest packet still
// outstanding -- the number a peer repeats when it is reporting a hole.
//
// libutp computes it inline as `seq_nr - cur_window_packets - 1`
// (utp_internal.cpp:1922). The arithmetic here is the same: UnackedCount is
// `NextSeqNum() - 1 - lastAck` whenever anything has been acknowledged, so
// this recovers lastAck, and before the first acknowledgement it gives the
// number before the first packet sent, which is what libutp's expression
// gives there too.
//
// Wrapping is deliberate and correct: sequence numbers are uint16 and libutp
// masks with ACK_NR_MASK for the same reason.
func (s *sentPackets) LastAckedSeqNum() uint16 {
	return s.NextSeqNum() - 1 - s.UnackedCount()
}

func (s *sentPackets) HasUnackedPackets() bool {
	_, err := s.FirstUnackedSeqNum()
	return err == nil
}

func (s *sentPackets) HasLostPackets() bool {
	return s.lostPackets.Len() != 0
}

// maxFastResendsPerAck caps how many packets one ack may fast retransmit.
//
// libutp: "Re-send max 4 packets" (utp_internal.cpp:1605-1606). Without a cap
// a single ack that reveals a burst of losses answers with a burst of
// retransmissions, on a path that has just demonstrated it cannot carry one.
const maxFastResendsPerAck = 4

// TakeLostPackets returns the packets to fast retransmit now, at most
// maxFastResendsPerAck of them, and advances fastResendSeqNum past each so it
// is not fast retransmitted again. Only the retransmission timer will send
// them a third time.
//
// It mutates, unlike an ordinary getter, because taking a packet and marking
// it taken cannot be separated without reintroducing the duplicate-resend bug
// this exists to prevent.
func (s *sentPackets) TakeLostPackets() []*LostPacket {
	var result []*LostPacket
	var stale []uint16

	// In window order, oldest first. The set is ordered by raw sequence
	// number, which puts 0 before 65535 and so the newest packets first
	// whenever the window spans a wrap.
	s.lostPackets.Ascend(func(seqNum uint16) bool {
		if wrappingLessThan(seqNum, s.fastResendSeqNum) || s.SeqNumIndex(seqNum) >= len(s.packets) {
			// Already acked or already fast retransmitted.
			stale = append(stale, seqNum)
		}
		return true
	})
	for _, seqNum := range stale {
		s.lostPackets.Delete(seqNum)
	}
	for _, packetInst := range s.packets {
		if len(result) >= maxFastResendsPerAck || s.lostPackets.Len() == 0 {
			break
		}
		if !s.lostPackets.Has(packetInst.seqNum) {
			continue
		}
		result = append(result, &LostPacket{
			packetInst.seqNum,
			packetInst.packetType,
			packetInst.data,
		})
		s.lostPackets.Delete(packetInst.seqNum)
		s.fastResendSeqNum = packetInst.seqNum + 1
	}
	return result
}

func (s *sentPackets) OnTransmit(
	seqNum uint16,
	packetType PacketType,
	data []byte,
	dataLen uint32,
	now time.Time,
) {
	if seqNum == s.NextSeqNum() {
		s.trimAcked()
	}
	index := s.SeqNumIndex(seqNum)
	isRetransmission := index < len(s.packets)

	// Check for out of order transmit
	if index > len(s.packets) {
		if index >= 1<<15 {
			// Behind the window: a packet already acknowledged and dropped.
			// Nothing to account.
			return
		}
		panic("out of order transmit")
	}

	// Check window size for new transmissions
	if !isRetransmission && dataLen > s.Window() {
		panic("transmit exceeds available send window")
	}

	if index < len(s.packets) {
		// Update existing packet
		s.packets[index].retransmission = now
		// Sent again, so no longer waiting to be: libutp clears need_resend
		// in send_packet (utp_internal.cpp:881).
		s.packets[index].needResend = false
	} else {
		// Create new packet
		var sent *sentPacket
		if k := len(s.spare); k > 0 {
			sent, s.spare = s.spare[k-1], s.spare[:k-1]
		} else {
			sent = new(sentPacket)
		}
		*sent = sentPacket{
			seqNum:         seqNum,
			packetType:     packetType,
			data:           data,
			transmission:   now,
			retransmission: now,
		}
		s.packets = append(s.packets, sent)
	}

	var transmit Transmit
	if isRetransmission {
		transmit = Retransmission
	} else {
		transmit = Initial
	}

	if err := s.congestionCtrl.OnTransmit(seqNum, transmit, dataLen); err != nil {
		panic(err)
	}
}

// onAckOfNothingNew is onAck for an acknowledgement of the number just before
// the window with no selective ack: the delay sample, and nothing to retire.
func (s *sentPackets) onAckOfNothingNew(delay time.Duration, now time.Time) {
	s.congestionCtrl.OnAckDelay(delay, now)
	s.congestionCtrl.ApplyAck()
}

func (s *sentPackets) onAck(
	ackNum uint16,
	selectiveAck *SelectiveAck,
	delay time.Duration,
	now time.Time,
) (*circularRangeInclusive, []uint16, error) {
	// Once per acknowledgement, whatever it acknowledges: libutp records the
	// sample even from one whose ack number it discards (utp_internal.cpp:
	// 1907, :2023-2024).
	s.congestionCtrl.OnAckDelay(delay, now)
	// Whatever path the acknowledgement takes below, its window update
	// happens once. detectAndRecordLosses applies it earlier, before a loss
	// can decay the window, as libutp's order has it.
	defer s.congestionCtrl.ApplyAck()

	// Check if ack number is in valid range
	seqRange := s.SeqNumRange()
	if !seqRange.Contains(ackNum) {
		if len(s.packets) != 0 && seqRange.end == seqRange.start {
			seqRange = newCircularRangeInclusive(seqRange.start, seqRange.end-1)
			if !seqRange.Contains(ackNum) {
				return nil, nil, ErrInvalidAckNum
			}
		} else {
			return nil, nil, ErrInvalidAckNum
		}
	}

	if ackNum != seqRange.Start() {
		if err := s.OnAckNum(ackNum, selectiveAck, delay, now); err != nil {
			return nil, nil, err
		}
	} else if selectiveAck != nil {
		// ackNum names the sequence number before the first packet we sent,
		// so there is nothing to acknowledge cumulatively. The selective ack
		// still does: it names packets that arrived past the gap, and those
		// are what declare the first packet lost.
		//
		// This branch previously did nothing at all, which meant that when
		// the very first packet of a connection was lost, every selective ack
		// the peer sent about it was discarded -- no packet was ever declared
		// lost, no fast retransmit happened, and recovery waited for the
		// retransmission timeout. libutp processes the extension whatever
		// ack_nr says (utp_internal.cpp:2289).
		if err := s.applySelectiveAckBits(ackNum, selectiveAck, delay, now); err != nil {
			return nil, nil, err
		}
		if err := s.detectAndRecordLosses(now); err != nil {
			return nil, nil, err
		}
	}

	// Mark all packets up to ackNum as acknowledged
	// Valid until the next acknowledgement, which is as long as anyone uses
	// it: a new one for each was an allocation per acknowledgement.
	s.fullAcked = circularRangeInclusive{start: seqRange.Start(), end: ackNum}
	fullAcked := &s.fullAcked

	if selectiveAck != nil {
		selectedAcks := make([]uint16, 0)
		acked := selectiveAck.Acked()
		for i, isAcked := range acked {
			if isAcked {
				// Double wrapping addition for uint16
				indexNum := ackNum + uint16(2) + uint16(i)
				selectedAcks = append(selectedAcks, indexNum)
			}
		}
		return fullAcked, selectedAcks, nil
	}
	return fullAcked, nil, nil
}

func (s *sentPackets) OnAckNum(
	ackNum uint16,
	selectiveAck *SelectiveAck,
	delay time.Duration,
	now time.Time,
) error {
	var err error
	if selectiveAck != nil {
		err = s.OnSelectiveAck(ackNum, selectiveAck, delay, now)
	} else {
		err = s.Ack(ackNum, delay, now)
	}
	if err != nil {
		return err
	}

	// An ACK for ackNum implicitly ACKs all sequence numbers that precede ackNum
	// Account for any preceding innerMap packets
	//
	// Nothing left unacknowledged is the ordinary outcome of an ack that
	// retires everything in flight, not an error. It used to be returned as
	// one, and processAck then stopped short of everything after the ack
	// itself: the timers of the retired packets stayed armed, the
	// retransmission deadline was not restarted, the consecutive-timeout
	// count was not reset, and an MTU probe's acknowledgement was never seen.
	// On any connection not sending flat out, that was every ack. libutp's
	// ack_packet has no such failure (utp_internal.cpp:1329-1400).
	firstUnacked, err := s.FirstUnackedSeqNum()
	switch {
	case err == nil:
		if err = s.AckPriorUnacked(ackNum, firstUnacked, delay, now); err != nil {
			return err
		}
	case !errors.Is(err, ErrNoneAckNum):
		return err
	}

	// Advance the fast-retransmit floor with the cumulative ack, as libutp
	// does at utp_internal.cpp:2186-2188.
	if wrappingLessThan(s.fastResendSeqNum, ackNum+1) {
		s.fastResendSeqNum = ackNum + 1
	}

	return s.detectAndRecordLosses(now)
}

// detectAndRecordLosses declares newly lost packets and tells the congestion
// controller about each one. A packet is declared lost once: see
// fastResendSeqNum.
func (s *sentPackets) detectAndRecordLosses(now time.Time) error {
	// libutp's apply_ccontrol (utp_internal.cpp:2139) precedes the decay a
	// selective ack can cause (:2289).
	s.congestionCtrl.ApplyAck()
	firstUnacked, err := s.FirstUnackedSeqNum()
	if err != nil {
		// Nothing outstanding, so nothing can be lost.
		return nil
	}
	for _, seqNum := range s.DetectLostPackets(firstUnacked) {
		s.lostPackets.ReplaceOrInsert(seqNum)
		_ = s.OnLost(seqNum, true, now)
	}
	return nil
}

func (s *sentPackets) OnSelectiveAck(
	ackNum uint16,
	selectiveAck *SelectiveAck,
	delay time.Duration,
	now time.Time,
) error {
	if err := s.Ack(ackNum, delay, now); err != nil {
		return err
	}
	return s.applySelectiveAckBits(ackNum, selectiveAck, delay, now)
}

// applySelectiveAckBits acknowledges the packets named by a selective ack's
// bitfield, without touching ackNum itself.
func (s *sentPackets) applySelectiveAckBits(
	ackNum uint16,
	selectiveAck *SelectiveAck,
	delay time.Duration,
	now time.Time,
) error {
	seqRange := s.SeqNumRange()

	// The first bit of the selective ACK corresponds to ackNum + 2,
	// where ackNum + 1 is assumed to have been dropped
	sackNum := ackNum + 2

	for _, ack := range selectiveAck.Acked() {
		// Break once we exhaust all sent sequence numbers
		// The selective ACK length is a multiple of 32, so it may be padded
		if !seqRange.Contains(sackNum) {
			break
		}

		if ack {
			if err := s.Ack(sackNum, delay, now); err != nil {
				return err
			}
		}

		sackNum += uint16(1) // wrapping addition for uint16
	}
	return nil
}

func (s *sentPackets) DetectLostPackets(firstUnacked uint16) []uint16 {
	var lost []uint16
	acked := 0

	startIndex := s.SeqNumIndex(firstUnacked)
	packets := s.packets[startIndex:]

	// Iterate in reverse order
	for i := len(packets) - 1; i >= 0; i-- {
		packetInst := packets[i]

		if !packetInst.acked && acked >= LossThreshold &&
			!wrappingLessThan(packetInst.seqNum, s.fastResendSeqNum) {
			// The fastResendSeqNum test is libutp's, at
			// utp_internal.cpp:1537: a packet already fast retransmitted once
			// is not declared lost again, so the window is not halved again
			// for the same loss.
			lost = append(lost, packetInst.seqNum)
		}
		if packetInst.acked {
			acked++
		}
	}

	return lost
}

func (s *sentPackets) Ack(seqNum uint16, delay time.Duration, now time.Time) error {
	index := s.SeqNumIndex(seqNum)
	if index >= 1<<15 {
		// Behind the window: already acknowledged and dropped.
		return nil
	}
	packetInst := s.packets[index]
	ack := Ack{
		Delay:      delay,
		RTT:        packetInst.rtt(now),
		ReceivedAt: now,
	}

	if err := s.congestionCtrl.OnAck(packetInst.seqNum, ack); err != nil {
		return err
	}
	if s.logger != nil && s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		log.Trace("record Acks", "seqNum", packetInst.seqNum, "acked", packetInst.acked)
	}
	if !packetInst.acked {
		packetInst.acked = true
		s.lostPackets.Delete(packetInst.seqNum)
	}
	return nil
}

func (s *sentPackets) AckPriorUnacked(seqNum uint16, firstUnacked uint16, delay time.Duration, now time.Time) error {
	start := s.SeqNumIndex(firstUnacked)
	end := s.SeqNumIndex(seqNum)
	if start >= end {
		return nil
	}
	if s.logger != nil && s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		s.logger.Trace("AckPriorUnacked", "sentPackets.len", len(s.packets), "start", start, "end", end)
	}
	for _, packetInst := range s.packets[start:end] {
		if s.logger != nil && s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			s.logger.Trace("record Ack", "seqNum", packetInst.seqNum)
		}
		if err := s.Ack(packetInst.seqNum, delay, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *sentPackets) LastAckNum() (uint16, bool) {
	num, none := s.lastAck, !s.hasAck
	if none {
		num = 0
	}
	for _, packetInst := range s.packets {
		if packetInst.acked {
			num = packetInst.seqNum
			none = false
		} else {
			break
		}
	}
	return num, none
}

func (s *sentPackets) OnLost(seqNum uint16, retransmitting bool, now time.Time) error {
	if !s.SeqNumRange().Contains(seqNum) {
		return ErrCannotFindLostPacket
	}

	err := s.congestionCtrl.OnLostPacket(seqNum, retransmitting, now)
	if err != nil {
		return ErrSentPacketMarkLost
	}
	return nil
}

// SeqNumIndex is seqNum's position in the window, counting from packets[0].
// A number before the window comes out at 32768 or more.
func (s *sentPackets) SeqNumIndex(seqNum uint16) int {
	return int(seqNum - s.base)
}

// Outstanding reports whether seqNum names a packet that was sent and has not
// been acknowledged.
//
// It exists for the retransmission timer. Timers are armed per packet and
// cancelled when the ack arrives, but a timer that has already fired is out of
// the wheel and on its way to the event loop, where cancelling it can no
// longer stop it. Acting on such a timeout resends a packet the peer already
// has.
func (s *sentPackets) Outstanding(seqNum uint16) bool {
	if len(s.packets) == 0 {
		return false
	}
	if !s.SeqNumRange().Contains(seqNum) {
		return false
	}
	i := s.SeqNumIndex(seqNum)
	if i < 0 || i >= len(s.packets) {
		return false
	}
	return !s.packets[i].acked
}

// MarkAllForResend gives up every packet still outstanding as lost, as libutp
// does on a retransmission timeout: "every packet should be considered lost"
// (utp_internal.cpp:1230-1237). Each is flagged to be sent again and stops
// counting in flight. Nothing is sent here: the caller resends the oldest,
// and the rest go out oldest first as the window allows -- see
// NextNeedingResend.
func (s *sentPackets) MarkAllForResend() {
	first, err := s.FirstUnackedSeqNum()
	if err != nil {
		return
	}
	for i := s.SeqNumIndex(first); i >= 0 && i < len(s.packets); i++ {
		pkt := s.packets[i]
		if pkt.acked || pkt.needResend {
			continue
		}
		pkt.needResend = true
		s.congestionCtrl.MarkForResend(pkt.seqNum)
	}
}

// NextNeedingResend is the oldest outstanding packet waiting to be sent again
// after a retransmission timeout, if any. libutp's flush_packets walks the
// same packets in the same order (utp_internal.cpp:970-985).
func (s *sentPackets) NextNeedingResend() (*sentPacket, bool) {
	first, err := s.FirstUnackedSeqNum()
	if err != nil {
		return nil, false
	}
	for i := s.SeqNumIndex(first); i >= 0 && i < len(s.packets); i++ {
		pkt := s.packets[i]
		if pkt.needResend && !pkt.acked {
			return pkt, true
		}
	}
	return nil, false
}

// OldestOutstanding is the packet a retransmission timeout resends: libutp's
// `outbuf.get(seq_nr - cur_window_packets)` (utp_internal.cpp:1249).
func (s *sentPackets) OldestOutstanding() (*sentPacket, bool) {
	first, err := s.FirstUnackedSeqNum()
	if err != nil {
		return nil, false
	}
	i := s.SeqNumIndex(first)
	if i < 0 || i >= len(s.packets) {
		return nil, false
	}
	return s.packets[i], true
}

func (s *sentPackets) FirstUnackedSeqNum() (uint16, error) {
	if len(s.packets) == 0 {
		return 0, ErrNoneAckNum
	}

	var seqNum uint16
	lastAckNum, isNone := s.LastAckNum()
	if s.logger != nil && s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		s.logger.Trace("get last innerMap num",
			"lastAckNum", lastAckNum, "isNone", isNone)
	}
	if isNone {
		seqNum = s.base
	} else {
		if s.packets[len(s.packets)-1].seqNum == lastAckNum {
			return 0, ErrNoneAckNum
		}
		seqNum = lastAckNum + 1 // wrapping addition for uint16
	}

	return seqNum, nil
}
