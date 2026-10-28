package utp

import (
	"errors"
	"time"

	"github.com/google/btree"
)

// lostFreeList is the node free list every connection's lost-packet tree
// shares, as the receive buffers' reorder trees do (pendingFreeList).
var lostFreeList = btree.NewFreeListG[uint16](btree.DefaultFreeListSize)

const lossThreshold = 3

var errInvalidAckNum = errors.New("invalid ack number")
var errNoneAckNum = errors.New("none ack number")
var errCannotFindLostPacket = errors.New("cannot mark unsent packet lost")
var errSentPacketMarkLost = errors.New("lost packet was previously sent")

type lostPacket struct {
	SeqNum     uint16
	PacketType packetType
	Data       []byte
}

type sentPacket struct {
	seqNum         uint16
	packetType     packetType
	data           []byte
	transmission   time.Time
	retransmission time.Time
	// acked reports whether the packet has been acknowledged.
	acked bool
	// needResend marks a packet declared lost by a retransmission timeout
	// and waiting to be sent again. See MarkAllForResend.
	needResend bool
	// rackLostAt is the transmission RACK last declared lost, and
	// rackPending whether it is waiting to be resent. See rack.go.
	rackLostAt  time.Time
	rackPending bool
	// sentOrder is the number of transmissions before this packet's last
	// one: its position in send order. See rackState.xmitOrder.
	sentOrder uint64
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

type sentPackets struct {
	logger *ulogger
	// packets is the send window: packets[0] has sequence number base and
	// each following one the next. Acknowledged packets at the front are
	// dropped when a new one is added (trimAcked), so it holds little but
	// what is outstanding, and is indexed by distance from base, which is
	// unaffected by wraparound.
	packets []*sentPacket
	// fullAcked is what onAck last returned as cumulatively acknowledged.
	fullAcked circularRangeInclusive
	// spare holds records of acknowledged packets for reuse. A record is
	// read only while its packet is in the window, so one trimmed from the
	// front is free.
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
	// retransmission. Everything below it has been acknowledged or fast
	// retransmitted once already, and only the retransmission timer will
	// send it again. It advances past each packet as it is resent and with
	// the cumulative ack. Without it, every acknowledgement still reporting
	// a packet missing would resend it and halve the window again.
	fastResendSeqNum uint16
	congestionCtrl   controller
	// rack is RFC 8985 loss detection, used instead of the rule above when
	// rack.active. See rack.go.
	rack rackState
	// transmissions counts every packet sent, first or again; see
	// sentPacket.sentOrder.
	transmissions uint64
}

func newSentPackets(initSeqNum uint16, congestionCtrl controller, logger *ulogger) *sentPackets {
	return &sentPackets{
		logger:      logger,
		packets:     make([]*sentPacket, 0),
		base:        initSeqNum + 1,
		lastAck:     initSeqNum,
		initSeqNum:  initSeqNum,
		lostPackets: btree.NewWithFreeListG(2, btree.Less[uint16](), lostFreeList),
		// The number the next packet will take, whichever side opened the
		// connection, so fast retransmit works from any initial sequence
		// number.
		fastResendSeqNum: initSeqNum + 1,
		congestionCtrl:   congestionCtrl,
		rack:             rackState{reoWndMult: 1},
	}
}

func newSentPacketsWithoutLogger(initSeqNum uint16, congestionCtrl controller) *sentPackets {
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
// the controller's records of them. Nothing asks about a packet behind the
// window.
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

// SeqNumRange returns the sequence numbers an acknowledgement may name: from
// the one before the window, which acknowledges nothing new, to the last
// sent.
func (s *sentPackets) SeqNumRange() *circularRangeInclusive {
	end := s.NextSeqNum() - uint16(1)
	return newCircularRangeInclusive(s.base-1, end)
}

func (s *sentPackets) Timeout() time.Duration {
	return s.congestionCtrl.Timeout()
}

// controllerStats returns a snapshot of the congestion controller's state.
func (s *sentPackets) controllerStats() controllerStats {
	return s.congestionCtrl.Stats()
}

// QueueingDelay returns controllerStats().FilteredQueueingDelay, directly
// from the controller when it provides it.
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

// UnackedCount returns how many packets have been sent and not acknowledged.
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

// LastAckedSeqNum returns the sequence number just before the oldest packet
// still outstanding: the number a peer repeats when reporting a hole. Before
// the first acknowledgement it is the number before the first packet sent.
// Sequence numbers are uint16, so the arithmetic wraps.
func (s *sentPackets) LastAckedSeqNum() uint16 {
	return s.NextSeqNum() - 1 - s.UnackedCount()
}

func (s *sentPackets) HasUnackedPackets() bool {
	_, err := s.FirstUnackedSeqNum()
	return err == nil
}

func (s *sentPackets) HasLostPackets() bool {
	return s.lostPackets.Len() != 0 || s.rack.pending > 0
}

// maxFastResendsPerAck caps how many packets one ack may fast retransmit, so
// an ack revealing a burst of losses does not trigger a burst of
// retransmissions on a path that has just shown it cannot carry one.
const maxFastResendsPerAck = 4

// TakeLostPackets returns the packets to fast retransmit now, at most
// maxFastResendsPerAck, and advances fastResendSeqNum past each so it is not
// fast retransmitted again. Taking and marking are one operation so a packet
// cannot be resent twice.
func (s *sentPackets) TakeLostPackets() []*lostPacket {
	if s.rack.active {
		return s.rackTakeLost()
	}
	var result []*lostPacket
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
		result = append(result, &lostPacket{
			packetInst.seqNum,
			packetInst.packetType,
			packetInst.data,
		})
		s.lostPackets.Delete(packetInst.seqNum)
		s.fastResendSeqNum = packetInst.seqNum + 1
	}
	return result
}

// OnTransmit records a packet going out, new or resent.
//
// The send path sends only what the window allows and numbers packets in
// order, so an error here means the connection's bookkeeping is wrong; the
// connection then ends with the error (connection.transmit) rather than
// panicking. The packet is recorded only once nothing has refused it.
func (s *sentPackets) OnTransmit(
	seqNum uint16,
	packetType packetType,
	data []byte,
	dataLen uint32,
	now time.Time,
) error {
	if seqNum == s.NextSeqNum() {
		s.trimAcked()
	}
	index := s.SeqNumIndex(seqNum)
	isRetransmission := index < len(s.packets)

	// Check for out-of-order transmit.
	if index > len(s.packets) {
		if index >= 1<<15 {
			// Behind the window: already acknowledged and dropped. Nothing to
			// account.
			return nil
		}
		return errOutOfOrderTransmit
	}

	// Check the window for new transmissions.
	if !isRetransmission && dataLen > s.Window() {
		return errTransmitExceedsWindow
	}

	var transmit transmitKind
	if isRetransmission {
		transmit = transmitRetransmission
	} else {
		transmit = transmitInitial
	}
	if err := s.congestionCtrl.OnTransmit(seqNum, transmit, dataLen); err != nil {
		return err
	}

	if index < len(s.packets) {
		// Update an existing packet.
		s.packets[index].retransmission = now
		s.transmissions++
		s.packets[index].sentOrder = s.transmissions
		// Sent again, so no longer waiting to be.
		s.packets[index].needResend = false
	} else {
		// Record a new packet.
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
		s.transmissions++
		sent.sentOrder = s.transmissions
		s.packets = append(s.packets, sent)
	}
	return nil
}

// onAckOfNothingNew is onAck for an acknowledgement of the number just before
// the window with no selective ack: it records the delay sample and retires
// nothing.
func (s *sentPackets) onAckOfNothingNew(delay time.Duration, now time.Time) {
	s.congestionCtrl.OnAckDelay(delay, now)
	s.congestionCtrl.ApplyAck()
}

func (s *sentPackets) onAck(
	ackNum uint16,
	selectiveAck *selectiveAck,
	delay time.Duration,
	now time.Time,
) (*circularRangeInclusive, []uint16, error) {
	// Record the delay sample once per acknowledgement, even one whose ack
	// number is discarded.
	s.congestionCtrl.OnAckDelay(delay, now)
	if s.rack.active {
		s.rackOnAck(ackNum, selectiveAck)
	}
	// The window update happens once, whatever path the acknowledgement
	// takes below. detectAndRecordLosses applies it before a loss can decay
	// the window.
	defer s.congestionCtrl.ApplyAck()

	// Check that the ack number is in range.
	seqRange := s.SeqNumRange()
	if !seqRange.Contains(ackNum) {
		if len(s.packets) != 0 && seqRange.end == seqRange.start {
			seqRange = newCircularRangeInclusive(seqRange.start, seqRange.end-1)
			if !seqRange.Contains(ackNum) {
				return nil, nil, errInvalidAckNum
			}
		} else {
			return nil, nil, errInvalidAckNum
		}
	}

	if ackNum != seqRange.Start() {
		if err := s.OnAckNum(ackNum, selectiveAck, delay, now); err != nil {
			return nil, nil, err
		}
	} else if selectiveAck != nil {
		// ackNum is the number before the first packet sent, so nothing is
		// acknowledged cumulatively. The selective ack still names packets
		// that arrived past the gap, which can declare the first packet
		// lost.
		if err := s.applySelectiveAckBits(ackNum, selectiveAck, delay, now); err != nil {
			return nil, nil, err
		}
		if err := s.detectAndRecordLosses(now); err != nil {
			return nil, nil, err
		}
	}

	// Mark all packets up to ackNum as acknowledged. The slice is reused
	// and valid until the next acknowledgement.
	s.fullAcked = circularRangeInclusive{start: seqRange.Start(), end: ackNum}
	fullAcked := &s.fullAcked

	if selectiveAck != nil {
		selectedAcks := make([]uint16, 0)
		acked := selectiveAck.Acked()
		for i, isAcked := range acked {
			if isAcked {
				// wrapping add
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
	selectiveAck *selectiveAck,
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

	// An ACK for ackNum implicitly acknowledges every preceding sequence
	// number. Nothing left unacknowledged is the ordinary outcome of an ack
	// that retires everything in flight, not an error.
	firstUnacked, err := s.FirstUnackedSeqNum()
	switch {
	case err == nil:
		if err = s.AckPriorUnacked(ackNum, firstUnacked, delay, now); err != nil {
			return err
		}
	case !errors.Is(err, errNoneAckNum):
		return err
	}

	// Advance the fast-retransmit floor with the cumulative ack.
	if wrappingLessThan(s.fastResendSeqNum, ackNum+1) {
		s.fastResendSeqNum = ackNum + 1
	}
	if s.rack.active {
		s.rackOnAckNum(ackNum)
	}

	return s.detectAndRecordLosses(now)
}

// detectAndRecordLosses declares newly lost packets and tells the congestion
// controller about each one. A packet is declared lost once: see
// fastResendSeqNum.
func (s *sentPackets) detectAndRecordLosses(now time.Time) error {
	// Apply the window update before any decay a selective ack causes.
	s.congestionCtrl.ApplyAck()
	if s.rack.active {
		s.rackDetectAndMark(now)
		return nil
	}
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
	selectiveAck *selectiveAck,
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
	selectiveAck *selectiveAck,
	delay time.Duration,
	now time.Time,
) error {
	seqRange := s.SeqNumRange()

	// The first bit of the selective ack is ackNum+2; ackNum+1 is
	// presumed dropped.
	sackNum := ackNum + 2

	for _, ack := range selectiveAck.Acked() {
		// Stop once past the last sequence number sent; the bitfield is a
		// multiple of 32 bits, so it may be padded.
		if !seqRange.Contains(sackNum) {
			break
		}

		if ack {
			if err := s.Ack(sackNum, delay, now); err != nil {
				return err
			}
		}

		sackNum += uint16(1) // wrapping add
	}
	return nil
}

func (s *sentPackets) DetectLostPackets(firstUnacked uint16) []uint16 {
	var lost []uint16
	acked := 0

	startIndex := s.SeqNumIndex(firstUnacked)
	packets := s.packets[startIndex:]

	// Iterate in reverse order.
	for i := len(packets) - 1; i >= 0; i-- {
		packetInst := packets[i]

		if !packetInst.acked && acked >= lossThreshold &&
			!wrappingLessThan(packetInst.seqNum, s.fastResendSeqNum) {
			// A packet already fast retransmitted once is not declared lost
			// again, so the window is not halved twice for one loss.
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
	if index >= len(s.packets) {
		// Past what was sent. The connection drops such acknowledgements
		// before they get here; this guards against indexing past the
		// window.
		return errAckOfUnsent
	}
	packetInst := s.packets[index]
	ack := ackSample{
		Delay:      delay,
		RTT:        packetInst.rtt(now),
		ReceivedAt: now,
	}

	if err := s.congestionCtrl.OnAck(packetInst.seqNum, ack); err != nil {
		return err
	}
	if s.logger != nil && s.logger.Enabled(baseContext, LevelTrace) {
		s.logger.Trace("record Acks", "seqNum", packetInst.seqNum, "acked", packetInst.acked)
	}
	if !packetInst.acked {
		packetInst.acked = true
		s.lostPackets.Delete(packetInst.seqNum)
		if s.rack.active {
			s.rackOnDelivered(packetInst, now)
		}
	}
	return nil
}

func (s *sentPackets) AckPriorUnacked(seqNum uint16, firstUnacked uint16, delay time.Duration, now time.Time) error {
	start := s.SeqNumIndex(firstUnacked)
	end := s.SeqNumIndex(seqNum)
	if start >= end {
		return nil
	}
	if s.logger != nil && s.logger.Enabled(baseContext, LevelTrace) {
		s.logger.Trace("AckPriorUnacked", "sentPackets.len", len(s.packets), "start", start, "end", end)
	}
	for _, packetInst := range s.packets[start:end] {
		if s.logger != nil && s.logger.Enabled(baseContext, LevelTrace) {
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
		return errCannotFindLostPacket
	}

	err := s.congestionCtrl.OnLostPacket(seqNum, retransmitting, now)
	if err != nil {
		return errSentPacketMarkLost
	}
	return nil
}

// SeqNumIndex is seqNum's position in the window, counting from packets[0].
// A number before the window comes out at 32768 or more.
func (s *sentPackets) SeqNumIndex(seqNum uint16) int {
	return int(seqNum - s.base)
}

// Outstanding reports whether seqNum names a packet that was sent and has not
// been acknowledged. The retransmission timer uses it: a timer that has
// already fired can no longer be canceled, and acting on it would resend a
// packet the peer already has.
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

// MarkAllForResend declares every outstanding packet lost, as on a
// retransmission timeout. Each is flagged to be resent and stops counting as
// in flight. Nothing is sent here: the caller resends the oldest, and the
// rest go out oldest first as the window allows (NextNeedingResend).
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

// NextNeedingResend returns the oldest outstanding packet waiting to be
// resent after a retransmission timeout, if any.
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

// OldestOutstanding returns the packet a retransmission timeout resends.
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
		return 0, errNoneAckNum
	}

	var seqNum uint16
	lastAckNum, isNone := s.LastAckNum()
	if s.logger != nil && s.logger.Enabled(baseContext, LevelTrace) {
		s.logger.Trace("get last innerMap num",
			"lastAckNum", lastAckNum, "isNone", isNone)
	}
	if isNone {
		seqNum = s.base
	} else {
		if s.packets[len(s.packets)-1].seqNum == lastAckNum {
			return 0, errNoneAckNum
		}
		seqNum = lastAckNum + 1 // wrapping add
	}

	return seqNum, nil
}
