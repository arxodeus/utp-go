package utp

import (
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"
)

// unackTimeoutQueueLen is the capacity of a connection's queue of expired
// retransmission timers. When the timeout passes, every outstanding packet's
// timer expires on the same tick, but only the first delivered acts; the
// rest find the deadline moved and re-arm (onTimeout). The wheel never
// blocks on a full queue but re-arms for its next tick, so a small queue
// suffices.
const unackTimeoutQueueLen = 16

// armRetransmit schedules a retransmission timer for one packet.
func (c *connection) armRetransmit(pkt *packet, delay time.Duration) {
	seq := pkt.Header.SeqNum
	if len(c.armed) == 0 {
		// First packet into an empty window: start the connection's RTO.
		c.rtoDeadline = c.now().Add(delay)
		c.rearmLossProbe(c.lossProbeAckNum)
	}
	c.armed[seq] = struct{}{}
	c.timers.arm(
		retransmitKey{scope: c.timerScope, seq: seq},
		retransmitTimer{packet: pkt, deliver: c.unackTimeoutCh, ctx: c.ctx, wake: c.wakeFn},
		delay,
	)
}

// isArmed reports whether seq has a retransmission timer.
func (c *connection) isArmed(seq uint16) bool {
	_, ok := c.armed[seq]
	return ok
}

// disarmRetransmit cancels one packet's retransmission timer. It always
// removes the key from the wheel: a timer that has fired is out of the wheel
// but still in the armed set until the event loop drains it. Both removals
// are no-ops when the key is absent.
func (c *connection) disarmRetransmit(seq uint16) {
	delete(c.armed, seq)
	c.timers.disarm(retransmitKey{scope: c.timerScope, seq: seq})
}

// disarmAcked cancels the timers this connection has armed for sequence
// numbers in acked, scanning only its own armed set rather than the shared
// wheel. It returns how many it canceled, which is how many packets the ack
// newly retired.
func (c *connection) disarmAcked(acked *circularRangeInclusive) int {
	n := 0
	for seq := range c.armed {
		if acked.Contains(seq) {
			delete(c.armed, seq)
			c.timers.disarm(retransmitKey{scope: c.timerScope, seq: seq})
			n++
		}
	}
	return n
}

// disarmAll cancels every timer this connection holds. The shared wheel
// outlives the connection, so anything left armed would later expire into a
// dead event loop.
func (c *connection) disarmAll() {
	for seq := range c.armed {
		c.timers.disarm(retransmitKey{scope: c.timerScope, seq: seq})
	}
	clear(c.armed)
}

