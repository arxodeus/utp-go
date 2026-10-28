package utp

import "time"

// RACK loss detection: the time-based rule of RFC 8985, enabled by
// ConnectionConfig.RACK and off by default. When off, nothing here runs and
// a packet is lost once three packets sent after it have been acknowledged;
// each is fast-retransmitted once (DetectLostPackets, fastResendSeqNum).
//
// When on, a packet is lost once a packet sent after it has been delivered
// and a round trip plus a reordering window has passed since it was sent
// (RFC 8985 section 6.2). Compared with the count:
//
//   - reordering within the window is not mistaken for loss;
//   - a retransmission can itself be declared lost a round trip after it
//     was sent, rather than waiting for the retransmission timeout;
//   - the decision is time-based, so a timer makes it when no
//     acknowledgement arrives.
//
// uTP has no DSACK, which RFC 8985 uses to widen the reordering window after
// a spurious retransmission. Spurious-loss detection (checkSpuriousLoss)
// widens the window instead.

// rackReoWndPersist is how many recoveries the reordering window stays
// widened after a spurious retransmission (RACK.reo_wnd_persist).
const rackReoWndPersist = 16

// rackState is the per-connection RACK state, kept by sentPackets.
type rackState struct {
	active bool

	// xmitTs and endSeq are the send time and sequence number of the most
	// recently sent packet that has been delivered (RACK.xmit_ts,
	// RACK.end_seq); rtt is the round trip measured to it (RACK.rtt).
	delivered bool
	xmitTime  time.Time
	endSeq    uint16
	rtt       time.Duration
	// xmitOrder is the same packet's position in send order
	// (sentPacket.sentOrder), which decides what was sent before it. Send
	// times are not used for this, since they are read at different points
	// of an event-loop pass and need not be monotonic in send order.
	xmitOrder uint64

	// minRTT is the lowest round trip from a packet sent once.
	minRTT time.Duration

	// fack is the highest sequence number delivered without retransmission
	// (RACK.fack) by earlier acknowledgements; a packet delivered below it
	// arrived out of order, and reorderingSeen records that. ackFack is the
	// same for the acknowledgement being processed, folded into fack once it
	// is done, since packets retired by one acknowledgement are not
	// reordered relative to each other.
	fack           uint16
	haveFack       bool
	ackFack        uint16
	haveAckFack    bool
	reorderingSeen bool

	// reoWndMult and reoWndPersist widen the reordering window after a
	// spurious retransmission, for rackReoWndPersist recoveries.
	reoWndMult    uint32
	reoWndPersist int

	// inRecovery is set when a loss is declared and cleared once the
	// cumulative acknowledgement passes recoveryEnd, the last packet sent
	// when it began.
	inRecovery  bool
	recoveryEnd uint16

	// coverEnd is the highest sequence number the last acknowledgement
	// reported on: its ack number or its highest selective-ack bit. Below it
	// the acknowledgement accounts for every packet; beyond it a packet may
	// have arrived unreported. uTP's selective ack covers a fixed window past
	// the ack number, whereas RFC 8985 assumes TCP's SACK blocks, which can
	// reach anywhere, so packets past coverEnd are not declared lost.
	coverEnd uint16

	// pending counts packets declared lost and not yet resent.
	pending int

	// timeout is how long until the next packet still inside its reordering
	// window leaves it, or zero if none is. Set by rackDetect for the
	// connection to arm its timer.
	timeout time.Duration

	losses  uint64 // packets declared lost
	relosts uint64 // of those, retransmissions declared lost again
}

// rackOnDelivered is RACK_update (RFC 8985 section 6.2, steps 1-3) for a
// packet this acknowledgement delivered for the first time.
func (s *sentPackets) rackOnDelivered(p *sentPacket, now time.Time) {
	r := &s.rack
	if p.rackPending {
		p.rackPending = false
		r.pending--
	}
	retransmitted := !p.retransmission.Equal(p.transmission)
	if !retransmitted {
		if sample := now.Sub(p.transmission); r.minRTT == 0 || sample < r.minRTT {
			r.minRTT = sample
		}
		if r.haveFack && wrappingLessThan(p.seqNum, r.fack) {
			r.reorderingSeen = true
		}
		if !r.haveAckFack || wrappingLessThan(r.ackFack, p.seqNum) {
			r.ackFack, r.haveAckFack = p.seqNum, true
		}
	} else if r.minRTT > 0 && now.Sub(p.retransmission) < r.minRTT {
		// Acknowledged sooner after the resend than any round trip: most
		// likely the original's acknowledgement, so no evidence about the
		// resend (step 2).
		return
	}
	if !r.delivered || p.sentOrder > r.xmitOrder {
		sent := p.retransmission
		r.delivered, r.xmitTime, r.xmitOrder, r.endSeq, r.rtt = true, sent, p.sentOrder, p.seqNum, now.Sub(sent)
	}
}

// rackOnAckNum ends a recovery once the cumulative acknowledgement has
// passed everything sent before it began.
func (s *sentPackets) rackOnAckNum(ackNum uint16) {
	r := &s.rack
	if r.inRecovery && !wrappingLessThan(ackNum, r.recoveryEnd) {
		r.inRecovery = false
	}
}

