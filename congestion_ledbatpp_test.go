package utp_go

import (
	"math"
	"testing"
	"time"
)

// GAIN = 1 / min(16, ceil(2*TARGET/base)), draft §4.2.
//
// The point of the formula is that a background flow grows more slowly than
// Reno on a short path -- where it can do the most damage to an interactive
// flow -- and at up to Reno's rate on a long one, where growing any slower
// would mean never using the link. The cases below pin both ends and the
// boundary between them.
func TestLedbatPPGain(t *testing.T) {
	const target = 60 * time.Millisecond

	cases := []struct {
		name    string
		baseRTT time.Duration
		want    float64
	}{
		{"no sample yet is the most conservative gain", 0, 1.0 / 16},
		{"1ms LAN path is capped at 1/16", time.Millisecond, 1.0 / 16},
		{"7.5ms path: ceil(120/7.5) = 16, exactly at the cap", 7500 * time.Microsecond, 1.0 / 16},
		{"8ms path: ceil(120/8) = 15", 8 * time.Millisecond, 1.0 / 15},
		{"20ms path: ceil(120/20) = 6", 20 * time.Millisecond, 1.0 / 6},
		{"60ms path: ceil(120/60) = 2", 60 * time.Millisecond, 1.0 / 2},
		{"120ms path: ceil(120/120) = 1, Reno's rate", 120 * time.Millisecond, 1.0},
		{"200ms path stays at 1, never faster than Reno", 200 * time.Millisecond, 1.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ledbatPPGain(tc.baseRTT)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("ledbatPPGain(%v) = %v, want %v", tc.baseRTT, got, tc.want)
			}
			if got > 1.0 {
				t.Errorf("gain %v exceeds 1: LEDBAT++ must never grow faster than Reno", got)
			}
			if got <= 0 {
				t.Errorf("gain %v is not positive", got)
			}
		})
	}
	_ = target
}

// The window response curve, at the points that matter. draft §4.3.
func TestLedbatPPWindowDeltaPerRTT(t *testing.T) {
	const target = 60 * time.Millisecond
	const gain = 1.0 / 6 // a 20ms path

	t.Run("no queueing delay grows by the full gain", func(t *testing.T) {
		got := ledbatPPWindowDeltaPerRTT(gain, 100, 0, target)
		if math.Abs(got-gain) > 1e-9 {
			t.Errorf("delta = %v, want the full gain %v", got, gain)
		}
	})

	t.Run("below the target the increase is the full gain, whatever the delay", func(t *testing.T) {
		// "the per RTT window when delay is less than target is: W += GAIN".
		// Not RFC 6817's GAIN * (1 - delay/target), which fades to nothing
		// near the target and with it the pressure that makes two flows
		// converge.
		for _, delay := range []time.Duration{target / 4, target / 2, target * 9 / 10, target} {
			got := ledbatPPWindowDeltaPerRTT(gain, 100, delay, target)
			if math.Abs(got-gain) > 1e-9 {
				t.Errorf("delay %v: delta = %v, want the full gain %v", delay, got, gain)
			}
		}
	})

	t.Run("the curve is continuous at the target", func(t *testing.T) {
		below := ledbatPPWindowDeltaPerRTT(gain, 100, target, target)
		above := ledbatPPWindowDeltaPerRTT(gain, 100, target+time.Microsecond, target)
		if math.Abs(below-above) > 0.01 {
			t.Errorf("delta jumps from %v to %v across the target", below, above)
		}
	})

	t.Run("growth never exceeds the gain, whatever the window", func(t *testing.T) {
		for _, w := range []float64{1, 10, 100, 1000, 10000} {
			got := ledbatPPWindowDeltaPerRTT(gain, w, 0, target)
			if got > gain+1e-9 {
				t.Errorf("window %v grew by %v per RTT, more than the gain %v", w, got, gain)
			}
		}
	})

	t.Run("overshoot shrinks the window proportionally", func(t *testing.T) {
		// 10% over target, 100-packet window: gain - 1*100*0.1 = gain - 10.
		got := ledbatPPWindowDeltaPerRTT(gain, 100, target*110/100, target)
		want := gain - 10
		if math.Abs(got-want) > 1e-6 {
			t.Errorf("delta = %v, want %v", got, want)
		}
		if got >= 0 {
			t.Errorf("delta %v is not a decrease, but the delay is over target", got)
		}
	})

	t.Run("the decrease is floored at half the window", func(t *testing.T) {
		// Ten times the target would otherwise take 900 packets off a
		// 100-packet window.
		got := ledbatPPWindowDeltaPerRTT(gain, 100, target*10, target)
		if got < -50 {
			t.Errorf("delta = %v, want no worse than -50: the floor is -W/2", got)
		}
		if math.Abs(got-(-50)) > 1e-9 {
			t.Errorf("delta = %v, want exactly the floor -50", got)
		}
	})

	t.Run("a wild delay sample cannot collapse the window", func(t *testing.T) {
		for _, delay := range []time.Duration{target * 100, target * 10000, time.Hour} {
			got := ledbatPPWindowDeltaPerRTT(gain, 200, delay, target)
			if got < -100 {
				t.Errorf("a %v delay took %v packets off a 200-packet window; the floor is -100",
					delay, got)
			}
		}
	})
}