func (c *connection) onTimeout(originPacket *packet, now time.Time) {
	switch c.state.stateType {
	case connConnecting:
		if c.endpoint.Type == acceptor {
			return
		}
		if c.endpoint.Attempts >= c.config.MaxConnAttempts {
			c.logger.Error("quitting connection attempt", "attempts", c.endpoint.Attempts)
			err := ErrTimedOut
			if c.state.connectedCh != nil {
				c.state.connectedCh <- err
			}
			c.state.stateType = connClosed
			c.state.Err = err
			return
		} else {
			seq := c.endpoint.SynNum
			logMsg := fmt.Sprintf("retrying connection, after %d attempts", c.endpoint.Attempts)
			switch c.endpoint.Attempts {
			case 1, 0:
				c.logger.Trace(logMsg)
			case 2:
				c.logger.Debug(logMsg)
			case 3:
				c.logger.Info(logMsg)
			default:
				c.logger.Warn(logMsg)
			}
			c.endpoint.Attempts += 1

			// Double the timeout on each retransmission. The cap only matters if
			// a caller raises MaxConnAttempts; at the default the give-up
			// limit bounds the backoff.
			c.synTimeout = capTimeout(c.synTimeout*2, c.config.MaxTimeout)

			c.armRetransmit(originPacket, c.synTimeout)

			// Resend the SYN.
			c.emit(c.synPacket(seq))
		}

	case connConnected:
		// If the timed-out packet is a SYN, do nothing.
		if originPacket.Header.PacketType == stSyn {
			return
		}

		// A timeout for a packet the peer has already acknowledged is no
		// timeout: a timer that fired before the ack arrived cannot be
		// canceled. Acting on it would cut the window and resend data the
		// peer has.
		if c.state.SentPackets != nil &&
			!c.state.SentPackets.Outstanding(originPacket.Header.SeqNum) {
			return
		}

		// One timeout per RTO expiry, checked against the deadline, and one
		// packet resent for it. Every outstanding packet is marked for
		// resend, which takes it out of flight, and only the oldest is
		// resent now. The rest go out oldest first from processWrites as
		// the window, just cut to one packet, allows, or one per
		// acknowledgement through fastTimeout. Each packet has its own
		// timer, so an expiry arrives as several callbacks; the others
		// resend nothing.
		now := c.now()

		// A retransmission timeout on the MTU probe with nothing else
		// outstanding means the path will not carry that size, not that it
		// is congested: lower the ceiling and leave the window alone.
		probeTimedOut := c.mtu.probeOutstanding(originPacket.Header.SeqNum) &&
			c.state.SentPackets.UnackedCount() == 1
		if probeTimedOut && !now.Before(c.rtoDeadline) {
			// The rest of the timeout still applies: the give-up check comes
			// first, and the timeout counts towards it and starts the
			// fast-timeout retry. Only the doubling and the window collapse
			// are skipped.
			if c.retransmitCount >= maxConsecutiveTimeouts {
				c.state.stateType = connClosed
				c.state.Err = ErrTimedOut
				return
			}
			c.retransmitCount++
			c.fastTimeout, c.probeRecovery = true, false
			c.mtu.onProbeLost(now)
			c.logger.Debug("MTU probe timed out",
				"floor", c.mtu.floor, "ceiling", c.mtu.ceiling, "current", c.mtu.current)
			c.rtoDeadline = now.Add(c.state.SentPackets.Timeout())
			c.packetsRetransmitted++
			c.bytesRetransmitted += uint64(len(originPacket.Body))
			c.retransmit(originPacket, now)
			return
		}

		// Not yet the connection's timeout: this packet's timer fired before
		// the deadline, because it was armed before the deadline last moved
		// or because the wheel ran ahead of the clock. Keep the timer alive
		// against the deadline and send nothing.
		if now.Before(c.rtoDeadline) {
			c.armRetransmit(originPacket, c.rtoDeadline.Sub(now))
			return
		}

		// Give up once enough consecutive RTOs have passed with nothing
		// acknowledged. The check precedes the increment, so the connection
		// ends on the RTO after the fourth retransmission.
		if c.retransmitCount >= maxConsecutiveTimeouts {
			// Record what was known of the peer: having heard nothing at all, or
			// only that its window was closed, distinguishes a dead path from a
			// peer that stopped reading.
			lastAck, _ := c.state.SentPackets.LastAckNum()
			c.logger.Warn("giving up on connection",
				"consecutiveTimeouts", c.retransmitCount,
				"cid.send", c.cid.Send, "cid.recv", c.cid.Recv,
				"seq", originPacket.Header.SeqNum, "lastAck", lastAck,
				"inFlight", c.state.SentPackets.BytesInFlight(),
				"cwnd", c.state.SentPackets.CongestionWindow(),
				"peerWindow", c.peerRecvWindow,
				"rto", c.state.SentPackets.Timeout(),
				"sinceActivity", now.Sub(c.lastActivity).Round(time.Millisecond),
				"packetsReceived", c.peerActivity.Load())
			c.state.stateType = connClosed
			c.state.Err = ErrTimedOut
			return
		}
		// Forget any outstanding MTU probe, whether or not it timed out: it is
		// gone either way, and a search that kept waiting for it would never
		// send another.
		c.mtu.clearProbe()

		c.retransmitCount++
		c.state.SentPackets.OnTimeout()
		c.timeouts++
		currentTime := c.now()
		c.latestTimeout = &currentTime
		// The deadline moves by the doubled timeout.
		c.rtoDeadline = currentTime.Add(c.state.SentPackets.Timeout())

		// Every packet in flight is considered lost, and the oldest is resent.
		c.state.SentPackets.MarkAllForResend()
		c.fastTimeout, c.probeRecovery = true, false
		oldest, ok := c.state.SentPackets.OldestOutstanding()
		if !ok {
			return
		}
		c.resendSentPacket(oldest, now)
		if oldest.seqNum != originPacket.Header.SeqNum {
			// This packet's callback is spent. Keep a timer on it against the new
			// deadline, so every outstanding packet stays armed.
			c.armRetransmit(originPacket, c.rtoDeadline.Sub(now))
		}
	default:
	}
}

