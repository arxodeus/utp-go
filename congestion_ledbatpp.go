package utp_go

import (
	"math"
	"time"
)

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

// CongestionAlgorithm selects which congestion controller a connection runs.
type CongestionAlgorithm int

const (
	// AlgorithmLEDBAT is classic LEDBAT as libutp implements it. This is the
	// default, because matching the reference implementation is the default
	// everywhere else in this library too.
	AlgorithmLEDBAT CongestionAlgorithm = iota
	// AlgorithmLEDBATPP is LEDBAT++.
	AlgorithmLEDBATPP
)

func (a CongestionAlgorithm) String() string {
	switch a {
	case AlgorithmLEDBAT:
		return "LEDBAT"
	case AlgorithmLEDBATPP:
		return "LEDBAT++"
	default:
		return "unknown"
	}
}

const (
	// ledbatPPTargetDelay is LEDBAT++'s queueing-delay target. draft §4.5:
	// "LEDBAT++ default delay target of 60ms is different from the 100ms
	// value recommended in RFC6817."
	ledbatPPTargetDelay = 60 * time.Millisecond

	// ledbatPPGainCap is the 16 in GAIN = 1 / min(16, ceil(2*TARGET/base)).
	// draft §4.2, which notes implementations "MAY experiment with the
	// constant value 16".
	ledbatPPGainCap = 16.0

	// ledbatPPSlowStartExitFraction is the 3/4 in "If the queuing delay is
	// larger than 3/4ths of the target delay, exit slow start and immediately
	// move to the congestion avoidance phase" (draft §4.1).
	ledbatPPSlowStartExitFraction = 0.75

	// ledbatPPDecreaseConstant is the Constant in the §4.3 decrease formula.
	// The draft recommends 1.
	ledbatPPDecreaseConstant = 1.0

	// ledbatPPFreezeRTTs is how long the congestion window stays pinned at
	// two packets during a slowdown: "frozen at 2 packets for 2 RTT"
	// (draft §4.4).
	ledbatPPFreezeRTTs = 2

	// ledbatPPSlowdownPeriodFactor is the 9 in "9 times this duration"
	// (draft §4.4) -- the gap between the end of one slowdown and the start
	// of the next, sized so a slowdown costs at most about 10% of the
	// bottleneck's utilisation.
	ledbatPPSlowdownPeriodFactor = 9
)

// ledbatPPPhase is where a LEDBAT++ connection is in its cycle.
type ledbatPPPhase int

const (
	// ppSlowStart is the initial ramp, before any slowdown.
	ppSlowStart ledbatPPPhase = iota
	// ppCongestionAvoidance is the steady state.
	ppCongestionAvoidance
	// ppSlowdownFreeze is the two round trips with the window pinned at two
	// packets, which is what lets the queue drain so the delay samples taken
	// during it measure the path rather than the queue.
	ppSlowdownFreeze
	// ppSlowdownRamp is the slow-start ramp back up to where the window was
	// before the slowdown.
	ppSlowdownRamp
)

func (p ledbatPPPhase) String() string {
	switch p {
	case ppSlowStart:
		return "slow-start"
	case ppCongestionAvoidance:
		return "congestion-avoidance"
	case ppSlowdownFreeze:
		return "slowdown-freeze"
	case ppSlowdownRamp:
		return "slowdown-ramp"
	default:
		return "unknown"
	}
}

// ledbatPPGain is draft §4.2: GAIN = 1 / min(16, ceil(2*TARGET/base)).
//
// `base` is the base round-trip time. The effect is that the window grows
// more slowly than Reno on a short path -- where a background flow can do the
// most damage to an interactive one -- and at up to Reno's rate on a long
// one, where growing any slower would mean never using the link.
//
// It is a free function so it can be tested on its own, without a controller.
func ledbatPPGain(baseRTT time.Duration) float64 {
	target := float64(ledbatPPTargetDelay.Microseconds())
	base := float64(baseRTT.Microseconds())
	if base <= 0 {
		// No RTT sample yet. The most conservative gain is the safe answer:
		// it makes the first few acks grow the window as slowly as possible,
		// and the real value arrives with the first sample.
		return 1 / ledbatPPGainCap
	}
	divisor := math.Ceil(2 * target / base)
	if divisor < 1 {
		divisor = 1
	}
	return 1 / math.Min(ledbatPPGainCap, divisor)
}

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
// This used to use RFC 6817's increase below the target, on the reading that
// the draft gave only the decrease formula and left the increase ambiguous.
// It does not: "In LEDBAT++, with multiplicative decrease, the per RTT window
// when delay is less than target is: W += GAIN". With the proportional
// increase, two LEDBAT++ flows that left slow start at different windows kept
// most of the difference. See KNOWN-LIMITATIONS.md.
//
// A free function, so the shape of the response curve can be tested directly
// at the interesting points rather than inferred from a transfer.
func ledbatPPWindowDeltaPerRTT(gain float64, windowPackets float64, queueingDelay, target time.Duration) float64 {
	targetMicros := float64(target.Microseconds())
	if targetMicros <= 0 {
		return 0
	}
	delayRatio := float64(queueingDelay.Microseconds()) / targetMicros

	if delayRatio <= 1 {
		return gain
	}

	// draft §4.3. The decrease is proportional to both the window and the
	// overshoot, and is floored at half the window per round trip so a
	// single wild delay sample cannot collapse the connection.
	delta := gain - ledbatPPDecreaseConstant*windowPackets*(delayRatio-1)
	return math.Max(delta, -windowPackets/2)
}

