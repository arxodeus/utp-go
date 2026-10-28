package utp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A round trip's acknowledgements grow the window by the same amount however
// many acknowledgements it is split into: a full window of them adds the
// gain once, scaled by how far off target the delay is. Each is reckoned
// against the window its packets were sent in; reckoned against the current
// window, the growth falls short by about G^2/2W per update.
func TestWindowGrowthDoesNotDependOnHowAcksAreSplit(t *testing.T) {
	const (
		packets    = 20
		size       = 1000
		rtt        = 50 * time.Millisecond
		baseDelay  = 20 * time.Millisecond
		queueDelay = 10 * time.Millisecond
	)
	grow := func(t *testing.T, slowStart bool, perAck int) int {
		t.Helper()
		c := newDefaultController(defaultCtrlConfig())
		c.maxWindowSizeBytes = packets * size
		c.slowStart = slowStart
		c.ssthreshBytes = 1 << 30
		c.lastMaxedOutWindow = time.Now()
		now := time.Now()
		for i := 0; i < 3; i++ {
			c.OnAckDelay(baseDelay, now)
		}
		for s := uint16(1); s <= packets; s++ {
			require.NoError(t, c.OnTransmit(s, transmitInitial, size))
		}
		for i := 0; i < 3; i++ {
			c.OnAckDelay(baseDelay+queueDelay, now)
		}
		before := int(c.maxWindowSizeBytes)
		for s := uint16(1); s <= packets; s++ {
			require.NoError(t, c.OnAck(s, ackSample{Delay: baseDelay + queueDelay, RTT: rtt, ReceivedAt: now}))
			if int(s)%perAck == 0 {
				c.ApplyAck()
			}
		}
		c.ApplyAck()
		return int(c.maxWindowSizeBytes) - before
	}
	for _, slowStart := range []bool{false, true} {
		whole := grow(t, slowStart, packets)
		for _, perAck := range []int{1, 2, 4} {
			split := grow(t, slowStart, perAck)
			updates := packets / perAck
			t.Logf("slow start %v: one acknowledgement grew the window %d bytes, %d of them %d", slowStart, whole, updates, split)
			// Each update truncates the window to whole bytes, so allow up to a
			// byte per update.
			if d := whole - split; d < 0 || d > updates {
				t.Errorf("slow start %v: %d acknowledgements of %d packets grew the window %d bytes, one of all %d grew it %d",
					slowStart, updates, perAck, split, packets, whole)
			}
		}
	}

	// Against the current window only, the split costs growth.
	t.Run("libutp's factor", func(t *testing.T) {
		was := sendWindowCredit.Swap(false)
		defer sendWindowCredit.Store(was)
		whole, split := grow(t, false, packets), grow(t, false, 1)
		t.Logf("libutp's factor: one acknowledgement %d bytes, twenty %d", whole, split)
		if whole-split <= packets {
			t.Errorf("libutp's factor grew the window %d bytes in one acknowledgement and %d in twenty; "+
				"expected the twenty to fall short", whole, split)
		}
	})
}
