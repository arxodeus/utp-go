# congestion.go

Every comment in `congestion.go` at e56c9aa, verbatim.

## Line 13 — const defaultTargetMicros, defaultInitialTimeout, defaultMinTimeout, defaultM...

```
// defaultInitialTimeout matches libutp's initial rto and its connect
	// timer, both 3000ms (utp_internal.cpp:2609 and :2762).
```

## Line 16 — const defaultTargetMicros, defaultInitialTimeout, defaultMinTimeout, defaultM...

```
// defaultMinTimeout is the RTO floor. libutp computes
	// rto = max(rtt + rtt_var * 4, 1000) in milliseconds
	// (utp_internal.cpp:1380), so the floor is one second.
```

## Line 20 — const defaultTargetMicros, defaultInitialTimeout, defaultMinTimeout, defaultM...

```
// defaultMaxTimeout is no cap, as libutp has none: its timeout is
	// max(rtt + rtt_var * 4, 1000) with no upper bound (utp_internal.cpp:1380)
	// and doubles on each timeout without one (:1179), and the connection gives
	// up at the fifth (:1191). This was 60 seconds, which bound only the last
	// wait on a path whose timeout was past 3.75 seconds, where it gave up a
	// little sooner than libutp. ConnectionConfig.MaxTimeout sets one.
```

## Line 27 — const defaultTargetMicros, defaultInitialTimeout, defaultMinTimeout, defaultM...

```
// defaultMaxPacketSizeBytes is the largest datagram this library will
	// ever try -- the ceiling of the path-MTU search, not a size it sends
	// straight away.
	//
	// libutp derives its ceiling from the interface MTU (`get_udp_mtu`,
	// utp_internal.cpp:1316) and searches downward from there. With no
	// embedder callback its default for IPv4 is UDP_IPV4_MTU, 1402: a
	// 1500-byte Ethernet MTU less the IPv4 and UDP headers and an allowance
	// for GRE, PPPoE, MPPE and 36 bytes of "fudge" (utp_utils.cpp:211-228).
	// IPv6 peers get libutp's 1232 (path_mtu.go). The search starts here, as
	// libutp's does, and comes down when a probe of this size is lost. See
	// mtu.go; history: HISTORY.md, "defaultMaxPacketSizeBytes".
```

## Line 40 — const defaultTargetMicros, defaultInitialTimeout, defaultMinTimeout, defaultM...

```
// defaultMaxWindowSizeIncBytes is the cap on how far the congestion
	// window may grow in one RTT. libutp:
	// `#define MAX_CWND_INCREASE_BYTES_PER_RTT 3000` (utp_internal.cpp:43).
	// This was 1024, one packet per RTT, which on a 100 ms path meant about
	// ten kilobytes of window per second -- a 20 Mbps link needs 500 KB.
```

## Line 47 — const defaultTargetMicros, defaultInitialTimeout, defaultMinTimeout, defaultM...

```
// defaultDelayWindow is how far back the base delay reaches: libutp's
	// thirteen one-minute buckets (DELAY_BASE_HISTORY, utp_internal.cpp:50).
	// It was two minutes, a sliding minimum, chosen to bound clock drift
	// before this library had libutp's correction for it; see baseDelayHist.
```

## Line 52 — const defaultTargetMicros, defaultInitialTimeout, defaultMinTimeout, defaultM...

```
// ppRTTWindow is how far back LEDBAT++'s minimum round trip reaches. A
	// round trip is measured on one clock and cannot drift, so it keeps the
	// two minutes it was tuned with.
```

## Line 66 — type packetRecord

```
// SentWindow is the congestion window when the packet was last
	// transmitted: what its acknowledgement's share of a round trip's growth
	// is reckoned against. See ackBatch.credit.
```

## Line 73 — type packetRecord

```
// NeedResend is libutp's need_resend: the packet was given up as lost
	// and its bytes no longer count in flight. Resending it counts them
	// again (utp_internal.cpp:877-881); acknowledging it does not subtract
	// them a second time (:1390-1396).
```

## Line 78 — type packetRecord

```
// SentAt is when the packet was last transmitted. LEDBAT++ uses it to
	// tell which window a loss belongs to; see OnLostPacket.
```

## Line 97 — var errInsufficientWindowSize, errUnknownSeqNum, errDuplicateTransmission, er...

```
// errInternal ends a connection whose own bookkeeping has failed: a
	// bug here, not anything the peer did or the network lost.
```

## Line 112 — type ctrlConfig

```
// Clock is where the controller reads time: the delay window's expiry
	// and the age of the application-limited mark. Defaults to RealClock.
```

## Line 129 — func defaultCtrlConfig

```
// The window ceiling, libutp's opt_sndbuf, whose default is also
		// 1 MB (utp_api.cpp:91). This was absent, which was harmless while
		// nothing read the field and a zero ceiling the moment something
		// did.
```

## Line 143 — type controller

```
// OnTick lets a controller act on the passage of time when no acks are
	// arriving. Only LEDBAT++ needs it, for its slowdowns.
```

## Line 146 — type controller

```
// OnPeerDelay reports the one-way delay this end measured on a packet
	// arriving from the peer -- libutp's `their_delay`, in raw wrapping
	// microseconds. It is not a congestion signal for this sender; it is how
	// clock drift between the two ends is detected. See
	// defaultController.OnPeerDelay.
```

## Line 152 — type controller

```
// OnAckDelay reports the delay the peer measured on our packets, as
	// carried by one incoming acknowledgement, once per acknowledgement and
	// before OnAck for the packets it covers. libutp adds it to our_hist
	// once per incoming packet (utp_internal.cpp:2017-2024), not once per
	// packet acknowledged.
