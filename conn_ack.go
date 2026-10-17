package utp

import (
	"errors"
	"math"
	"time"
)

// maxAckCoalesce bounds how many already-queued packets one pass of the event
// loop will take before answering them.
//
// libutp has no explicit bound: its embedder drains its socket and then calls
// utp_issue_deferred_acks. The bound here exists because this loop also
// services writes and timers, and an unbounded inner drain would let a peer
// sending continuously starve them.
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
	// run. It waits no longer than two of the latest gap between packets per
	// packet it is waiting for, so the end of a burst is acknowledged once
	// the packets stop coming. Waiting by a smoothed gap, or with a 1 ms
	// floor, held each slow-start burst's acknowledgement for packets that
	// could not come until it was sent.
	maxAckWait = 5 * time.Millisecond
	// ackRateGate is how much queueing delay the way back must show before
	// the rate term applies, and ackRateMemory how long it goes on applying
	// after the last time it did. On paths where nothing queues, the delay
	// this receiver reads for its acknowledgements still reaches 2-6 ms: the
	// sender stamps an acknowledgement when it reads it, and a sender busy
	// sending reads late. A return path that cannot carry the
	// acknowledgements queues them by tens of milliseconds within a round
	// trip or two.
	ackRateGate   = 10 * time.Millisecond
	ackRateMemory = 10 * time.Second
	// A queue on the way back is taken for this receiver's own
	// acknowledgements once it has climbed twice in a row: three windows of
	// ackQueueWindow samples, ackQueueSampleEvery apart and the starts of
	// consecutive windows ackQueueSpan samples apart, each window's least
	// above the greatest of the one before by ackQueueClimb -- over 400 ms.
	// See ackPathSelfQueued.
	ackQueueSampleEvery = 25 * time.Millisecond
	ackQueueWindow      = 4
	ackQueueSpan        = 6
	ackQueueClimb       = 2 * time.Millisecond
)

// ackEvery is how many data packets an acknowledgement may wait to cover.
//
// libutp acknowledges once per pass of its embedder's loop: whatever arrived
// while it was busy shares one acknowledgement, and a packet that arrives
// alone gets its own. On a link, where packets arrive spaced, that is one per
// packet -- measured over real sockets with the relay pacing each datagram to
// its time, 0.99 per data packet at 10 Mb/s and 0.98 at 20 Mb/s -- and only
// at 100 Mb/s, where packets come 110 us apart, does its loop gather a few
// (0.58-0.62). So libutp sends a 20 Mb/s flow's acknowledgements at 1,750 a
// second whatever the return path can carry: over a 160 kb/s return path,
// which carries 1,000 a second, it took 2.97 s to receive 4 MB where 2.0 s
// was the data path's limit, and over 64 kb/s 7.3 s.
//
// This receiver acknowledges as libutp does until its acknowledgements are
// seen to queue on the way back, and then lets one wait for more packets, by
// two terms, the larger winning:
//
//   - The return path: the peer reports on every packet how long ours take to
//     reach it, and for a connection that only receives, ours are its
//     acknowledgements. Each ackQueueStep of filtered queueing delay on that
//     path (libutp's our_hist.get_value()) adds one packet to the run.
//   - The rate, for ackRateMemory after that delay last reached ackRateGate:
//     while packets arrive closer together than ackRatePeriod, round(1 ms /
//     gap) of them share one, at most maxAckRateRun. The gap is the smaller
//     of the latest and a smoothed one, so a burst after a pause is coalesced
//     from its second packet. Without it the queue the first term answers to
//     stands: 2.13 s over 160 kb/s and 3.38 s over 64 kb/s.
//
// Measured against libutp receiving, a libutp sender, over real sockets
// (native/libutp TestAsymmetricAckPath), 20 runs each: 2.03 s against 2.99 s
// over 160 kb/s and 2.43 s against 7.33 s over 64 kb/s; over 320 kb/s, 20
// Mb/s and 100 Mb/s the same times and as many acknowledgements as libutp's.
// The rate term used to apply on every path, and cost the wait it imposes: at
// 100 Mb/s the median acknowledgement left 240 us after its packet arrived,
// against libutp's 30 us and 50 us now (TestAckTurnaround), for no gain in
// throughput there. DEVIATIONS.md, "Acknowledgements: one per read, fewer
// when they would crowd the way back".
//
// Never while a gap is open: the selective ack is how the peer learns of a
// loss, and holding it back would delay recovery.
//
// And only when the queue is this receiver's own. Coalescing helps when the
// way back is clogged with its acknowledgements; then sending fewer drains
// it. When the queue is other traffic's -- an uplink shared with someone's
// upload -- fewer acknowledgements change nothing there, and cost the
// sender's yielding, libutp's sender as much as ours. Measured with a
// loss-based flow sharing one bottleneck both ways, where this receiver
// coalesced to a third of libutp's acknowledgements: the competitor kept
// 64.0% of its rate against 67.5% with libutp's receiver, and 67.5% with
// this one acknowledging as libutp does (30 runs each). So it coalesces only
// after the queue has been seen climbing as only its own acknowledgements
// make it climb (ackPathSelfQueued), and otherwise acknowledges every read,
// as libutp does.
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