// rackOnSpuriousLoss widens the reordering window, as RFC 8985 does on a
// DSACK (section 6.2, step 4), and records that the path reorders, since a
// spurious loss is a packet that arrived after later ones. Without the
// latter, a path that reorders past the threshold could have every original
// resent before it arrived, and RACK would never see reordering.
func (s *sentPackets) rackOnSpuriousLoss() {
	if !s.rack.active {
		return
	}
	s.rack.reorderingSeen = true
	s.rack.reoWndMult++
	s.rack.reoWndPersist = rackReoWndPersist
}

// rackReoWnd is RACK_update_reo_wnd (RFC 8985 section 6.2, step 4). The
// window is zero until reordering has been seen, once three packets have
// been selectively acknowledged or a recovery is under way; otherwise it is
// a quarter of the minimum round trip, widened after spurious losses and
// capped at the smoothed round trip.
func (s *sentPackets) rackReoWnd(sacked int) time.Duration {
	r := &s.rack
	if !r.reorderingSeen && (r.inRecovery || sacked >= lossThreshold) {
		return 0
	}
	w := r.minRTT / 4 * time.Duration(r.reoWndMult)
	if srtt := s.controllerStats().FineRTT; srtt > 0 && w > srtt {
		w = srtt
	}
	return w
}

// rackDetect is RACK_detect_loss (RFC 8985 section 6.2, step 5). It returns
// the outstanding packets now lost and sets rack.timeout to when the next
// would be.
func (s *sentPackets) rackDetect(now time.Time) []*sentPacket {
	r := &s.rack
	if r.haveAckFack {
		if !r.haveFack || wrappingLessThan(r.fack, r.ackFack) {
			r.fack, r.haveFack = r.ackFack, true
		}
		r.haveAckFack = false
	}
	r.timeout = 0
	if !r.delivered {
		return nil
	}
	first, err := s.FirstUnackedSeqNum()
	if err != nil {
		return nil
	}
	start := s.SeqNumIndex(first)
	if start < 0 || start >= len(s.packets) {
		return nil
	}
	window := s.packets[start:]
	sacked := 0
	for _, p := range window {
		if p.acked {
			sacked++
		}
	}
	reoWnd := s.rackReoWnd(sacked)
	var lost []*sentPacket
	for _, p := range window {
		if wrappingLessThan(r.coverEnd, p.seqNum) {
			break // nothing reported this far, either way
		}
		if p.acked || p.needResend || p.rackPending {
			continue
		}
		sent := p.retransmission
		if !p.rackLostAt.IsZero() && p.rackLostAt.Equal(sent) {
			continue // this transmission is already counted lost
		}
		if p.sentOrder >= r.xmitOrder {
			continue // not sent before anything delivered
		}
		if remaining := sent.Add(r.rtt + reoWnd).Sub(now); remaining > 0 {
			r.timeout = max(r.timeout, remaining)
			continue
		}
		lost = append(lost, p)
	}
	return lost
}

// rackMarkLost queues a packet RACK found lost for resending and charges the
// loss to the congestion controller.
func (s *sentPackets) rackMarkLost(p *sentPacket, now time.Time) {
	r := &s.rack
	p.rackLostAt = p.retransmission
	p.rackPending = true
	r.pending++
	r.losses++
	if !p.retransmission.Equal(p.transmission) {
		r.relosts++
	}
	if !r.inRecovery {
		r.inRecovery = true
		r.recoveryEnd = s.NextSeqNum() - 1
		if r.reoWndPersist > 0 {
			r.reoWndPersist--
			if r.reoWndPersist == 0 {
				r.reoWndMult = 1
			}
		}
	}
	_ = s.OnLost(p.seqNum, true, now)
}

// rackDetectAndMark marks what RACK finds lost now and reports whether
// anything was.
func (s *sentPackets) rackDetectAndMark(now time.Time) bool {
	lost := s.rackDetect(now)
	for _, p := range lost {
		s.rackMarkLost(p, now)
	}
	return len(lost) > 0
}

// rackTakeLost is TakeLostPackets under RACK: the packets declared lost,
// oldest first, at most maxFastResendsPerAck, retransmissions included.
func (s *sentPackets) rackTakeLost() []*lostPacket {
	var result []*lostPacket
	for _, p := range s.packets {
		if s.rack.pending == 0 || len(result) >= maxFastResendsPerAck {
			break
		}
		if !p.rackPending {
			continue
		}
		p.rackPending = false
		s.rack.pending--
		if p.acked {
			continue
		}
		result = append(result, &lostPacket{p.seqNum, p.packetType, p.data})
		if wrappingLessThan(s.fastResendSeqNum, p.seqNum+1) {
			s.fastResendSeqNum = p.seqNum + 1
		}
	}
	return result
}

// rackOnAck records how far an acknowledgement reports; see coverEnd.
func (s *sentPackets) rackOnAck(ackNum uint16, sack *selectiveAck) {
	s.rack.coverEnd = ackNum
	if sack == nil {
		return
	}
	for i, set := range sack.Acked() {
		if set {
			s.rack.coverEnd = ackNum + 2 + uint16(i)
		}
	}
}