// retransmit resends a packet with its acknowledgement fields brought up to
// date. The sequence number and payload are the original's; everything the
// peer reads about our current state is not.
func (c *connection) retransmit(originPacket *packet, now time.Time) {
	retransmissionPacket := &packet{
		Header: &packetHeaderV1{
			PacketType: originPacket.Header.PacketType,
			Version:    originPacket.Header.Version,
			// No selective ack: see statePacket.
			Extension:     0,
			ConnectionID:  originPacket.Header.ConnectionID,
			SeqNum:        originPacket.Header.SeqNum,
			WndSize:       c.recvWindow(),
			Timestamp:     int64(c.nowMicros()),
			TimestampDiff: uint32(c.peerTimestampDiff.Microseconds()),
			AckNum:        c.state.RecvBuf.AckNum(),
		},
		Body: originPacket.Body,
	}
	c.transmit(retransmissionPacket, now, false)
}

// lossUndoer is a controller that can undo a cut for a loss that proved
// spurious. See defaultController.undoSpuriousLoss.
type lossUndoer interface {
	lossCause() (uint16, bool)
	undoSpuriousLoss(seq uint16) bool
}

// noteResend records the wire timestamp of the first resend of the packet
// whose loss made the controller's last cut, so that the acknowledgement can
// tell which copy arrived. See checkSpuriousLoss.
func (c *connection) noteResend(pkt *packet) {
	u, ok := c.state.SentPackets.congestionCtrl.(lossUndoer)
	if !ok || pkt.Header.PacketType != stData {
		return
	}
	seq, pending := u.lossCause()
	if !pending || seq != pkt.Header.SeqNum || (c.undoResent && c.undoSeq == seq) {
		return
	}
	c.undoSeq, c.undoStamp, c.undoResent = seq, uint32(pkt.Header.Timestamp), true
}

// checkSpuriousLoss undoes the controller's last cut if the packet it was
// for turns out to have arrived after all, using RFC 3522's timestamp
// detection. An acknowledgement echoes the timestamp of the packet that drew
// it (ackEcho), so the first one to cover the packet shows which copy filled
// the hole: an echo older than the resend means the original arrived and the
// packet was late, not lost. An acknowledgement covering it before it was
// resent shows the same. Anything else leaves the cut standing, so an error
// can only keep a cut, never remove a justified one.
//
// Every resend goes through transmit, which records it (noteResend), so "not
// resent" is known rather than assumed.
func (c *connection) checkSpuriousLoss(fullAcked *circularRangeInclusive, selected []uint16) {
	u, ok := c.state.SentPackets.congestionCtrl.(lossUndoer)
	if !ok {
		return
	}
	seq, pending := u.lossCause()
	if !pending {
		c.undoResent = false
		return
	}
	if !(fullAcked != nil && fullAcked.Contains(seq)) && !slices.Contains(selected, seq) {
		return
	}
	resent := c.undoResent && c.undoSeq == seq
	if !resent || (c.ackEchoKnown && int32(c.ackEcho-c.undoStamp) < 0) {
		if u.undoSpuriousLoss(seq) {
			c.state.SentPackets.rackOnSpuriousLoss()
		}
	}
	c.undoResent = false
}

