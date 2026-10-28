package utp

import (
	"math"
	"time"
)

// LEDBAT++, from draft-irtf-iccrg-ledbat-plus-plus-01.
//
// Classic LEDBAT, the default, has two known weaknesses:
//
//   - Its base delay is the minimum seen over a long window. A flow running
//     long enough to build a standing queue measures that queue as the path
//     and stops backing off, while a later flow measures the true base and
//     yields to it: the latecomer advantage.
//   - Its window grows at a fixed rate per round trip regardless of the
//     path, too aggressive on a short link and too slow on a long one.
//
// LEDBAT++ addresses both with four mechanisms:
//
//	§4.1 modified slow start, exiting at 3/4 of the delay target
//	§4.2 a gain that scales with the base RTT
//	§4.3 multiplicative decrease when the delay exceeds the target
//	§4.4 periodic slowdowns, which keep the base delay accurate
//
// It is opt-in; see ConnectionConfig.CongestionAlgorithm.

// CongestionAlgorithm selects which congestion controller a connection runs.
type CongestionAlgorithm int

const (
	// AlgorithmLEDBAT is classic LEDBAT (RFC 6817), the default.
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
	// ledbatPPTargetDelay is LEDBAT++'s queuing-delay target, 60 ms
	// (draft §4.5).
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

	// ledbatPPRTTFilter is how many recent round trips the current delay is
	// the minimum of: "the minimum of the 4 most recent delay samples"
	// (draft §4.5).
	ledbatPPRTTFilter = 4

	// ledbatPPSlowdownPeriodFactor is the 9 in "9 times this duration"
	// (draft §4.4): the gap between the end of one slowdown and the start
	// of the next, so slowdowns cost about 10% of the bottleneck's
	// utilization.
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
	// packets, which lets the queue drain so delay samples taken then
	// measure the path rather than the queue.
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

// ledbatPPGain is draft §4.2: GAIN = 1 / min(16, ceil(2*TARGET/base)),
// where base is the base round-trip time. The window grows more slowly than
// Reno on a short path, where a background flow does the most harm to
// interactive traffic, and at up to Reno's rate on a long one.
func ledbatPPGain(baseRTT time.Duration) float64 {
	target := float64(ledbatPPTargetDelay.Microseconds())
	base := float64(baseRTT.Microseconds())
	if base <= 0 {
		// No RTT sample yet: use the most conservative gain until one
		// arrives.
		return 1 / ledbatPPGainCap
	}
	divisor := math.Ceil(2 * target / base)
	if divisor < 1 {
		divisor = 1
	}
	return 1 / math.Min(ledbatPPGainCap, divisor)
}

// ledbatPPWindowDeltaPerRTT returns the per-round-trip change in the
// congestion window, in packets, in congestion avoidance (draft §4.3):
//
//	delay below target:  W += GAIN
//	delay above target:  W += max( (GAIN - Constant * W * (delay/target - 1)), -W/2) )
//
// Additive increase with multiplicative decrease. With RFC 6817's
// proportional response both increase and decrease vanish near the target,
// so whatever split two flows reached persists (§3.2). A constant increase
// keeps pushing until the decrease, proportional to each flow's window,
// takes more from the larger.
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
	// overshoot, and is limited to half the window per round trip so a
	// single outlying sample cannot collapse the connection.
	delta := gain - ledbatPPDecreaseConstant*windowPackets*(delayRatio-1)
	return math.Max(delta, -windowPackets/2)
}

// pushLedbatPPRTT records a round-trip sample for LEDBAT++'s delay
// measurement. Called with the lock held, and only for a packet sent once,
// whose acknowledgement can be matched to its transmission.
func (c *defaultController) pushLedbatPPRTT(rtt time.Duration, now time.Time) {
	if rtt <= 0 {
		return
	}
	c.ppRTTRecent[c.ppRTTNext] = rtt
	c.ppRTTNext = (c.ppRTTNext + 1) % ledbatPPRTTFilter
	if c.ppRTTCount < ledbatPPRTTFilter {
		c.ppRTTCount++
	}
	c.ppRTTBase.Push(rtt, now)
	c.minRTT = c.ppRTTBase.BaseDelay()
}

// ledbatPPRTTDelay returns LEDBAT++'s base and current round trip, whose
// difference is its queuing delay. As draft §4.5 specifies, it uses round
// trips rather than one-way delay, with the current value the minimum of the
// 4 most recent samples; the base is the lowest round trip within the delay
// window.
//
// A round trip is timed entirely on the local clock, so it needs no skew
// correction and does not trust the peer, though it includes queuing on the
// return path and any acknowledgement delay. Both values are zero until the
// first sample.
func (c *defaultController) ledbatPPRTTDelay() (base, current time.Duration) {
	if c.ppRTTCount == 0 {
		return 0, 0
	}
	current = c.ppRTTRecent[0]
	for i := 1; i < c.ppRTTCount; i++ {
		if c.ppRTTRecent[i] < current {
			current = c.ppRTTRecent[i]
		}
	}
	base = c.minRTT
	if base <= 0 || base > current {
		base = current
	}
	return base, current
}