// A LEDBAT++ connection runs slowdowns: it periodically drops to two packets
// for two round trips so the queue drains and the base-delay estimate is
// taken against an empty path rather than against its own standing queue.
//
// That mechanism is what LEDBAT++ exists for -- without it a long-running
// flow measures its own queue as the path and stops yielding at all -- so it
// is tested directly rather than inferred from a transfer.
func TestLedbatPPSlowdownCycle(t *testing.T) {
	config := defaultCtrlConfig()
	config.Algorithm = AlgorithmLEDBATPP
	ctrl := newDefaultController(config)

	packetSize := ctrl.minWindowSizeBytes / 2
	rtt := 20 * time.Millisecond
	ctrl.minRTT = rtt

	if ctrl.ppPhase != ppSlowStart {
		t.Fatalf("a new LEDBAT++ connection starts in %v, want slow start", ctrl.ppPhase)
	}
	if ctrl.maxWindowSizeBytes != ctrl.minWindowSizeBytes {
		t.Errorf("initial window is %d bytes, want two packets (%d): draft §4.1",
			ctrl.maxWindowSizeBytes, ctrl.minWindowSizeBytes)
	}
	if got := time.Duration(ctrl.targetDelayMicros) * time.Microsecond; got != ledbatPPTargetDelay {
		t.Errorf("delay target is %v, want %v: draft §4.5", got, ledbatPPTargetDelay)
	}

	now := time.Now()

	// Reach congestion avoidance, and with it a scheduled first slowdown.
	ctrl.ppPhase = ppCongestionAvoidance
	ctrl.maxWindowSizeBytes = 64 * packetSize
	ctrl.exitLedbatPPSlowStart(now)
	if ctrl.ppNextSlowdownAt.IsZero() {
		t.Fatal("leaving slow start did not schedule a slowdown")
	}
	wantFirst := now.Add(time.Duration(ledbatPPFreezeRTTs) * rtt)
	if !ctrl.ppNextSlowdownAt.Equal(wantFirst) {
		t.Errorf("first slowdown scheduled for %v after slow start, want %v: draft §4.4 says 2 RTT",
			ctrl.ppNextSlowdownAt.Sub(now), wantFirst.Sub(now))
	}

	// Before it is due, nothing happens.
	ctrl.advanceLedbatPPPhase(now.Add(rtt), packetSize)
	if ctrl.ppPhase != ppCongestionAvoidance {
		t.Fatalf("slowdown started early: phase %v one RTT in", ctrl.ppPhase)
	}

	// When it is due, the window drops to two packets and ssthresh records
	// where it was.
	windowBefore := ctrl.maxWindowSizeBytes
	slowdownAt := now.Add(2 * rtt)
	ctrl.advanceLedbatPPPhase(slowdownAt, packetSize)
	if ctrl.ppPhase != ppSlowdownFreeze {
		t.Fatalf("phase is %v when the slowdown was due, want slowdown-freeze", ctrl.ppPhase)
	}
	if ctrl.maxWindowSizeBytes != ctrl.minWindowSizeBytes {
		t.Errorf("window is %d during a slowdown, want two packets (%d)",
			ctrl.maxWindowSizeBytes, ctrl.minWindowSizeBytes)
	}
	if ctrl.ssthreshBytes != windowBefore {
		t.Errorf("ssthresh is %d, want the window from before the slowdown (%d)",
			ctrl.ssthreshBytes, windowBefore)
	}

	// It stays frozen for two round trips, not one.
	ctrl.advanceLedbatPPPhase(slowdownAt.Add(rtt), packetSize)
	if ctrl.ppPhase != ppSlowdownFreeze {
		t.Errorf("phase is %v one RTT into the freeze, want it still frozen: draft §4.4 says 2 RTT",
			ctrl.ppPhase)
	}

	// Then it ramps back up.
	rampAt := slowdownAt.Add(2 * rtt)
	ctrl.advanceLedbatPPPhase(rampAt, packetSize)
	if ctrl.ppPhase != ppSlowdownRamp {
		t.Fatalf("phase is %v after the freeze, want slowdown-ramp", ctrl.ppPhase)
	}

	// When the ramp finishes, the next slowdown is nine slowdown-durations
	// out, so slowdowns cost about a tenth of the link.
	endAt := rampAt.Add(3 * rtt)
	ctrl.exitLedbatPPSlowStart(endAt)
	if ctrl.ppPhase != ppCongestionAvoidance {
		t.Fatalf("phase is %v after the ramp, want congestion avoidance", ctrl.ppPhase)
	}
	duration := endAt.Sub(slowdownAt)
	wantNext := endAt.Add(time.Duration(ledbatPPSlowdownPeriodFactor) * duration)
	if !ctrl.ppNextSlowdownAt.Equal(wantNext) {
		t.Errorf("next slowdown in %v, want %v (%d x the %v the last one took)",
			ctrl.ppNextSlowdownAt.Sub(endAt), wantNext.Sub(endAt),
			ledbatPPSlowdownPeriodFactor, duration)
	}

	// Which is to say: at most about a tenth of the time is spent slowed
	// down. That is the property the constant exists to give, so assert it
	// rather than only the constant.
	cycle := duration + ctrl.ppNextSlowdownAt.Sub(endAt)
	if fraction := float64(duration) / float64(cycle); fraction > 0.11 {
		t.Errorf("slowdowns occupy %.1f%% of the cycle, want no more than about 10%%",
			fraction*100)
	}
}

