# clock_drift_test.go

Every comment in `clock_drift_test.go` at e56c9aa, verbatim.

## Line 8

```
// The drift estimator, checked against values worked out by hand rather than
// only through a transfer.
//
// The arithmetic is wrapping uint32 throughout, and a wrap read the wrong way
// round would show up as a penalty applied to an honest peer. That is the
// failure worth being careful about, and it is not one an end-to-end test
// would localise.
```

## Line 24 — func TestDriftSlotAverage

```
// A slot closes only once its interval has passed, and its average is the mean
// of what arrived.
```

## Line 30 — func TestDriftSlotAverage

```
// All within the first slot. The first sample sets the base, so the
	// samples relative to it are 0, 1000 and 2000: mean 1000.
```

## Line 42 — func TestDriftSlotAverage

```
// Past the boundary: the slot closes on this push, and the sample that
	// crosses it is included before the mean is taken.
```

## Line 53 — func TestDriftSampleBelowBase

```
// A sample below the base is a negative offset, and the wrap is read the short
// way round.
```

## Line 59 — func TestDriftSampleBelowBase

```
// sets the base
```

## Line 60 — func TestDriftSampleBelowBase

```
// 1000 below it
```

## Line 61 — func TestDriftSampleBelowBase

```
// 2000 below it
```

## Line 62 — func TestDriftSampleBelowBase

```
// closes the slot, 3000 below
```

## Line 70 — func TestDriftWrapsAroundZero

```
// The delays come off a wrapping 32-bit microsecond clock, so a base near the
// top and a sample just past zero are 1000 apart, not four billion.
```

## Line 76 — func TestDriftWrapsAroundZero

```
// 500 below the wrap point
```

## Line 77 — func TestDriftWrapsAroundZero

```
// base
```

## Line 78 — func TestDriftWrapsAroundZero

```
// 1000 past it, having wrapped
```

## Line 86 — func TestDriftWrapsBelowZero

```
// The same in the other direction: a base just past zero and a sample below
// it, which wraps downwards.
```

## Line 92 — func TestDriftWrapsBelowZero

```
// base
```

## Line 93 — func TestDriftWrapsBelowZero

```
// 1000 below it, having wrapped
```

## Line 100 — func TestDriftIsSmoothed

```
// The slope between two slots is smoothed 7:1 towards the history.
```

## Line 105 — func TestDriftIsSmoothed

```
// Exactly one slot closes: the base, then a sample 6000 below it that
	// crosses the boundary. The slot's mean is -3000 against a previous
	// average of zero, so the slope is -3000.
```

## Line 114 — func TestDriftIsSmoothed

```
// First slot, so the history it is averaged against is zero:
	// drift = (0*7 + -3000)/8.
```

## Line 120 — func TestDriftIsSmoothed

```
// A second identical slot moves it further, but by less than the raw
	// slope, which is what the smoothing is for.
```

## Line 130 — func TestDriftPenalty

```
// The penalty is zero until the drift passes the threshold, and then grows as
// libutp's formula says.
```

## Line 138 — func TestDriftPenalty

```
// drifting the harmless way
```

## Line 139 — func TestDriftPenalty

```
// exactly at the threshold, still nothing
```

## Line 152 — func TestDriftIgnoresZeroSamples

```
// A zero sample means "the peer has no measurement yet", not "no delay", and
// must not enter the average.
```

## Line 167 — func TestDriftEmptySlot

```
// A slot with nothing in it moves the boundary and draws no conclusion, rather
// than dividing by zero.
```

## Line 181 — func TestDriftRenormalisationBoundsTheAverage

```
// Renormalisation keeps the average bounded while leaving the slope alone.
//
// Without it a steadily rising delay walks the average up forever and
// eventually overflows an int32. libutp's comment: "since we're only
// interested in the slope of the curve formed by the average delay samples,
// we can cancel out the actual offset to make sure we won't have problems
// with wrapping" (utp_internal.cpp:2076-2080).
```

## Line 192 — func TestDriftRenormalisationBoundsTheAverage

```
// Forty slots, each 8000 microseconds further out than the last. Left
	// unnormalised the average would reach 320,000 and keep going.
```

## Line 208 — func TestDriftRenormalisationBoundsTheAverage

```
// The average is held near the slope rather than accumulating it.
```

## Line 215 — func TestDriftBaseOfZeroIsReseeded

```
// libutp takes a base of zero as unset and seeds it from the next sample
// (utp_internal.cpp:2028), including when a renormalisation has left it there.
```

## Line 221 — func TestDriftBaseOfZeroIsReseeded

```
// as a renormalisation can leave it
```
