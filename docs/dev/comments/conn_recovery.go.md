# conn_recovery.go

Every comment in `conn_recovery.go` at e56c9aa, verbatim.

## Line 11 — const unackTimeoutQueueLen

```
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
```

## Line 23 — func (*connection).armRetransmit

```
// armRetransmit schedules a retransmission timer for one packet.
```

## Line 27 — func (*connection).armRetransmit

```
// First packet into an empty window: start the connection's RTO.
		//
		// libutp: "Setup initial timeout timer" in write_outgoing_packet,
		// guarded by `cur_window_packets == 0`
		// (utp_internal.cpp:994-998).
```

## Line 43 — func (*connection).isArmed

```
// isArmed reports whether seq has a retransmission timer.
```

## Line 49 — func (*connection).disarmRetransmit

```
// disarmRetransmit cancels one packet's retransmission timer.
//
// Unconditional on purpose. A timer that has already fired is out of the
// wheel but still in the armed set until the event loop drains it, so
// skipping the wheel when the set does not hold the key could leave a stale
// entry behind. Both operations are no-ops when the key is absent.
```

## Line 60 — func (*connection).disarmAcked

```
// disarmAcked cancels the timers for every sequence number this connection
// still has armed that falls inside acked.
//
// Only this connection's own armed set is scanned. The wheel is shared with
// every other connection on the socket, so scanning it would be O(all
// outstanding packets on the socket) on every ack.
//
// It returns how many timers it cancelled, which is how many packets this ack
// newly retired.
```

## Line 81 — func (*connection).disarmAll

```
// disarmAll cancels every timer this connection holds. The shared wheel
// outlives the connection, so anything left armed would linger until it
// expired and then be delivered to a dead event loop.
```

## Line 121 — func (*connection).onTimeout

```
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
```

## Line 136 — func (*connection).onTimeout

```
// Re-send SYN packet
```

## Line 141 — func (*connection).onTimeout

```
// If the timed out packet is a SYN, do nothing
```

## Line 146 — func (*connection).onTimeout

```
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
```

## Line 166 — func (*connection).onTimeout

```
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
```

## Line 184 — func (*connection).onTimeout

```
// A retransmission timeout on the MTU probe, with nothing else
		// outstanding, says the path will not carry that size -- not that it
		// is congested. libutp lowers the ceiling and sets `ignore_loss`, so
		// the window is left alone and the binary search moves on
		// (utp_internal.cpp:1152-1167).
```

## Line 192 — func (*connection).onTimeout

```
// Everything else in libutp's timeout still happens: the give-up
			// check comes first (:1191), and the timeout counts towards it
			// and starts the fast-timeout retry (:1240, :1247). Only the
			// doubling and the window collapse are skipped (:1179, :1206).
```

## Line 213 — func (*connection).onTimeout

```
// Not yet the connection's timeout: this packet's callback came
		// before the deadline, because it was armed before the deadline last
		// moved, or because the wheel ran ahead of the clock (it counts
		// ticks, and a descheduled wheel catches up in a burst; measured, a
		// 200ms timer delivered at 198.6ms). libutp would do nothing at all
		// here. Keep this packet's timer alive against the deadline and send
		// nothing.
```

## Line 225 — func (*connection).onTimeout

```
// Give up once enough consecutive RTOs have passed with the peer
		// acking nothing, as libutp does (utp_internal.cpp:1191). The check
		// precedes the increment there, so the connection dies on the RTO
		// after the fourth retransmission.
```

## Line 230 — func (*connection).onTimeout

```
// With what it knew of the peer: whether it had heard nothing at
			// all, or heard only that the peer's window was shut, is what tells
			// a dead path from a peer that stopped reading.
```

## Line 248 — func (*connection).onTimeout

```
// Forget any outstanding MTU probe, whether or not it was the one
		// that timed out. libutp clears it on every RTO, outside the branch
		// that lowers the ceiling (utp_internal.cpp:1166-1167): the probe is
		// gone either way, and a search that keeps waiting for it never sends
		// another.
```

## Line 260 — func (*connection).onTimeout

```
// libutp: `rto_timeout = ctx->current_ms + new_timeout`
		// (utp_internal.cpp:1204), with new_timeout already doubled.
```

## Line 264 — func (*connection).onTimeout

```
// "every packet should be considered lost" (:1230-1237), and the
		// oldest resent (:1249-1251).
```

## Line 274 — func (*connection).onTimeout

```
// This packet's callback is spent. Keep a timer on it, against
			// the new deadline, so every outstanding packet stays armed.
```

## Line 282 — func (*connection).retransmit

```
// retransmit resends a packet with its acknowledgement fields brought up to
// date. The sequence number and payload are the original's; everything the
// peer reads about our current state is not.
```

## Line 290 — func (*connection).retransmit

```
// No selective ack: see statePacket.
```

## Line 304 — type lossUndoer

```
// lossUndoer is a controller that can undo a cut for a loss that proved
// spurious. See defaultController.undoSpuriousLoss.
```

