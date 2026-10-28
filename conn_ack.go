package utp

import (
	"errors"
	"math"
	"time"
)

// maxAckCoalesce bounds how many already-queued packets one pass of the event
// loop takes before answering them, so a peer sending continuously cannot
// starve the loop's writes and timers.
const maxAckCoalesce = 64

// Acknowledgement coalescing. See ackEvery.
const (
	// ackQueueStep is how much queueing delay on the way back adds one packet
	// to the run an acknowledgement waits for.
	ackQueueStep = 500 * time.Microsecond
	// ackRatePeriod is the shortest spacing this receiver keeps between its
	// acknowledgements when data packets arrive closer together than that,
	// up to maxAckRateRun packets apiece.
	ackRatePeriod  = time.Millisecond
	maxAckRateRun  = 4
	maxAckQueueRun = 16
	// maxAckWait is the longest an acknowledgement waits for the rest of its
	// run: no longer than twice the latest inter-packet gap per packet
	// awaited, so the end of a burst is acknowledged once packets stop.
	maxAckWait = 5 * time.Millisecond
	// ackRateGate is how much queuing delay the return path must show before
	// the rate term applies, and ackRateMemory how long it keeps applying
	// after the last time it did. Even where nothing queues, the measured
	// return delay reaches a few milliseconds, since a busy sender reads
	// acknowledgements late; a return path that cannot carry them queues
	// them by tens of milliseconds.
	ackRateGate   = 10 * time.Millisecond
	ackRateMemory = 10 * time.Second
	// A queue on the return path is attributed to this receiver's own
	// acknowledgements once it has climbed twice in a row: three windows
	// of ackQueueWindow samples, ackQueueSampleEvery apart, with window
	// starts ackQueueSpan samples apart, each window's least sample above
	// the greatest of the one before by ackQueueClimb. See
	// ackPathSelfQueued.
	ackQueueSampleEvery = 25 * time.Millisecond
	ackQueueWindow      = 4
	ackQueueSpan        = 6
	ackQueueClimb       = 2 * time.Millisecond
)

// ackEvery returns how many data packets an acknowledgement may wait to
// cover.
//
// By default every socket read is acknowledged once: whatever arrived
// together shares one acknowledgement. Once this receiver's acknowledgements
// are seen to queue on the return path, an acknowledgement may wait for more
// packets, by the larger of two terms:
//
//   - The return path: the peer reports on every packet how long ours take
//     to reach it. Each ackQueueStep of filtered queuing delay on that path
//     adds one packet to the run.
//   - The rate, for ackRateMemory after that delay last reached
//     ackRateGate: while packets arrive closer together than ackRatePeriod,
//     round(1 ms / gap) of them share one, at most maxAckRateRun. The gap is
//     the smaller of the latest and a smoothed one, so a burst after a pause
//     is coalesced from its second packet.
//
// Never while a gap is open: the selective ack is how the peer learns of a
// loss.
//
// And only when the queue is this receiver's own (ackPathSelfQueued). When
// the queue is other traffic's, fewer acknowledgements do not relieve it and
// would slow the sender's response to it.
func (c *connection) ackEvery() int {
	if c.state == nil || c.state.SentPackets == nil || c.state.RecvBuf == nil {
		return 1
	}
	if c.state.RecvBuf.SelectiveAck() != nil {
		return 1
	}
	q := c.state.SentPackets.QueueingDelay()
	now := c.now()
	if !c.ackPathSelfQueued(q, now) {
		return 1
	}
	k := min(1+int(q/ackQueueStep), maxAckQueueRun)
	if q >= ackRateGate {
		c.ackPathQueuedAt = now
	}
	if !c.ackPathQueuedAt.IsZero() && now.Sub(c.ackPathQueuedAt) < ackRateMemory {
		gap := c.dataGap
		if c.lastDataGap > 0 {
			gap = min(gap, c.lastDataGap)
		}
		if gap > 0 {
			k = max(k, min(max(int((ackRatePeriod+gap/2)/gap), 1), maxAckRateRun))
		}
	}
	return k
}

