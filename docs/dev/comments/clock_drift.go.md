# clock_drift.go

Every comment in `clock_drift.go` at e56c9aa, verbatim.

## Line 5 — const driftSlotInterval

```
// driftSlotInterval is how long each averaging slot runs. libutp: "each slot
// represents 5 seconds", `conn->average_sample_time += 5000`
// (utp_internal.cpp:2062).
```

## Line 10 — const driftPenaltyThresholdMicros

```
// driftPenaltyThresholdMicros is the drift beyond which the congestion
// controller starts adding a penalty to its measured delay, in microseconds
// per slot. libutp: `if (clock_drift < -200000)` (utp_internal.cpp:1647).
//
// The magnitude is worth reading twice. 200000 microseconds per five seconds
// is 40,000 parts per million, where an ordinary crystal oscillator drifts by
// tens of ppm. libutp's own comment says what the number is for: "The main
// purpose is to compensate for people trying to 'cheat' uTP by making their
// clock run slower, and this definitely catches that without any risk of
// false positives". It is not a correction for hardware; it is a floor under
// deliberate manipulation.
```

## Line 23 — const driftPenaltyDivisor

```
// driftPenaltyDivisor scales the penalty. libutp: `(-clock_drift - 200000) / 7`
// (utp_internal.cpp:1648).
```

## Line 27 — type driftEstimator

```
// driftEstimator tracks the slope of the delay the peer reports for our
// packets, which is libutp's clock_drift.
//
// This is the second of libutp's two clock-drift mechanisms, and until now
// this library had only the first. They do different jobs and neither
// substitutes for the other:
//
//   - The delay-base shift (utp_internal.cpp:2009-2015, implemented in
//     baseDelayHist) corrects the delay *measurement* when the peer's base
//     delay falls, which is what ordinary drift between two honest clocks
//     looks like. It is bounded to 10ms per adjustment and ages out.
//   - This one does not correct anything. It watches the long-run slope and,
//     past a threshold far outside honest hardware, inflates the delay the
//     congestion controller sees so that the flow yields rather than gains
//     from the drift.
//
// The arithmetic is wrapping throughout, because the delays are uint32
// microsecond counts taken from a wrapping clock, and it is kept in its own
// type so it can be tested against hand-worked values rather than only
// through a transfer. Getting a wrap wrong here would show up as a penalty
// applied to an honest peer, which is the failure worth being careful about.
```

## Line 49 — type driftEstimator

```
// base is the offset every sample is measured against, so that a wrapping
	// uint32 can be averaged as a signed quantity. libutp's
	// average_delay_base, which it also renormalises (see push). Zero means
	// unset, as in libutp.
```

## Line 55 — type driftEstimator

```
// sum and samples accumulate the current slot. libutp's
	// current_delay_sum and current_delay_samples.
```

## Line 60 — type driftEstimator

```
// average is the mean of the last completed slot: libutp's average_delay.
```

## Line 63 — type driftEstimator

```
// slotEnds is when the current slot closes. libutp's average_sample_time,
	// which it sets to "now + 5 seconds" when the socket is created
	// (utp_internal.cpp:2553) rather than when the first sample arrives.
```

## Line 68 — type driftEstimator

```
// drift is the rolling average of the slope, weighted 7:1 against the
	// newest slot: libutp's clock_drift (utp_internal.cpp:2105). driftRaw is
	// the last slope before smoothing, kept because libutp keeps it and
	// because it is what a test can check against a known input.
```

## Line 80 — func (*driftEstimator).push

```
// push records one delay the peer reported for our packets.
//
// sample is libutp's actual_delay: the reply_micro field of an incoming
// packet, which is how long the peer measured our last packet taking to reach
// it. A zero means the peer has no measurement yet rather than a delay of
// zero, and libutp excludes it from this average for that reason
// (utp_internal.cpp:2020-2023).
```

## Line 91 — func (*driftEstimator).push

```
// `if (conn->average_delay_base == 0) conn->average_delay_base =
	// actual_delay;` (utp_internal.cpp:2028). Zero is libutp's "unset", so a
	// base that a renormalisation has left at exactly zero is seeded again
	// from the next sample; a separate flag, as this used to have, kept it.
```

## Line 99 — func (*driftEstimator).push

```
// The signed distance from base to sample, on a wrapping uint32. Both
	// differences are computed and the smaller one wins, which is what picks
	// the short way round the circle. libutp: utp_internal.cpp:2032-2045.
```

## Line 102 — func (*driftEstimator).push

```
// wrapping
```

## Line 103 — func (*driftEstimator).push

```
// wrapping
```

## Line 105 — func (*driftEstimator).push

```
// base is below sample: a positive sample.
```

## Line 108 — func (*driftEstimator).push

```
// base is at or above sample: a negative one.
```

## Line 119 — func (*driftEstimator).closeSlot

```
// closeSlot ends the averaging slot and folds its slope into the estimate.
```

## Line 122 — func (*driftEstimator).closeSlot

```
// Nothing arrived this slot, so there is no slope to take. libutp
		// cannot reach this: it only tests the slot boundary from inside the
		// branch that has just added a sample, so samples is at least one.
		// Reaching it here would mean a caller drove the clock forward
		// without delays, and the honest answer is to move the boundary and
		// draw no conclusion.
```

## Line 137 — func (*driftEstimator).closeSlot

```
// Renormalise around zero. Only the slope matters, so the pair can be
	// slid to keep the minimum at or below zero and the maximum at or above
	// it, which keeps both away from the ends of an int32 however long the
	// connection runs. libutp: utp_internal.cpp:2070-2091, whose comment is
	// "since we're only interested in the slope of the curve formed by the
	// average delay samples, we can cancel out the actual offset to make sure
	// we won't have problems with wrapping".
```

## Line 156 — func (*driftEstimator).closeSlot

```
// wrapping
```

## Line 162 — func (*driftEstimator).closeSlot

```
// A rolling average weighted 7:1 towards the history
	// (utp_internal.cpp:2105).
```

## Line 169 — func (*driftEstimator).penaltyMicros

```
// penaltyMicros is how much to add to the measured queueing delay, given the
// drift estimated so far.
//
// Zero unless the drift is more negative than the threshold, which is the
// direction that makes a flow *under*-measure the queue and so take more of a
// bottleneck than it should. Drift in the other direction needs no penalty:
// it makes the flow over-measure and yield, which costs the drifting peer and
// nobody else.
//
// libutp: utp_internal.cpp:1646-1650.
```