## Line 311 — func (*connection).noteResend

```
// noteResend records the wire timestamp of the first resend of the packet
// whose loss made the controller's last cut, so that the acknowledgement can
// tell which copy arrived. See checkSpuriousLoss.
```

## Line 326 — func (*connection).checkSpuriousLoss

```
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
```

## Line 362 — func (*connection).resendSentPacket

```
// resendSentPacket sends a packet again from what was recorded when it was
// first sent, with its acknowledgement fields brought up to date. It is the
// resend for a packet a retransmission timeout gave up as lost, whether the
// timeout itself, processWrites or onFastTimeout is sending it. It returns the
// packet as sent.
```

## Line 373 — func (*connection).resendSentPacket

```
// No selective ack: see statePacket.
```

## Line 380 — func (*connection).resendSentPacket

```
// Not a first transmission, so never an MTU probe (utp_internal.cpp:911).
```

## Line 385 — var lossProbeEnabled

```
// lossProbeEnabled switches the loss probe. It is not libutp's: see
// DEVIATIONS.md, "A loss probe resends before the retransmission timeout".
```

## Line 393 — func (*connection).lossProbeActive

```
// lossProbeActive reports whether this connection uses the loss probe:
// classic LEDBAT only. Under LEDBAT++ the retransmission timeout it avoids is
// part of how the flow yields -- after one, the window collapses to two
// packets -- and with the probe LEDBAT++ took more than its share from a
// loss-based flow in about one run in five (DEVIATIONS.md). Classic LEDBAT's
// share was unchanged.
```

## Line 403 — const minLossProbeTimeout

```
// minLossProbeTimeout is RFC 8985's floor on the probe timeout (§7.2).
```

## Line 406 — func (*connection).lossProbeTimeout

```
// lossProbeTimeout is RFC 8985's PTO, max(2 x SRTT, 10 ms), or zero before
// there is a round-trip estimate.
```

## Line 409 — func (*connection).lossProbeTimeout

```
// The microsecond estimate: libutp's own is in whole milliseconds, and
	// on a path faster than one it is zero.
```

## Line 418 — func (*connection).rearmLossProbe

```
// rearmLossProbe restarts the loss probe's timeout. It runs on every
// acknowledgement and when the first packet enters an empty window, so the
// probe fires only after a probe timeout with nothing acknowledged. An
// acknowledgement that moves the cumulative ack forward starts a new
// episode, with a new probe to spend.
```

## Line 433 — func (*connection).rearmLossProbe

```
// Nothing left to probe for. A probe left armed would fire to find
		// nothing outstanding, and until it did the connection could not
		// rest (mayRest): 10 ms on Linux, past 400 ms on Windows CI.
```

## Line 447 — func (*connection).onLossProbe

```
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
```

## Line 481 — func (*connection).onLossProbe

```
// transmit armed the packet's timer a whole timeout from now; the
	// timeout itself is due where it was, and the probe must not move it.
	// The wheel places a timer by its deadline, so this keeps its tick.
```

## Line 487 — func (*connection).onFastTimeout

```
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
```

## Line 526 — func (*connection).probeAnswered

```
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
```

## Line 551 — func (*connection).probeAnswered

```
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
```

## Line 585 — func (*connection).resendProbedLosses

```
// resendProbedLosses is onFastTimeout for losses the probe showed: resend the
// oldest outstanding packet if it was last sent before the probe.
```

## Line 600 — const duplicateAcksBeforeResend

```
// duplicateAcksBeforeResend is libutp's DUPLICATE_ACKS_BEFORE_RESEND
// (utp_internal.cpp:64).
```

## Line 604 — func (*connection).noteDuplicateAck

```
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
```

## Line 660 — func (*connection).judgeProbeFromDuplicates

```
// judgeProbeFromDuplicates draws libutp's conclusion about an outstanding MTU
// probe once a run of duplicates has reached three. See noteDuplicateAck.
```

## Line 678 — func (*connection).judgeProbeFromDuplicates

```
// A packet ahead of the probe was lost. Nothing follows about size.
```

## Line 682 — func (*connection).noteSelectiveAckCount

```
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
```

## Line 707 — func (*connection).noteSelectiveAckCount

```
// libutp's cur_window_packets
```

## Line 711 — func (*connection).noteSelectiveAckCount

```
// libutp's seq_nr
```

## Line 747 — func (*connection).retransmitLostPackets

```
// No selective ack: see statePacket.
```

## Line 763 — func (*connection).retransmitLostPackets

```
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
```

## Line 778 — func (*connection).rearmRack

```
// rearmRack arms RACK's reordering timer for the next packet still inside
// its reordering window, or stops it when there is none.
```

## Line 788 — func (*connection).onRackTimer

```
// onRackTimer is RFC 8985's reordering timer (section 6.3): a packet that
// was inside its reordering window when the last acknowledgement came has
// passed it, with nothing since to say it arrived.
```

## Line 801 — func sentRack

```
// sentRack is the connection's RACK state, or a zero one before there is a
// send window.
```