// ackPathSelfQueued reports whether the queue on the return path appears to
// be this receiver's own acknowledgements: within ackRateMemory it has been
// seen climbing steadily, twice in a row, by ackQueueClimb or more between
// windows of samples. See ackEvery.
//
// Acknowledgements a return path cannot carry pile up steadily. Another
// flow's queue fluctuates from sample to sample, so a window of samples
// includes one near zero. Two climbs are required because a busy host can
// step the measured delay once, and a step is one climb and then level.
func (c *connection) ackPathSelfQueued(q time.Duration, now time.Time) bool {
	if c.ackQueueSampledAt.IsZero() || now.Sub(c.ackQueueSampledAt) >= ackQueueSampleEvery {
		c.ackQueueSampledAt = now
		n := len(c.ackQueueSamples)
		c.ackQueueSamples[c.ackQueueNext%n] = q
		c.ackQueueNext++
		if c.ackQueueNext >= n {
			oldest := c.ackQueueNext - n
			// window returns the least and greatest of the ackQueueWindow
			// samples starting k spans after the oldest.
			window := func(k int) (lo, hi time.Duration) {
				lo = time.Duration(math.MaxInt64)
				for i := 0; i < ackQueueWindow; i++ {
					v := c.ackQueueSamples[(oldest+k*ackQueueSpan+i)%n]
					lo, hi = min(lo, v), max(hi, v)
				}
				return lo, hi
			}
			_, aHi := window(0)
			bLo, bHi := window(1)
			cLo, _ := window(2)
			if bLo-aHi >= ackQueueClimb && cLo-bHi >= ackQueueClimb {
				c.ackSelfQueuedAt = now
			}
		}
	}
	return !c.ackSelfQueuedAt.IsZero() && now.Sub(c.ackSelfQueuedAt) < ackRateMemory
}

// flushAck sends the acknowledgement owed for whatever was received, if any,
// unless ackEvery says it should wait for more.
func (c *connection) flushAck() {
	if !c.ackPending {
		return
	}
	// One acknowledgement per socket read: all datagrams already waiting
	// are processed, then one acknowledgement covers them. While a read is
	// still being dispatched the acknowledgement is held; the reader ends
	// the batch (endBatch) once it has dispatched the whole read.
	if c.inBatch {
		return
	}
	if k := c.ackEvery(); k > 1 && c.dataSinceAck > 0 && c.dataSinceAck < k {
		now := c.now()
		if c.ackHeldSince.IsZero() {
			c.ackHeldSince = now
		}
		hold := min(maxAckWait, 2*time.Duration(k)*c.lastDataGap)
		if due := c.ackHeldSince.Add(hold); now.Before(due) {
			if c.armAckHold != nil {
				c.armAckHold(due.Sub(now))
			}
			return
		}
	}
	c.ackHeldSince = time.Time{}
	c.dataSinceAck = 0
	c.ackPending = false
	if statePacket := c.statePacket(); statePacket != nil {
		c.emit(statePacket)
	}
}

// ackNrAllowedWindow is how far behind the last sent sequence number a peer's
// acknowledgement may point and still be accepted.
const ackNrAllowedWindow = uint16(3)

// invalidAckNum reports whether a packet acknowledges something this
// connection never sent, or something too old to be meaningful. The
// acceptable range is a short window ending at the last sequence number
// sent; anything above acknowledges a packet that does not exist yet, which
// implies a spoofed or malicious packet.
func (c *connection) invalidAckNum(pkt *packet) bool {
	if pkt.Header.PacketType == st_syn {
		// A SYN arriving in CS_SYN_RECV has no earlier packets to acknowledge.
		return false
	}
	if c.state.stateType != connConnected || c.state.SentPackets == nil {
		return false
	}

	lastSent := c.state.SentPackets.NextSeqNum() - 1
	window := c.state.SentPackets.UnackedCount() + ackNrAllowedWindow
	if window < ackNrAllowedWindow {
		window = ackNrAllowedWindow
	}

	ackNum := pkt.Header.AckNum
	if wrappingLessThan(lastSent, ackNum) {
		// Acknowledges a packet not yet sent.
		return true
	}
	if wrappingLessThan(ackNum, lastSent-window) {
		// Too far behind to mean anything.
		return true
	}
	return false
}

