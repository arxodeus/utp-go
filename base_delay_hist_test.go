package utp

import (
	"testing"
	"time"
)

// The base delay is libutp's DelayHist (utp_internal.cpp:260-381): thirteen
// one-minute buckets by default, so a low sample is remembered for twelve to
// thirteen minutes of samples, and then forgotten.
func TestBaseDelayRemembersThirteenMinutes(t *testing.T) {
	clk := &stepClock{t: time.Unix(0, 0)}
	h := newBaseDelayHist(defaultDelayWindow, clk)
	push := func(d time.Duration) { h.Push(d, clk.Now()) }

	push(50 * time.Millisecond)
	clk.Advance(30 * time.Second)
	push(20 * time.Millisecond) // the empty path, once
	for i := 0; i < 12*60; i++ {
		clk.Advance(time.Second)
		push(50 * time.Millisecond)
	}
	if got := h.BaseDelay(); got != 20*time.Millisecond {
		t.Fatalf("12.5 minutes on, the base is %v; libutp still remembers the 20 ms sample", got)
	}
	for i := 0; i < 2*60; i++ {
		clk.Advance(time.Second)
		push(50 * time.Millisecond)
	}
	if got := h.BaseDelay(); got != 50*time.Millisecond {
		t.Fatalf("14.5 minutes on, the base is %v; libutp has rotated the 20 ms sample out", got)
	}
}

// Only a sample rotates the history, as in libutp: a connection that falls
// silent for twenty minutes still has its base when it speaks again, the
// first sample after the silence moving it on by one bucket only.
func TestBaseDelayDoesNotAgeWhileIdle(t *testing.T) {
	clk := &stepClock{t: time.Unix(0, 0)}
	h := newBaseDelayHist(defaultDelayWindow, clk)
	h.Push(20*time.Millisecond, clk.Now())
	clk.Advance(20 * time.Minute)
	h.Push(50*time.Millisecond, clk.Now())
	if got := h.BaseDelay(); got != 20*time.Millisecond {
		t.Fatalf("after twenty idle minutes the base is %v, want the 20 ms it had", got)
	}
}

// A drift correction raises the base at once and lasts only as long as the
// buckets carrying it: libutp's shift adds to every bucket, and each is
// replaced by a fresh sample as the history rotates onto it.
func TestBaseDelayShiftAgesOut(t *testing.T) {
	clk := &stepClock{t: time.Unix(0, 0)}
	h := newBaseDelayHist(defaultDelayWindow, clk)
	h.Push(20*time.Millisecond, clk.Now())
	h.shift(5 * time.Millisecond)
	if got := h.BaseDelay(); got != 25*time.Millisecond {
		t.Fatalf("base %v after a 5 ms shift, want 25 ms", got)
	}
	for i := 0; i < 14*60; i++ {
		clk.Advance(time.Second)
		h.Push(20*time.Millisecond, clk.Now())
	}
	if got := h.BaseDelay(); got != 20*time.Millisecond {
		t.Fatalf("base %v fourteen minutes later; the shift should have rotated out", got)
	}
	if h.skew != 5*time.Millisecond {
		t.Fatalf("the reported correction is %v, want the 5 ms applied", h.skew)
	}
}