// ackPathSelfQueued reports whether the queue on the way back is this
// receiver's own acknowledgements, as far as can be told: it has been seen,
// within ackRateMemory, climbing steadily out of its own noise. The delay is
// sampled every ackQueueSampleEvery; it is climbing steadily when it has
// climbed twice in a row: the least of a window of ackQueueWindow samples
// above the greatest of the window ackQueueSpan samples before it by
// ackQueueClimb or more, and that window likewise above the one before it.
// See ackEvery.
//
// Acknowledgements a return path cannot carry pile up steadily: over a
// 160 kb/s return path, with a 20 Mb/s flow in slow start, the delay read
// for the way back climbed from nothing to 43 ms in 1.2 s, about 2 ms every
// 25 ms, with small dips. Another flow's queue does not climb like that
// under this one's acknowledgements: with a loss-based flow sharing the
// bottleneck, the delay swung between nothing and 50-80 ms from one 50 ms
// sample to the next, and a window of those holds a sample near nothing.
//
// Twice in a row, because the delay also steps when a host is busy. Every
// timestamp it is read from is taken by a process that may be waiting for
// a CPU, and with every package's tests running at once one climb -- one
// window 2.7 to 7.7 ms above the one before -- was seen in four runs of ten
// behind a loss-based flow. Each made this receiver coalesce for
// ackRateMemory: 0.28-0.36 acknowledgements per data packet against
// libutp's 0.97, which yields less. A step is one climb and then level; a
// queue of acknowledgements climbs the whole time. Under the same load the
// two-climb rule fired in none of ten
// (netem.TestReceiverDoesNotCoalesceBehindAnotherFlow). It sees a real
// queue later, and costs a little where coalescing pays: 4 MB from libutp
// over a 64 kb/s return path in 2.53 s against the one-climb rule's 2.47,
// over 160 kb/s 2.09 against 2.03, at 320 kb/s the same (ten interleaved
// runs each; libutp's own receiver takes 7.32 and 3.0 s). Windows eight
// samples apart rather than six took 2.62 and 2.12.
//
// Four simpler tests were tried before the one-climb rule and failed:
// whether coalescing halved the queue (those swings halve on their own),
// whether the queue came back while coalescing, whether it fell between the
// halves of a second of coalescing (over 64 kb/s the sender is still in slow
// start, its acknowledgements growing faster than coalescing takes them),
// and whether it rose by 5 ms in each of four samples (it rises by about 2).
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
	// One acknowledgement per socket read, as libutp's embedder sends it: the
	// datagrams already waiting in the kernel are all processed, and then
	// utp_issue_deferred_acks runs once (utp.h:512-517). A read that is still
	// being handed out holds the acknowledgement; the reader ends the batch
	// (endBatch) once it has dispatched the whole read, and the
	// acknowledgement then covers everything the read brought.
	//
	// This used to flush at the end of every pass of this loop, and the batch
	// boundary was wherever the goroutine hand-offs between the socket and
	// here happened to fall. With batches alone, and ackEvery at one, a
	// libutp sender at 100 Mb/s got 0.57-0.68 acknowledgements per data
	// packet from this receiver and 0.58-0.62 from libutp's.
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
// acknowledgement may point and still be believed.
//
// libutp: `#define ACK_NR_ALLOWED_WINDOW DUPLICATE_ACKS_BEFORE_RESEND`, which
// is 3 (utp_internal.cpp:64, :69).
const ackNrAllowedWindow = uint16(3)