func (c *connection) processAck(
	ackNum uint16,
	selectiveAck *selectiveAck,
	delay time.Duration,
	now time.Time,
) error {
	if c.state.stateType != connConnected {
		return nil
	}

	// An acknowledgement that names nothing new and carries no selective
	// ack still gives a delay sample, and the MTU search still checks
	// whether it is due; nothing else below has work to do. The exception is
	// a timer still armed for that number, such as an initiator's SYN,
	// which takes the full path.
	if sp := c.state.SentPackets; selectiveAck == nil && ackNum == sp.base-1 && !c.isArmed(ackNum) {
		sp.onAckOfNothingNew(delay, now)
		c.mtu.onAck(ackNum, now)
		if c.mtu.dueForSearch(now) {
			c.mtu.research(uint32(c.config.MaxPacketSize), now)
		}
		return nil
	}

	fullAcked, selectedAcks, err := c.state.SentPackets.onAck(
		ackNum, selectiveAck, delay, now)
	if err != nil {
		seqRange := c.state.SentPackets.SeqNumRange()
		if c.logger.Enabled(baseContext, LevelTrace) {
			c.logger.Trace("sent packets onAck has error",
				"err", err,
				"cid.send", c.cid.Send,
				"cid.recv", c.cid.Recv,
				"ackNum", ackNum,
				"seqStart",
				seqRange.start,
				"seqEnd", seqRange.end)
		}
		if errors.Is(err, errInvalidAckNum) {
			// An acknowledgement number outside what this connection has
			// outstanding acknowledges nothing and does not end the
			// connection: a delayed or duplicated acknowledgement is
			// ordinary on a lossy path, and a forged one is cheap. Its
			// selective ack is dropped with it, since it is relative to the
			// same out-of-window number.
			c.logger.Debug("ignoring an acknowledgement outside the window",
				"ackNum", ackNum, "seqStart", seqRange.start, "seqEnd", seqRange.end,
				"cid.send", c.cid.Send, "cid.recv", c.cid.Recv)
			return nil
		}
		return err
	}
	if c.logger.Enabled(baseContext, LevelTrace) {
		c.logger.Trace("process ack",
			"cid.send", c.cid.Send,
			"cid.recv", c.cid.Recv,
			"fullAcked.start", fullAcked.start,
			"fullAcked.end", fullAcked.end)
	}
	// A probe acknowledged proves the path carries its size.
	for seq := fullAcked.Start(); ; seq++ {
		if c.mtu.onAck(seq, now) {
			c.logger.Debug("MTU probe acknowledged",
				"floor", c.mtu.floor, "ceiling", c.mtu.ceiling, "current", c.mtu.current)
		}
		if seq == fullAcked.End() {
			break
		}
	}
	for _, selectedAck := range selectedAcks {
		if c.mtu.onAck(selectedAck, now) {
			c.logger.Debug("MTU probe selectively acknowledged",
				"floor", c.mtu.floor, "ceiling", c.mtu.ceiling, "current", c.mtu.current)
		}
	}

	// A converged search is redone after a while, because paths change.
	if c.mtu.dueForSearch(now) {
		c.mtu.research(uint32(c.config.MaxPacketSize), now)
	}

	retired := c.disarmAcked(fullAcked)
	recovering := c.probeAnswered(fullAcked, now)
	c.checkSpuriousLoss(fullAcked, selectedAcks)

	// Restart the retransmission timeout, but only when this ack retired
	// something cumulatively. Duplicate acks are how a peer reports a hole;
	// if each pushed the deadline out, a packet fast retransmit could not
	// recover would never be resent. A selective ack does not restart it.
	//
	// Nor when this is the loss probe's acknowledgement and it has shown
	// other packets lost: probeAnswered has started resending them, and
	// the timeout stays where it was so the probe never delays recovery.
	if retired > 0 && !recovering {
		c.rtoDeadline = now.Add(c.state.SentPackets.Timeout())
	}
	for _, selectedAck := range selectedAcks {
		if c.logger.Enabled(baseContext, LevelTrace) {
			c.logger.Trace("process ack, will remove acked num from innerMap",
				"cid.send", c.cid.Send,
				"cid.recv", c.cid.Recv,
				"ackNum", selectedAck)
		}
		if _, armed := c.armed[selectedAck]; armed {
			retired++
		}
		c.disarmRetransmit(selectedAck)
	}

	if retired > 0 {
		// The peer retired something we were timing, so the path is alive:
		// reset the consecutive-timeout count. Selective acks count, since
		// under reordering or loss they may be the only progress.
		c.retransmitCount = 0
	}

	return nil
}

// statePacket builds an ST_STATE packet, the only kind that carries a
// selective ack. Data packets, FINs and resends carry none, so a data packet
// fits in the header plus payload.
//
// On a connection carrying data both ways, the peer therefore learns of a
// hole only from an ST_STATE, and a data packet carrying the same
// acknowledgement number cancels a pending one (transmit).
func (c *connection) statePacket() *packet {
	now := int64(c.nowMicros())
	tsDiffMicros := uint32(c.peerTsDiff.Microseconds())

	switch c.state.stateType {
	case connConnecting:
		if c.endpoint.Type == initiator {
			return nil
		}

		syn := c.endpoint.SynNum
		synAck := c.endpoint.SynAck
		return newPacketBuilder(st_state, c.cid.Send, uint32(now), c.config.WindowSize, synAck).
			WithTsDiffMicros(tsDiffMicros).
			WithAckNum(syn).
			Build()

	case connConnected:
		// STATE packets always carry the next sequence number.
		seqNum := c.state.SentPackets.NextSeqNum()
		ackNum := c.state.RecvBuf.AckNum()
		recvWindow := c.recvWindow()

		// No selective ack once the peer's FIN has been reached in order:
		// anything pending beyond it is data sent after the peer declared it
		// had no more, and requesting it would invite retransmissions
		// neither side will deliver.
		var selectiveAck *selectiveAck
		if !c.eof() {
			selectiveAck = c.state.RecvBuf.SelectiveAck()
		}

		return newPacketBuilder(st_state, c.cid.Send, uint32(now), recvWindow, seqNum).
			WithTsDiffMicros(tsDiffMicros).
			WithAckNum(ackNum).
			WithSelectiveAck(selectiveAck).
			Build()

	default: // ClosedState
		return nil
	}
}
