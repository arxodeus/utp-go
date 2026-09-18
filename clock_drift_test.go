package utp_go

import (
	"testing"
	"time"
)

// The drift estimator, checked against values worked out by hand rather than
// only through a transfer.
//
// The arithmetic is wrapping uint32 throughout, and a wrap read the wrong way
// round would show up as a penalty applied to an honest peer. That is the
// failure worth being careful about, and it is not one an end-to-end test
// would localise.

func driftAt(t *testing.T, start time.Time) (*driftEstimator, func(sample uint32, at time.Duration)) {
	t.Helper()
	d := newDriftEstimator(start)
	return d, func(sample uint32, at time.Duration) {
		d.push(sample, start.Add(at))
	}
}

// A slot closes only once its interval has passed, and its average is the mean
// of what arrived.
func TestDriftSlotAverage(t *testing.T) {
	start := time.Unix(0, 0)
	d, push := driftAt(t, start)

	// All within the first slot. The first sample sets the base, so the
	// samples relative to it are 0, 1000 and 2000: mean 1000.
	push(10000, time.Second)
	push(11000, 2*time.Second)
	push(12000, 3*time.Second)
	if d.average != 0 {
		t.Errorf("the slot closed early: average %d", d.average)
	}
	if d.samples != 3 {
		t.Errorf("accumulated %d samples, expected 3", d.samples)
	}

	// Past the boundary: the slot closes on this push, and the sample that
	// crosses it is included before the mean is taken.
	push(13000, 6*time.Second)
	if want := int32((0 + 1000 + 2000 + 3000) / 4); d.average != want {
		t.Errorf("slot average %d, expected %d", d.average, want)
	}
	if d.samples != 0 {
		t.Errorf("the slot did not reset: %d samples carried over", d.samples)
	}
}

// A sample below the base is a negative offset, and the wrap is read the short
// way round.
func TestDriftSampleBelowBase(t *testing.T) {
	start := time.Unix(0, 0)
	d, push := driftAt(t, start)

	push(50000, time.Second)   // sets the base
	push(49000, 2*time.Second) // 1000 below it
	push(48000, 3*time.Second) // 2000 below it
	push(47000, 6*time.Second) // closes the slot, 3000 below

	if want := int32((0 - 1000 - 2000 - 3000) / 4); d.average != want {
		t.Errorf("slot average %d, expected %d -- a sample below the base must "+
			"count negative", d.average, want)
	}
}

// The delays come off a wrapping 32-bit microsecond clock, so a base near the
// top and a sample just past zero are 1000 apart, not four billion.
func TestDriftWrapsAroundZero(t *testing.T) {
	start := time.Unix(0, 0)
	d, push := driftAt(t, start)

	const nearTop = ^uint32(0) - 499 // 500 below the wrap point
	push(nearTop, time.Second)       // base
	push(500, 6*time.Second)         // 1000 past it, having wrapped

	if want := int32((0 + 1000) / 2); d.average != want {
		t.Errorf("slot average %d, expected %d -- the wrap was read the long way "+
			"round", d.average, want)
	}
}

// The same in the other direction: a base just past zero and a sample below
// it, which wraps downwards.
func TestDriftWrapsBelowZero(t *testing.T) {
	start := time.Unix(0, 0)
	d, push := driftAt(t, start)

	push(500, time.Second)              // base
	push(^uint32(0)-499, 6*time.Second) // 1000 below it, having wrapped

	if want := int32((0 - 1000) / 2); d.average != want {
		t.Errorf("slot average %d, expected %d", d.average, want)
	}
}

