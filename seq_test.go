package utp_go

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContainsStart(t *testing.T) {
	prop := func(start, end uint16) bool {
		rangeInclusive := newCircularRangeInclusive(start, end)
		return rangeInclusive.Contains(start)
	}

	if !prop(0, 65535) {
		t.Error("TestContainsStart failed")
	}
}

func TestContainsEnd(t *testing.T) {
	prop := func(start, end uint16) bool {
		rangeInclusive := newCircularRangeInclusive(start, end)
		return rangeInclusive.Contains(end)
	}

	if !prop(0, 65535) {
		t.Error("TestContainsEnd failed")
	}
}

func TestIterator(t *testing.T) {
	prop := func(start, end uint16) bool {
		rangeInclusive := newCircularRangeInclusive(start, end)

		var length int
		expectedIdx := start
		for {
			idx, ok := rangeInclusive.Next()
			if !ok {
				break
			}
			require.Equal(t, expectedIdx, idx, "Expected %v, got %v", expectedIdx, idx)
			expectedIdx += 1
			length += 1
		}

		var expectedLen int
		if start <= end {
			expectedLen = int(end-start) + 1
		} else {
			expectedLen = int(65535-start) + int(end) + 2
		}
		if length != expectedLen {
			t.Errorf("Expected length %v, got %v", expectedLen, length)
			return false
		}

		return true
	}

	if !prop(0, 65535) {
		t.Error("TestIterator failed")
	}
}

func TestIteratorSingle(t *testing.T) {
	prop := func(x uint16) bool {
		rangeInclusive := newCircularRangeInclusive(x, x)
		val, ok := rangeInclusive.Next()
		if !ok || val != x {
			t.Errorf("Expected %v, got %v", x, val)
			return false
		}
		if _, ok := rangeInclusive.Next(); ok {
			t.Error("Expected no more elements")
			return false
		}

		return true
	}

	if !prop(0) {
		t.Error("TestIteratorSingle failed")
	}
}

// wrappingLessThan is the sequence-number comparison every fast-retransmit
// decision goes through, so it is tested on its own rather than only through
// a transfer. The wrap cases are the point: a naive `a < b` gets them wrong.
func TestWrappingLessThan(t *testing.T) {
	cases := []struct {
		name string
		a, b uint16
		want bool
	}{
		{"plainly less", 5, 9, true},
		{"plainly greater", 9, 5, false},
		{"equal is not less", 7, 7, false},
		{"adjacent", 100, 101, true},
		{"adjacent reversed", 101, 100, false},
		{"zero precedes one", 0, 1, true},
		{"across the wrap", 65535, 0, true},
		{"across the wrap reversed", 0, 65535, false},
		{"wide span across the wrap", 65000, 500, true},
		{"wide span across the wrap reversed", 500, 65000, false},
		{"just inside half the space", 0, 32767, true},
		{"just inside half the space reversed", 32767, 0, false},
		{"exactly half the space is unordered", 0, 32768, false},
		{"exactly half the space is unordered reversed", 32768, 0, false},
		{"just past half the space flips", 0, 32769, false},
		{"just past half the space flips reversed", 32769, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wrappingLessThan(tc.a, tc.b); got != tc.want {
				t.Errorf("wrappingLessThan(%d, %d) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// The relation must be antisymmetric everywhere except the two unordered
// cases, across the whole space rather than the handful of points above.
func TestWrappingLessThanIsAntisymmetric(t *testing.T) {
	for i := 0; i < 4096; i++ {
		a := uint16(i * 7919)
		for _, delta := range []uint16{1, 2, 100, 32767, 32768, 32769, 65535} {
			b := a + delta
			ab := wrappingLessThan(a, b)
			ba := wrappingLessThan(b, a)
			if delta == 32768 {
				if ab || ba {
					t.Fatalf("(%d, %d) are exactly half the space apart and must be unordered, got %v/%v", a, b, ab, ba)
				}
				continue
			}
			if ab == ba {
				t.Fatalf("wrappingLessThan(%d, %d) = %v and the reverse = %v; exactly one must hold", a, b, ab, ba)
			}
		}
	}
}
