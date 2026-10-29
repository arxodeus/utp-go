# congestion_drift_penalty_test.go

Every comment in `congestion_drift_penalty_test.go` at e56c9aa, verbatim.

## Line 8 — func TestDriftPenaltyReachesTheWindow

```
// The drift penalty reaches the congestion window.
//
// The estimator has its own tests and the emulated-network case shows it fed
// from real acknowledgements, but neither would notice a penalty that was
// computed and then never added to anything. This drives the controller
// directly and compares the window it settles on with the penalty reachable
// and not, over identical acknowledgements.
//
// It is done here rather than over the network because the network cannot say
// it cheaply. Measured there, on a link fast enough not to be the limit, a
// 24ms penalty moved the mean window by 9% over three minutes of transfer; the
// same claim costs milliseconds at this level, and is the sharper one, since
// every other cause of a window difference is held fixed by construction.
```

## Line 22 — func TestDriftPenaltyReachesTheWindow

```
// Identical acknowledgements, identical everything, except that one
	// controller is handed a drift estimate past the threshold.
```

## Line 30 — func TestDriftPenaltyReachesTheWindow

```
// A steady, unremarkable queueing delay: 20ms measured against a 10ms
		// base, so 10ms of queue against a 100ms target. Comfortably in the
		// region where the window grows.
```

## Line 46 — func TestDriftPenaltyReachesTheWindow

```
// Keep the window full, or the application-limited guard stops
			// growth and both runs settle at the same floor for a reason that
			// has nothing to do with drift.
```

## Line 54 — func TestDriftPenaltyReachesTheWindow

```
// The base delay has to settle below the sample or there is no queueing
	// delay for the penalty to add to, so seed both the same way.
```

## Line 57 — func TestDriftPenaltyReachesTheWindow

```
// Past the threshold by enough to matter: the penalty is
	// (-drift - 200000) / 7, so -900000 gives 100ms -- the whole target.
```

## Line 74 — func TestDriftPenaltyIgnoresTheHarmlessDirection

```
// A drift on the harmless side earns nothing, so the window is untouched.
//
// The penalty exists for a delay signal that is shrinking, which makes a flow
// under-measure the queue and take more than its share. Drift the other way
// makes it over-measure and yield, which costs the drifting peer and nobody
// else, and libutp penalises only the first (utp_internal.cpp:1647).
```