// applyLedbatPP is the LEDBAT++ equivalent of applyCongestionControl, called
// once per acked packet with the controller's lock held. baseMicros and
// currentMicros are the base and current round trip (ledbatPPRTTDelay);
// their difference is the queueing delay.
func (c *defaultController) applyLedbatPP(
	baseMicros uint32,
	currentMicros uint32,
	bytesAcked uint32,
	sentWindow uint32,
	rtt time.Duration,
	now time.Time,
) {
	packetSize := c.minWindowSizeBytes / 2
	if packetSize == 0 {
		packetSize = 1
	}

	queueingDelayMicros := int64(currentMicros) - int64(baseMicros)
	if queueingDelayMicros < 0 {
		queueingDelayMicros = 0
	}
	// The queuing delay cannot exceed the round trip. With the current
	// value filtered this does not bind, but it keeps the rule true for any
	// input.
	if rttMicros := rtt.Microseconds(); rttMicros > 0 && queueingDelayMicros > rttMicros {
		queueingDelayMicros = rttMicros
	}
	// No clock-drift penalty: the delay is measured from round trips, so
	// the peer's clock takes no part in it.
	queueingDelay := time.Duration(queueingDelayMicros) * time.Microsecond
	target := time.Duration(c.targetDelayMicros) * time.Microsecond
	gain := ledbatPPGain(c.minRTT)

	c.advanceLedbatPPPhase(now, packetSize)

	switch c.ppPhase {
	case ppSlowdownFreeze:
		// Window pinned at two packets. Nothing to adjust: the phase exists to
		// let the queue drain, so the delay samples now arriving measure the
		// path.
		c.maxWindowSizeBytes = c.minWindowSizeBytes
		return

	case ppSlowStart, ppSlowdownRamp:
		// draft §4.1: exit on a queuing delay over 3/4 of the target, but
		// "only during the initial slow start". The ramp out of a slowdown
		// runs until the window reaches SSTHRESH (§4.4): two flows'
		// slowdowns synchronize, and a ramp that also exited on delay would
		// see the other flow's refilled queue and stop at two packets.
		if c.ppPhase == ppSlowStart &&
			float64(queueingDelay) > ledbatPPSlowStartExitFraction*float64(target) {
			c.exitLedbatPPSlowStart(now)
			return
		}

		// The initial ramp is gain-scaled (draft §4.1); the ramp out of a
		// slowdown is not. §4.4 requires a slowdown to cost no more than
		// about 10% of the bottleneck, which holds only if the ramp doubles
		// per round trip, and the ramp only returns to a window the
		// connection was just using.
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
		// window, so it earns that fraction of the change. The fraction is
		// taken of the window the packet was sent in when that is larger,
		// since earlier acks of the same round trip have already grown the
		// current window.
		fraction := float64(bytesAcked) / float64(c.maxWindowSizeBytes)
		if sendWindowCredit.Load() && sentWindow > 0 {
			fraction = math.Max(fraction, float64(bytesAcked)/float64(sentWindow))
		}
		if fraction > 1 {
			fraction = 1
		}
		// Carry the fraction of a byte the last update could not add.
		// Updates can be a few bytes each when the gain is small and the
		// window large, and truncating each would lose a significant share
		// of the growth.
		adjusted := float64(c.maxWindowSizeBytes) + c.ppWindowFraction + deltaPerRTT*fraction*float64(packetSize)

		// A sender that never fills its window is limited by the
		// application, not the path, and learns nothing from the delay it
		// measures. Same rule as classic LEDBAT.
		if adjusted > float64(c.maxWindowSizeBytes) && c.applicationLimited(now) {
			adjusted = float64(c.maxWindowSizeBytes)
		}

		if adjusted < float64(c.minWindowSizeBytes) {
			adjusted = float64(c.minWindowSizeBytes)
		}
		c.maxWindowSizeBytes = clampUint32(uint32(adjusted), c.minWindowSizeBytes, c.maxWindowUpperBytes)
		c.ppWindowFraction = 0
		if whole := math.Floor(adjusted); whole == float64(c.maxWindowSizeBytes) {
			c.ppWindowFraction = adjusted - whole
		}
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

// advanceLedbatPPPhase applies the phase transitions the clock calls for.
// It is separate from the window arithmetic so the send path can drive it
// too; otherwise a connection receiving no acks would stay in a slowdown.
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

// OnTick drives time-based phase changes when no acks are arriving. During a
// slowdown the sender is pinned to two packets, and an application that goes
// quiet at that moment would otherwise leave it there.
func (c *defaultController) OnTick(now time.Time) {
	if c.algorithm != AlgorithmLEDBATPP {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.advanceLedbatPPPhase(now, c.minWindowSizeBytes/2)
}
