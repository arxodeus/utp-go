package utp_go

import (
	"sync"
	"time"
)

type expireFunc[P any] func(key any, value P)

type timeWheelItem[P any] struct {
	value P
	key   any
	// rounds is how many further full revolutions of the wheel must pass
	// before this item expires. It is what lets the wheel schedule delays
	// longer than one revolution instead of silently clamping them.
	rounds int
}

// timeWheel is a hashed timing wheel.
//
// Two properties matter for retransmission timing, and the original
// implementation had neither:
//
//   - Resolution. A delay shorter than one tick used to land in the slot
//     being processed next, so it fired somewhere in [0, interval) rather
//     than at the requested time. With a one-second interval and a 500 ms
//     RTO, that declared packets lost long before their ack could arrive --
//     about 6% of them on a lossless path. An item now fires on the first
//     tick at or after its deadline: never early, and less than one interval
//     late.
//   - Range. Delays beyond slotNum*interval used to be clamped to that
//     ceiling, so exponential RTO backoff stopped growing. The rounds
//     counter removes the ceiling.
//
// Removal is O(1): an index maps each key to the slot holding it. Scanning
// every slot was acceptable for a per-connection wheel but not for one shared
// across a whole socket.
type timeWheel[P any] struct {
	stopped  chan struct{}
	interval time.Duration
	slots    []map[any]*timeWheelItem[P]
	index    map[any]int
	ticker   Ticker
	clk      Clock
	// nextTick is when the tick that processes slots[current] is due: the
	// last tick's scheduled time plus one interval. A ticker keeps its phase,
	// so the next tick is never sooner than this, and later only if one was
	// dropped. See put.
	nextTick         time.Time
	current          int
	slotNum          int
	handleExpireFunc expireFunc[P]
	barrier          IdleBarrier
	mu               sync.RWMutex
	stopOnce         sync.Once
}

func newTimeWheel[P any](interval time.Duration, slotNum int, handleExpireFunc expireFunc[P]) *timeWheel[P] {
	return newTimeWheelWithClock(interval, slotNum, RealClock, handleExpireFunc)
}

// newTimeWheelWithClock is the wheel on a given clock. The wheel is per
// socket rather than per connection, so every connection on a socket shares
// this one's notion of time -- which is what makes a virtual clock for a
// socket coherent rather than per-connection and contradictory.
func newTimeWheelWithClock[P any](interval time.Duration, slotNum int, clk Clock, handleExpireFunc expireFunc[P]) *timeWheel[P] {
	tw := &timeWheel[P]{
		stopped:          make(chan struct{}),
		interval:         interval,
		slots:            make([]map[any]*timeWheelItem[P], slotNum),
		index:            make(map[any]int),
		current:          0,
		slotNum:          slotNum,
		handleExpireFunc: handleExpireFunc,
	}

	for i := 0; i < slotNum; i++ {
		tw.slots[i] = make(map[any]*timeWheelItem[P])
	}

	if clk == nil {
		clk = RealClock
	}
	tw.clk = clk
	// Read before the ticker exists, so that the first tick is no sooner than
	// this says.
	tw.nextTick = clk.Now().Add(interval)
	tw.ticker = clk.NewTicker(interval)
	// A virtual clock may not move while this goroutine is processing a tick.
	// Most ticks expire nothing and so never reach a connection, which is
	// exactly why the wheel has to report for itself: a clock that fired this
	// ticker and then waited for a *connection* to react would wait forever.
	tw.barrier, _ = clk.(IdleBarrier)
	if tw.barrier != nil {
		tw.barrier.Register()
	}
	go tw.run()
	return tw
}