// Slow start ends when the queueing delay reaches three quarters of the
// target, not when it reaches the target. draft §4.1.
//
// The round trips passed in below are the *measured* ones, so they include
// the queue: a 48ms standing queue means a round trip of at least 48ms. Using
// a smaller RTT would be physically impossible, and the clamp that says so
// would swallow the delay -- which is the clamp doing its job, not the
// slow-start rule failing.
func TestLedbatPPSlowStartExitsAtThreeQuartersTarget(t *testing.T) {
	config := defaultCtrlConfig()
	config.Algorithm = AlgorithmLEDBATPP
	ctrl := newDefaultController(config)
	ctrl.minRTT = 20 * time.Millisecond
	ctrl.maxWindowSizeBytes = 16 * (ctrl.minWindowSizeBytes / 2)

	target := time.Duration(ctrl.targetDelayMicros) * time.Microsecond
	now := time.Now()

	// Half the target: still ramping.
	half := target / 2
	ctrl.applyLedbatPP(0, uint32(half.Microseconds()), 1024, ctrl.minRTT+half, now)
	if ctrl.ppPhase != ppSlowStart {
		t.Fatalf("left slow start at half the target delay, phase %v", ctrl.ppPhase)
	}

	// Four fifths of it: over the three-quarter line, so out.
	fourFifths := target * 4 / 5
	ctrl.applyLedbatPP(0, uint32(fourFifths.Microseconds()), 1024, ctrl.minRTT+fourFifths, now)
	if ctrl.ppPhase != ppCongestionAvoidance {
		t.Errorf("phase is %v at 4/5 of the target delay, want congestion avoidance", ctrl.ppPhase)
	}
}

