package utp_go

import (
	"strings"
	"testing"
	"time"
)

// The wire clock must not jump when the wall clock is stepped. A test cannot
// step the host's clock, so this checks the two things the guarantee rests
// on: the epoch carries Go's monotonic reading, which time.Since uses and an
// NTP step does not move, and while nothing steps the clock the stamps agree
// with the wall clock they replaced.
func TestWireClockIsMonotonic(t *testing.T) {
	if !strings.Contains(wireClockEpoch.String(), "m=") {
		t.Fatal("the wire clock's epoch has no monotonic reading, so time.Since would follow the wall clock")
	}
	got := NowMicro()
	want := uint32(time.Now().UnixMicro())
	if d := int32(got - want); d < -50_000 || d > 50_000 {
		t.Errorf("the wire clock reads %d against the wall clock's %d, %dus apart", got, want, d)
	}
	// Strictly not backwards across consecutive reads.
	prev := NowMicro()
	for i := 0; i < 1000; i++ {
		now := NowMicro()
		if int32(now-prev) < 0 {
			t.Fatalf("the wire clock went backwards: %d after %d", now, prev)
		}
		prev = now
	}
}
