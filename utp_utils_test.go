package utp_go

import (
	"math"
	"sync"
	"testing"
	"time"
)

// test for data race
func TestRandomUint16DataRace(t *testing.T) {
	var wg sync.WaitGroup
	const numGoroutines = 100

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			_ = RandomUint16()
		}()
	}
	wg.Wait()
}

func TestWrappingSubUint32(t *testing.T) {
	const max = uint32(math.MaxUint32)
	cases := []struct {
		name           string
		later, earlier uint32
		want           uint32
	}{
		{"zero", 0, 0, 0},
		{"simple", 500, 100, 400},
		{"one apart", 1, 0, 1},
		{"at the wrap boundary", 0, max, 1},
		{"across the wrap", 5, max - 4, 10},
		{"far across the wrap", 1000, max - 999, 2000},
		{"whole ring minus one", max, 0, max},
		// A "later" value that is numerically smaller is the normal case
		// after a wrap, not an error: the difference is still small.
		{"peer stamped just before wrap", 10, max - 10, 21},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wrappingSubUint32(tc.later, tc.earlier); got != tc.want {
				t.Fatalf("wrappingSubUint32(%d, %d) = %d, want %d",
					tc.later, tc.earlier, got, tc.want)
			}
		})
	}
}

// The difference of any two uint32 timestamps taken within one wrap period
// must be the true elapsed value, wherever the wrap point falls between them.
func TestWrappingSubUint32AcrossEveryOffset(t *testing.T) {
	const elapsed = uint32(12345)
	for _, base := range []uint32{0, 1, 1 << 16, math.MaxUint32 - elapsed - 1, math.MaxUint32 - elapsed, math.MaxUint32 - 1, math.MaxUint32} {
		later := base + elapsed // wraps naturally
		if got := wrappingSubUint32(later, base); got != elapsed {
			t.Fatalf("base=%d: wrappingSubUint32(%d, %d) = %d, want %d",
				base, later, base, got, elapsed)
		}
	}
}

func TestTimestampDiffMicros(t *testing.T) {
	const max = uint32(math.MaxUint32)
	cases := []struct {
		name      string
		now, peer uint32
		want      time.Duration
	}{
		{"same instant", 1000, 1000, 0},
		{"200us ago", 1200, 1000, 200 * time.Microsecond},
		{"across the wrap", 100, max - 99, 200 * time.Microsecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := timestampDiffMicros(tc.now, tc.peer); got != tc.want {
				t.Fatalf("timestampDiffMicros(%d, %d) = %v, want %v",
					tc.now, tc.peer, got, tc.want)
			}
		})
	}
}

func TestDurationBetween(t *testing.T) {
	const max = uint32(math.MaxUint32)
	if got, want := DurationBetween(1000, 1200), 200*time.Microsecond; got != want {
		t.Fatalf("DurationBetween(1000, 1200) = %v, want %v", got, want)
	}
	// Across the wrap: earlier is just below the boundary, later just above.
	if got, want := DurationBetween(max-99, 100), 200*time.Microsecond; got != want {
		t.Fatalf("DurationBetween across wrap = %v, want %v", got, want)
	}
	if got, want := DurationBetween(0, 0), time.Duration(0); got != want {
		t.Fatalf("DurationBetween(0, 0) = %v, want %v", got, want)
	}
}