```

## Line 158 — type controller

```
// ApplyAck ends one incoming acknowledgement: after OnAck for every
	// packet it covers, and before any loss it reveals is reported. Classic
	// LEDBAT updates the window here, once, for all the bytes the
	// acknowledgement covered -- libutp's single apply_ccontrol call per
	// incoming packet (utp_internal.cpp:2139-2140), which comes before
	// selective_ack can decay the window (:2289).
```

## Line 167 — type controller

```
// BytesInFlight is libutp's cur_window: payload bytes sent and neither
	// acknowledged nor given up as lost.
```

## Line 170 — type controller

```
// CongestionWindow is libutp's max_window.
```

## Line 172 — type controller

```
// OnRTTSample feeds a round trip measured outside OnAck: the SYN's, on
	// the dialling side. libutp takes it through the same ack_packet as any
	// other (utp_internal.cpp:1362-1380).
```

## Line 176 — type controller

```
// MarkForResend gives a packet up as lost: its bytes stop counting in
	// flight until it is sent again. libutp does this to every packet in
	// flight on a retransmission timeout (utp_internal.cpp:1230-1237). A
	// packet already acknowledged or already marked is left alone.
```

## Line 181 — type controller

```
// Stats returns a snapshot of the controller's internal state.
	//
	// It exists so a test harness can plot the congestion window and RTT
	// estimate over time. A controller that can only be observed through its
	// effect on throughput cannot be told apart from one that is doing
	// nothing -- which is not hypothetical here; see KNOWN-LIMITATIONS.md.
```

## Line 190 — type controllerStats

```
// controllerStats is a snapshot of a congestion controller's state.
```

## Line 192 — type controllerStats

```
// WindowSizeBytes is the volume currently considered in flight.
```

## Line 194 — type controllerStats

```
// MaxWindowSizeBytes is the congestion window: what the controller
	// currently believes the path will carry.
```

## Line 197 — type controllerStats

```
// MinWindowSizeBytes is the floor the window will not drop below.
```

## Line 199 — type controllerStats

```
// RTT is the smoothed round-trip time estimate: libutp's, in whole
	// milliseconds, and so zero on a path faster than one.
```

## Line 202 — type controllerStats

```
// FineRTT is the same estimator run in microseconds, for what libutp does
	// not have and needs a round trip below a millisecond to be nonzero: the
	// loss probe's timeout.
```

## Line 206 — type controllerStats

```
// MinFineRTT is the lowest round-trip sample, in microseconds, from a
	// packet sent once. The loss probe reads it to tell an acknowledgement of
	// the packet it resent from one of the original (see probeAnswered).
```

## Line 210 — type controllerStats

```
// SpuriousLossesUndone counts cuts for lost packets undone because the
	// packet proved only late. See defaultController.undoSpuriousLoss.
```

## Line 213 — type controllerStats

```
// RTTVarianceMicros is the RTT variance estimate, in microseconds.
```

## Line 215 — type controllerStats

```
// Timeout is the current retransmission timeout.
```

## Line 217 — type controllerStats

```
// BaseDelay is the lowest one-way delay observed in the delay window --
	// LEDBAT's estimate of the path with no queue. Queueing delay is the
	// difference between CurrentDelay and this.
	//
	// The series both are drawn from is the peer's measurement of *our*
	// outgoing path: the timestamp_difference field it puts on every packet,
	// which is libutp's `our_hist` (utp_internal.cpp:2016-2021). That is the
	// direction LEDBAT controls, because it is the direction this sender's
	// packets travel.
```

## Line 227 — type controllerStats

```
// ClockSkewCorrection is how much has been added to the base delay to
	// cancel clock drift between the two ends. Zero on a pair of clocks
	// running at the same rate, which is every pair that has not been
	// deliberately skewed.
	//
	// Exposed because a correction that cannot be observed cannot be tested,
	// and this repository has already learned that the expensive way.
```

## Line 235 — type controllerStats

```
// ClockDrift and ClockDriftPenalty are libutp's clock_drift and the delay
	// penalty it earns. See ConnectionMetrics for what they mean.
```

## Line 239 — type controllerStats

```
// CurrentDelay is the most recent sample of that same series.
	//
	// It is here because there was no way to compute a queueing delay
	// without it. ConnectionMetrics.QueueingDelay used to subtract BaseDelay
	// from the *other* direction's measurement, which is not a queue in
	// either direction. See the note there.
```

## Line 246 — type controllerStats

```
// FilteredQueueingDelay is the queue our packets are meeting on the way
	// to the peer: the least of the last three samples, each over the base
	// as it stood (libutp's our_hist.get_value(), utp_internal.cpp:383-391).
	// For a connection that only receives, these are its acknowledgements.
```

## Line 251 — type controllerStats

```
// TargetDelayMicros is the standing queue the controller aims for.
```

## Line 253 — type controllerStats

```
// SlowStart reports whether the controller is still in slow start.
	//
	// It is here because two very different behaviours are indistinguishable
	// without it. libutp's application-limited guard zeroes the LEDBAT gain
	// (utp_internal.cpp:1681-1686), but in slow start the window is
	// `max(ss_cwnd, ledbat_cwnd)` (:1699) and `ss_cwnd` is not gated -- so a
	// window growing while the application sends nothing is correct in slow
	// start and a defect after it. A test that cannot tell which phase it is
	// in cannot measure the guard at all; see
	// netem.TestApplicationLimitedWindowDoesNotGrow.