// resendSentPacket resends a packet declared lost by a retransmission
// timeout, from what was recorded when it was first sent, with its
// acknowledgement fields brought up to date. It is used by the timeout,
// processWrites and onFastTimeout, and returns the packet as sent.
func (c *connection) resendSentPacket(pkt *sentPacket, now time.Time) *packet {
	builder := newPacketBuilder(pkt.packetType, c.cid.Send, c.nowMicros(),
		c.recvWindow(), pkt.seqNum)
	if pkt.data != nil {
		builder.WithPayload(pkt.data)
	}
	// No selective ack: see statePacket.
	resend := builder.
		WithTimestampDiffMicros(uint32(c.peerTimestampDiff.Microseconds())).
		WithAckNum(c.state.RecvBuf.AckNum()).
		Build()
	c.packetsRetransmitted++
	c.bytesRetransmitted += uint64(len(pkt.data))
	// Not a first transmission, so never an MTU probe.
	c.transmit(resend, now, false)
	return resend
}

// lossProbeEnabled enables the loss probe. Tests switch it off.
var lossProbeEnabled = func() *atomic.Bool {
	b := new(atomic.Bool)
	b.Store(true)
	return b
}()

// lossProbeActive reports whether this connection uses the loss probe:
// classic LEDBAT only. Under LEDBAT++ the retransmission timeout, after
// which the window collapses to two packets, is part of how the flow yields,
// and the probe would let it take more than its share from loss-based flows.
func (c *connection) lossProbeActive() bool {
	return lossProbeEnabled.Load() && c.config.CongestionAlgorithm != AlgorithmLEDBATPP
}

// minLossProbeTimeout is RFC 8985's floor on the probe timeout (§7.2).
const minLossProbeTimeout = 10 * time.Millisecond

// lossProbeTimeout is RFC 8985's PTO, max(2 x SRTT, 10 ms), or zero before
// there is a round-trip estimate.
func (c *connection) lossProbeTimeout() time.Duration {
	// Use the microsecond estimate: the millisecond one is zero on a path
	// faster than a millisecond.
	rtt := c.state.SentPackets.controllerStats().FineRTT
	if rtt <= 0 {
		return 0
	}
	return max(2*rtt, minLossProbeTimeout)
}

// rearmLossProbe restarts the loss probe's timeout. It runs on every
// acknowledgement and when the first packet enters an empty window, so the
// probe fires only after a probe timeout with nothing acknowledged. An
// acknowledgement that moves the cumulative ack forward starts a new
// episode, with a new probe to spend.
func (c *connection) rearmLossProbe(ackNum uint16) {
	if !c.lossProbeActive() || c.armLossProbe == nil || c.state.stateType != connConnected ||
		c.state.SentPackets == nil {
		return
	}
	if ackNum != c.lossProbeAckNum {
		c.lossProbeAckNum = ackNum
		c.lossProbeSent = false
	}
	if !c.state.SentPackets.HasUnackedPackets() {
		// Nothing left to probe for. A probe left armed would fire to find
		// nothing outstanding, and until it did the connection could not
		// rest (mayRest).
		c.armLossProbe(0)
		return
	}
	if c.lossProbeSent || c.fastTimeout {
		return
	}
	if pto := c.lossProbeTimeout(); pto > 0 {
		c.armLossProbe(pto)
	}
}

