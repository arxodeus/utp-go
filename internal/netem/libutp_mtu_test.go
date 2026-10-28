//go:build cgo

package netem

import (
	"context"
	"testing"
	"time"
)

// libutp on a path whose MTU is below the size it sends at.
//
// This pins the reference behaviour behind
// TestMtuSearchCannotRecoverFromAPathLimitBelowItsChoice. That test documents a
// limitation this library has; this one establishes that libutp has it too, so
// the limitation is uTP's MTU discovery as implemented rather than a defect
// this fork introduced.
//
// It is the difference between "believed, from reading utp_internal.cpp" and
// "measured" -- which matters here, because the reading alone would suggest
// libutp recovers: it lowers mtu_ceiling on a probe timeout
// (utp_internal.cpp:1152-1160) and clears the probe on every RTO so another
// can go out (:1166-1167). Both are true, and neither rescues a connection
// whose window has stalled, because the ceiling only moves when the probe is
// the *only* packet outstanding and nothing is being acknowledged.
//
// If libutp is ever changed to handle this, this test fails, which is the
// point: it should be re-derived rather than assumed.
func TestLibutpStallsOnAPathItCannotFit(t *testing.T) {
	n := NewNetwork(61)
	defer n.Close()
	a := n.MustAddEndpoint("a")
	b := n.MustAddEndpoint("b")
	n.Connect(a, b, Config{
		Delay:        10 * time.Millisecond,
		BandwidthBps: 20_000_000,
		QueueBytes:   64 * 1024,
		MTU:          1100,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	payload := make([]byte, 128*1024)
	for i := range payload {
		payload[i] = byte(i * 31)
	}

	start := time.Now()
	_, got, err := libutpToGo(ctx, n, a, b, payload, 7000)
	fwd := n.Link("a", "b").Stats()
	t.Logf("libutp over a 1100-byte path after %v: delivered %d of %d bytes, err=%v; link %s",
		time.Since(start).Round(time.Millisecond), len(got), len(payload), err, fwd)

	if err == nil && len(got) == len(payload) {
		t.Errorf("libutp completed a transfer over a path that refuses anything above 1100 " +
			"bytes. The reference behaviour this pins has changed: re-derive it, and revisit " +
			"TestMtuSearchCannotRecoverFromAPathLimitBelowItsChoice, which records the same " +
			"limitation here on the strength of libutp having it too")
	}
	if fwd.DroppedByMTU == 0 {
		t.Error("no datagram was refused for size, so this measured nothing")
	}
}