```

## Line 264 — type controllerStats

```
// AppLimitedSince is how long it has been since the sender last had data
	// to send and no window to send it in -- the age of libutp's
	// `last_maxed_out_window` (:945, :957, read at :1681). Growth is
	// suppressed once this exceeds one second. Zero means the window has
	// never been filled.
```

## Line 285 — type defaultController

```
// freeRecords are records forgotten and ready for reuse. See
	// forgetTransmission.
```

## Line 288 — type defaultController

```
// undo is what the last cut for a lost packet changed, while it can
	// still be undone. See undoSpuriousLoss.
```

## Line 291 — type defaultController

```
// spuriousLossesUndone counts undoSpuriousLoss's undos.
```

## Line 293 — type defaultController

```
// see controllerStats.FineRTT
```

## Line 294 — type defaultController

```
// see controllerStats.MinFineRTT
```

## Line 296 — type defaultController

```
// curDelayHist is libutp's cur_delay_hist: the last curDelaySize
	// queueing-delay samples, each taken against the base as it stood when
	// it arrived, zero until filled (DelayHist, utp_internal.cpp:247-248,
	// :269-271, :364-365). Classic LEDBAT's delay is their minimum.
```

## Line 302 — type defaultController

```
// ackBatch is what the acknowledgement being processed has covered so
	// far, for ApplyAck. Classic LEDBAT only.
```

## Line 305 — type defaultController

```
// lastWindowDecay is when the congestion window was last halved for
	// loss. libutp's `last_rwin_decay` (utp_internal.cpp:461).
```

## Line 308 — type defaultController

```
// slowStart and ssthreshBytes are the slow-start phase and its exit
	// threshold. libutp starts every connection in slow start with
	// `ssthresh = opt_sndbuf` (utp_internal.cpp:2620-2621) and grows the
	// window by a packet per acked packet until either the threshold is
	// crossed or the delay approaches the target (:1691-1702).
	//
	// This fork had no slow start at all: every connection began at two
	// packets and crept up by the LEDBAT increment alone.
```

## Line 318 — type defaultController

```
// maxWindowUpperBytes is the ceiling on the congestion window --
	// libutp's `opt_sndbuf`, which it clamps against at
	// utp_internal.cpp:1710.
```

## Line 322 — type defaultController

```
// lastMaxedOutWindow is when the sender last had data to send and no
	// window to send it in. libutp's `last_maxed_out_window`, set by
	// `is_full` (utp_internal.cpp:945, :957) and read at :1681: a sender
	// that has not filled its window in the last second is limited by the
	// application rather than the path, and growing the window further would
	// be measuring nothing.
```

## Line 330 — type defaultController

```
// peerDelayHist is the base delay of packets arriving *from* the peer,
	// libutp's `their_hist` (utp_internal.cpp:507). It is not a congestion
	// signal for this sender -- it describes the other direction -- and its
	// only use is detecting clock drift. See OnPeerDelay.
```

## Line 336 — type defaultController

```
// drift estimates the long-run slope of the delay the peer reports for
	// our packets: the second of libutp's two clock-drift mechanisms. See
	// driftEstimator, and applyCongestionControl for what it is used for.
```

## Line 341 — type defaultController

```
// clk is where this controller reads time. Never nil after
	// newDefaultController; read through now().
```

## Line 345 — type defaultController

```
// currentDelay is the most recent delay sample pushed into delayAcc --
	// the newest value of the series BaseDelay is the minimum of. Reported,
	// not acted on: the control path already has the sample in hand when it
	// needs it.
```

## Line 351 — type defaultController

```
// algorithm selects the congestion controller. Everything above is
	// shared; the LEDBAT++ state below is used only when it is selected.
```

## Line 354 — type defaultController

```
// minRTT is LEDBAT++'s base round trip -- the lowest seen within the
	// delay window -- which its gain is computed from
	// (draft-irtf-iccrg-ledbat-plus-plus-01 §4.2) and its queueing delay
	// measured against (§4.5).
```

## Line 359 — type defaultController

```
// ppRTTBase holds the round-trip samples minRTT is the minimum of.
```

## Line 361 — type defaultController

```
// ppRTTRecent is the last ledbatPPRTTFilter round-trip samples, and
	// ppRTTCount how many of them are filled. §4.5: "filter the round trip
	// measurements by using the minimum of the 4 most recent delay samples".
```

## Line 367 — type defaultController

```
// ppPhase and the four fields after it drive the periodic slowdowns that
	// keep the base-delay estimate honest (§4.4).
```

## Line 374 — type defaultController

```
// ppWindowFraction is the part of a byte LEDBAT++'s last update in
	// congestion avoidance added to the window and could not store in it.
```

## Line 397 — func newDefaultController

```
// libutp starts rtt_var at 800 (utp_internal.cpp:2610). Its rtt and
		// rtt_var are milliseconds -- the RTT sample is computed as
		// microseconds/1000 at utp_internal.cpp:1364 -- so that is 800ms.
		// It is superseded by the first RTT sample below, and exists so a
		// timeout computed before any ack is conservative rather than zero.
```

## Line 408 — func newDefaultController

```
// libutp starts the first averaging slot five seconds after the
		// socket is created, not after the first sample arrives
		// (utp_internal.cpp:2553).
```

## Line 412 — func newDefaultController

```
// libutp starts every connection in slow start with
		// `ssthresh = opt_sndbuf` (utp_internal.cpp:2620-2621), and its
		// default opt_sndbuf is 1 MB (utp_api.cpp:91) -- the same value as
		// DefaultWindowSize here.
```

## Line 421 — func newDefaultController

```
// A ceiling below the floor pins the window shut on the first ack, which
	// is what an unset WindowSize used to produce.
