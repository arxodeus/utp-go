# congestion_ledbatpp.go

Every comment in `congestion_ledbatpp.go` at e56c9aa, verbatim.

## Line 8

```
// LEDBAT++, from draft-irtf-iccrg-ledbat-plus-plus-01.
//
// Classic LEDBAT, which libutp implements and which this library matches bit
// for bit by default, has two known failures that no amount of tuning fixes:
//
//   - Its base-delay estimate is the minimum delay seen over a long window.
//     A flow that has been running long enough to build a standing queue
//     measures that queue as if it were the path, stops backing off, and
//     stops being "less than best effort" at all. A flow arriving later
//     measures the true base and yields to it -- the latecomer advantage.
//   - Its window grows at a fixed byte rate per round trip regardless of the
//     path, so it is far too aggressive on a short link and far too slow on
//     a long one.
//
// LEDBAT++ addresses both, and this file implements its four mechanisms:
//
//	§4.1 modified slow start, exiting at 3/4 of the delay target
//	§4.2 a gain that scales with the base RTT
//	§4.3 multiplicative decrease when the delay exceeds the target
//	§4.4 periodic slowdowns, which are what make the base delay honest
//
// This is a deliberate divergence from libutp, and the only one the brief
// asks for. It is opt-in: see ConnectionConfig.CongestionAlgorithm.
```

## Line 32 — type CongestionAlgorithm

```
// CongestionAlgorithm selects which congestion controller a connection runs.
```

## Line 36 — const AlgorithmLEDBAT, AlgorithmLEDBATPP

```
// AlgorithmLEDBAT is classic LEDBAT as libutp implements it. This is the
	// default, because matching the reference implementation is the default
	// everywhere else in this library too.
```

## Line 40 — const AlgorithmLEDBAT, AlgorithmLEDBATPP

```
// AlgorithmLEDBATPP is LEDBAT++.
```

## Line 56 — const ledbatPPTargetDelay, ledbatPPGainCap, ledbatPPSlowStartExitFraction, le...

```
// ledbatPPTargetDelay is LEDBAT++'s queueing-delay target. draft §4.5:
	// "LEDBAT++ default delay target of 60ms is different from the 100ms
	// value recommended in RFC6817."
```

## Line 61 — const ledbatPPTargetDelay, ledbatPPGainCap, ledbatPPSlowStartExitFraction, le...

```
// ledbatPPGainCap is the 16 in GAIN = 1 / min(16, ceil(2*TARGET/base)).
	// draft §4.2, which notes implementations "MAY experiment with the
	// constant value 16".
```

## Line 66 — const ledbatPPTargetDelay, ledbatPPGainCap, ledbatPPSlowStartExitFraction, le...

```
// ledbatPPSlowStartExitFraction is the 3/4 in "If the queuing delay is
	// larger than 3/4ths of the target delay, exit slow start and immediately
	// move to the congestion avoidance phase" (draft §4.1).
```

## Line 71 — const ledbatPPTargetDelay, ledbatPPGainCap, ledbatPPSlowStartExitFraction, le...

```
// ledbatPPDecreaseConstant is the Constant in the §4.3 decrease formula.
	// The draft recommends 1.
```

## Line 75 — const ledbatPPTargetDelay, ledbatPPGainCap, ledbatPPSlowStartExitFraction, le...

```
// ledbatPPFreezeRTTs is how long the congestion window stays pinned at
	// two packets during a slowdown: "frozen at 2 packets for 2 RTT"
	// (draft §4.4).
```

## Line 80 — const ledbatPPTargetDelay, ledbatPPGainCap, ledbatPPSlowStartExitFraction, le...

```
// ledbatPPRTTFilter is how many recent round trips the current delay is
	// the minimum of: "the minimum of the 4 most recent delay samples"
	// (draft §4.5).
```

## Line 85 — const ledbatPPTargetDelay, ledbatPPGainCap, ledbatPPSlowStartExitFraction, le...

```
// ledbatPPSlowdownPeriodFactor is the 9 in "9 times this duration"
	// (draft §4.4) -- the gap between the end of one slowdown and the start
	// of the next, sized so a slowdown costs at most about 10% of the
	// bottleneck's utilisation.
```

## Line 92 — type ledbatPPPhase

```
// ledbatPPPhase is where a LEDBAT++ connection is in its cycle.
```

## Line 96 — const ppSlowStart, ppCongestionAvoidance, ppSlowdownFreeze, ppSlowdownRamp