// onLossProbe resends the oldest outstanding packet when nothing has been
// acknowledged for a probe timeout, and backs off for the next probe.
//
// Without it, a packet whose fast retransmission is also lost, common just
// after a queue overflows, and the last packet of a transfer, which has
// nothing after it to raise duplicate acknowledgements, both wait for the
// retransmission timeout, whose floor is a second. This is RFC 8985's tail
// loss probe, resending the oldest packet rather than the newest because the
// oldest is the one known to be missing.
//
// One probe per episode, as in RFC 8985: after it, only an acknowledgement
// that advances the cumulative ack allows another, and none is sent after a
// retransmission timeout has fired or when the timeout is already due. It
// does not touch the congestion window: the resent packet is already counted
// in flight, and the loss has already been charged.
func (c *connection) onLossProbe(now time.Time) {
	if !c.lossProbeActive() || c.lossProbeSent || c.fastTimeout ||
		c.state.stateType != connConnected || c.state.SentPackets == nil ||
		c.rtoDeadline.IsZero() || !now.Before(c.rtoDeadline) {
		return
	}
	oldest, ok := c.state.SentPackets.OldestOutstanding()
	if !ok {
		return
	}
	c.lossProbeSent = true
	c.lossProbes++
	c.lossProbeSeq, c.lossProbeAt = oldest.seqNum, now
	resent := c.resendSentPacket(oldest, now)
	c.lossProbeStamp = uint32(resent.Header.Timestamp)
	// transmit armed the packet's timer a full timeout from now; the
	// timeout is still due where it was, and the probe must not move it.
	c.armRetransmit(resent, c.rtoDeadline.Sub(now))
}

// onFastTimeout is the fast-timeout retry, run on every acknowledgement.
// After a retransmission timeout resends the oldest packet, its
// acknowledgement advances fastResendSeqNum to the next one, which is now
// the oldest outstanding and is resent at once, and so on, one per
// acknowledgement, until a packet turns out to have arrived after all.
//
// It runs after the cumulative and selective acknowledgements and before any
// fast retransmission.
func (c *connection) onFastTimeout(now time.Time) {
	if !c.fastTimeout || c.state.stateType != connConnected || c.state.SentPackets == nil {
		return
	}
	if c.probeRecovery {
		c.resendProbedLosses(now)
		return
	}
	oldest, ok := c.state.SentPackets.OldestOutstanding()
	if !ok || oldest.seqNum != c.state.SentPackets.fastResendSeqNum {
		c.fastTimeout = false
		return
	}
	c.state.SentPackets.fastResendSeqNum++
	c.resendSentPacket(oldest, now)
}

// probeAnswered reports whether this acknowledgement answered the loss probe
// by showing packets lost, and if so starts resending them.
//
// The probe resends the oldest outstanding packet. When its acknowledgement
// arrives, every packet sent before the probe and still unacknowledged has
// had a probe timeout and a round trip to be acknowledged and has not been,
// so it is lost: RFC 8985 pairs the probe with that rule. Without it the
// probe would repair only one hole per probe timeout.
//
// The packets are resent one per acknowledgement, as onFastTimeout does,
// stopping at the first packet sent after the probe. The loss is charged
// once, as a detected loss rather than a timeout: the window decays rather
// than collapses.
func (c *connection) probeAnswered(fullAcked *circularRangeInclusive, now time.Time) bool {
	if !c.lossProbeSent || c.fastTimeout || fullAcked == nil || !fullAcked.Contains(c.lossProbeSeq) {
		return false
	}
	sp := c.state.SentPackets
	// The acknowledgement must be the probe's, not the original's: if the
	// original was not lost, the packets behind it are in flight, and
	// treating its acknowledgement as the answer would resend them all.
	//
	// An acknowledgement arriving within the minimum round trip of the probe
	// is ambiguous and is not taken as evidence (RFC 8985 section 6.2).
	// Beyond that, the echoed timestamp identifies which copy drew it: the
	// original's is older than the probe's.
	if c.ackEchoKnown {
		if int32(c.ackEcho-c.lossProbeStamp) < 0 {
			return false
		}
	} else if minRTT := sp.controllerStats().MinFineRTT; minRTT == 0 || now.Sub(c.lossProbeAt) < minRTT {
		return false
	}
	oldest, ok := sp.OldestOutstanding()
	if !ok || !oldest.retransmission.Before(c.lossProbeAt) {
		return false
	}
	_ = sp.OnLost(oldest.seqNum, true, now)
	c.fastTimeout, c.probeRecovery = true, true
	return true
}