```

## Line 428 — func newDefaultController

```
// A caller asking for a window smaller than two packets gets two
		// packets: below that nothing can be sent at all. Their intent to be
		// small is respected rather than replaced with the default.
```

## Line 437 — func newDefaultController

```
// draft §4.5: LEDBAT++ targets 60ms of queueing delay where RFC 6817
		// and libutp use 100ms.
```

## Line 440 — func newDefaultController

```
// draft §4.1: "LEDBAT++ sender limits the initial window to 2
		// packets" -- which is already minWindowSizeBytes here.
```

## Line 448 — func (*defaultController).Stats

```
// Stats returns a snapshot of this controller's state.
```

## Line 499 — func (*defaultController).setWindowCeiling

```
// setWindowCeiling moves the ceiling on the congestion window of a live
// connection: libutp's utp_setsockopt(UTP_SNDBUF), which sets the socket's
// opt_sndbuf (utp_internal.cpp:2699-2702). libutp sends no more than it from
// then on -- is_full takes the least of it and the window (:936) -- and
// clamps the window to it at the next acknowledgement (:1710); clamping now
// sends the same. The slow-start threshold is left where it is, as libutp
// leaves ssthresh. Zero restores the default, and a ceiling below the floor
// is raised to it, as the constructor does.
```

## Line 539 — var lossUndoEnabled

```
// lossUndoEnabled switches undoing a cut whose loss proved spurious. It is
// not libutp's: see DEVIATIONS.md, "A cut for a loss that was only
// reordering is undone".
```

## Line 548 — type lossUndo

```
// lossUndo is the window a cut for a lost packet replaced, kept until that
// packet's fate is known.
```

## Line 559 — func (*defaultController).lossCause

```
// lossCause reports the packet whose loss made the last cut, while that cut
// can still be undone.
```

## Line 567 — func (*defaultController).undoSpuriousLoss

```
// undoSpuriousLoss puts back the window that a cut for seq replaced: the
// packet was not lost after all, only late, and the original arrived. It
// reports whether there was such a cut to undo.
//
// RFC 4015's response to a spurious retransmission (RFC 3522 detects it),
// restoring the window and the threshold to what they were. libutp has no
// undo: a packet displaced by reordering halves its window as a loss would.
// On a link that reorders, every displaced packet that the selective acks
// overtake is taken for lost, and each such halving is undone here once the
// acknowledgement shows the original arrived first. Slow start is not
// resumed, and a newer cut, another loss, or a timeout in between leaves
// the cut standing.
```

## Line 595 — func (*defaultController).forgetTransmission

```
// forgetTransmission drops the record of an acknowledged packet that the
// sender no longer holds, keeping it for the next packet sent, so the
// records are the window, as libutp's are: its records are its outgoing
// packets, freed when acknowledged (the free(pkt) in ack_packet,
// utp_internal.cpp:1397).
//
// A record still unacknowledged is kept: the sender drops only packets it has
// seen acknowledged, so this cannot happen from it, and a caller driving the
// controller directly keeps what it has not finished with. History:
// HISTORY.md, "forgetTransmission".
```

## Line 622 — func (*defaultController).OnTransmit

```
// A record already acknowledged is from 65,536 packets ago: sequence
		// numbers wrap, and the new packet takes its place. Only one still
		// outstanding is a real duplicate. (This map is never otherwise
		// pruned, so it holds at most one record per sequence number.)
```

## Line 656 — func (*defaultController).OnTransmit

```
// A packet given up as lost counts in flight again once it is
		// resent: `if (pkt->transmissions == 0 || pkt->need_resend)
		// cur_window += pkt->payload` (utp_internal.cpp:877-881). Not
		// window-checked, as libutp's send_packet is not: the caller has
		// already decided it may go.
```

## Line 676 — type ackBatch

```
// ackBatch is libutp's acked_bytes and min_rtt for one incoming packet
// (utp_internal.cpp:1956-1987, :1403-1436).
```

## Line 679 — type ackBatch

```
// credit is each acknowledged packet's bytes over the window it was
	// sent in, summed: the share of a round trip's growth this
	// acknowledgement is owed. See applyCongestionControl.
```

## Line 690 — const ackBatchRTTFallback

```
// ackBatchRTTFallback is what libutp takes as a packet's round trip when the
// clock has not moved since it was sent: `min_rtt = min<int64>(min_rtt,
// 50000)` (utp_internal.cpp:1982-1985, :1428-1431).
```

## Line 695 — const curDelaySize

```
// curDelaySize is libutp's CUR_DELAY_SIZE (utp_internal.cpp:44).
```

## Line 698 — func (*defaultController).OnAckDelay

```
// OnAckDelay is one incoming acknowledgement's delay sample. See the
// controller interface.
//
// A zero is no measurement -- the peer has not timed one of our packets yet,
// or reported the MaxInt32 sentinel -- and libutp neither records it nor
// runs its congestion control on that acknowledgement (`if (actual_delay !=
// 0)`, utp_internal.cpp:2023, :2139).
```

## Line 712 — func (*defaultController).OnAckDelay

```
// The same sample drives the clock-drift estimate (utp_internal.cpp:
	// 2025-2107), on the same condition.
