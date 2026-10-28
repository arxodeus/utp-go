# netem/recorder.go

Every comment in `netem/recorder.go` at e56c9aa, verbatim.

## Line 12 — type Recorder

```
// Recorder collects a connection's metric snapshots into a time series.
//
// Use its Observe method as a utp.ConnectionConfig.Metrics observer. It
// only appends under a mutex, so it satisfies the requirement that an
// observer must not block the connection's event loop.
```

## Line 23 — const DefaultRecorderLimit

```
// DefaultRecorderLimit caps retained samples so a long soak cannot exhaust
// memory. Once reached, the recorder keeps the first and last halves and
// drops the middle, which preserves start-up and steady-state behaviour.
```

## Line 28 — func NewRecorder

```
// NewRecorder returns a recorder with the default sample limit.
```

## Line 31 — func (*Recorder).Observe

```
// Observe records one snapshot. It satisfies utp.MetricsObserver.
```

## Line 42 — func (*Recorder).Samples

```
// Samples returns a copy of the recorded series, oldest first.
```

## Line 51 — func (*Recorder).Len

```
// Len returns how many samples have been recorded.
```

## Line 58 — func (*Recorder).Last

```
// Last returns the most recent snapshot, and whether there was one.
```

## Line 68 — type Summary

```
// Summary condenses a recorded series into the numbers the M5 gates report.
```

## Line 76 — type Summary

```
// CwndPinnedAtMin is the fraction of samples where the window sat at its
	// floor. A controller stuck here has collapsed rather than backed off.
```

## Line 84 — type Summary

```
// RTTMin is the lowest round trip seen over the whole flow. On a path
	// this flow started on while it was empty, this is the path's real
	// unloaded round trip.
```

## Line 89 — type Summary

```
// StandingQueueP50 and StandingQueueP95 are the round trip above
	// RTTMin -- the queue this flow actually left standing on the path.
	//
	// This exists because QueueingDelay below cannot be trusted for that
	// question. QueueingDelay is `PeerTsDiff - BaseDelay`, and BaseDelay is
	// the *controller's own estimate* of the empty path. A delay-based
	// controller whose base-delay estimate has drifted upwards -- which is
	// precisely the failure LEDBAT++ exists to fix -- reports a small
	// queueing delay while sitting on a large queue, because it is
	// subtracting the queue from itself.
	//
	// Measured on a 40ms path: classic LEDBAT reported 156µs of queueing
	// delay while its round trip sat at 80ms. It was causing 40ms of queue
	// and could not see it. StandingQueueP50 reports the 40ms.
```

## Line 106 — type Summary

```
// Queueing delay is the excess of measured one-way delay over the lowest
	// seen: the standing queue LEDBAT exists to bound.
```

## Line 121 — func (Summary).String

```
// String renders the summary for a test log.
```

## Line 148 — func (*Recorder).Summary

```
// Summary condenses the recorded series.
//
// Samples taken before the connection is established carry no controller
// state, so they are excluded from the congestion statistics; including them
// would drag every average towards zero.
```

## Line 182 — func (*Recorder).Summary

```
// The fine estimate: the whole-millisecond one rounds a 2 ms queue
		// to 1 or 3 and cannot show one under a millisecond at all.
```

## Line 244 — func (*Recorder).CwndSeries

```
// CwndSeries returns the congestion window over time, for plotting or for
// asserting that the window actually moves.
```