// resendProbedLosses resends the oldest outstanding packet if it was last
// sent before the probe, as onFastTimeout does for timeout losses.
func (c *connection) resendProbedLosses(now time.Time) {
	sp := c.state.SentPackets
	oldest, ok := sp.OldestOutstanding()
	if !ok || !oldest.retransmission.Before(c.lossProbeAt) {
		c.fastTimeout, c.probeRecovery = false, false
		return
	}
	if wrappingLessThan(sp.fastResendSeqNum, oldest.seqNum+1) {
		sp.fastResendSeqNum = oldest.seqNum + 1
	}
	c.resendSentPacket(oldest, now)
}

// duplicateAcksBeforeResend is how many duplicate acknowledgements declare a
// packet lost.
const duplicateAcksBeforeResend = 3

// noteDuplicateAck counts a peer repeating the same acknowledgement, and uses
// the third to conclude whether an outstanding MTU probe was too large. This
// is the only route to lowering the MTU ceiling during a bulk transfer; the
// timeout route needs the probe to be the only packet outstanding.
//
// The rules:
//
//   - Only bare ST_STATE packets count. A data packet carrying an
//     acknowledgement was most likely sent because the peer had data, so on a
//     bidirectional connection it would look like a duplicate after every
//     packet.
//   - The acknowledgement must repeat LastAckedSeqNum, the number just before
//     the oldest outstanding packet. Anything else resets the count.
//   - Nothing is counted, or reset, while nothing is outstanding.
//
// On the third such acknowledgement with a probe outstanding: if the repeated
// number is the one just before the probe, the probe is the hole and the
// ceiling drops below it. Otherwise another packet was lost ahead of the
// probe, which says nothing about size; the probe is forgotten so another can
// be sent.
//
// It acts once per run of duplicates, on the first that finds the count at
// three or more. The selective ack also sets the count
// (noteSelectiveAckCount), and can raise it past three in one step.
func (c *connection) noteDuplicateAck(packetType packetType, ackNum uint16) {
	if c.state.stateType != connConnected || c.state.SentPackets == nil {
		return
	}
	if c.state.SentPackets.UnackedCount() == 0 {
		return
	}
	if packetType != stState || ackNum != c.state.SentPackets.LastAckedSeqNum() {
		c.duplicateAcks = 0
		c.duplicatesJudged = false
		return
	}

	c.duplicateAcks++
	c.judgeProbeFromDuplicates(ackNum)
}

// judgeProbeFromDuplicates draws the conclusion about an outstanding MTU
// probe once a run of duplicates has reached three. See noteDuplicateAck.
func (c *connection) judgeProbeFromDuplicates(ackNum uint16) {
	if c.duplicateAcks < duplicateAcksBeforeResend || c.duplicatesJudged || !c.mtu.probing {
		return
	}
	c.duplicatesJudged = true

	if ackNum == c.mtu.probeSeq-1 {
		c.mtu.onProbeLost(c.now())
		c.mtuProbesLostToDuplicateAcks++
		if c.logger.Enabled(baseContext, slog.LevelDebug) {
			c.logger.Debug("MTU probe presumed too big, from duplicate acks",
				"floor", c.mtu.floor, "ceiling", c.mtu.ceiling, "current", c.mtu.current,
				"probeSeq", c.mtu.probeSeq)
		}
		return
	}
	// A packet ahead of the probe was lost. Nothing follows about size.
	c.mtu.clearProbe()
}

