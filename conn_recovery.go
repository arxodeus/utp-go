package utp

import (
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"
)

// unackTimeoutQueueLen is the capacity of a connection's queue of expired
// retransmission timers.
//
// It was 1000, 8 KB allocated whole on every connection, more than a third
// of what an idle one held. When the retransmission timeout passes, every
// outstanding packet's timer expires on the same tick, but only the first
// delivered acts: it moves the deadline, and each one after it finds the
// deadline ahead and re-arms (onTimeout). The wheel never blocks on a full
// queue -- it re-arms the timer for its next tick (newRetransmitTimers) -- so
// what does not fit arrives one tick later to the same no-op.
const unackTimeoutQueueLen = 16

// armRetransmit schedules a retransmission timer for one packet.
func (c *connection) armRetransmit(pkt *packet, delay time.Duration) {
	seq := pkt.Header.SeqNum
	if len(c.armed) == 0 {
		// First packet into an empty window: start the connection's RTO.
		//
		// libutp: "Setup initial timeout timer" in write_outgoing_packet,
		// guarded by `cur_window_packets == 0`
		// (utp_internal.cpp:994-998).
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

// disarmRetransmit cancels one packet's retransmission timer.
//
// Unconditional on purpose. A timer that has already fired is out of the
// wheel but still in the armed set until the event loop drains it, so
// skipping the wheel when the set does not hold the key could leave a stale
// entry behind. Both operations are no-ops when the key is absent.
func (c *connection) disarmRetransmit(seq uint16) {
	delete(c.armed, seq)
	c.timers.disarm(retransmitKey{scope: c.timerScope, seq: seq})
}

// disarmAcked cancels the timers for every sequence number this connection
// still has armed that falls inside acked.
//
// Only this connection's own armed set is scanned. The wheel is shared with
// every other connection on the socket, so scanning it would be O(all
// outstanding packets on the socket) on every ack.
//
// It returns how many timers it cancelled, which is how many packets this ack
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
// outlives the connection, so anything left armed would linger until it
// expired and then be delivered to a dead event loop.
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

			// Double the previous timeout, which is what libutp does on every
			// retransmission timeout: `retransmit_timeout * 2`
			// (utp_internal.cpp:1179, applied at :1203). This previously
			// computed InitialTimeout * 1.5^attempts, which grows from the
			// initial value rather than the current one and uses a different
			// factor.
			//
			// The cap is defensive rather than libutp's behaviour: libutp has
			// none, because its own give-up limit of two timeouts in
			// CS_SYN_SENT bounds the backoff. It is unreachable at the
			// default MaxConnAttempts and only matters if a caller raises it.
			c.synTimeout = capTimeout(c.synTimeout*2, c.config.MaxTimeout)

			c.armRetransmit(originPacket, c.synTimeout)

			// Re-send SYN packet
			c.emit(c.synPacket(seq))
		}

	case connConnected:
		// If the timed out packet is a SYN, do nothing
		if originPacket.Header.PacketType == st_syn {
			return
		}

		// A timeout for a packet the peer has already acknowledged is not a
		// timeout at all.
		//
		// Timers are armed per packet and cancelled when the ack arrives, but
		// cancelling cannot stop one that has already fired: it is out of the
		// wheel and on its way to this loop, and disarmAcked will not find it.
		// Acting on it -- re-arming it, or counting it towards a timeout -- would
		// halve the window and resend packets the peer already has, on a quiet
		// connection with no loss at all.
		//
		// libutp cannot reach this state. It has one timeout deadline rather
		// than a timer per packet, and it retransmits out of its outgoing
		// buffer (utp_internal.cpp:1230-1244), which no longer holds a packet
		// the peer has acknowledged. History: HISTORY.md, "onTimeout: a timeout
		// for an acknowledged packet".
		if c.state.SentPackets != nil &&
			!c.state.SentPackets.Outstanding(originPacket.Header.SeqNum) {
			return
		}

		// One timeout per RTO expiry, measured against the clock, and one
		// packet resent for it.
		//
		// libutp keeps a single deadline per connection and acts only when
		// `current_ms - rto_timeout >= 0` (utp_internal.cpp:1147-1148). Then
		// it marks every packet in flight need_resend, which takes their bytes
		// out of cur_window, and resends only the oldest (:1230-1252). The
		// rest go out oldest first from flush_packets, as the congestion
		// window -- which the timeout has just cut to one packet (:1225) --
		// allows, or one per acknowledgement through the fast-timeout path
		// (:2256-2282). This connection arms a timer per packet, so an expiry
		// arrives as several callbacks; the others resend nothing, and
		// libutp's two routes replace them: processWrites resends marked
		// packets ahead of new data, and fastTimeout resends the oldest on each
		// acknowledgement. netem.TestRTOBurstMeasure; history: HISTORY.md,
		// "onTimeout: one resend per timeout".
		now := c.now()

		// A retransmission timeout on the MTU probe, with nothing else
		// outstanding, says the path will not carry that size -- not that it
		// is congested. libutp lowers the ceiling and sets `ignore_loss`, so
		// the window is left alone and the binary search moves on
		// (utp_internal.cpp:1152-1167).
		probeTimedOut := c.mtu.probeOutstanding(originPacket.Header.SeqNum) &&
			c.state.SentPackets.UnackedCount() == 1
		if probeTimedOut && !now.Before(c.rtoDeadline) {
			// Everything else in libutp's timeout still happens: the give-up
			// check comes first (:1191), and the timeout counts towards it
			// and starts the fast-timeout retry (:1240, :1247). Only the
			// doubling and the window collapse are skipped (:1179, :1206).
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

		// Not yet the connection's timeout: this packet's callback came
		// before the deadline, because it was armed before the deadline last
		// moved, or because the wheel ran ahead of the clock (it counts
		// ticks, and a descheduled wheel catches up in a burst; measured, a
		// 200ms timer delivered at 198.6ms). libutp would do nothing at all
		// here. Keep this packet's timer alive against the deadline and send
		// nothing.
		if now.Before(c.rtoDeadline) {
			c.armRetransmit(originPacket, c.rtoDeadline.Sub(now))
			return
		}

		// Give up once enough consecutive RTOs have passed with the peer
		// acking nothing, as libutp does (utp_internal.cpp:1191). The check
		// precedes the increment there, so the connection dies on the RTO
		// after the fourth retransmission.
		if c.retransmitCount >= maxConsecutiveTimeouts {
			c.logger.Warn("giving up on connection",
				"consecutiveTimeouts", c.retransmitCount,
				"cid.send", c.cid.Send, "cid.recv", c.cid.Recv)
			c.state.stateType = connClosed
			c.state.Err = ErrTimedOut
			return
		}
		// Forget any outstanding MTU probe, whether or not it was the one
		// that timed out. libutp clears it on every RTO, outside the branch
		// that lowers the ceiling (utp_internal.cpp:1166-1167): the probe is
		// gone either way, and a search that keeps waiting for it never sends
		// another.
		c.mtu.clearProbe()

		c.retransmitCount++
		c.state.SentPackets.OnTimeout()
		c.timeouts++
		currentTime := c.now()
		c.latestTimeout = &currentTime
		// libutp: `rto_timeout = ctx->current_ms + new_timeout`
		// (utp_internal.cpp:1204), with new_timeout already doubled.
		c.rtoDeadline = currentTime.Add(c.state.SentPackets.Timeout())

		// "every packet should be considered lost" (:1230-1237), and the
		// oldest resent (:1249-1251).
		c.state.SentPackets.MarkAllForResend()
		c.fastTimeout, c.probeRecovery = true, false
		oldest, ok := c.state.SentPackets.OldestOutstanding()
		if !ok {
			return
		}
		c.resendSentPacket(oldest, now)
		if oldest.seqNum != originPacket.Header.SeqNum {
			// This packet's callback is spent. Keep a timer on it, against
			// the new deadline, so every outstanding packet stays armed.
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
			ConnectionId:  originPacket.Header.ConnectionId,
			SeqNum:        originPacket.Header.SeqNum,
			WndSize:       c.recvWindow(),
			Timestamp:     int64(c.nowMicros()),
			TimestampDiff: uint32(c.peerTsDiff.Microseconds()),
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
	if !ok || pkt.Header.PacketType != st_data {
		return
	}
	seq, pending := u.lossCause()
	if !pending || seq != pkt.Header.SeqNum || (c.undoResent && c.undoSeq == seq) {
		return
	}
	c.undoSeq, c.undoStamp, c.undoResent = seq, uint32(pkt.Header.Timestamp), true
}

// checkSpuriousLoss undoes the controller's last cut if the packet it was
// for turns out to have arrived after all: RFC 3522's detection, by
// timestamp. An acknowledgement echoes the timestamp of the packet that drew
// it (ackEcho), so the first one to cover the packet says which copy filled
// the hole. If it echoes a time before the resend was sent, the original did;
// the packet was late, not lost. One that covers it before it was resent at
// all says the same without a timestamp. Anything else -- the resend's echo,
// a peer that reports no delay, an acknowledgement drawn by a later packet --
// leaves the cut standing, so a mistake can only leave a cut that libutp
// would also have made.
//
// Every resend goes through transmit, which notes it (noteResend), so "not
// resent" is not a guess. DEVIATIONS.md; history: HISTORY.md,
// "checkSpuriousLoss".
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

// resendSentPacket sends a packet again from what was recorded when it was
// first sent, with its acknowledgement fields brought up to date. It is the
// resend for a packet a retransmission timeout gave up as lost, whether the
// timeout itself, processWrites or onFastTimeout is sending it. It returns the
// packet as sent.
func (c *connection) resendSentPacket(pkt *sentPacket, now time.Time) *packet {
	builder := newPacketBuilder(pkt.packetType, c.cid.Send, c.nowMicros(),
		c.recvWindow(), pkt.seqNum)
	if pkt.data != nil {
		builder.WithPayload(pkt.data)
	}
	// No selective ack: see statePacket.
	resend := builder.
		WithTsDiffMicros(uint32(c.peerTsDiff.Microseconds())).
		WithAckNum(c.state.RecvBuf.AckNum()).
		Build()
	c.packetsRetransmitted++
	c.bytesRetransmitted += uint64(len(pkt.data))
	// Not a first transmission, so never an MTU probe (utp_internal.cpp:911).
	c.transmit(resend, now, false)
	return resend
}

// lossProbeEnabled switches the loss probe. It is not libutp's: see
// DEVIATIONS.md, "A loss probe resends before the retransmission timeout".
var lossProbeEnabled = func() *atomic.Bool {
	b := new(atomic.Bool)
	b.Store(true)
	return b
}()

// lossProbeActive reports whether this connection uses the loss probe:
// classic LEDBAT only. Under LEDBAT++ the retransmission timeout it avoids is
// part of how the flow yields -- after one, the window collapses to two
// packets -- and with the probe LEDBAT++ took more than its share from a
// loss-based flow in about one run in five (DEVIATIONS.md). Classic LEDBAT's
// share was unchanged.
func (c *connection) lossProbeActive() bool {
	return lossProbeEnabled.Load() && c.config.CongestionAlgorithm != AlgorithmLEDBATPP
}

// minLossProbeTimeout is RFC 8985's floor on the probe timeout (§7.2).
const minLossProbeTimeout = 10 * time.Millisecond

// lossProbeTimeout is RFC 8985's PTO, max(2 x SRTT, 10 ms), or zero before
// there is a round-trip estimate.
func (c *connection) lossProbeTimeout() time.Duration {
	// The microsecond estimate: libutp's own is in whole milliseconds, and
	// on a path faster than one it is zero.
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
	if c.lossProbeSent || c.fastTimeout || !c.state.SentPackets.HasUnackedPackets() {
		return
	}
	if pto := c.lossProbeTimeout(); pto > 0 {
		c.armLossProbe(pto)
	}
}

// onLossProbe resends the oldest outstanding packet when nothing has been
// acknowledged for a probe timeout, and backs off for the next probe.
//
// libutp has nothing between fast retransmission and its retransmission
// timeout, whose floor is a second. A packet whose fast retransmission is
// lost as well -- common on a queue that has just overflowed, which is when
// fast retransmission happens -- and the last packet of a transfer, which
// has nothing after it to raise duplicate acknowledgements, both wait for
// it. On a 1 ms LAN path that made a 0.4 s transfer take 1.4 s, in every
// pairing of this library and libutp (KNOWN-LIMITATIONS.md). This is TCP's
// answer, RFC 8985's tail loss probe, resending the oldest packet rather
// than the newest because the oldest is the one known to be missing.
//
// One probe per episode, as in RFC 8985: after it, only an acknowledgement
// that moves the cumulative ack forward allows another, and none is sent
// once a retransmission timeout has fired (libutp's fast-timeout retry takes
// over from there) or when the timeout is already due. It does not touch the
// congestion window: the packet it resends is already counted in flight, and
// the loss that led here has already been charged.
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
	// transmit armed the packet's timer a whole timeout from now; the
	// timeout itself is due where it was, and the probe must not move it.
	// The wheel places a timer by its deadline, so this keeps its tick.
	c.armRetransmit(resent, c.rtoDeadline.Sub(now))
}

// onFastTimeout is libutp's fast-timeout retry, run on every acknowledgement
// (utp_internal.cpp:2256-2282):
//
//	if (((conn->seq_nr - conn->cur_window_packets) & ACK_NR_MASK) != conn->fast_resend_seq_nr) {
//	    conn->fast_timeout = false;
//	} else {
//	    OutgoingPacket *pkt = (OutgoingPacket*)conn->outbuf.get(conn->seq_nr - conn->cur_window_packets);
//	    if (pkt && pkt->transmissions > 0) {
//	        conn->fast_resend_seq_nr++;
//	        conn->send_packet(pkt);
//	    }
//	}
//
// After a timeout resends the oldest packet, the acknowledgement for it moves
// fast_resend_seq_nr up to the next one (:2186-2188), which is then the oldest
// outstanding, so it is resent at once -- and so on, one per acknowledgement,
// until a packet turns out to have arrived after all. That ends it: the
// oldest outstanding is then past fast_resend_seq_nr.
//
// libutp runs it after the cumulative acknowledgement and before the
// selective one; here it runs after both, before any fast retransmission,
// which sees fast_resend_seq_nr already moved on exactly as libutp's does.
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
// The probe resends the oldest outstanding packet. When the acknowledgement
// that retires it arrives, a round trip later, every packet sent before the
// probe and still unacknowledged has had a probe timeout and a round trip to
// be acknowledged and has not been: it is lost. RFC 8985 pairs the probe with
// that rule (RACK: a packet sent before one that has been delivered is lost
// once the reordering allowance has passed); without it the probe repaired one
// hole per probe timeout. In the 1000-transfer stress test a burst of 13
// consecutive packets lost at the tail of a window, with one packet after them
// to raise a selective ack -- fast retransmission needs three -- came back one
// every 4.3 seconds, and each repair restarted the retransmission timeout, so
// libutp's recovery never began either.
//
// The packets are resent the way libutp resends them after its timeout: one
// per acknowledgement, the fast-timeout retry (onFastTimeout), here stopping
// at the first packet sent after the probe, whose fate is not yet known. The
// loss is charged once, as a detected loss rather than a timeout: the window
// decays and does not collapse.
func (c *connection) probeAnswered(fullAcked *circularRangeInclusive, now time.Time) bool {
	if !c.lossProbeSent || c.fastTimeout || fullAcked == nil || !fullAcked.Contains(c.lossProbeSeq) {
		return false
	}
	sp := c.state.SentPackets
	// The acknowledgement must be the probe's, not the original's. If the
	// original was never lost, the packets behind it are in flight, not
	// missing, and taking its acknowledgement for the answer resends them
	// all: 86-101 needless retransmissions on a lossless 100 Mb/s link, each
	// time a probe had fired early.
	//
	// It fired early two ways. The process stalled and woke to the probe and
	// the acknowledgements together: the answer came 11 us to 1.4 ms after
	// the probe, under the 2.2 ms the path had ever taken for a round trip.
	// RFC 8985 makes that call (section 6.2: an ACK arriving within min_RTT
	// of a retransmission is ambiguous and is not taken as evidence). Or a
	// queue filling in slow start took the round trip from 10 ms to 33 ms
	// faster than the smoothed estimate the probe's timer runs on: then the
	// original's acknowledgement came 2.8-10 ms after the probe, past the
	// minimum, and only the timestamps tell them apart. The peer reports, on
	// every packet, how long ours took to reach it; from that and its own
	// timestamp comes the timestamp of the packet that drew the
	// acknowledgement, and the original's is older than the probe's.
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

// resendProbedLosses is onFastTimeout for losses the probe showed: resend the
// oldest outstanding packet if it was last sent before the probe.
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

// duplicateAcksBeforeResend is libutp's DUPLICATE_ACKS_BEFORE_RESEND
// (utp_internal.cpp:64).
const duplicateAcksBeforeResend = 3

// noteDuplicateAck counts a peer repeating the same acknowledgement, and uses
// the third one to conclude that an outstanding MTU probe was too big.
//
// libutp: utp_internal.cpp:1921-1941. This is the second of its two routes to
// lowering the MTU ceiling, and the only one that works during a bulk
// transfer. The other needs the probe to be the only packet outstanding
// (:1152-1167, and onTimeout), which a saturated send window never leaves
// it.
//
// Three rules, all libutp's:
//
//   - Only bare ST_STATE packets count. libutp is emphatic about why
//     (:1911-1920): an ST_DATA carrying an acknowledgement was most likely sent
//     because the peer had data of its own, not in response to anything, so a
//     bidirectional connection would otherwise see three "duplicates"
//     immediately after every payload packet it sent.
//   - The acknowledgement must repeat LastAckedSeqNum, the number just before
//     the oldest outstanding packet. Anything else resets the count.
//   - Nothing is counted while nothing is outstanding, and the count is not
//     reset then either -- libutp wraps the whole block in
//     `if (cur_window_packets > 0)`.
//
// On the third such acknowledgement, with a probe outstanding, there are two
// cases and libutp draws a different conclusion from each. If the repeated
// number is the one just before the probe, the probe is the hole: the ceiling
// drops below it. If it is anything else, some other packet was lost ahead of
// the probe, which says nothing about size -- the probe is forgotten so
// another can be sent, and the ceiling is left alone.
//
// It acts once per run of duplicates -- a run ends when the acknowledgement
// moves -- on the first duplicate that finds the count at three or more,
// where libutp tests equality with three (`duplicate_ack ==
// DUPLICATE_ACKS_BEFORE_RESEND`, :1928). The selective ack sets the count
// (noteSelectiveAckCount), and an acknowledgement covering a read batch can
// name more than three later packets at once, so the count can jump past
// three without ever equalling it. Three or more is the same evidence that
// libutp's selective ack already resends on (`count >=
// DUPLICATE_ACKS_BEFORE_RESEND`, :1538). History: HISTORY.md,
// "noteDuplicateAck".
func (c *connection) noteDuplicateAck(packetType packetType, ackNum uint16) {
	if c.state.stateType != connConnected || c.state.SentPackets == nil {
		return
	}
	if c.state.SentPackets.UnackedCount() == 0 {
		return
	}
	if packetType != st_state || ackNum != c.state.SentPackets.LastAckedSeqNum() {
		c.duplicateAcks = 0
		c.duplicatesJudged = false
		return
	}

	c.duplicateAcks++
	c.judgeProbeFromDuplicates(ackNum)
}

// judgeProbeFromDuplicates draws libutp's conclusion about an outstanding MTU
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

// noteSelectiveAckCount is the last thing libutp's selective_ack does:
//
//	duplicate_ack = count;          (utp_internal.cpp:1612)
//
// where count is the number of bits the selective ack sets for packets inside
// the send window other than its oldest (:1454-1461) -- counted whether or not
// those packets were already acknowledged, and not at all when the
// acknowledgement has emptied the window (:1440). The next duplicate
// acknowledgement then counts on from there, so on a path where each
// acknowledgement's selective ack names more packets than the last, the third
// "duplicate" that judges an MTU probe too big (noteDuplicateAck) comes
// sooner than three repeats would -- or, once the count has passed three, not
// at all until an acknowledgement that moves resets it.
//
// It runs for every packet type that carries the extension, as libutp's
// does, and after the acknowledgement has been applied, which is where
// libutp's window stands when it counts (:2289, after :2194).
//
// A count that reaches three judges an outstanding probe there and then,
// which libutp does not do: see noteDuplicateAck.
func (c *connection) noteSelectiveAckCount(ackNum uint16, sack *selectiveAck) {
	sp := c.state.SentPackets
	if c.state.stateType != connConnected || sp == nil {
		return
	}
	window := sp.UnackedCount() // libutp's cur_window_packets
	if window == 0 {
		return
	}
	next := sp.NextSeqNum() // libutp's seq_nr
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
	tsDiffMicros := uint32(c.peerTsDiff.Microseconds())

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
			WithTsDiffMicros(tsDiffMicros).
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
				"packet.cid", packetInst.Header.ConnectionId,
				"packet.data.len", len(payload))
		}
		// Not a first transmission: a fast retransmission must never be taken
		// as an MTU probe. libutp requires `pkt->transmissions == 0`
		// (utp_internal.cpp:911) and says why in the comment above it -- a
		// packet larger than the ceiling "was probably used as a probe already
		// and failed, now we need it to fragment just to get it through".
		//
		// Passing true here made loss shrink the path MTU: a fast
		// retransmission could be adopted as the probe, and a packet that had
		// already been lost once is a poor bet to arrive, so onProbeLost would
		// lower the ceiling on evidence about congestion rather than about
		// size. The RTO retransmission path already passes false.
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
// was inside its reordering window when the last acknowledgement came has
// passed it, with nothing since to say it arrived.
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