```
// ppSlowStart is the initial ramp, before any slowdown.
```

## Line 98 — const ppSlowStart, ppCongestionAvoidance, ppSlowdownFreeze, ppSlowdownRamp

```
// ppCongestionAvoidance is the steady state.
```

## Line 100 — const ppSlowStart, ppCongestionAvoidance, ppSlowdownFreeze, ppSlowdownRamp

```
// ppSlowdownFreeze is the two round trips with the window pinned at two
	// packets, which is what lets the queue drain so the delay samples taken
	// during it measure the path rather than the queue.
```

## Line 104 — const ppSlowStart, ppCongestionAvoidance, ppSlowdownFreeze, ppSlowdownRamp

```
// ppSlowdownRamp is the slow-start ramp back up to where the window was
	// before the slowdown.
```

## Line 124 — func ledbatPPGain

```
// ledbatPPGain is draft §4.2: GAIN = 1 / min(16, ceil(2*TARGET/base)).
//
// `base` is the base round-trip time. The effect is that the window grows
// more slowly than Reno on a short path -- where a background flow can do the
// most damage to an interactive one -- and at up to Reno's rate on a long
// one, where growing any slower would mean never using the link.
//
// It is a free function so it can be tested on its own, without a controller.
```

## Line 136 — func ledbatPPGain

```
// No RTT sample yet. The most conservative gain is the safe answer:
		// it makes the first few acks grow the window as slowly as possible,
		// and the real value arrives with the first sample.
```

## Line 148 — func ledbatPPWindowDeltaPerRTT

```
// ledbatPPWindowDeltaPerRTT is the per-round-trip change in the congestion
// window, in packets, for a connection in congestion avoidance. draft §4.3,
// which gives both branches:
//
//	delay below target:  W += GAIN
//	delay above target:  W += max( (GAIN - Constant * W * (delay/target - 1)), -W/2) )
//
// Additive increase and multiplicative decrease. That pairing is the point
// of the section: RFC 6817's increase, GAIN * (1 - delay/target), shrinks to
// nothing as the delay nears the target, and so does the decrease, so near
// the target neither acts and whatever split two flows arrived at persists --
// the "persistent unfairness" of proportional feedback (§3.2). A constant
// increase keeps pushing until the decrease, proportional to each flow's
// window, takes more from the larger.
//
// A free function, so the shape of the response curve can be tested directly
// at the interesting points rather than inferred from a transfer. History:
// HISTORY.md, "ledbatPPWindowDeltaPerRTT".
```

## Line 177 — func ledbatPPWindowDeltaPerRTT

```
// draft §4.3. The decrease is proportional to both the window and the
	// overshoot, and is floored at half the window per round trip so a
	// single wild delay sample cannot collapse the connection.
```

## Line 184 — func (*defaultController).pushLedbatPPRTT

```
// pushLedbatPPRTT records a round-trip sample for LEDBAT++'s delay
// measurement. Called with the lock held, and only for a packet sent once,
// whose acknowledgement can be matched to its transmission.
```

## Line 200 — func (*defaultController).ledbatPPRTTDelay

```
// ledbatPPRTTDelay is LEDBAT++'s base and current round trip, whose
// difference is its queueing delay. draft §4.5: "LEDBAT++ uses Round Trip
// Time measurements instead of one way delay", filtered by "the minimum of
// the 4 most recent delay samples"; the base is the lowest round trip within
// the delay window, as the one-way base is for classic LEDBAT.
//
// A round trip is timed entirely on this end's clock, so it has no clock skew
// to correct and no peer to trust, though it does include queueing on the
// return path and any delay in acknowledging.
//
// Both are zero until the first sample. History: HISTORY.md,
// "ledbatPPRTTDelay".
```

## Line 229 — func (*defaultController).applyLedbatPP

```
// applyLedbatPP is the LEDBAT++ equivalent of applyCongestionControl, called
// once per acked packet with the controller's lock held. baseMicros and
// currentMicros are the base and current round trip (ledbatPPRTTDelay);
// their difference is the queueing delay.
```

## Line 250 — func (*defaultController).applyLedbatPP

```
// The queueing delay cannot exceed the round trip. With round-trip
	// delay the filtered current value is at most this packet's own round
	// trip, so this does not bind; it is kept so the rule holds whatever the
	// inputs.
```

## Line 257 — func (*defaultController).applyLedbatPP