// noteSelectiveAckCount sets the duplicate count from a selective ack: the
// number of bits it sets for packets in the send window other than the
// oldest, whether or not already acknowledged, and nothing once the
// acknowledgement has emptied the window. Later duplicates count on from
// there, so on a path where each selective ack names more packets than the
// last, the third duplicate comes sooner than three repeats would.
//
// It runs for every packet type that carries the extension, after the
// acknowledgement has been applied. A count reaching three judges an
// outstanding probe immediately; see noteDuplicateAck.
func (c *connection) noteSelectiveAckCount(ackNum uint16, sack *selectiveAck) {
	sp := c.state.SentPackets
	if c.state.stateType != connConnected || sp == nil {
		return
	}
	window := sp.UnackedCount() // packets in flight
	if window == 0 {
		return
	}
	next := sp.NextSeqNum() // next sequence number
	count := uint32(0)
	for i, set := range sack.Acked() {
		v := ackNum + 2 + uint16(i)
		if set && next-v-1 < window-1 {
			count++
		}
	}
	c.duplicateAcks = count
	if ackNum == sp.LastAckedSeqNum() {
		c.judgeProbeFromDuplicates(ackNum)
	}
}

func (c *connection) retransmitLostPackets(now time.Time) {
	if c.state.stateType != connConnected {
		return
	}
	if !c.state.SentPackets.HasLostPackets() {
		return
	}
	connID := c.cid.Send
	nowMicros := int64(c.nowMicros())
	recvWindow := c.recvWindow()
	tsDiffMicros := uint32(c.peerTimestampDiff.Microseconds())

	for _, lostPacket := range c.state.SentPackets.TakeLostPackets() {
		seqNum := lostPacket.SeqNum
		packetType := lostPacket.PacketType
		payload := lostPacket.Data

		builder := newPacketBuilder(packetType, connID, uint32(nowMicros), recvWindow, seqNum)
		if payload != nil {
			builder.WithPayload(payload)
		}

		// No selective ack: see statePacket.
		packetInst := builder.
			WithTimestampDiffMicros(tsDiffMicros).
			WithAckNum(c.state.RecvBuf.AckNum()).
			Build()
		c.packetsRetransmitted++
		c.bytesRetransmitted += uint64(len(payload))
		c.fastRetransmits++
		if c.logger.Enabled(baseContext, slog.LevelDebug) {
			c.logger.Debug("will retransmit lost packet",
				"packet.type", packetInst.Header.PacketType,
				"packet.seqNum", packetInst.Header.SeqNum,
				"packet.ackNum", packetInst.Header.AckNum,
				"packet.cid", packetInst.Header.ConnectionID,
				"packet.data.len", len(payload))
		}
		// Not a first transmission: a fast retransmission is never an MTU
		// probe. A packet already lost once is a poor test of size, and its
		// loss would lower the ceiling on evidence of congestion.
		c.transmit(packetInst, now, false)
	}
}

// rearmRack arms RACK's reordering timer for the next packet still inside
// its reordering window, or stops it when there is none.
func (c *connection) rearmRack() {
	sp := c.state.SentPackets
	if sp == nil || !sp.rack.active || c.armRack == nil {
		return
	}
	c.armRack(sp.rack.timeout)
}

// onRackTimer is RFC 8985's reordering timer (section 6.3): a packet that
// was inside its reordering window at the last acknowledgement has now left
// it, with nothing since to show it arrived.
func (c *connection) onRackTimer(now time.Time) {
	sp := c.state.SentPackets
	if c.state.stateType != connConnected || sp == nil || !sp.rack.active {
		return
	}
	sp.rackDetectAndMark(now)
	c.retransmitLostPackets(now)
	c.rearmRack()
}

// sentRack is the connection's RACK state, or a zero one before there is a
// send window.
func sentRack(c *connection) rackState {
	if c.state == nil || c.state.SentPackets == nil {
		return rackState{}
	}
	return c.state.SentPackets.rack
}
