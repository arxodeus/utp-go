# conformance_timing_test.go

Every comment in `conformance_timing_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build cgo
```

## Line 14

```
// Retransmission timing, compared against libutp.
//
// This is the gap CONFORMANCE.md has recorded since M2: the corpus compares
// *what* each implementation emits, never *when*. A retransmission schedule
// that backs off differently is invisible to it, and to the differential
// fuzzer, and would show up in the wild as a connection that either gives up
// too early or hammers a dead path.
//
// The obstacle was always our side's clock: libutp runs on a virtual clock the
// driver controls, ours runs on the real one with real timers, so the two
// cannot be stepped together. The way round it is to compare the schedule's
// *shape* rather than its absolute times. Both implementations parameterise
// the backoff on a base timeout and double from there, so the schedule is a
// sequence of multiples of that base -- and multiples are comparable across
// two different time scales.
//
// So: libutp's schedule is measured on its virtual clock, at its real 3000ms
// and 1000ms defaults, on every run. Ours is measured on a virtual clock too,
// with the base timeout scaled down. The multiples must
// match, and the bases must be equal -- which together mean the absolute
// schedules coincide. Both halves are asserted; neither is assumed.
```

## Line 36 — const scheduleLateness

```
// scheduleLateness is how late one of our retransmissions may be: less than
// one tick of the retransmission wheel (defaultRetransmitTickInterval), which
// is its resolution. On the virtual clock that is all the lateness there is.
//
// This was a 20% tolerance while our side ran on real timers, and it hid two
// things. Under CPU load the scheduler alone used it up (first retransmissions
// at 245-249 ms against 200). And the wheel rounded every re-arm up by nearly
// a tick, so each backoff landed 25 ms later than the last -- 208, 633, 1458,
// 3084 ms -- which a percentage of a growing deadline never caught.
```

## Line 47 — func libutpRetransmitSchedule

```
// libutpRetransmitSchedule drives libutp until it stops retransmitting, and
// returns the times (in milliseconds from the first transmission) at which it
// resent, plus the time it gave up.
//
// The clock is virtual and driven in 10ms steps, so this is exact to 10ms and
// takes microseconds of real time.
```

## Line 68 — func libutpRetransmitSchedule

```
// Answer the SYN, then send data that is never acknowledged. The
		// SYN-ACK arrives with the virtual clock unmoved, so libutp's first
		// RTT sample is zero and its RTO falls to the 1000ms floor -- which
		// is the case worth pinning, since it is what any connection whose
		// peer stops answering ends up at.
```

## Line 101 — func multiples

```
// multiples turns a list of times into multiples of a base, rounded to the
// nearest whole number. A schedule of 1000, 3000, 7000 against a base of 1000
// becomes 1, 3, 7 -- the shape that is comparable across time scales.
```

## Line 112 — func TestConformanceSynRetransmitSchedule

```
// libutp's SYN backoff, and ours.
//
// libutp gives up when retransmit_count reaches 2 in CS_SYN_SENT, which is
// three transmissions in total; the timeout starts at 3000ms and doubles
// (utp_internal.cpp:1179 applied at :1203, initial value at :2762).
// DEVIATIONS.md records that as matched. This measures it.
```

## Line 126 — func TestConformanceSynRetransmitSchedule

```
// Doubling from the base gives cumulative 1, 3, 7, ... Two retransmissions
	// then give up is three transmissions in total.
```

## Line 138 — func TestConformanceSynRetransmitSchedule

```
// Ours, on the real clock with the base scaled down.
```

## Line 152 — func TestConformanceSynRetransmitSchedule

```
// The multiples matching only means the absolute schedules match if the
	// bases do. libutp's is a hard-coded 3000ms; ours is configurable, and its
	// default must equal it.
```

## Line 161 — func TestConformanceDataRetransmitSchedule

```
// The data retransmission schedule, which is the one that matters in a
// transfer: libutp doubles its RTO from a 1000ms floor
// (rto = max(rtt + rtt_var*4, 1000), utp_internal.cpp:1380).
```

## Line 165 — func TestConformanceDataRetransmitSchedule

```
// This pins libutp's retransmission-timeout schedule. The loss probe is
	// not libutp's and would add one packet before the first timeout; it is
	// measured on its own in loss_probe_test.go.
```

## Line 204 — func assertScheduleMatches

```
// assertScheduleMatches checks that measured times land on the expected
// multiples of a base: never early, and less than scheduleLateness late.
```

## Line 216 — func assertScheduleMatches

```
// Never early. A retransmission before the timeout has elapsed
		// resends a packet the peer was still going to acknowledge, and
		// libutp cannot do it: it compares the clock against rto_timeout
		// before declaring a timeout (utp_internal.cpp:1147-1148). To its
		// millisecond, which is also the resolution of these times.
```

## Line 234 — func ourRetransmitSchedule

```
// ourRetransmitSchedule drives this implementation as the initiator over a
// scripted transport and records when it retransmits.
//
// The peer never acknowledges anything, so every emission after the first is a
// retransmission.
//
// On a virtual clock (virtual_clock_test.go), as the corpus runs. This used to
// run on the real clock, and the schedule it measured was the machine's as
// much as the connection's: the retransmission wheel ticks every 25 ms, which
// against the 200 ms base the callers scale libutp's down to is already 12.5%,
// and the 20% tolerance left 15 ms for the scheduler. Under CPU load the first
// retransmission came at 245-249 ms, failing 4 runs in 12 before any change
// and once in each of two full runs of the package. The times come from each
// packet's header, which the connection stamps from the same clock.
```

## Line 278 — func ourRetransmitSchedule

```
// The socket's wheel and its read and event loops, and the connection:
	// the SYN has gone out once all four are parked.
```

## Line 284 — func ourRetransmitSchedule

```
// Times since the first packet that matches, from the header stamps.
```

## Line 304 — func ourRetransmitSchedule

```
// Giving up ends the connection's event loop, which leaves the
		// clock within the step that did it. ConnectWithCID reports it later,
		// from a goroutine the clock does not wait for.
```

## Line 327 — func ourRetransmitSchedule

```
// Complete the handshake, then write data nobody will acknowledge. The
	// SYN-ACK comes back 10 ms later: a round-trip estimate of zero would
	// leave the loss probe, which needs one, never armed.
```

## Line 345 — func ourRetransmitSchedule

```
// Retransmissions of the data packet specifically: the acknowledgement of
	// the SYN-ACK can go out alongside, and counting it would put every time
	// one step out.
```
