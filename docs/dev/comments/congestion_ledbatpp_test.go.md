# congestion_ledbatpp_test.go

Every comment in `congestion_ledbatpp_test.go` at e56c9aa, verbatim.

## Line 9 — func TestLedbatPPGain

```
// GAIN = 1 / min(16, ceil(2*TARGET/base)), draft §4.2.
//
// The point of the formula is that a background flow grows more slowly than
// Reno on a short path -- where it can do the most damage to an interactive
// flow -- and at up to Reno's rate on a long one, where growing any slower
// would mean never using the link. The cases below pin both ends and the
// boundary between them.
```

## Line 50 — func TestLedbatPPWindowDeltaPerRTT

```
// The window response curve, at the points that matter. draft §4.3.
```

## Line 53 — func TestLedbatPPWindowDeltaPerRTT

```
// a 20ms path
```

## Line 63 — func TestLedbatPPWindowDeltaPerRTT

```
// "the per RTT window when delay is less than target is: W += GAIN".
		// Not RFC 6817's GAIN * (1 - delay/target), which fades to nothing
		// near the target and with it the pressure that makes two flows
		// converge.
```

## Line 93 — func TestLedbatPPWindowDeltaPerRTT

```
// 10% over target, 100-packet window: gain - 1*100*0.1 = gain - 10.
```

## Line 105 — func TestLedbatPPWindowDeltaPerRTT

```
// Ten times the target would otherwise take 900 packets off a
		// 100-packet window.
```

## Line 127 — func TestLedbatPPSlowdownCycle

```
// A LEDBAT++ connection runs slowdowns: it periodically drops to two packets
// for two round trips so the queue drains and the base-delay estimate is
// taken against an empty path rather than against its own standing queue.
//
// That mechanism is what LEDBAT++ exists for -- without it a long-running
// flow measures its own queue as the path and stops yielding at all -- so it
// is tested directly rather than inferred from a transfer.
```

## Line 156 — func TestLedbatPPSlowdownCycle

```
// Reach congestion avoidance, and with it a scheduled first slowdown.
```

## Line 169 — func TestLedbatPPSlowdownCycle

```
// Before it is due, nothing happens.
```

## Line 175 — func TestLedbatPPSlowdownCycle

```
// When it is due, the window drops to two packets and ssthresh records
	// where it was.
```

## Line 192 — func TestLedbatPPSlowdownCycle

```
// It stays frozen for two round trips, not one.
```

## Line 199 — func TestLedbatPPSlowdownCycle

```
// Then it ramps back up.
```

## Line 206 — func TestLedbatPPSlowdownCycle

```
// When the ramp finishes, the next slowdown is nine slowdown-durations
	// out, so slowdowns cost about a tenth of the link.
```

## Line 221 — func TestLedbatPPSlowdownCycle

```
// Which is to say: at most about a tenth of the time is spent slowed
	// down. That is the property the constant exists to give, so assert it
	// rather than only the constant.
```

## Line 231 — func TestLedbatPPSlowStartExitsAtThreeQuartersTarget

```
// Slow start ends when the queueing delay reaches three quarters of the
// target, not when it reaches the target. draft §4.1.
//
// The round trips passed in below are the *measured* ones, so they include
// the queue: a 48ms standing queue means a round trip of at least 48ms. Using
// a smaller RTT would be physically impossible, and the clamp that says so
// would swallow the delay -- which is the clamp doing its job, not the
// slow-start rule failing.
```

## Line 249 — func TestLedbatPPSlowStartExitsAtThreeQuartersTarget

```
// Half the target: still ramping.
```

## Line 256 — func TestLedbatPPSlowStartExitsAtThreeQuartersTarget

```
// Four fifths of it: over the three-quarter line, so out.
```

## Line 264 — func TestLedbatPPClampsDelayToRoundTrip

```
// The queueing delay a peer reports is clamped to the round trip actually
// measured, in LEDBAT++ as in classic LEDBAT. A peer claiming a queue longer
// than the round trip is reporting a broken clock or lying, and acting on it
// would end slow start -- or collapse the window -- on nothing.
```

## Line 275 — func TestLedbatPPClampsDelayToRoundTrip

```
// A reported queue of a full second on a 5ms path.
```

## Line 286 — func TestClassicLedbatIsTheDefault

```
// Classic LEDBAT is the default, and selecting it changes nothing about the
// controller's shape. A connection that does not ask for LEDBAT++ must not
// get any of it.
```

## Line 303 — func TestClassicLedbatIsTheDefault

```
// OnTick is a no-op for classic LEDBAT.
```

## Line 314 — func TestWindowCeilingIsNeverBelowTheFloor

```
// The window ceiling must never be below the window floor. It was, for any
// caller that did not set WindowSize, which was every direct user of
// defaultCtrlConfig -- and a ceiling of zero clamps the window shut on the
// first ack.
```

## Line 342 — type ppStepClock

```
// ppStepClock is a clock that reads whatever time the test sets, so the
// send times the controller records and the times the test passes in agree.
```

## Line 361 — func TestLedbatPPLossFromBeforeASlowdownHalvesTheSavedWindow

```
// A packet sent before a slowdown and declared lost during the ramp out of
// it was sent from the window the slowdown saved. Its loss halves that
// window, where the ramp is going, and leaves the ramp where it is. Charged
// to the ramp's partial window instead, it ended the ramp at a fraction of
// what the connection had been using, and two LEDBAT++ flows through a
// shallow bottleneck split it as unevenly as 37/63 (KNOWN-LIMITATIONS.md).
```

## Line 405 — func TestLedbatPPLossIsChargedOncePerCongestionEvent

```
// One congestion event is charged once. A packet sent before the window was
// last cut belongs to the event that cut it, however much later its loss is
// detected; a packet sent after the cut is a new event. The 100ms rate limit
// alone charged one overflow twice when a slowdown fell between its losses.
```

## Line 451 — func TestLedbatPPRoundTripGrowth

```
// A round trip of acknowledgements below the target grows LEDBAT++'s window
// by the draft's figure for a round trip: GAIN packets (§4.2).
//
// Each acknowledgement earns its share of the round trip's change. Two things
// took some of it: the share was reckoned against the window as it stood,
// which the round trip's earlier acknowledgements had already grown, and
// each update was truncated to whole bytes -- a few bytes apiece when the
// gain is small and the window large, so up to a byte of each was lost.
// Measured before: 2-14% short, worst at the 20 ms gain of a sixth of a
// packet and a hundred-packet window.
```

## Line 476 — func TestLedbatPPRoundTripGrowth

```
// A millisecond of queueing, well under the target.
```