// invalidAckNum reports whether a packet acknowledges something this
// connection never sent, or something so old it cannot be meaningful.
//
// libutp (utp_internal.cpp:1794-1807), which is unusually explicit about why:
//
//	// ignore packets whose ack_nr is invalid. This would imply a spoofed
//	// address or a malicious attempt to attach the uTP implementation.
//	// acking a packet that hasn't been sent yet!
//
//	const uint16 curr_window = max<uint16>(
//	    conn->cur_window_packets + ACK_NR_ALLOWED_WINDOW, ACK_NR_ALLOWED_WINDOW);
//	if ((pk_flags != ST_SYN || conn->state != CS_SYN_RECV) &&
//	    (wrapping_compare_less(conn->seq_nr - 1, pk_ack_nr, ACK_NR_MASK)
//	     || wrapping_compare_less(pk_ack_nr, conn->seq_nr - 1 - curr_window, ACK_NR_MASK)))
//	    return 0;
//
// The acceptable range is a short window ending at the last sequence number
// actually sent. Anything above it acknowledges a packet that does not exist
// yet; anything below is too stale to act on.
//
// This fork had no such rule. CONFORMANCE.md previously recorded that we
// "already match" here, on the strength of two corpus cases that happened to
// produce the same silence for other reasons -- the behaviour was incidental,
// not implemented. FuzzDifferentialResponder showed the difference with a
// single ST_DATA whose ack_nr named a packet we had never sent: libutp
// dropped it without a word, we answered it.
func (c *connection) invalidAckNum(pkt *packet) bool {
	if pkt.Header.PacketType == st_syn {
		// libutp's exception for a SYN arriving in CS_SYN_RECV: there are no
		// previous packets for it to be acking.
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
		// Acknowledges a packet we have not sent.
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
	// ack -- every data packet a receiver with nothing in flight is sent --
	// still gives the delay sample libutp takes from every packet
	// (our_hist, utp_internal.cpp:2116-2127), and the MTU search still
	// checks whether it is due. Nothing else below has work to do, and on a
	// reader just woken, walking it was 3.5-4us of each packet's path to its
	// acknowledgement.
	//
	// Except a timer still armed for that number: an initiator's SYN, whose
	// timer the first acknowledgement after the handshake retires. That one
	// takes the full path.
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
			// outstanding acknowledges nothing. It does not end the
			// connection.
			//
			// libutp computes how many packets an acknowledgement covers and
			// then throws the answer away when it exceeds the window:
			//
			//	int acks = (pk_ack_nr - (conn->seq_nr - 1 - conn->cur_window_packets)) & ACK_NR_MASK;
			//	// this happens when we receive an old ack nr
			//	if (acks > conn->cur_window_packets) acks = 0;
			//	                                (utp_internal.cpp:1904-1907)
			//
			// This used to call reset, which is the divergence
			// CONFORMANCE.md records as one the packet-injection corpus
			// cannot see: both sides answer such a packet with silence, so
			// the transcripts match while one connection is dead and the
			// other is not.
			//
			// Two reasons it had to go. It is an attack surface -- a single
			// forged ST_STATE from anyone who can guess a connection id ends
			// an established transfer -- and it is a live defect: a delayed
			// or duplicated acknowledgement arriving after the window has
			// moved on is an ordinary event on a lossy path, and it killed
			// connections that should have carried on. It is what made
			// integrated.TestCloseSucceedsIfOnlyFinAckDropped fail about one
			// run in eight, reporting "invalid ack number" where the test
			// expects the idle timeout.
			//
			// The selective-ack extension on such a packet is dropped with
			// it, where libutp would still apply it. That is deliberate and
			// narrow: our extension is applied relative to this same
			// acknowledgement number, so honouring it would mean indexing the
			// loss-detection machinery off a figure just established to be
			// outside the window.
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
	// A probe that came back acknowledged proves the path carries its size.
	// libutp: utp_internal.cpp:1969-1974.
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

	// A converged search stands for a while and is then redone, because paths
	// change. libutp: 30 minutes (utp_internal.cpp:1310).
	if c.mtu.dueForSearch(now) {
		c.mtu.research(uint32(c.config.MaxPacketSize), now)
	}

	retired := c.disarmAcked(fullAcked)
	recovering := c.probeAnswered(fullAcked, now)
	c.checkSpuriousLoss(fullAcked, selectedAcks)

	// Restart the retransmission timeout, but only when this ack actually
	// retired something.
	//
	// libutp resets rto_timeout from inside its RTT-sample path
	// (utp_internal.cpp:1388-1389), which runs once per newly acknowledged
	// packet -- not once per ack received. Resetting on every ack looks
	// equivalent and is not: duplicate acks are how a peer reports a hole,
	// so on a lossy path they arrive in a stream, and each one would push the
	// deadline further out. The RTO would then never fire, and a packet that
	// fast retransmit could not recover -- it resends each packet once, and
	// at most four per ack -- would never be retransmitted at all.
	//
	// Measured: with the reset unconditional, the 5%-loss and broadband
	// profiles in the netem benchmark suite stopped completing, the receiver
	// timing out after 76 seconds on a transfer that takes one.
	//
	// One deliberate difference: libutp's ack_packet also runs for a packet
	// acknowledged selectively (:1529), so a selective ack moves its deadline
	// too. Here only the cumulative acknowledgement does. Adopting libutp's
	// rule was measured and was slower -- see DEVIATIONS.md, "A selective
	// ack does not restart the retransmission timeout".
	//
	// Not when this is the loss probe's acknowledgement and it has shown
	// other packets lost: probeAnswered has started resending them, and the
	// timeout stays where it was, so the probe never makes recovery wait
	// longer than libutp's timeout would. Restarted here, each probe that
	// repaired one hole pushed the timeout back, and a burst came back one
	// packet per probe timeout.
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
		// The peer retired something we were still timing, so the path is
		// alive: reset the consecutive-timeout count. libutp resets
		// retransmit_count in ack_packet for the same reason
		// (utp_internal.cpp:1398).
		//
		// Selective acks count. Under reordering or loss the only progress
		// may be a SACK, and ignoring those would kill a connection that is
		// in fact delivering.
		c.retransmitCount = 0
	}

	return nil
}