// applyLedbatPP is the LEDBAT++ equivalent of applyCongestionControl, called
// once per acked packet with the controller's lock held.
func (c *defaultController) applyLedbatPP(
	baseDelayMicros uint32,
	packetDelayMicros uint32,
	bytesAcked uint32,
	rtt time.Duration,
	now time.Time,
) {
	packetSize := c.minWindowSizeBytes / 2
	if packetSize == 0 {
		packetSize = 1
	}

	// Track the base RTT, which the gain is computed from.
	if rtt > 0 && (c.minRTT == 0 || rtt < c.minRTT) {
		c.minRTT = rtt
	}

	queueingDelayMicros := int64(packetDelayMicros) - int64(baseDelayMicros)
	if queueingDelayMicros < 0 {
		queueingDelayMicros = 0
	}
	// The same clamp classic LEDBAT needs, and for the same reason: the
	// queueing delay cannot exceed the round trip, and a peer that says
	// otherwise is reporting a broken clock or lying.
	if rttMicros := rtt.Microseconds(); rttMicros > 0 && queueingDelayMicros > rttMicros {
		queueingDelayMicros = rttMicros
	}
	// The clock-drift penalty, applied here as well as in classic LEDBAT.
	//
	// This one goes beyond libutp, which has no LEDBAT++ and so no opinion.
	// The reason is that the penalty defends against a peer manipulating its
	// clock to under-measure the queue, and that attack does not care which
	// controller this end happens to be running. Leaving it out here would
	// make the opt-in algorithm the weaker choice, which is not a trade
	// anyone would be choosing knowingly. Recorded in DEVIATIONS.md.
	if penalty := c.drift.penaltyMicros(); penalty > 0 {
		queueingDelayMicros += penalty
	}
	queueingDelay := time.Duration(queueingDelayMicros) * time.Microsecond
	target := time.Duration(c.targetDelayMicros) * time.Microsecond
	gain := ledbatPPGain(c.minRTT)

	c.advanceLedbatPPPhase(now, packetSize)

	switch c.ppPhase {
	case ppSlowdownFreeze:
		// Window pinned at two packets. Nothing to adjust; the point of the
		// phase is to stop sending long enough for the queue to drain, so
		// that the delay samples arriving now measure the path.
		c.maxWindowSizeBytes = c.minWindowSizeBytes
		return

	case ppSlowStart, ppSlowdownRamp:
		// draft §4.1: exit on a queueing delay over 3/4 of the target -- but
		// "Exit slow start on excessive delay SHOULD be applied only during
		// the initial slow start." The ramp out of a slowdown runs "until the
		// congestion window reaches SSTHRESH" (§4.4) and nothing else.
		//
		// It used to exit the ramp on delay as well, and that is what split a
		// link unevenly between two LEDBAT++ flows. Their slowdowns
		// synchronise, as §4.4 intends; the one that ramps first refills the
		// queue, or the queue has not finished draining from before the
		// slowdown, and the other reads a delay over the threshold and leaves
		// its ramp at two packets. It then climbs back at GAIN packets a
		// round trip, and since the slowdown it measured was short, the next
		// comes nine short durations later and knocks it back again.
		// Measured on two flows through an 8Mb/s bottleneck with a 2MB
		// queue: the squeezed flow took 17.8-33.9% of the link while both
		// ran. See KNOWN-LIMITATIONS.md.
		if c.ppPhase == ppSlowStart &&
			float64(queueingDelay) > ledbatPPSlowStartExitFraction*float64(target) {
			c.exitLedbatPPSlowStart(now)
			return
		}

		// The initial ramp is gain-scaled, draft §4.1: increase "by that
		// number multiplied by the dynamic GAIN value". The ramp back out of
		// a slowdown is not.
		//
		// §4.4 says only "ramp up the congestion window according to the
		// slow start algorithm, until the congestion window reaches
		// SSTHRESH", and separately that a slowdown should cost "not more
		// than a 10% drop in the utilization of the bottleneck". Those two
		// statements only hold together if the ramp doubles per round trip.
		// Gain-scaled on a 20 ms path the gain is 1/6, so the window grows by
		// a seventh per round trip and climbing back from two packets to
		// fifty takes about twenty-one of them -- measured, that turned a
		// 1.3 s transfer into 2.8 s and cost more than half the throughput.
		//
		// There is also no reason for the ramp to be cautious: it is
		// returning to a window this connection was already using a moment
		// ago, and it stops at exactly that window.
		rampGain := gain
		if c.ppPhase == ppSlowdownRamp {
			rampGain = 1
		}
		grown := float64(c.maxWindowSizeBytes) + rampGain*float64(bytesAcked)
		c.maxWindowSizeBytes = clampUint32(uint32(grown), c.minWindowSizeBytes, c.maxWindowUpperBytes)
		if c.maxWindowSizeBytes >= c.ssthreshBytes {
			c.exitLedbatPPSlowStart(now)
		}
		return

	case ppCongestionAvoidance:
		windowPackets := float64(c.maxWindowSizeBytes) / float64(packetSize)
		if windowPackets <= 0 {
			windowPackets = 1
		}
		deltaPerRTT := ledbatPPWindowDeltaPerRTT(gain, windowPackets, queueingDelay, target)

		// The formula is per round trip; this ack covers bytesAcked of the
		// window, so it earns that fraction of the change.
		fraction := float64(bytesAcked) / float64(c.maxWindowSizeBytes)
		if fraction > 1 {
			fraction = 1
		}
		adjusted := float64(c.maxWindowSizeBytes) + deltaPerRTT*fraction*float64(packetSize)

		// A sender that never fills its window is limited by the
		// application, not the path, and learns nothing from the delay it
		// measures. Same rule as classic LEDBAT, same reason.
		if adjusted > float64(c.maxWindowSizeBytes) && c.applicationLimited(now) {
			adjusted = float64(c.maxWindowSizeBytes)
		}

		if adjusted < float64(c.minWindowSizeBytes) {
			adjusted = float64(c.minWindowSizeBytes)
		}
		c.maxWindowSizeBytes = clampUint32(uint32(adjusted), c.minWindowSizeBytes, c.maxWindowUpperBytes)
	}
}

