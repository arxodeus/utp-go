package utp_go

import (
	"sync"
	"testing"
	"time"
)

// The retransmission timer's behaviour is the difference between a working
// congestion controller and one being fed phantom loss, so the wheel's timing
// is tested directly rather than inferred from transfer results.

type firing struct {
	mu    sync.Mutex
	times map[any]time.Duration
	start time.Time
}

func newFiring() *firing {
	return &firing{times: make(map[any]time.Duration), start: time.Now()}
}

func (f *firing) record(key any, _ int) {
	f.mu.Lock()
	f.times[key] = time.Since(f.start)
	f.mu.Unlock()
}

func (f *firing) at(key any) (time.Duration, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.times[key]
	return d, ok
}

func (f *firing) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.times)
}

// An item must never fire before its delay. Firing early is what declared
// packets lost before their ack could arrive.
func TestTimeWheelNeverFiresEarly(t *testing.T) {
	const interval = 20 * time.Millisecond
	f := newFiring()
	tw := newTimeWheel[int](interval, 8, f.record)
	defer tw.stop()

	// Delays deliberately not multiples of the interval, including one
	// shorter than a single tick.
	delays := map[any]time.Duration{
		"quarter-tick": interval / 4,
		"one-tick":     interval,
		"tick-and-bit": interval + interval/3,
		"three-ticks":  3 * interval,
		"seven-ticks":  7 * interval,
	}
	for k, d := range delays {
		tw.put(k, 1, d)
	}

	time.Sleep(10*interval + 200*time.Millisecond)

	for k, want := range delays {
		got, ok := f.at(k)
		if !ok {
			t.Errorf("%v (delay %v) never fired", k, want)
			continue
		}
		if got < want {
			t.Errorf("%v fired at %v, before its %v delay", k, got, want)
		}
		// The wheel's resolution is one interval, plus scheduler slack.
		if ceiling := want + interval + 80*time.Millisecond; got > ceiling {
			t.Errorf("%v (delay %v) fired at %v, later than the %v allowance", k, want, got, ceiling)
		}
	}
}

// A delay shorter than one tick must still wait a tick, not fire on the very
// next one regardless of when it was scheduled.
func TestTimeWheelSubIntervalDelayWaitsATick(t *testing.T) {
	const interval = 50 * time.Millisecond
	f := newFiring()
	tw := newTimeWheel[int](interval, 8, f.record)
	defer tw.stop()

	tw.put("tiny", 1, time.Microsecond)
	time.Sleep(interval + 150*time.Millisecond)

	got, ok := f.at("tiny")
	if !ok {
		t.Fatal("a sub-interval delay never fired")
	}
	if got > 2*interval+100*time.Millisecond {
		t.Errorf("sub-interval delay fired at %v, want within about one tick", got)
	}
}

// Delays longer than one revolution must be scheduled, not clamped. RTO
// backoff depends on this.
func TestTimeWheelHandlesDelaysBeyondOneRevolution(t *testing.T) {
	const interval = 20 * time.Millisecond
	const slots = 4 // one revolution is 80ms
	f := newFiring()
	tw := newTimeWheel[int](interval, slots, f.record)
	defer tw.stop()

	revolution := interval * slots
	long := 3*revolution + interval // 260ms: well past the old ceiling

	tw.put("long", 1, long)
	// Before it is due, it must not have fired.
	time.Sleep(revolution + interval)
	if got, ok := f.at("long"); ok {
		t.Fatalf("a %v delay fired after only %v -- it was clamped to one revolution", long, got)
	}

	time.Sleep(long)
	got, ok := f.at("long")
	if !ok {
		t.Fatalf("a %v delay never fired", long)
	}
	if got < long {
		t.Errorf("fired at %v, before the requested %v", got, long)
	}
	if got > long+revolution {
		t.Errorf("fired at %v, more than a revolution past the requested %v", got, long)
	}
}

func TestTimeWheelRemoveCancels(t *testing.T) {
	const interval = 20 * time.Millisecond
	f := newFiring()
	tw := newTimeWheel[int](interval, 8, f.record)
	defer tw.stop()

	tw.put("keep", 1, 2*interval)
	tw.put("cancel", 1, 2*interval)
	if tw.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", tw.Len())
	}
	tw.remove("cancel")
	if tw.Len() != 1 {
		t.Fatalf("after remove, Len() = %d, want 1", tw.Len())
	}
	if tw.contains("cancel") {
		t.Error("contains reports a removed key as present")
	}

	time.Sleep(6*interval + 150*time.Millisecond)
	if _, ok := f.at("cancel"); ok {
		t.Error("a removed item fired anyway")
	}
	if _, ok := f.at("keep"); !ok {
		t.Error("the item that was not removed never fired")
	}
}