// statePacket builds an ST_STATE, the only packet that carries a selective
// ack. libutp attaches the extension in send_ack alone (`pfa.pf.ext = 1`,
// utp_internal.cpp:795); a data packet, a FIN and every resend go out with
// `ext = 0` (:1080, :2781), and its packet size leaves room for the 20-byte
// header and nothing else (:1757-1761). This library used to attach the receive buffer's selective ack to
// all of them, and to reserve room for it in every packet.
//
// On a connection carrying data both ways, that means the peer hears about a
// hole in what it sent only from an ST_STATE -- and a data packet that goes
// out carrying the same acknowledgement number cancels the pending one
// (send_data, :768; transmit here). libutp lives with that, and so does this
// library now.
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
		// NOTE: Consistent with the reference implementation and the libtorrent
		// implementation, STATE packets always include the next sequence number.
		seqNum := c.state.SentPackets.NextSeqNum()
		ackNum := c.state.RecvBuf.AckNum()
		recvWindow := c.recvWindow()

		// No selective ack once the peer's FIN has been reached in order.
		//
		// libutp: "we never need to send EACK for connections that are
		// shutting down" (utp_internal.cpp:786-788, the `!got_fin_reached`
		// term). `eof()` is our equivalent of `got_fin_reached`. Anything
		// still pending past a reached FIN is data the peer sent after
		// declaring it had none left; naming it in a selective ack invites a
		// retransmission of data neither side will deliver.
		//
		// This is close to unreachable as the connection stands: `eof()` only
		// becomes true when the receive buffer has caught up to the FIN, and
		// the same event loop then tears the connection down (the RemoteFin
		// branch below). It is kept because it matches the reference and
		// because it becomes load-bearing the moment a half-close exists --
		// see TestConformanceDataAfterReachedFin.
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