// The slope between two slots is smoothed 7:1 towards the history.
func TestDriftIsSmoothed(t *testing.T) {
	start := time.Unix(0, 0)
	d, push := driftAt(t, start)

	// Exactly one slot closes: the base, then a sample 6000 below it that
	// crosses the boundary. The slot's mean is -3000 against a previous
	// average of zero, so the slope is -3000.
	push(100000, time.Second)
	push(94000, 6*time.Second)

	if d.driftRaw != -3000 {
		t.Fatalf("raw slope %d, expected -3000", d.driftRaw)
	}
	// First slot, so the history it is averaged against is zero:
	// drift = (0*7 + -3000)/8.
	if want := int64(-3000) / 8; d.drift != want {
		t.Errorf("smoothed drift %d, expected %d", d.drift, want)
	}

	// A second identical slot moves it further, but by less than the raw
	// slope, which is what the smoothing is for.
	push(88000, 7*time.Second)
	push(88000, 12*time.Second)
	if d.drift <= -3000 || d.drift >= int64(-3000)/8 {
		t.Errorf("after two slots the smoothed drift is %d; it should sit between "+
			"one eighth of the slope and the slope itself", d.drift)
	}
}

// The penalty is zero until the drift passes the threshold, and then grows as
// libutp's formula says.
func TestDriftPenalty(t *testing.T) {
	cases := []struct {
		drift int64
		want  int64
	}{
		{0, 0},
		{500000, 0},                       // drifting the harmless way
		{-driftPenaltyThresholdMicros, 0}, // exactly at the threshold, still nothing
		{-driftPenaltyThresholdMicros - 7, 1},
		{-300000, (300000 - 200000) / 7},
		{-900000, (900000 - 200000) / 7},
	}
	for _, tc := range cases {
		d := &driftEstimator{drift: tc.drift}
		if got := d.penaltyMicros(); got != tc.want {
			t.Errorf("drift %d: penalty %d, expected %d", tc.drift, got, tc.want)
		}
	}
}

// A zero sample means "the peer has no measurement yet", not "no delay", and
// must not enter the average.
func TestDriftIgnoresZeroSamples(t *testing.T) {
	start := time.Unix(0, 0)
	d, push := driftAt(t, start)

	push(0, time.Second)
	if d.haveBase {
		t.Error("a zero sample set the base; zero means the peer has no measurement")
	}
	if d.samples != 0 {
		t.Errorf("a zero sample was accumulated: %d samples", d.samples)
	}
}

// A slot with nothing in it moves the boundary and draws no conclusion, rather
// than dividing by zero.
func TestDriftEmptySlot(t *testing.T) {
	start := time.Unix(0, 0)
	d := newDriftEstimator(start)
	d.closeSlot(start.Add(6 * time.Second))
	if d.drift != 0 || d.average != 0 {
		t.Errorf("an empty slot produced drift %d average %d", d.drift, d.average)
	}
	if !d.slotEnds.After(start.Add(driftSlotInterval)) {
		t.Error("the slot boundary did not move past an empty slot")
	}
}

// Renormalisation keeps the average bounded while leaving the slope alone.
//
// Without it a steadily rising delay walks the average up forever and
// eventually overflows an int32. libutp's comment: "since we're only
// interested in the slope of the curve formed by the average delay samples,
// we can cancel out the actual offset to make sure we won't have problems
// with wrapping" (utp_internal.cpp:2076-2080).
func TestDriftRenormalisationBoundsTheAverage(t *testing.T) {
	start := time.Unix(0, 0)
	d := newDriftEstimator(start)

	// Forty slots, each 8000 microseconds further out than the last. Left
	// unnormalised the average would reach 320,000 and keep going.
	const step = 8000
	sample := uint32(1000)
	at := time.Second
	for slot := 0; slot < 40; slot++ {
		d.push(sample, start.Add(at))
		at += 5 * time.Second
		d.push(sample, start.Add(at))
		sample += step
		at += time.Second
	}

	if d.driftRaw != step {
		t.Errorf("raw slope %d after 40 slots, expected a steady %d", d.driftRaw, step)
	}
	// The average is held near the slope rather than accumulating it.
	if d.average > 2*step || d.average < -2*step {
		t.Errorf("average %d after 40 slots of +%d; renormalisation should keep it "+
			"within a slot or two of zero, not accumulate", d.average, step)
	}
}
