package utp_go

import (
	"testing"
	"time"
)

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
func TestDriftPenaltyReachesTheWindow(t *testing.T) {
	// Identical acknowledgements, identical everything, except that one
	// controller is handed a drift estimate past the threshold.
	run := func(drift int64) uint32 {
		ctrl := newDefaultController(defaultCtrlConfig())
		ctrl.drift.drift = drift

		now := time.Now()
		const packetBytes = uint32(1000)
		// A steady, unremarkable queueing delay: 20ms measured against a 10ms
		// base, so 10ms of queue against a 100ms target. Comfortably in the
		// region where the window grows.
		for i := 1; i <= 400; i++ {
			seq := uint16(i)
			if err := ctrl.OnTransmit(seq, Initial, packetBytes); err != nil {
				t.Fatalf("transmit %d: %v", i, err)
			}
			now = now.Add(time.Millisecond)
			if err := ctrl.OnAck(seq, Ack{
				Delay:      20 * time.Millisecond,
				RTT:        50 * time.Millisecond,
				ReceivedAt: now,
			}); err != nil {
				t.Fatalf("ack %d: %v", i, err)
			}
			// Keep the window full, or the application-limited guard stops
			// growth and both runs settle at the same floor for a reason that
			// has nothing to do with drift.
			ctrl.OnWindowFull(now)
		}
		return ctrl.maxWindowSizeBytes
	}

	// The base delay has to settle below the sample or there is no queueing
	// delay for the penalty to add to, so seed both the same way.
	honest := run(0)
	// Past the threshold by enough to matter: the penalty is
	// (-drift - 200000) / 7, so -900000 gives 100ms -- the whole target.
	penalised := run(-900000)

	t.Logf("congestion window after 400 acks: %d honest, %d with a drift penalty",
		honest, penalised)

	if honest == 0 {
		t.Fatal("the honest run ended with a zero window; the harness is not driving " +
			"the controller as intended")
	}
	if penalised >= honest {
		t.Errorf("the drift penalty did not reach the window: %d with it against %d "+
			"without. It is being computed and not acted on.", penalised, honest)
	}
}

// A drift on the harmless side earns nothing, so the window is untouched.
//
// The penalty exists for a delay signal that is shrinking, which makes a flow
// under-measure the queue and take more than its share. Drift the other way
// makes it over-measure and yield, which costs the drifting peer and nobody
// else, and libutp penalises only the first (utp_internal.cpp:1647).
func TestDriftPenaltyIgnoresTheHarmlessDirection(t *testing.T) {
	run := func(drift int64) uint32 {
		ctrl := newDefaultController(defaultCtrlConfig())
		ctrl.drift.drift = drift
		now := time.Now()
		for i := 1; i <= 200; i++ {
			seq := uint16(i)
			if err := ctrl.OnTransmit(seq, Initial, 1000); err != nil {
				t.Fatalf("transmit %d: %v", i, err)
			}
			now = now.Add(time.Millisecond)
			if err := ctrl.OnAck(seq, Ack{
				Delay:      20 * time.Millisecond,
				RTT:        50 * time.Millisecond,
				ReceivedAt: now,
			}); err != nil {
				t.Fatalf("ack %d: %v", i, err)
			}
			ctrl.OnWindowFull(now)
		}
		return ctrl.maxWindowSizeBytes
	}

	if got, want := run(+900000), run(0); got != want {
		t.Errorf("a clock drifting the harmless way changed the window: %d against %d",
			got, want)
	}
}