// The queueing delay a peer reports is clamped to the round trip actually
// measured, in LEDBAT++ as in classic LEDBAT. A peer claiming a queue longer
// than the round trip is reporting a broken clock or lying, and acting on it
// would end slow start -- or collapse the window -- on nothing.
func TestLedbatPPClampsDelayToRoundTrip(t *testing.T) {
	config := defaultCtrlConfig()
	config.Algorithm = AlgorithmLEDBATPP
	ctrl := newDefaultController(config)
	ctrl.minRTT = 5 * time.Millisecond
	ctrl.maxWindowSizeBytes = 16 * (ctrl.minWindowSizeBytes / 2)

	// A reported queue of a full second on a 5ms path.
	ctrl.applyLedbatPP(0, uint32(time.Second.Microseconds()), 1024, 5*time.Millisecond, time.Now())

	if ctrl.ppPhase != ppSlowStart {
		t.Errorf("a one-second delay claim on a 5ms path ended slow start; it should be clamped to the round trip")
	}
	if ctrl.maxWindowSizeBytes < ctrl.minWindowSizeBytes {
		t.Errorf("window fell below its floor to %d", ctrl.maxWindowSizeBytes)
	}
}

// Classic LEDBAT is the default, and selecting it changes nothing about the
// controller's shape. A connection that does not ask for LEDBAT++ must not
// get any of it.
func TestClassicLedbatIsTheDefault(t *testing.T) {
	cfg := NewConnectionConfig()
	if cfg.CongestionAlgorithm != AlgorithmLEDBAT {
		t.Errorf("default algorithm is %v, want %v", cfg.CongestionAlgorithm, AlgorithmLEDBAT)
	}

	ctrl := newDefaultController(fromConnConfig(cfg))
	if got := time.Duration(ctrl.targetDelayMicros) * time.Microsecond; got != defaultTargetMicros {
		t.Errorf("default target delay is %v, want libutp's %v", got, defaultTargetMicros)
	}
	if !ctrl.slowStart {
		t.Error("classic LEDBAT should start in libutp's slow start")
	}

	// OnTick is a no-op for classic LEDBAT.
	before := ctrl.maxWindowSizeBytes
	ctrl.OnTick(time.Now().Add(time.Hour))
	if ctrl.maxWindowSizeBytes != before {
		t.Errorf("OnTick changed a classic LEDBAT window from %d to %d", before, ctrl.maxWindowSizeBytes)
	}
	if ctrl.ppPhase != ppSlowStart {
		t.Errorf("classic LEDBAT touched LEDBAT++ phase state: %v", ctrl.ppPhase)
	}
}

// The window ceiling must never be below the window floor. It was, for any
// caller that did not set WindowSize, which was every direct user of
// defaultCtrlConfig -- and a ceiling of zero clamps the window shut on the
// first ack.
func TestWindowCeilingIsNeverBelowTheFloor(t *testing.T) {
	for _, tc := range []struct {
		name       string
		windowSize uint32
	}{
		{"unset", 0},
		{"absurdly small", 1},
		{"below the floor", 512},
		{"the default", DefaultWindowSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := defaultCtrlConfig()
			config.WindowSize = tc.windowSize
			ctrl := newDefaultController(config)
			if ctrl.maxWindowUpperBytes < ctrl.minWindowSizeBytes {
				t.Fatalf("ceiling %d is below the floor %d", ctrl.maxWindowUpperBytes, ctrl.minWindowSizeBytes)
			}
			if got := clampUint32(ctrl.maxWindowSizeBytes, ctrl.minWindowSizeBytes, ctrl.maxWindowUpperBytes); got < ctrl.minWindowSizeBytes {
				t.Errorf("clamping the initial window gave %d, below the floor %d", got, ctrl.minWindowSizeBytes)
			}
		})
	}
}

// ppStepClock is a clock that reads whatever time the test sets, so the
// send times the controller records and the times the test passes in agree.
type ppStepClock struct {
	Clock
	t time.Time
}

func (c *ppStepClock) Now() time.Time { return c.t }

func newPPTestController(t0 time.Time) (*defaultController, *ppStepClock) {
	clk := &ppStepClock{Clock: RealClock, t: t0}
	config := defaultCtrlConfig()
	config.Algorithm = AlgorithmLEDBATPP
	config.Clock = clk
	ctrl := newDefaultController(config)
	ctrl.minRTT = 20 * time.Millisecond
	return ctrl, clk
}

