# conformance_recovery_test.go

Every comment in `conformance_recovery_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build cgo
```

## Line 16

```
// Loss recovery, compared against libutp on a virtual clock.
//
// COMPATIBILITY.md rated four of these "cited": read against utp_internal.cpp
// and matched by hand, which is how the selective-ack bit reversal survived.
// These drive both implementations through the same situations and compare
// what each does -- when it resends, and which packets.
//
// The corpus compares transcripts field by field, and cannot here: libutp's
// window opens at one packet and ours at two (DEVIATIONS.md), so after the
// first round trip the two are sending different numbers of packets, and
// every packet after that has a different sequence number on each side.
// These cases drive each side separately, in lockstep on the clock, and
// compare the decision, not the bytes: how long until a resend, and which
// packet it was relative to that side's own oldest outstanding one.
```

## Line 31 — type recoveryRun

```
// recoveryRun is both implementations after the handshake, driven separately.
```

## Line 35 — type recoveryRun

```
// sent is every data sequence number each side has put on the wire,
	// first transmissions only.
```

## Line 41 — func newRecoveryRun

```
// newRecoveryRun completes the handshake, the SYN-ACK arriving synDelay
// after the SYN.
```

## Line 48 — func newRecoveryRunMTU

```
// newRecoveryRunMTU is newRecoveryRun with libutp's reported MTU set; see
// newInitiatorRunMTU.
```

## Line 66 — type sentData

```
// sentPacket is one data packet a side emitted.
```

## Line 72 — func (*recoveryRun).data

```
// data decodes emissions and returns the data packets, marking each as a
// first transmission or a resend.
```

## Line 107 — func (*recoveryRun).advance

```
// advance moves both clocks by the same amount, giving libutp its timeout
// pass as an embedder would.
```

## Line 128 — func (*recoveryRun).writeBoth

```
// writeBoth hands the same bytes to both. Ours is written from a goroutine:
// a large write blocks until it is buffered, and the writer is not a clock
// participant.
```

## Line 141 — func (*recoveryRun).writeBoth

```
// Let the write reach the connection before anything else happens.
```

## Line 146 — func ackFor

```
// ackFor is the peer acknowledging everything up to and including seq, with
// an optional selective ack. The receive timestamp difference is small and
// constant, so neither side reads any queueing delay into it.
```

## Line 158 — func (*recoveryRun).untilResend

```
// untilResend advances both clocks in steps until each side resends the given
// sequence number, and returns how long that took on each.
```

## Line 179 — func libutpRTO

```
// libutpRTO measures libutp's retransmission timeout after a chain of round
// trips, to within step.
//
// libutp acts on timeouts only every TIMEOUT_CHECK_INTERVAL, 500ms
// (utp_internal.cpp:37, :3284), so the moment it resends lies up to 500ms
// after its deadline, not on it. The deadline itself is recovered by moving
// the phase of those checks: the last check before the write is placed delta
// earlier, for every delta across the interval, and the shortest time from
// write to resend is the timeout. Each run is microseconds of real time.
```

## Line 211 — func libutpRTO

```
// No timeout pass while the samples are taken: every wait is shorter
		// than the timeout in force, so none would fire, and passes here
		// would only move the phase this is trying to control.
```

## Line 232 — func libutpRTO

```
// the pass that fixes the phase
```

## Line 257 — func oursRTO

```
// oursRTO measures ours after the same chain. Our retransmission wheel arms
// to whole 25ms ticks, so what is measured is the timeout rounded up to one.
```

## Line 288 — func TestConformanceRetransmissionTimeoutComputation

```
// The retransmission timeout is computed as libutp computes it.
//
// libutp keeps rtt and rtt_var in milliseconds, takes the first sample as
// rtt with half of it as rtt_var, smooths later ones by 1/8 and 1/4, and sets
// rto = max(rtt + rtt_var*4, 1000) (utp_internal.cpp:1362-1380). Its
// first-sample test is `rtt == 0`, so a zero first sample leaves the next one
// to be taken as first again. The SYN counts: the SYN-ACK acknowledges it
// through the same ack_packet.
//
// Each case feeds both sides the same chain of round trips -- the SYN-ACK
// after the first, then one data packet acknowledged after each of the rest,
// every wait shorter than the timeout in force -- then leaves one packet
// unacknowledged. libutp's timeout is measured to within 10ms (libutpRTO);
// ours is measured to its wheel's 25ms tick. The tolerance is the sum, and
// ours may not be earlier than libutp's by more than the 10ms.
```

## Line 304 — func TestConformanceRetransmissionTimeoutComputation

