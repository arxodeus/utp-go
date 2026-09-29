//go:build cgo

package utp_go

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

// TestLossProbeSendsOnePacketBeforeTheTimeout pins the loss probe on a path
// that has gone silent after the handshake: exactly one retransmission before
// the first retransmission timeout, a probe timeout after the data went out,
// and then libutp's timeout schedule -- the probe does not replace the
// timeout, and does not move its deadline. (Re-arming the packet's timer for
// the same deadline can land it one 25 ms wheel tick later, which the
// schedule's tolerance absorbs; libutp's own check runs every 500 ms.)
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
	// The probe: well before the first timeout, and not before the floor
	// RFC 8985 puts on its timeout.
	if probe := resends[0]; probe >= int64(base/time.Millisecond)/2 || probe < int64(minLossProbeTimeout/time.Millisecond) {
		t.Errorf("first retransmission at %d ms; the probe belongs between %v and half the %v timeout",
			probe, minLossProbeTimeout, base)
	}
	// One probe only: the rest is the timeout schedule, [1 3 7 15] x base.
	assertScheduleMatches(t, "data", []int64{1, 3, 7, 15}, resends[1:], int64(base/time.Millisecond))
}

// TestLossProbeIsNotUsedByLedbatPP pins that a LEDBAT++ connection keeps
// libutp's behaviour: on a silent path its first retransmission is the
// timeout's, with no probe before it. The timeout the probe avoids is part of
// how LEDBAT++ yields to loss-based traffic (DEVIATIONS.md).
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
