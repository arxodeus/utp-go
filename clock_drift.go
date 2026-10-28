package utp

import "time"

// driftSlotInterval is the length of each averaging slot.
const driftSlotInterval = 5 * time.Second

// driftPenaltyThresholdMicros is the drift, in microseconds per slot, beyond
// which the congestion controller adds a penalty to its measured delay.
// 200000 per five seconds is 40,000 ppm, far beyond any oscillator's drift:
// the penalty guards against a peer deliberately slowing its clock, not
// against hardware error.
const driftPenaltyThresholdMicros = 200000

// driftPenaltyDivisor scales the penalty.
const driftPenaltyDivisor = 7

// driftEstimator tracks the slope of the delay the peer reports for our
// packets.
//
// It complements the base-delay shift in baseDelayHist, which corrects the
// delay measurement when the peer's base delay falls, as it does with
// ordinary drift between honest clocks. This estimator corrects nothing: it
// watches the long-run slope and, past a threshold far outside honest
// hardware, inflates the delay the congestion controller sees so the flow
// yields rather than gains from the drift.
//
// Arithmetic is modulo 2^32 throughout, since the delays are uint32
// microsecond counts from a wrapping clock.
type driftEstimator struct {
	// base is the offset samples are measured against, so a wrapping uint32
	// can be averaged as a signed value. It is renormalized in closeSlot.
	// Zero means unset.
	base uint32

	// sum and samples accumulate the current slot.
	sum     int64
	samples int64

	// average is the mean of the last completed slot.
	average int32

	// slotEnds is when the current slot closes. The first slot starts when
	// the connection is created, not at the first sample.
	slotEnds time.Time

	// drift is the slope smoothed with a 7:1 rolling average; driftRaw is the
	// last unsmoothed slope.
	drift    int64
	driftRaw int32
}

func newDriftEstimator(now time.Time) *driftEstimator {
	return &driftEstimator{slotEnds: now.Add(driftSlotInterval)}
}

// push records one delay the peer reported for our packets: the reply_micro
// field of an incoming packet. Zero means the peer has no measurement yet
// and is ignored.
func (d *driftEstimator) push(sample uint32, now time.Time) {
	if sample == 0 {
		return
	}
	// Zero means unset, so a base renormalized to exactly zero is seeded
	// again from the next sample.
	if d.base == 0 {
		d.base = sample
	}

	// The signed distance from base to sample modulo 2^32: compute both
	// differences and take the smaller, the short way round.
	distDown := d.base - sample // wrapping
	distUp := sample - d.base   // wrapping
	if distDown > distUp {
		// base is below sample: a positive sample.
		d.sum += int64(distUp)
	} else {
		// base is at or above sample: a negative one.
		d.sum -= int64(distDown)
	}
	d.samples++

	if !now.After(d.slotEnds) {
		return
	}
	d.closeSlot(now)
}

// closeSlot ends the averaging slot and folds its slope into the estimate.
func (d *driftEstimator) closeSlot(now time.Time) {
	if d.samples == 0 {
		// No samples this slot, so there is no slope; move the boundary and
		// draw no conclusion.
		d.slotEnds = d.slotEnds.Add(driftSlotInterval)
		return
	}

	previous := d.average
	d.average = int32(d.sum / d.samples)
	d.slotEnds = d.slotEnds.Add(driftSlotInterval)
	d.sum, d.samples = 0, 0

	// Renormalize around zero. Only the slope matters, so shifting both
	// values keeps them away from the ends of an int32 however long the
	// connection runs.
	low, high := previous, d.average
	if high < low {
		low, high = high, low
	}
	adjust := int32(0)
	switch {
	case low > 0:
		adjust = -low
	case high < 0:
		adjust = -high
	}
	if adjust != 0 {
		d.base = uint32(int64(d.base) - int64(adjust)) // wrapping
		d.average += adjust
		previous += adjust
	}

	slope := int64(d.average) - int64(previous)
	// A rolling average weighted 7:1 towards the history.
	d.drift = (d.drift*7 + slope) / 8
	d.driftRaw = int32(slope)
	_ = now
}

// penaltyMicros returns the amount to add to the measured queuing delay.
//
// It is zero unless the drift is more negative than the threshold, the
// direction in which a flow under-measures the queue and takes more than its
// share. Drift the other way makes the flow over-measure and yield, which
// costs only the drifting peer.
func (d *driftEstimator) penaltyMicros() int64 {
	if d.drift >= -driftPenaltyThresholdMicros {
		return 0
	}
	return (-d.drift - driftPenaltyThresholdMicros) / driftPenaltyDivisor
}