```
// This pins libutp's retransmission-timeout schedule. The loss probe is
	// not libutp's and would add one packet before the first timeout; it is
	// measured on its own in loss_probe_test.go.
```

## Line 311 — func TestConformanceRetransmissionTimeoutComputation

```
// the SYN's round trip first
```

## Line 335 — func TestConformanceZeroWindowProbeTiming

```
// The zero-window probe fires when libutp's does -- where libutp's fires at all.
//
// An acknowledgement advertising a zero window arms a timer, `zerowindow_time
// = current_ms + 15000` (utp_internal.cpp:2149-2151). When a timeout pass
// finds it expired with the window still zero, libutp opens the window to one
// PACKET_SIZE (:1142-1145), and the next flush sends a packet.
//
// PACKET_SIZE is 1435 (:57). The flush charges a whole get_packet_size()
// against it (is_full with no argument, :933-936, :974), and that is the MTU
// the embedder reports less the 20-byte header. With libutp's own default MTU
// callback, 1402 on IPv4 (utp_utils.cpp:228), a packet is 1382 bytes and fits.
// With an embedder reporting a real Ethernet interface, 1472, a packet is 1452
// bytes and never fits: libutp's probe never fires, and a connection whose
// peer advertised a zero window and then went quiet stays stalled until it
// dies. Ours opens the window to MaxPacketSize, which the payload never
// exceeds, so it probes at any MTU.
//
// Both are asserted, so a change on either side shows. libutp's probe waits
// for a timeout pass, which it runs only every 500ms (:37, :3284); ours fires
// on the interval exactly.
```

## Line 383 — func zeroWindowProbeTimes

```
// zeroWindowProbeTimes advertises a zero window to both sides, writes, and
// returns how long each took to send anything; -1 for nothing within 40s.
```

## Line 414 — type grown

```
// grown is the state after growWindows: each side's oldest outstanding
// sequence number and how many it has in flight.
```

## Line 422 — func (*recoveryRun).growWindows

```
// growWindows writes a large buffer to both sides and acknowledges everything
// each sends, a round trip at a time, until both have at least want packets
// in flight. libutp's window opens at one packet and grows by about one a
// round trip in slow start (utp_internal.cpp:1691-1702); ours opens at two
// and grows faster, so ours will have more in flight. That does not matter:
// each scenario is expressed relative to each side's own oldest packet.
```

## Line 435 — func (*recoveryRun).growWindows

```
// Ours is written from a goroutine that is not on the virtual clock, and
	// on a loaded machine it can start late. Each round below moves libutp's
	// clock 50 ms and acknowledges only the side still short, so rounds spent
	// waiting for our writer left libutp unacknowledged until its
	// retransmission timeout fired -- it resent its oldest packet, and the
	// scenario compared a timeout with a fast retransmit. Under six busy
	// loops on four cores that failed TestConformanceFastRetransmitDecisions
	// about one run in two. No virtual time passes until ours has started.
```

## Line 476 — func sackFrom

```
// sackFrom builds a selective ack for a peer that has everything before
// oldest and, past it, the packets at the given offsets from oldest. Bit i of
// the extension is ack_nr+2+i, which is oldest+1+i: oldest itself is the one
// the cumulative ack is waiting for and cannot appear in it.
```

## Line 488 — func resentOffsets

```
// resentOffsets is which packets a side resent, as offsets from its oldest.
```

## Line 499 — func TestConformanceFastRetransmitDecisions

```
// Fast retransmission decides what libutp decides.
//
// libutp's selective_ack walks the bits from the top down, counting packets
// acknowledged past each one; an unacknowledged packet with at least
// DUPLICATE_ACKS_BEFORE_RESEND (3) past it, and not below fast_resend_seq_nr,
// is resent, and so is the packet the cumulative ack is waiting for, on the
// same count. At most four per acknowledgement, oldest first, each moving
// fast_resend_seq_nr past it so it is not fast resent again
// (utp_internal.cpp:1480-1610). A duplicate acknowledgement with no selective
// ack resends nothing: libutp's duplicate count feeds only its MTU search
// (:1921-1941).
//
// Each scenario starts from a fresh connection with at least 14 packets in
// flight on both sides, and compares which packets each resent, as offsets
// from its own oldest outstanding one.
```

## Line 516 — func TestConformanceFastRetransmitDecisions

```
// offsets past the oldest; nil for a bare duplicate ack
```

## Line 521 — func TestConformanceFastRetransmitDecisions

```
// per acknowledgement, what libutp's source says it resends
```

## Line 568 — type ccEntry

```
// ccEntry is one line of libutp's congestion-control log: the inputs
// apply_ccontrol acted on and the window that resulted
// (utp_internal.cpp:1713-1730).
```

## Line 573 — type ccEntry

```
// the delay the acknowledgement reported, unfiltered
```

## Line 618 — type ccPhase

```
// ccPhase is a stretch of acknowledgements with one queueing delay.
```

## Line 622 — type ccPhase

```
// queueing delay the peer reports, over a fixed base
```

## Line 623 — type ccPhase

```
// between acknowledgements
```

## Line 624 — type ccPhase

```
// appLimited writes 100 bytes before each acknowledgement instead of
	// keeping a large write queued, so the window is never full.