```
// No clock-drift penalty. It used to be added here as well as in classic
	// LEDBAT, to stop a peer manipulating its clock so that this end
	// under-measured the queue. With the delay measured from round trips the
	// peer's clock takes no part in it, so there is nothing to defend, and
	// the penalty would only slow a flow whose peer's clock happens to
	// drift. See DEVIATIONS.md.
```

## Line 271 — func (*defaultController).applyLedbatPP

```
// Window pinned at two packets. Nothing to adjust; the point of the
		// phase is to stop sending long enough for the queue to drain, so
		// that the delay samples arriving now measure the path.
```

## Line 278 — func (*defaultController).applyLedbatPP

```
// draft §4.1: exit on a queueing delay over 3/4 of the target -- but
		// "Exit slow start on excessive delay SHOULD be applied only during
		// the initial slow start." The ramp out of a slowdown runs "until the
		// congestion window reaches SSTHRESH" (§4.4) and nothing else: two
		// flows' slowdowns synchronise, and a ramp that also left on delay would
		// read the other's refilled queue and stop at two packets. History:
		// HISTORY.md, "OnAck: LEDBAT++ slow-start exit".
```

## Line 291 — func (*defaultController).applyLedbatPP

```
// The initial ramp is gain-scaled, draft §4.1: increase "by that
		// number multiplied by the dynamic GAIN value". The ramp back out of
		// a slowdown is not.
		//
		// §4.4 says only "ramp up the congestion window according to the
		// slow start algorithm, until the congestion window reaches
		// SSTHRESH", and separately that a slowdown should cost "not more
		// than a 10% drop in the utilization of the bottleneck". Those two
		// statements only hold together if the ramp doubles per round trip.
		// And there is no reason for the ramp to be cautious: it is returning
		// to a window this connection was already using a moment ago, and it
		// stops at exactly that window. History: HISTORY.md, "OnAck: the ramp
		// out of a slowdown".
```

## Line 322 — func (*defaultController).applyLedbatPP

```
// The formula is per round trip; this ack covers bytesAcked of the
		// window, so it earns that fraction of the change.
		//
		// Of the window it was sent in, too, when that is the larger share:
		// the window as it stands has been grown by the acknowledgements of
		// the same round trip before this one, so a round trip's worth of
		// them added less than a round trip's change. Classic LEDBAT does the
		// same (sendWindowCredit; DEVIATIONS.md, "The window grows by a round
		// trip's worth per round trip").
```

## Line 338 — func (*defaultController).applyLedbatPP

```
// With the fraction of a byte the last update could not add. An
		// update is a few bytes when the gain is small and the window large
		// -- a sixth of a packet a round trip, shared among a hundred
		// packets, is two bytes each -- and truncating each to whole bytes
		// lost up to a byte of it: a round trip grew the window 14% less
		// than the draft's figure, measured (TestLedbatPPRoundTripGrowth).
```

## Line 346 — func (*defaultController).applyLedbatPP

```
// A sender that never fills its window is limited by the
		// application, not the path, and learns nothing from the delay it
		// measures. Same rule as classic LEDBAT, same reason.
```

## Line 364 — func (*defaultController).exitLedbatPPSlowStart

```
// exitLedbatPPSlowStart moves to congestion avoidance and schedules the next
// slowdown.
```

## Line 379 — func (*defaultController).exitLedbatPPSlowStart

```
// draft §4.4: the next slowdown starts nine slowdown-durations after
		// this one ended, so slowdowns cost about a tenth of the link.
```

## Line 389 — func (*defaultController).exitLedbatPPSlowStart

```
// draft §4.4: "the initial slowdown begins 2 RTT after the initial slow
	// start completes".
```

## Line 394 — func (*defaultController).advanceLedbatPPPhase

```
// advanceLedbatPPPhase applies whatever phase transitions the clock calls
// for. It is separate from the window arithmetic so it can also be driven
// from the send path, where a connection that has stopped receiving acks
// would otherwise sit in a slowdown forever.
//
// Called with the lock held.
```

## Line 419 — func (*defaultController).advanceLedbatPPPhase

```
// draft §4.4: "set SSTHRESH to the current version of the congestion
		// window CWND, and then reduce CWND to 2 packets."
```

## Line 431 — func (*defaultController).OnTick

```
// OnTick lets the connection drive time-based phase changes even when no acks
// are arriving -- during a slowdown the sender is pinned to two packets, and
// an application that goes quiet at the wrong moment would otherwise leave it
// there.
```