// put schedules value to expire after delay. Re-putting an existing key
// reschedules it.
//
// The item goes in the slot whose tick is the first due at or after the
// deadline, to the millisecond, so it never fires early and fires less than
// one interval late. To the millisecond is libutp's resolution: its clock is
// current_ms, a deadline is current_ms plus the timeout, and it fires once
// (int)(current_ms - rto_timeout) >= 0 (utp_internal.cpp:997, :1147, :1389),
// which can be up to a millisecond before the microsecond the deadline was
// set for.
//
// It used to count whole intervals from now, rounded up, and add one more,
// because the wheel ticks on its own schedule and "n ticks from now" could be
// as little as n-1 intervals away. Placing it n intervals past the slot about
// to be processed made it never early -- measured against libutp before that:
// a SYN with a 200 ms timeout retransmitted at 186 ms -- but nearly a whole
// interval late whenever the put came just after a tick. A retransmission
// re-arms its timer from the tick that fired it, so every backoff landed one
// 25 ms tick later than the last: a 200 ms timeout's retransmissions at 208,
// 633, 1458 and 3084 ms against 200, 600, 1400 and 3000. Placing by deadline
// to the microsecond did no better, because the re-arm runs a fraction of a
// millisecond after the tick and its deadline lands just past the matching
// one.
// wheelResolution is how precisely a deadline is honoured: libutp's
// millisecond. See put.
const wheelResolution = time.Millisecond

func (tw *timeWheel[P]) put(key any, value P, delay time.Duration) {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	if old, exists := tw.index[key]; exists {
		delete(tw.slots[old], key)
		delete(tw.index, key)
	}

	// Slot current+n is processed by a tick due at nextTick + n*interval or
	// later, so the least n that reaches the deadline is safe.
	ticks := 0
	if wait := tw.clk.Now().Add(delay).Sub(tw.nextTick) - wheelResolution; wait > 0 {
		ticks = int(wait / tw.interval)
		if wait%tw.interval != 0 {
			ticks++
		}
	}
	idx := (tw.current + ticks) % tw.slotNum
	tw.slots[idx][key] = &timeWheelItem[P]{
		key:    key,
		value:  value,
		rounds: ticks / tw.slotNum,
	}
	tw.index[key] = idx
}

func (tw *timeWheel[P]) remove(key any) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if idx, exists := tw.index[key]; exists {
		delete(tw.slots[idx], key)
		delete(tw.index, key)
	}
}

// contains reports whether key is currently scheduled.
func (tw *timeWheel[P]) contains(key any) bool {
	tw.mu.RLock()
	defer tw.mu.RUnlock()
	_, exists := tw.index[key]
	return exists
}

func (tw *timeWheel[P]) run() {
	var expired []*timeWheelItem[P]
	for {
		if tw.barrier != nil {
			tw.barrier.MarkIdle()
		}
		select {
		case at := <-tw.ticker.C():
			if tw.barrier != nil {
				tw.barrier.MarkBusy()
			}
			// Collect the expired items under the lock, then dispatch them
			// with the lock released. handleExpireFunc blocks (it hands the
			// packet to a connection's event loop over a channel), and that
			// same event loop calls put/remove, which need this lock.
			// Running the callback under the lock deadlocks the connection as
			// soon as the timeout channel fills up.
			expired = expired[:0]
			tw.mu.Lock()
			currentSlot := tw.slots[tw.current]
			for key, item := range currentSlot {
				if item.rounds > 0 {
					item.rounds--
					continue
				}
				delete(currentSlot, key)
				delete(tw.index, key)
				expired = append(expired, item)
			}
			tw.current = (tw.current + 1) % tw.slotNum
			tw.nextTick = at.Add(tw.interval)
			tw.mu.Unlock()

			for _, item := range expired {
				tw.handleExpireFunc(item.key, item.value)
			}
		case <-tw.stopped:
			// Deliberately not marking busy: this goroutine is leaving, and a
			// participant that never parks again would block the clock
			// forever. Unregister instead.
			if tw.barrier != nil {
				if u, ok := tw.barrier.(interface{ Unregister() }); ok {
					u.Unregister()
				}
			}
			return
		}
	}
}

func (tw *timeWheel[P]) Len() int {
	tw.mu.RLock()
	defer tw.mu.RUnlock()
	return len(tw.index)
}

// stop halts the wheel. It is safe to call more than once: Close on the
// owning socket is expected to be idempotent, and a second close of the
// stopped channel would panic.
func (tw *timeWheel[P]) stop() {
	tw.stopOnce.Do(func() {
		tw.ticker.Stop()
		close(tw.stopped)
	})
}