```

## Line 629 — func libutpCCTrace

```
// libutpCCTrace drives libutp alone through the phases, acknowledging its
// oldest outstanding packet one at a time with the given delays, and returns
// its congestion-control log. initialWrite bytes are written before the first
// phase.
//
// Delays are whole milliseconds over a whole-millisecond base, and the clock
// moves in whole milliseconds, so every delay libutp acts on is a whole
// number of milliseconds -- which the log prints exactly.
```

## Line 698 — type ccReplay

```
// ccReplay is what replaying a libutp trace into our controller found.
```

## Line 701 — type ccReplay

```
// slowStartEnd is the entry at which our controller, fed libutp's
	// inputs, left slow start, and -1 if it never did.
```

## Line 706 — func replayCC

```
// replayCC feeds libutp's logged inputs, one acknowledgement at a time, to our
// classic-LEDBAT update, and records every entry where the window we compute
// differs from the window libutp logged.
//
// Our controller starts, at entry `from`, from libutp's state: the window
// libutp had before that entry (its first window is one packet_size), its
// target, its opt_sndbuf as both ssthresh and ceiling, and slow start on
// (utp_internal.cpp:2567, :2620-2621). From there it evolves by its own rules;
// only the inputs come from libutp. The delay is fed as the delay libutp acted
// on, after its clamp to the round trip, with no clamp of our own;
// last_maxed_out_window and the clock come from the log, 0 included.
//
// One documented deviation has to be neutralised for the rules to be
// compared at all. Our slow-start step is half our window floor, and libutp's
// is one current packet_size; setting our floor to two of libutp's packets
// makes the steps equal. The floor itself then differs from libutp's 10 bytes
// (DEVIATIONS.md), so the replay fails the case if libutp's window is ever
// below it, where the floor rather than the rules would decide.
```

## Line 768 — func replayCC

```
// Carry on from libutp's window, so one difference does not
			// show up as every later entry differing.
```

## Line 776 — func TestConformanceLedbatRules

```
// LEDBAT's window adjustment, rule by rule, against libutp.
//
// COMPATIBILITY.md rated these rules "partly measured": the controller's
// overall response to a queue was compared against libutp's, but each rule
// in apply_ccontrol (utp_internal.cpp:1615-1712) was only read. Here libutp
// is driven through phases that each exercise a rule, and every update it
// makes is replayed into ours (replayCC). A delay registers with libutp only
// once three acknowledgements carry it, since it acts on the least of its
// last three samples (CUR_DELAY_SIZE, :75), so each phase is longer than
// that. The first phase carries no queueing delay, so libutp's base delay is
// the fixed one and each later phase's delay is the queueing delay it names.
// The round trip must exceed that delay or libutp's clamp to the minimum
// round trip (:1617-1621) decides it instead; with the window full the queue
// of unacknowledged packets makes the round trip long, and with it never
// full the gap between acknowledgements is the round trip.
```

## Line 807 — func TestConformanceLedbatRules

```
// The application never fills the window, so libutp's is_full never
	// records a time (utp_internal.cpp:945, :957) and last_maxed_out_window
	// keeps its initial 0 (:2603). Slow start grows the window regardless
	// (:1691-1702 do not look at scaled_gain); after it, the gain is zeroed
	// by the application-limited guard (:1681-1686).
```

## Line 854 — func TestConformanceLedbatRules

```
// The window fills, the write drains, and the application then trickles.
	// is_full records the time the window was last full (:957); a second
	// later the guard zeroes the gain (:1681).
```

## Line 862 — func TestConformanceLedbatRules

```
// libutp's window starts at one packet, below our floor: start where
		// it has passed it.
```

## Line 899 — func checkCCCoverage

```
// checkCCCoverage fails the case if the trace did not exercise the rules it
// was built for, so a harness change cannot quietly turn it into a
// comparison of nothing.
```
