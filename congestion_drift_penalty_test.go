package utp

import (
	"testing"
	"time"
)

// The drift penalty reaches the congestion window. The controller is driven
// directly with identical acknowledgements, with and without a drift
// estimate past the threshold, and the windows they settle on are compared.
func TestDriftPenaltyReachesTheWindow(t *testing.T) {
	// Identical acknowledgements, identical everything, except that one
	// controller is handed a drift estimate past the threshold.
	run := func(drift int64) uint32 {
		ctrl := newDefaultController(defaultCtrlConfig())
		ctrl.drift.drift = drift

		now := time.Now()
		const packetBytes = uint32(1000)
		// A steady queuing delay: 20 ms measured against a 10 ms base, so 10 ms
		// of queue against a 100 ms target, where the window grows.
		for i := 1; i <= 400; i++ {
			seq := uint16(i)
			if err := ctrl.OnTransmit(seq, transmitInitial, packetBytes); err != nil {
				t.Fatalf("transmit %d: %v", i, err)
			}
			now = now.Add(time.Millisecond)
			if err := ackOne(ctrl, seq, ackSample{
				Delay:      20 * time.Millisecond,
				RTT:        50 * time.Millisecond,
				ReceivedAt: now,
			}); err != nil {
				t.Fatalf("ack %d: %v", i, err)
			}
			// Keep the window full, or the application-limited guard stops growth
			// and both runs settle at the same floor.
			ctrl.OnWindowFull(now)
		}
		return ctrl.maxWindowSizeBytes
	}

	// The base delay must settle below the sample or there is no queuing
	// delay for the penalty to add to, so seed both the same way.
	honest := run(0)
	// Past the threshold by enough to matter: the penalty is
	// (-drift - 200000) / 7, so -900000 gives 100 ms, the whole target.
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

// Drift in the harmless direction incurs no penalty, so the window is
// unchanged. Drift the other way makes the flow over-measure the queue and
// yield, which costs only the drifting peer.
func TestDriftPenaltyIgnoresTheHarmlessDirection(t *testing.T) {
	run := func(drift int64) uint32 {
		ctrl := newDefaultController(defaultCtrlConfig())
		ctrl.drift.drift = drift
		now := time.Now()
		for i := 1; i <= 200; i++ {
			seq := uint16(i)
			if err := ctrl.OnTransmit(seq, transmitInitial, 1000); err != nil {
				t.Fatalf("transmit %d: %v", i, err)
			}
			now = now.Add(time.Millisecond)
			if err := ackOne(ctrl, seq, ackSample{
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