```

## Line 723 — func (*defaultController).filteredDelayMicros

```
// filteredDelayMicros is libutp's our_hist.get_value(): the least of the last
// curDelaySize queueing-delay samples (utp_internal.cpp:383-391), which is
// what apply_ccontrol works from (:1621). One late packet does not move it;
// three do.
```

## Line 737 — func (*defaultController).QueueingDelay

```
// QueueingDelay is Stats().FilteredQueueingDelay without the rest of the
// snapshot, for the receiver's acknowledgement rule, which reads it for every
// data packet.
```

## Line 746 — func (*defaultController).ApplyAck

```
// ApplyAck is the once-per-acknowledgement end of classic LEDBAT. See the
// controller interface.
```

## Line 754 — func (*defaultController).ApplyAck

```
// libutp's min_rtt stays INT64_MAX, so neither step below runs.
```

## Line 758 — func (*defaultController).ApplyAck

```
// "if the delay estimate exceeds the RTT, adjust the base_delay to
	// compensate" (utp_internal.cpp:2127-2133). A one-way delay longer than
	// the round trip is a clock that has moved, and raising the base takes
	// it back out of the samples that follow. The value the window update
	// below sees is not affected: libutp's shift moves delay_base, not
	// cur_delay_hist (:277-289).
```

## Line 767 — func (*defaultController).ApplyAck

```
// "if we don't have a delay measurement, there's no point in invoking
	// the congestion control" (:2136-2140).
```

## Line 778 — var sendWindowCredit

```
// sendWindowCredit switches the window factor between libutp's, reckoned
// against the window as it stands, and the one reckoned against the window
// each packet was sent in. See applyCongestionControl. A switch so the two
// can be measured against each other.
```

## Line 802 — func (*defaultController).OnAck

```
// The delay sample itself was recorded once for the whole
	// acknowledgement, by OnAckDelay.
```

## Line 807 — func (*defaultController).OnAck

```
// §4.5: LEDBAT++ measures its queueing delay from round trips, not
		// from the one-way delay the peer reports. See ledbatPPRTTDelay.
```

## Line 816 — func (*defaultController).OnAck

```
// Counted towards the acknowledgement's single update, in ApplyAck.
```

## Line 833 — func (*defaultController).OnAck

```
// "if need_resend is set, this packet has already been considered
	// timed-out, and is not included in the cur_window anymore"
	// (utp_internal.cpp:1390-1396).
```

## Line 840 — func (*defaultController).OnAck

```
// Only unretransmitted packets update the RTT estimate: an ack for a
	// packet sent more than once cannot be attributed to a particular
	// transmission. libutp applies the same rule
	// (`if (pk->transmissions == 1)`, utp_internal.cpp:1362).
```

## Line 857 — func (*defaultController).updateRTT

```
// updateRTT is libutp's RTT estimator and timeout (utp_internal.cpp:1362-1380),
// for one sample in microseconds. Called with the lock held.
//
// The arithmetic is libutp's, in whole milliseconds: the sample is truncated
// to them (`ertt = (now - time_sent) / 1000`, :1364), rtt and rtt_var are
// unsigned millisecond counts, and each division truncates. Kept in
// microseconds, as it used to be, the estimate differed from libutp's by the
// fractions it drops -- a few milliseconds of RTO once the round trip is
// large enough to lift it off its 1000ms floor.
```

## Line 867 — func (*defaultController).updateRTT

```
// The microsecond estimate, for the loss probe; see FineRTT.
```

## Line 885 — func (*defaultController).updateRTT

```
// First sample: adopt it outright rather than easing an average
		// up from zero, which would take about twenty samples to
		// converge and leave the RTO wrong for all of them
		// (utp_internal.cpp:1364-1367). A sub-millisecond sample is 0,
		// so the next one counts as the first again, as in libutp.
```

## Line 892 — func (*defaultController).updateRTT

```
// utp_internal.cpp:1370-1372. The variance step is signed and
		// truncates toward zero, as Go's division does.
```

## Line 905 — const maxWindowDecayInterval

```
// maxWindowDecayInterval is the shortest gap between two halvings of the
// congestion window.
//
// libutp: `MAX_WINDOW_DECAY 100 // ms` (utp_internal.cpp:51), enforced by
// `can_decay_win` (:602-605) and applied once per ack that resent anything,
// not once per packet resent (:1609-1610).
```

## Line 913 — func (*defaultController).decayOnLoss

```
// decayOnLoss halves the window for a loss, libutp's decay (utp_internal.cpp:
// 606-617). Called with the lock held.
```

## Line 918 — func (*defaultController).decayOnLoss

```
// libutp leaves slow start on any decay and sets the threshold to
	// where the window ended up (utp_internal.cpp:616-617). LEDBAT++
	// leaves its own slow start on the same signal: a loss is congestion
	// however the delay looked.
```

## Line 930 — func (*defaultController).onLedbatPPLoss

```
// onLedbatPPLoss is LEDBAT++'s response to a lost packet. Called with the
// lock held; decay is whether the 100ms rate limit classic LEDBAT applies
// would allow a halving now.
//
// Two rules beyond the rate limit, both about which window a loss belongs
// to:
//
//   - A packet sent before the window was last cut belongs to the congestion
//     event that cut it, and is not charged again. That is TCP NewReno's
//     rule (RFC 6582's "recover"); the rate limit alone lets one overflow
//     halve the window twice when its losses are detected more than 100ms
//     apart.
//   - A packet sent before a slowdown was sent from the window the slowdown
//     saved, not from the two packets of the freeze or the partial window of
//     the ramp. Its loss halves the saved window, which is where the ramp is
//     going; the window the ramp has reached is left alone. RFC 5681 halves
//     the data in flight, and the data in flight is from the saved window.
//
// The draft does not cover a loss during a slowdown. History: HISTORY.md,
// "onLedbatPPLoss"; KNOWN-LIMITATIONS.md, "LEDBAT++ split a link unevenly
// between two of its own flows".
```

## Line 960 — func (*defaultController).onLedbatPPLoss

```
// During the freeze everything lost was sent from the saved window
		// or from the two packets the freeze allows, and charging either to
		// those two packets gives the same wrong answer.