// A packet sent before a slowdown and declared lost during the ramp out of
// it was sent from the window the slowdown saved. Its loss halves that
// window, where the ramp is going, and leaves the ramp where it is. Charged
// to the ramp's partial window instead, it ended the ramp at a fraction of
// what the connection had been using, and two LEDBAT++ flows through a
// shallow bottleneck split it as unevenly as 37/63 (KNOWN-LIMITATIONS.md).
func TestLedbatPPLossFromBeforeASlowdownHalvesTheSavedWindow(t *testing.T) {
	t0 := time.Now()
	ctrl, clk := newPPTestController(t0)
	packet := ctrl.minWindowSizeBytes / 2

	if err := ctrl.OnTransmit(1, Initial, packet); err != nil {
		t.Fatal(err)
	}

	ctrl.ppPhase = ppCongestionAvoidance
	ctrl.maxWindowSizeBytes = 64 * packet
	ctrl.ppNextSlowdownAt = t0.Add(time.Millisecond)
	clk.t = t0.Add(2 * time.Millisecond)
	ctrl.advanceLedbatPPPhase(clk.t, packet)
	if ctrl.ppPhase != ppSlowdownFreeze {
		t.Fatalf("setup: phase %v, want the freeze", ctrl.ppPhase)
	}
	clk.t = ctrl.ppFreezeUntil.Add(time.Millisecond)
	ctrl.advanceLedbatPPPhase(clk.t, packet)
	if ctrl.ppPhase != ppSlowdownRamp {
		t.Fatalf("setup: phase %v, want the ramp", ctrl.ppPhase)
	}
	ctrl.maxWindowSizeBytes = 8 * packet

	if err := ctrl.OnLostPacket(1, false, clk.t); err != nil {
		t.Fatal(err)
	}
	if ctrl.ppPhase != ppSlowdownRamp {
		t.Errorf("the ramp ended on the loss (phase %v); the loss was of the saved window", ctrl.ppPhase)
	}
	if got := ctrl.maxWindowSizeBytes; got != 8*packet {
		t.Errorf("the ramp's window went from %d to %d; the loss belongs to the saved window", 8*packet, got)
	}
	if got, want := ctrl.ssthreshBytes, 32*packet; got != want {
		t.Errorf("ssthresh %d after the loss, want the saved %d halved, %d", got, 64*packet, want)
	}
}

// One congestion event is charged once. A packet sent before the window was
// last cut belongs to the event that cut it, however much later its loss is
// detected; a packet sent after the cut is a new event. The 100ms rate limit
// alone charged one overflow twice when a slowdown fell between its losses.
func TestLedbatPPLossIsChargedOncePerCongestionEvent(t *testing.T) {
	t0 := time.Now()
	ctrl, clk := newPPTestController(t0)
	packet := ctrl.minWindowSizeBytes / 2
	ctrl.ppPhase = ppCongestionAvoidance
	ctrl.maxWindowSizeBytes = 64 * packet

	for _, seq := range []uint16{1, 2} {
		if err := ctrl.OnTransmit(seq, Initial, packet); err != nil {
			t.Fatal(err)
		}
	}
	clk.t = t0.Add(10 * time.Millisecond)
	if err := ctrl.OnLostPacket(1, false, clk.t); err != nil {
		t.Fatal(err)
	}
	if got := ctrl.maxWindowSizeBytes; got != 32*packet {
		t.Fatalf("setup: the first loss left %d, want %d", got, 32*packet)
	}

	clk.t = t0.Add(200 * time.Millisecond)
	if err := ctrl.OnLostPacket(2, false, clk.t); err != nil {
		t.Fatal(err)
	}
	if got := ctrl.maxWindowSizeBytes; got != 32*packet {
		t.Errorf("a packet sent before the cut, lost 190ms after it, cut the window again: %d, want %d",
			got, 32*packet)
	}

	if err := ctrl.OnTransmit(3, Initial, packet); err != nil {
		t.Fatal(err)
	}
	clk.t = t0.Add(400 * time.Millisecond)
	if err := ctrl.OnLostPacket(3, false, clk.t); err != nil {
		t.Fatal(err)
	}
	if got := ctrl.maxWindowSizeBytes; got != 16*packet {
		t.Errorf("a packet sent after the cut is a new event and should halve the window: %d, want %d",
			got, 16*packet)
	}
}
