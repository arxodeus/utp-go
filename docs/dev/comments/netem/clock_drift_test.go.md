# netem/clock_drift_test.go

Every comment in `netem/clock_drift_test.go` at e56c9aa, verbatim.

## Line 14 — type driftResult

```
// driftResult is what one drifted transfer observed from the sender's side.
```

## Line 21 — type driftResult

```
// lateQueueMean is the mean over samples taken after the delay window has
	// had time to fill twice over. The steady state is what the w x r rule
	// predicts; a mean over the whole transfer includes the ramp towards it
	// and is necessarily lower.
```

## Line 26 — type driftResult

```
// lateQueueMedian is the median of the same samples. The phantom queue
	// the correction cancels is a steady offset, a sawtooth that grows
	// between corrections, whose median and mean agree; a stall in the
	// runner's scheduling -- a 139ms spike in one macOS run -- moves the
	// mean of 600 samples by a millisecond and the median not at all.
```

## Line 59 — func runDrifted

```
// runDrifted sends payloadLen bytes with the sender's clock drifting at ppm,
// over a link with plenty of capacity and no bottleneck queue worth speaking
// of, so that any queue the sender *believes* in is the drift.
```

## Line 67 — func runDriftedWith

```
// runDriftedWith runs one transfer with the sender's clock drifting at ppm.
//
// chunkInterval paces the writes; zero writes as fast as the connection will
// take them, which is what makes the congestion window the limit.
```

## Line 87 — func runDriftedWith

```
// Only the sender's clock drifts. The receiver keeps the real one, so ppm
	// is the *relative* rate error, which is all that can matter.
```

## Line 155 — func runDriftedWith

```
// Paced well below the link rate, so no real queue ever forms.
	//
	// A bulk transfer cannot isolate drift: LEDBAT fills the bottleneck queue
	// to its target by design, so the "uncongested" control run sees tens of
	// milliseconds of perfectly real queueing delay and subtracting it from a
	// drifted run subtracts noise. An application-limited flow leaves the
	// queue empty, and then every millisecond the sender believes in is the
	// drift. Measured first the other way, which is how that was found.
```

## Line 252 — func TestClockSkewCorrectionCancelsThePhantomQueue

```
// The clock-skew correction, measured against the error it exists to cancel.
//
// LEDBAT's signal is a one-way delay, which is a difference between two
// clocks, so it carries their relative rate error. Without a correction the
// reported queueing delay settles at **the delay window multiplied by the
// drift rate** -- the base is a sliding-window minimum, so it follows the
// drift but lags by the window. That was measured before the correction
// existed: 0.99x the prediction at 5000, 10000 and 20000ppm.
//
// libutp cancels it by watching the *other* direction. The same slow clock
// that inflates the peer's view of this sender makes packets from the peer
// appear to arrive sooner, so a falling base delay in that direction is the
// visible half of the bias inflating the invisible half
// (utp_internal.cpp:2002-2014). This is that, and what it should now leave
// behind is nothing.
//
// Measured with a short window and large rates rather than the real window
// and real rates, because only the product matters and the real combination
// takes minutes per point. A 2-second window at 20000ppm is the same 40ms of
// error as the default 120-second window at 333ppm.
```

## Line 283 — func TestClockSkewCorrectionCancelsThePhantomQueue

```
// Back to back, because cross-run comparisons in this harness are not
	// worth having -- see BENCHMARKS.md.
```

## Line 310 — func TestClockSkewCorrectionCancelsThePhantomQueue

```
// Paced far below the link rate, so there is no real queue to confuse
	// with a phantom one.
```

## Line 318 — func TestClockSkewCorrectionCancelsThePhantomQueue

```
// The correction has to have actually fired, and by roughly the whole
		// drift accumulated over the run. Without this the test would pass
		// for a build where drift never reached the signal at all.
```

## Line 328 — func TestClockSkewCorrectionCancelsThePhantomQueue

```
// The median, not the mean: see lateQueueMedian. The mean was what
		// failed on macOS, twice, at -5000ppm, with one stall each time.
```

## Line 341 — func TestClockSkewCorrectionCancelsThePhantomQueue

```
// A tenth of what it would otherwise be. The residual is the lag
		// between a fall in the peer's base and the shift that answers it,
		// and it scales with the rate rather than vanishing.
```

## Line 351 — func TestClockDriftYieldsShareAtASharedBottleneck

```
// Fairness at a shared bottleneck, which is what the correction is for.
//
// TestClockDriftInflatesTheDelaySignal establishes the rule -- the reported
// queueing delay settles at the delay window multiplied by the drift rate.
// The question that decides whether libutp's correction is worth building
// here is what that costs, and the answer is not what it looks like.
//
// Measured first on a single flow with the link to itself: an above-target
// phantom queue of 139ms cost about **1%** of throughput. The controller does
// back off -- the real queue it left at the bottleneck fell from 29ms to 24ms
// -- but a delay-based controller that backs off while still filling the pipe
// loses almost nothing. That run is not kept, because a 1% difference on a
// saturated link is not distinguishable from noise and asserting on it would
// be asserting on noise.
//
// Where it has to show is in competition. LEDBAT exists to yield, and a flow
// that believes it is above target when it is not yields more than its share.
// So: two flows through one bottleneck, one with a drifting clock and one
// without, and the question is what fraction of the link each ends up with.
//
// Scaled the same way as the rule test and for the same reason -- only the
// product of window and rate matters. A 2-second window at 60000ppm is the
// 120ms of phantom queue that the default 120-second window reaches at
// 1000ppm, a rate an oversubscribed virtual machine's clock really does hit.
```

## Line 387 — func TestClockDriftYieldsShareAtASharedBottleneck

```
// One bottleneck, both flows through it, both directions so that
	// acknowledgements travel too.
	//
	// The queue is large enough that it never overflows, and the test depends
	// on that. It was 256KB, 105ms at this rate, barely above one flow's
	// 100ms target, so two flows overflowed it, and whichever took the tail
	// drop halved its window and spent seconds catching up. That is loss
	// recovery, not delay-based yielding, and it swamped what this test
	// measures: with *neither* flow drifted, the split ranged 44-58% over
	// six runs, 26-76% within single seconds; with the correction on, 38.5-
	// 57.5% over twenty; with it off, 28.5-43.4% over eight. The two
	// overlapped, so no threshold separated them, and the test failed about
	// one run in ten. At 1MB (420ms) no packet is dropped and the flows yield
	// to each other on delay alone: undrifted 49.8-50.2%, corrected 50.2-
	// 50.7%, uncorrected 32.0-33.3%, six runs each.
```

## Line 531 — func TestClockDriftYieldsShareAtASharedBottleneck

```
// Both flows must see the bottleneck. A drifted flow that sees no queue
	// at all is not being corrected, it is being blinded -- which is what an
	// unbounded correction does, and it wins the link by ignoring congestion
	// rather than by being treated fairly. Measured at 4ms against 40ms
	// before the correction was made to age out.
```

## Line 541 — func TestClockDriftYieldsShareAtASharedBottleneck

```
// Uncorrected it takes 32.0-33.3%, corrected 50.2-50.7% (see the queue
	// above). The bounds sit well clear of both.
```
