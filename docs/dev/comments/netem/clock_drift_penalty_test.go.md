# netem/clock_drift_penalty_test.go

Every comment in `netem/clock_drift_penalty_test.go` at e56c9aa, verbatim.

## Line 8 — func TestClockDriftPenaltyFiresOnlyOnAbsurdDrift

```
// The clock-drift penalty: libutp's second drift mechanism, and the one this
// library was missing until the M4b sweep found it.
//
// libutp watches the long-run slope of the delay the peer reports for its
// packets, and past a threshold adds a penalty to the delay its congestion
// controller sees (utp_internal.cpp:1646-1650):
//
//	if (clock_drift < -200000) {
//	    penalty = (-clock_drift - 200000) / 7;
//	    our_delay += penalty;
//	}
//
// It is not a correction. The other mechanism -- the delay-base shift in
// delayAccumulator -- is what corrects the measurement for ordinary drift
// between honest clocks. This one detects drift so far outside honest
// hardware that libutp attributes it to intent: "The main purpose is to
// compensate for people trying to 'cheat' uTP by making their clock run
// slower, and this definitely catches that without any risk of false
// positives".
//
// Both halves of that claim are worth testing, and the second is the one that
// would bite: a penalty applied to an honest peer would make this library
// yield bandwidth for no reason at all, on every connection, invisibly.
//
// Direction matters. The penalty fires on a *falling* reported delay, which
// is a sender whose clock gains against the receiver's -- positive ppm here,
// since the stamps run ahead and the receiver's `now - stamp` shrinks. The
// opposite direction needs no penalty: it inflates the measured delay, so the
// flow already yields, which costs the drifting peer and nobody else.
```

## Line 38 — func TestClockDriftPenaltyFiresOnlyOnAbsurdDrift

```
// Long enough for the estimate to converge, which takes longer than it
	// looks. Each five-second slot contributes one eighth of the difference
	// (clock_drift = (clock_drift*7 + slope)/8, utp_internal.cpp:2105), so
	// after n slots the estimate has reached 1-(7/8)^n of the true slope: two
	// slots is 23%, six is 55%. Measured at 12 seconds and 200000ppm the
	// estimate reached -179151 against a -200000 threshold -- the mechanism
	// working, and the run ending before it crossed. Twenty seconds is four
	// slots, 41%, which crosses at the drift rate used here.
	//
	// That slowness is libutp's design, not an artifact. A drift estimate
	// that reacted within a slot would swing on ordinary delay noise, and the
	// penalty it drives costs throughput.
```

## Line 53 — func TestClockDriftPenaltyFiresOnlyOnAbsurdDrift

```
// A real crystal. libutp's "without any risk of false positives" is the
	// claim under test: this must earn no penalty whatever.
```

## Line 58 — func TestClockDriftPenaltyFiresOnlyOnAbsurdDrift

```
// 40,000 ppm is the threshold exactly -- the reported delay falls by
	// 200,000 microseconds per five-second slot. Well past it, so the
	// estimate does not have to be precise to cross.
```

## Line 68 — func TestClockDriftPenaltyFiresOnlyOnAbsurdDrift

```
// No false positives, at a drift a thousand times smaller than the
	// threshold.
```

## Line 77 — func TestClockDriftPenaltyFiresOnlyOnAbsurdDrift

```
// And it fires on the drift it is for.
```