```

## Line 983 — func (*defaultController).OnLostPacket

```
// Halve the window at most once per maxWindowDecayInterval.
	//
	// This was halving once for every packet declared lost. A burst of four
	// losses -- one queue overflow -- took the window to a sixteenth in a
	// single event, and with the window already at its floor the connection
	// then crawled. libutp decays once per ack that resent anything, and not
	// again for 100 ms however many acks arrive in between.
```

## Line 991 — func (*defaultController).OnLostPacket

```
// Another loss while one is still undoable: the cut stands for both,
	// and one turning out spurious does not excuse the other.
```

## Line 1019 — func (*defaultController).OnTimeout

```
// OnTimeout is libutp's timeout branch (utp_internal.cpp:1206-1228).
//
// hasPacketsInFlight distinguishes the two cases libutp treats differently.
// This fork collapsed the window to its floor in both, so an application that
// paused long enough to hit an RTO -- routine -- restarted from two packets,
// and with no slow start took hundreds of round trips to recover on a
// high-bandwidth path.
```

## Line 1029 — func (*defaultController).OnTimeout

```
// A timeout's cut is not undone, and the loss cut before it is no longer
	// the window's last word.
```

## Line 1035 — func (*defaultController).OnTimeout

```
// "we don't have any packets in-flight, even though we could. This
		// implies that the connection is just idling. No need to be
		// aggressive about resetting the congestion window. Just let it decay
		// by a 3:rd." (utp_internal.cpp:1216-1222)
```

## Line 1041 — func (*defaultController).OnTimeout

```
// "our delay was so high that our congestion window was shrunk below
		// one packet ... reset the congestion window to fit one packet, to
		// start over again" (:1223-1228). libutp re-enters slow start here.
```

## Line 1047 — func (*defaultController).OnTimeout

```
// Back to the start of the cycle: ramp up again, and let the
			// slowdown schedule be set when that ramp ends.
```

## Line 1059 — func (*defaultController).applyCongestionControl

```
// applyMaxWindowSizeAdjustment adjusts the maximum window size based on the given adjustment.
// applyCongestionControl is libutp's apply_ccontrol (utp_internal.cpp:1615-1712),
// run once per acked packet rather than once per ack packet. The two are
// equivalent: the window factor scales each step by that packet's share of
// the window, so a full window of acks sums to one RTT's worth of increase
// either way.
//
// It is called with the controller's lock held.
```

## Line 1075 — func (*defaultController).applyCongestionControl

```
// The queueing delay this ack reports: how far above the lowest delay
	// seen on this path the packet ran.
```

## Line 1082 — func (*defaultController).applyCongestionControl

```
// "the delay can never be greater than the rtt" (utp_internal.cpp:1617-1621):
	// libutp clamps our_delay to the minimum RTT of the packets this ack
	// covers, and so does ApplyAck. Without the clamp a peer that reports a
	// wild timestamp -- by malice, by a clock step, or by a timestamp wrap --
	// drives off_target arbitrarily negative and collapses the window in one
	// ack.
```

## Line 1092 — func (*defaultController).applyCongestionControl

```
// A clock drifting fast enough to be dishonest gets a penalty added to
	// the delay it measures.
	//
	// libutp (utp_internal.cpp:1646-1650), applied after the RTT clamp above
	// and deliberately so: the penalty is allowed to push the delay past the
	// round trip, because its purpose is to make the flow yield rather than
	// to describe the path.
	//
	// This is the second of libutp's two clock-drift mechanisms and does a
	// different job from the first. baseDelayHist's shift corrects the
	// measurement for ordinary drift between honest clocks, bounded and aged
	// out. This one does not correct anything: past 40,000 ppm, which is
	// three orders of magnitude beyond crystal drift, it inflates the delay
	// so that a peer running its clock slow gains nothing by it.
```

## Line 1112 — func (*defaultController).applyCongestionControl

```
// utp_internal.cpp:1635-1636.
```

## Line 1119 — func (*defaultController).applyCongestionControl

```
// libutp: min(bytes_acked, max_window) / max(max_window, bytes_acked)
	// (utp_internal.cpp:1668). Two things this fork had wrong. It divided by
	// the bytes currently *in flight* rather than by the congestion window,
	// which is a smaller denominator and so a larger factor -- with one
	// packet outstanding the factor was 1, and a single ack claimed a whole
	// RTT's worth of increase. And it had no min/max, so the factor could
	// exceed 1 outright.
```

## Line 1130 — func (*defaultController).applyCongestionControl

```
// Deviation: each acknowledged packet counts against the window it was
	// sent in, not the window as it stands now, when the caller knows it
	// (credit > 0). libutp's factor is meant to share one round trip's
	// growth among that round trip's acknowledgements -- a full window of
	// them adding MAX_CWND_INCREASE_BYTES_PER_RTT -- but each update grows
	// the window the next one divides by, so the more acknowledgements a
	// round trip is split into, the less it grows: by about G^2/2W.
	//
	// The larger of the two, though, not the send-window figure alone: after
	// a loss halves the window, libutp divides the next round trip's
	// acknowledgements by the new one, and that round trip grows about twice
	// as fast. Taking the larger, the window never grows slower than
	// libutp's would; above target it shrinks no slower either, the factor
	// scaling the decrease the same way. DEVIATIONS.md, "The window grows by
	// a round trip's worth per round trip"; history: HISTORY.md, "ApplyAck:
	// the window at sending".
