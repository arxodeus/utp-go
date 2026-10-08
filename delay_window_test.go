package utp_go

import (
	"math/rand"
	"testing"
	"time"
)

// stepClock is a clock that only tells the time, moved by hand.
type stepClock struct{ t time.Time }

func (c *stepClock) Now() time.Time                 { return c.t }
func (c *stepClock) Advance(d time.Duration)        { c.t = c.t.Add(d) }
func (c *stepClock) NewTimer(time.Duration) Timer   { panic("not used") }
func (c *stepClock) NewTicker(time.Duration) Ticker { panic("not used") }

// The base delay is the least sample within the window, as a scan over every
// sample would give it, for samples arriving in time order.
func TestDelayWindowMatchesAScan(t *testing.T) {
	const window = 2 * time.Second
	for seed := int64(1); seed <= 30; seed++ {
		rng := rand.New(rand.NewSource(seed))
		clk := &stepClock{t: time.Unix(0, 0)}
		acc := newDelayAccumulatorWithClock(window, clk)
		type sample struct {
			v  time.Duration
			at time.Time
		}
		var all []sample
		for i := 0; i < 5000; i++ {
			clk.Advance(time.Duration(rng.Intn(5000)) * time.Microsecond)
			if rng.Intn(3) > 0 {
				v := time.Duration(20000+rng.Intn(30000)) * time.Microsecond
				acc.Push(v, clk.Now())
				all = append(all, sample{v, clk.Now()})
			}
			want := time.Duration(0)
			found := false
			for _, s := range all {
				if !clk.Now().After(s.at.Add(window)) && (!found || s.v < want) {
					want, found = s.v, true
				}
			}
			if got := acc.BaseDelay(); got != want {
				t.Fatalf("seed %d step %d: base delay %v, a scan of the window gives %v", seed, i, got, want)
			}
		}
	}
}

// What a busy connection keeps: two minutes of acknowledgements at a
// thousand a second, delays wandering as a queue's do. The heap this replaced
// popped a sample only once its least had expired, so it held more than the
// window: up to 239,689 samples here, 32 bytes each, 7.7 MB. This holds at
// most 495.
func TestDelayWindowStaysSmall(t *testing.T) {
	const window = 2 * time.Minute
	rng := rand.New(rand.NewSource(7))
	clk := &stepClock{t: time.Unix(0, 0)}
	acc := newDelayAccumulatorWithClock(window, clk)
	q := 0.0
	most := 0
	for i := 0; i < 240_000; i++ {
		clk.Advance(time.Millisecond)
		q += rng.NormFloat64() * 200 // a random walk, in microseconds
		if q < 0 {
			q = 0
		}
		acc.Push(20*time.Millisecond+time.Duration(q)*time.Microsecond, clk.Now())
		acc.BaseDelay()
		most = max(most, acc.delays.len())
	}
	t.Logf("at most %d samples held over four minutes of a thousand a second", most)
	if most > 2000 {
		t.Fatalf("held up to %d samples; the window's minimum needs far fewer", most)
	}
}
