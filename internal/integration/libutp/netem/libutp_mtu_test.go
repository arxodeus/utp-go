//go:build cgo

package netem_test

import (
	"context"
	"testing"
	"time"

	. "github.com/arxodeus/utp/internal/netem"
)

// libutp on a path whose MTU is below the size it sends at. This shows that
// the limitation in TestMtuSearchCannotRecoverFromAPathLimitBelowItsChoice
// belongs to uTP's MTU discovery as implemented, not to this library: libutp
// lowers its ceiling only when the probe times out as the only packet
// outstanding, which a stalled window never reaches. If libutp changes to
// handle this, the test fails and should be re-derived.
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