```

## Line 1152 — func (*defaultController).applyCongestionControl

```
// A sender that has not filled its window in the last second is limited
	// by the application, not by the path, so the delay it measures says
	// nothing about how much more the path would carry. libutp refuses to
	// grow the window in that case (utp_internal.cpp:1681-1686). Without
	// this, an idle-but-trickling connection grows its window without bound
	// and then dumps it all at once when the application speeds up.
	//
```

## Line 1169 — func (*defaultController).applyCongestionControl

```
// utp_internal.cpp:1691-1702.
```

## Line 1175 — func (*defaultController).applyCongestionControl

```
// "even if we're a little under the target delay, we
			// conservatively discontinue the slow start phase".
```

## Line 1186 — func (*defaultController).applyCongestionControl

```
// utp_internal.cpp:1710.
```

## Line 1190 — func (*defaultController).applicationLimited

```
// applicationLimited reports whether the sender has gone more than a second
// without filling its window: libutp's `current_ms - last_maxed_out_window >
// 1000` (utp_internal.cpp:1681).
//
// A sender that has never filled its window counts as application-limited, as
// it does in libutp. libutp initialises last_maxed_out_window to 0 (:2603) and
// reads current_ms from a clock that counts from boot, not from the process or
// the connection (CLOCK_MONOTONIC on POSIX, utp_utils.cpp:158-175), so unless
// the host booted within the last second the difference is already past 1000
// and the gain is zeroed until the window first fills. A bulk sender is
// unaffected, because its first write fills the window and records the time
// before any acknowledgement arrives; a sender that never fills its window
// grows only by the slow-start step. TestConformanceLedbatRules holds it to
// libutp's trace. History: HISTORY.md, "applicationLimited".
```

## Line 1208 — func (*defaultController).OnWindowFull

```
// OnWindowFull records that the sender had data to send and no window to send
// it in. libutp's `is_full` (utp_internal.cpp:945, :957).
```

## Line 1229 — func (*defaultController).applyTimeoutAdjustment

```
// applyTimeoutAdjustment recomputes the retransmission timeout from the RTT
// estimate, as libutp does: rto = max(rtt + rtt_var * 4, min), capped at max.
//
// rttVarianceMicros is a microsecond count, so it has to be scaled before
// being added to a Duration. It was previously passed to time.Duration
// directly, which reads it as nanoseconds -- understating the variance term
// by a factor of 1000 and leaving the timeout pinned to minTimeout.
```

## Line 1244 — func wrappingLessThanUint32

```
// wrappingLessThanUint32 is libutp's wrapping_compare_less over the 32-bit
// microsecond timestamp space:
//
//	bool wrapping_compare_less(uint32 l, uint32 r, uint32 mask) {
//	    uint32 dist_down = (l - r) & mask;
//	    uint32 dist_up = (r - l) & mask;
//	    return dist_up < dist_down;
//	}
//	                                        (utp_utils.h)
```

## Line 1257 — const peerDelayBuckets

```
// peerDelayBuckets is how many buckets the peer-direction base delay is kept
// in. libutp uses DELAY_BASE_HISTORY = 13 one-minute buckets
// (utp_internal.cpp:50, rotated at :367-380); the count is kept and the
// bucket length is the configured window divided by it.
```

## Line 1263 — type peerDelayHist

```
// peerDelayHist tracks the base delay of packets arriving *from* the peer:
// libutp's `their_hist`. Its only purpose is noticing that base fall, which
// is how clock drift becomes visible.
//
// It is deliberately not a delayAccumulator: these samples must stay in
// wrapping 32-bit microsecond arithmetic. A clock drifting slow makes the
// measured inbound delay shrink, and keep shrinking, so on an ordinary path
// it passes zero within minutes and the subtraction wraps. Taken as a
// Duration, the wrapped value would be capped as an unusable reading and the
// base would stop moving just when the drift has grown large enough to
// matter. libutp's `their_delay` is a raw wrapping uint32 throughout and
// every comparison in DelayHist goes through wrapping_compare_less. This does
// the same. History: HISTORY.md, "peerDelayHist".
```

## Line 1293 — func (*peerDelayHist).addSample

```
// addSample records one inbound delay and returns the base before and after,
// so a caller can see how far it fell. Mirrors DelayHist.add_sample
// (utp_internal.cpp:291-381), with the bucket rotation driven by the
// configured window rather than a hard-coded minute.
```

## Line 1305 — func (*peerDelayHist).addSample

```
// No previous base to have fallen from. libutp's `prev_delay_base != 0`.
```

## Line 1331 — type baseDelayHist

```
// baseDelayHist is libutp's DelayHist for this sender's own delay samples,
// our_hist (utp_internal.cpp:260-381): the base delay is the least of
// thirteen buckets, the newest taking each sample that is lower than it,
// rotated onto a fresh bucket when a sample arrives a bucket's length after
// the last rotation. With the default window a bucket is a minute, so the base
// remembers twelve to thirteen minutes, as libutp's does, and an idle
// connection's history does not age, since only a sample rotates it.
// Thirteen buckets are 104 bytes, however many samples arrive.
//
// Clock drift inflates a base kept this long by the drift rate times the
// window; OnPeerDelay corrects for it, as libutp does
// (netem.TestClockSkewCorrectionCancelsThePhantomQueue). History:
// HISTORY.md, "baseDelayHist".
```

## Line 1351 — type baseDelayHist

```
// skew is the total of every shift, for controllerStats. The shifts
	// themselves live in the buckets and age out with them.
