package utp

import (
	"testing"
	"time"
)

// withoutLossProbe switches the loss probe off for one test.
func withoutLossProbe(t *testing.T) {
	t.Helper()
	old := lossProbeEnabled.Swap(false)
	t.Cleanup(func() { lossProbeEnabled.Store(old) })
}

// On a path that goes silent after the handshake, the loss probe sends
// exactly one retransmission, a probe timeout after the data went out, and
// the retransmission timeouts then follow their usual schedule: the probe
// does not replace the timeout or move its deadline.
func TestLossProbeSendsOnePacketBeforeTheTimeout(t *testing.T) {
	const base = 200 * time.Millisecond
	cfg := NewConnectionConfig()
	cfg.InitialTimeout = base
	cfg.MinTimeout = base
	cfg.MaxTimeout = 10 * time.Second

	resends, _ := ourRetransmitSchedule(t, cfg, true)
	t.Logf("data retransmissions at %v ms", resends)
	if len(resends) < 5 {
		t.Fatalf("%d retransmissions, want the probe and four timeouts: %v", len(resends), resends)
	}
	// The probe comes well before the first timeout, and not before RFC
	// 8985's floor on its timeout.
	if probe := resends[0]; probe >= int64(base/time.Millisecond)/2 || probe < int64(minLossProbeTimeout/time.Millisecond) {
		t.Errorf("first retransmission at %d ms; the probe belongs between %v and half the %v timeout",
			probe, minLossProbeTimeout, base)
	}
	// One probe only; the rest is the timeout schedule, [1 3 7 15] x base.
	assertScheduleMatches(t, "data", []int64{1, 3, 7, 15}, resends[1:], int64(base/time.Millisecond))
}

// A LEDBAT++ connection does not use the loss probe: on a silent path its
// first retransmission is the timeout's. The timeout is part of how
// LEDBAT++ yields to loss-based traffic.
func TestLossProbeIsNotUsedByLedbatPP(t *testing.T) {
	const base = 200 * time.Millisecond
	cfg := NewConnectionConfig()
	cfg.CongestionAlgorithm = AlgorithmLEDBATPP
	cfg.InitialTimeout = base
	cfg.MinTimeout = base
	cfg.MaxTimeout = 10 * time.Second

	resends, _ := ourRetransmitSchedule(t, cfg, true)
	t.Logf("data retransmissions at %v ms", resends)
	assertScheduleMatches(t, "data", []int64{1, 3, 7, 15}, resends, int64(base/time.Millisecond))
}
