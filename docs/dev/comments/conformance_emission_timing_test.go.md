# conformance_emission_timing_test.go

Every comment in `conformance_emission_timing_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build cgo
```

## Line 13 — func emissionInstants

```
// emissionInstants reads the instant each packet was built from the packet
// itself.
//
// The timestamp field carries the sender's clock at the moment the header was
// assembled, and both implementations re-stamp it on every transmission --
// ours in retransmit and synPacket, libutp in send_data. So when both sides
// are on a virtual clock the field *is* the emission instant, exactly, with
// no sampling and no quantisation.
//
// Reading it from the packet rather than timing its arrival also sidesteps a
// question with no clean answer: a packet leaves a connection through a
// channel, and "every goroutine is parked" is not the same as "nothing is in
// flight" while a buffered handoff is outstanding. The instant is decided
// where the header is built, which is synchronous with the connection's own
// processing, and that is also where libutp's sendto callback fires.
```

## Line 45 — func ourSynRetransmissions

```
// ourSynRetransmissions returns the instants at which this implementation
// resends a SYN nothing ever answers, on a clock the test owns.
```

## Line 68 — func ourSynRetransmissions

```
// Four participants register on this clock: the socket's retransmission
	// wheel and its read and event loops, when the socket is built, and this
	// connection's event loop when its goroutine runs.
```

## Line 73 — func ourSynRetransmissions

```
// The first SYN is not a retransmission; wait for it and discard it.
	// Counting is allowed to poll -- it is the *instants* that must not be
	// inferred from wall time, and those come from the packets.
```

## Line 81 — func ourSynRetransmissions

```
// libutp gives up after three attempts, so at most two retransmissions
	// are expected; wait for them to reach the transport before reading.
```

## Line 87 — func waitForEmitted

```
// waitForEmitted blocks until the scripted transport has at least n packets,
// or fails. Used only for counting.
```

## Line 101 — func libutpSynRetransmissions

```
// libutpSynRetransmissions is the same measurement against the reference, off
// its own virtual clock and read the same way.
```

## Line 118 — func libutpSynRetransmissions

```
// libutp's driver is synchronous: it only acts when told to, so its clock
	// is advanced in steps with a timeout check after each. The step affects
	// when libutp *notices* a deadline, not the instant it stamps, which is
	// read from the packet exactly as ours is.
```

## Line 130 — func TestSynRetransmissionInstantsMatchLibutp

```
// *When* this implementation sends, compared against libutp as numbers.
//
// Every other comparison in this corpus is about what a packet contained.
// Timing was compared once, for retransmission, by running libutp on its
// virtual clock and this library on the real one and checking the two
// schedules had the same *shape* -- because nothing could ask at what instant
// our side sent anything. The answer depended on when a goroutine happened to
// be scheduled, which is also why several tests here wait for quiet rather
// than for an event, and why two of this session's defects turned out to be
// timing assumptions in the harness rather than faults in the library.
//
// Clock and idleBarrier close that. The connection's deadlines, timers and
// wall clock all come from a clock this test owns, and the event loop, the
// retransmission wheel and the socket's read loop each report when they are
// parked, so time moves between reactions rather than during one. The
// instants are then read from the packets, exactly.
```

## Line 168 — func TestSynRetransmissionInstantsMatchLibutp

```
// Within one wheel tick, however many retransmissions in. The wheel
		// fires a timer on the first tick at or after its deadline, never
		// early, because firing early resends a packet the peer was still
		// going to acknowledge; libutp compares the clock against an absolute
		// rto_timeout (utp_internal.cpp:1147-1148).
		//
		// This tolerance used to grow by a tick per retransmission. The wheel
		// counted ticks from the moment of arming and added one, and each
		// backoff re-arms just after the tick that fired it, so the lateness
		// carried forward: 3.025s and 9.05s against libutp's 3.0s and 9.0s.
		// It now places a timer by its deadline (timeWheel.put), and measures
		// 3.0s and 9.0s.
```

## Line 181 — func TestSynRetransmissionInstantsMatchLibutp

```
// libutp's own instants carry its driver's stepping granularity.
```

## Line 191 — func TestSynRetransmissionInstantsMatchLibutp

```
// And never early: that is the direction that matters.
```

## Line 200 — func TestVirtualClockHoldsTheConnectionStill

```
// The barrier itself, asserted directly: everything above depends on it, and
// a barrier that silently did nothing would leave every instant looking exact
// while being as racy as before.
```

## Line 226 — func TestVirtualClockHoldsTheConnectionStill

```
// Real time does not move this connection's clock.
```

## Line 233 — func TestVirtualClockHoldsTheConnectionStill

```
// And nothing is retransmitted while it stands still, however long the
	// test waits in the real world. The SYN's timeout is three seconds; this
	// waits a fifteenth of that and would fail outright if any part of the
	// connection were still on the real clock.
```

## Line 243 — func TestVirtualClockHoldsTheConnectionStill

```
// Moving past the timeout does produce one.
```

## Line 248 — func ourReplyInstant

```
// ourReplyInstant injects one packet and returns how long after it this
// implementation answered, measured on a clock the test owns.
//
// The injection point is the moment the clock is at when the packet is handed
// to the transport; the reply instant comes from the reply's own timestamp
// field. Both are exact.
```

## Line 279 — func ourReplyInstant

```
// Three participants register when the socket is built: the
	// retransmission wheel and the read and event loops. An accepting
	// connection does not exist yet -- AcceptWithCid parks until a SYN
	// arrives, and the event loop that would be the fourth is created only
	// then.
```

## Line 288 — func ourReplyInstant

```
// The handshake first, so the connection is established before the packet
	// whose reply is being timed.
```

## Line 305 — func ourReplyInstant

```
// Now one data packet, and the question is when the acknowledgement goes
	// out. AwaitQuiet is the inbound path settling: the read loop handles the
	// packet and sends the acknowledgement itself, and reports when it parks.
```

## Line 319 — func libutpReplyInstant

```
// libutpReplyInstant is the same measurement against the reference.
```

## Line 345 — func TestReplyInstantMatchesLibutp

```
// How long after a packet arrives the acknowledgement goes out, compared
// against libutp.
//
// This is the inbound half of the timing question, and until the socket's
// read and event loops became barrier participants it could not be asked at
// all: a test could inject a packet and advance the clock with no guarantee
// the injection had even been processed first.
//
// Both implementations defer acknowledgements and flush them when something
// external says so -- libutp when its embedder calls utp_issue_deferred_acks,
// this library at the end of the event-loop pass that received the packet.
// So the expected answer for both is *the same instant*: neither waits, and a
// non-zero delay on either side would mean the acknowledgement had slipped to
// a later pass than the packet that prompted it.
```