```

## Line 1356 — const delayBaseHistory

```
// delayBaseHistory is libutp's DELAY_BASE_HISTORY (utp_internal.cpp:50).
```

## Line 1359 — func newBaseDelayHist

```
// newBaseDelayHist starts the history at now, as libutp's DelayHist::clear
// does when the socket is made (utp_internal.cpp:262-271), so the first
// rotation is a bucket's length after the connection began.
```

## Line 1373 — func (*baseDelayHist).Push

```
// Push records a sample taken at now: DelayHist::add_sample
// (utp_internal.cpp:291-381).
```

## Line 1402 — func (*baseDelayHist).BaseDelay

```
// BaseDelay is the least delay in the history, never below zero.
```

## Line 1410 — func (*baseDelayHist).shift

```
// shift raises the base delay, cancelling drift that has inflated every
// sample in it: libutp's DelayHist::shift, which adds the offset to every
// bucket and to the base (utp_internal.cpp:277-289). Each bucket is replaced
// by a fresh, unshifted sample when the history rotates onto it, so a
// correction lasts only as long as the history does -- which is what keeps
// shifts that arrive for as long as the drift lasts from adding up without
// bound. (An unbounded correction was measured once: a drifted flow saw 4 ms
// of queue against its neighbour's 40 ms and took 56.6% of a shared link.)
```

## Line 1429 — const maxSkewAdjustment

```
// maxSkewAdjustment bounds a single clock-drift correction.
//
// libutp: "never adjust more than 10 milliseconds" (utp_internal.cpp:2011).
// A larger apparent fall in the peer's base delay is far more likely to be
// the reverse path genuinely getting faster -- a route change, or a queue
// draining -- than a clock jumping, and treating that as skew would blind
// this sender's own delay signal by however much it moved.
```

## Line 1438 — func (*defaultController).OnPeerDelay

```
// OnPeerDelay reports the one-way delay measured on a packet arriving from
// the peer, and corrects this sender's delay signal for clock drift.
//
// libutp:
//
//	uint32 prev_delay_base = conn->their_hist.delay_base;
//	if (their_delay != 0) conn->their_hist.add_sample(their_delay, conn->ctx->current_ms);
//
//	// if their new delay base is less than their previous one
//	// we should shift our delay base in the other direction in order
//	// to take the clock skew into account
//	if (prev_delay_base != 0 &&
//	    wrapping_compare_less(conn->their_hist.delay_base, prev_delay_base, TIMESTAMP_MASK)) {
//	    // never adjust more than 10 milliseconds
//	    if (prev_delay_base - conn->their_hist.delay_base <= 10000)
//	        conn->our_hist.shift(prev_delay_base - conn->their_hist.delay_base);
//	}
//	                                        (utp_internal.cpp:2002-2014)
//
// # Why a fall in the other direction means drift in this one
//
// A one-way delay is a difference between two clocks, so it carries their
// relative rate error. If this end's clock runs slow, the time it stamps into
// its own packets falls further behind, and the peer's measurement of this
// sender's path -- which is what LEDBAT here runs on -- grows without bound.
// The same slow clock makes packets *from* the peer appear to arrive sooner,
// so the delay measured in that direction shrinks by exactly as much.
//
// So a falling base delay in the peer's direction is the visible half of a
// bias that is inflating the invisible half. Raising this sender's base by
// the same amount cancels it. What it is worth:
// netem.TestClockDriftYieldsShareAtASharedBottleneck, and HISTORY.md,
// "OnPeerDelay".
```

## Line 1472 — func (*defaultController).OnPeerDelay

```
// libutp: `if (their_delay != 0)`. A zero means the peer has not stamped
	// a usable timestamp, not that the path is instant.
```

## Line 1484 — func (*defaultController).OnPeerDelay

```
// Only a *fall* is evidence of drift. A rise is a queue building in the
	// other direction, which says nothing about either clock.
```

## Line 1495 — func (*defaultController).now

```
// now is this controller's current time, nil-safe for controllers built
// directly in unit tests.
```

## Line 1504 — func absInt64

```
// absInt64 returns the absolute value of x.
```

## Line 1512

```
// Additional methods for handling acknowledgments, lost packets, and timeouts would follow...
```

## Line 1522 — type delayAccumulator

```
// clk decides when a sample has aged out of the window. Nil means the
	// real clock; read it through now().
```

## Line 1542 — func (*delayAccumulator).now

```
// now is nil-safe, for the accumulators built directly in unit tests.
```

## Line 1570 — type delayWindow

```
// delayWindow is the minimum of the samples within the window: a sliding
// window minimum. Samples are kept in the order they arrived, and a new one
// first drops every sample before it that is no smaller -- that one leaves the
// window first and can never be the least while the new one is in it -- so
// the front is always the minimum, and the oldest, and expires first.
//
// It was a heap of every sample, popped only when its least had expired:
// every acknowledgement of the last two minutes held, 32 bytes each, which at
// a few thousand acknowledgements a second is megabytes per connection, and a
// boxed allocation for each push through container/heap. With samples
// arriving in time order, the minimum it gives is the same.
```

## Line 1589 — func (*delayWindow).push

```
// push adds a sample. Callers pass the time an acknowledgement arrived, which
// only moves forward; a deadline earlier than the last is taken as the last,
// so the front stays the first to expire whatever the caller does.
```

## Line 1602 — func (*delayWindow).push

```
// Reclaim what has expired off the front before growing.
```

## Line 1616 — func capTimeout

```
// capTimeout bounds a timeout by max, where max is positive; zero or less is
// no bound, the default.
```