// Re-putting a key reschedules it rather than leaving two registrations,
// which is what retransmitting an already-armed packet does.
func TestTimeWheelPutReschedules(t *testing.T) {
	const interval = 20 * time.Millisecond
	f := newFiring()
	tw := newTimeWheel[int](interval, 8, f.record)
	defer tw.stop()

	tw.put("k", 1, interval)
	tw.put("k", 1, 5*interval)
	if tw.Len() != 1 {
		t.Fatalf("Len() = %d after re-put, want 1", tw.Len())
	}

	time.Sleep(3 * interval)
	if got, ok := f.at("k"); ok {
		t.Fatalf("fired at %v: the original short delay was not replaced", got)
	}
	time.Sleep(5*interval + 150*time.Millisecond)
	if _, ok := f.at("k"); !ok {
		t.Error("rescheduled item never fired")
	}
	if f.count() != 1 {
		t.Errorf("%d items fired, want 1 -- re-put left a duplicate registration", f.count())
	}
}

func TestTimeWheelRemoveIsIdempotent(t *testing.T) {
	tw := newTimeWheel[int](20*time.Millisecond, 8, func(any, int) {})
	defer tw.stop()
	tw.put("k", 1, time.Second)
	tw.remove("k")
	tw.remove("k")
	tw.remove("never-present")
	if tw.Len() != 0 {
		t.Errorf("Len() = %d, want 0", tw.Len())
	}
}

func TestTimeWheelStopIsIdempotent(t *testing.T) {
	tw := newTimeWheel[int](20*time.Millisecond, 8, func(any, int) {})
	tw.stop()
	tw.stop()
}

// The wheel is shared across a socket, so keys from different connections
// must not collide.
func TestRetransmitKeysAreScoped(t *testing.T) {
	timers := newRetransmitTimers(20*time.Millisecond, 8)
	defer timers.stop()

	a := timers.newScope()
	b := timers.newScope()
	if a == b {
		t.Fatal("newScope returned the same identifier twice")
	}

	chA := make(chan *packet, 1)
	chB := make(chan *packet, 1)
	pktA := &packet{Header: &PacketHeaderV1{SeqNum: 7}}
	pktB := &packet{Header: &PacketHeaderV1{SeqNum: 7}}

	timers.arm(retransmitKey{scope: a, seq: 7}, &retransmitTimer{packet: pktA, deliver: chA, ctx: BASE_CONTEXT}, time.Second)
	timers.arm(retransmitKey{scope: b, seq: 7}, &retransmitTimer{packet: pktB, deliver: chB, ctx: BASE_CONTEXT}, time.Second)

	if got := timers.Len(); got != 2 {
		t.Fatalf("same seq num in two scopes gave Len() = %d, want 2 -- the scopes collided", got)
	}

	// Disarming one scope must leave the other alone.
	timers.disarm(retransmitKey{scope: a, seq: 7})
	if got := timers.Len(); got != 1 {
		t.Fatalf("after disarming one scope, Len() = %d, want 1", got)
	}
}

// The same promise, but armed part-way through a tick.
//
// TestTimeWheelNeverFiresEarly arms everything immediately after the wheel is
// created, when the next tick is a full interval away. That is the one case
// where placing an item n-1 slots ahead happens to be right, so the wheel
// passed that test while firing up to a full interval early for every timer
// armed at any other moment -- which, in a live connection, is all of them.
//
// This arms at a range of offsets through the tick cycle. It reproduced the
// defect at every offset but zero.
func TestTimeWheelNeverFiresEarlyWhenArmedMidCycle(t *testing.T) {
	const interval = 20 * time.Millisecond

	for _, offsetNum := range []int{1, 2, 3} {
		offset := interval * time.Duration(offsetNum) / 4
		t.Run(offset.String()+"-into-the-tick", func(t *testing.T) {
			f := newFiring()
			tw := newTimeWheel[int](interval, 8, f.record)
			defer tw.stop()

			// Let the wheel get part-way through a tick before arming.
			time.Sleep(offset)

			delays := map[any]time.Duration{
				"one-tick":    interval,
				"two-ticks":   2 * interval,
				"three-ticks": 3 * interval,
			}
			armedAt := time.Since(f.start)
			for k, d := range delays {
				tw.put(k, 1, d)
			}

			time.Sleep(6*interval + 200*time.Millisecond)

			for k, want := range delays {
				got, ok := f.at(k)
				if !ok {
					t.Errorf("%v (delay %v) never fired", k, want)
					continue
				}
				// f.at records the time since the recorder was created; the
				// delay is owed from when the item was armed.
				sinceArmed := got - armedAt
				if sinceArmed < want {
					t.Errorf("%v fired %v after arming, before its %v delay "+
						"(armed %v into the tick cycle)", k, sinceArmed, want, offset)
				}
			}
		})
	}
}