// exitLedbatPPSlowStart moves to congestion avoidance and schedules the next
// slowdown.
func (c *defaultController) exitLedbatPPSlowStart(now time.Time) {
	wasRamp := c.ppPhase == ppSlowdownRamp
	c.ppPhase = ppCongestionAvoidance

	rtt := c.minRTT
	if rtt <= 0 {
		rtt = c.rtt
	}
	if rtt <= 0 {
		rtt = 100 * time.Millisecond
	}

	if wasRamp {
		// draft §4.4: the next slowdown starts nine slowdown-durations after
		// this one ended, so slowdowns cost about a tenth of the link.
		duration := now.Sub(c.ppSlowdownStartedAt)
		if duration <= 0 {
			duration = time.Duration(ledbatPPFreezeRTTs) * rtt
		}
		c.ppNextSlowdownAt = now.Add(time.Duration(ledbatPPSlowdownPeriodFactor) * duration)
		return
	}

	// draft §4.4: "the initial slowdown begins 2 RTT after the initial slow
	// start completes".
	c.ppNextSlowdownAt = now.Add(time.Duration(ledbatPPFreezeRTTs) * rtt)
}

// advanceLedbatPPPhase applies whatever phase transitions the clock calls
// for. It is separate from the window arithmetic so it can also be driven
// from the send path, where a connection that has stopped receiving acks
// would otherwise sit in a slowdown forever.
//
// Called with the lock held.
func (c *defaultController) advanceLedbatPPPhase(now time.Time, packetSize uint32) {
	rtt := c.minRTT
	if rtt <= 0 {
		rtt = c.rtt
	}
	if rtt <= 0 {
		rtt = 100 * time.Millisecond
	}

	switch c.ppPhase {
	case ppSlowdownFreeze:
		if !now.Before(c.ppFreezeUntil) {
			c.ppPhase = ppSlowdownRamp
		}

	case ppCongestionAvoidance:
		if c.ppNextSlowdownAt.IsZero() || now.Before(c.ppNextSlowdownAt) {
			return
		}
		// draft §4.4: "set SSTHRESH to the current version of the congestion
		// window CWND, and then reduce CWND to 2 packets."
		c.ppSlowdownSsthresh = c.maxWindowSizeBytes
		c.ssthreshBytes = c.maxWindowSizeBytes
		c.maxWindowSizeBytes = c.minWindowSizeBytes
		c.ppSlowdownStartedAt = now
		c.ppFreezeUntil = now.Add(time.Duration(ledbatPPFreezeRTTs) * rtt)
		c.ppPhase = ppSlowdownFreeze
	}
	_ = packetSize
}

// OnTick lets the connection drive time-based phase changes even when no acks
// are arriving -- during a slowdown the sender is pinned to two packets, and
// an application that goes quiet at the wrong moment would otherwise leave it
// there.
func (c *defaultController) OnTick(now time.Time) {
	if c.algorithm != AlgorithmLEDBATPP {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.advanceLedbatPPPhase(now, c.minWindowSizeBytes/2)
}
