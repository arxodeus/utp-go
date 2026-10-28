package utp

import (
	"sync"
	"time"
)

type expireFunc[K comparable, P any] func(key K, value P)

type timeWheelItem[K comparable, P any] struct {
	value P
	key   K
	// rounds is how many further full revolutions must pass before the item
	// expires, so delays may exceed one revolution.
	rounds int
}

// timeWheel is a hashed timing wheel. An item fires on the first tick at or
// after its deadline: never early, and less than one interval late. A rounds
// counter lets a delay exceed slotNum*interval, so exponential backoff is not
// clamped. Removal is O(1) through an index from key to slot. Items are
// stored by value, so scheduling does not allocate.
type timeWheel[K comparable, P any] struct {
	stopped  chan struct{}
	interval time.Duration
	slots    []map[K]timeWheelItem[K, P]
	index    map[K]int
	ticker   clockTicker
	clk      clockSource
	// nextTick is when the tick that processes slots[current] is due: the
	// last tick's scheduled time plus one interval. A ticker keeps its
	// phase, so the next tick is never sooner than this. See put.
	nextTick         time.Time
	current          int
	slotNum          int
	handleExpireFunc expireFunc[K, P]
	barrier          idleBarrier
	mu               sync.RWMutex
	stopOnce         sync.Once
}

func newTimeWheel[K comparable, P any](interval time.Duration, slotNum int, handleExpireFunc expireFunc[K, P]) *timeWheel[K, P] {
	return newTimeWheelWithClock(interval, slotNum, defaultClock, handleExpireFunc)
}

// newTimeWheelWithClock returns a wheel driven by clk. The wheel is shared
// by every connection on a socket, so they all share its notion of time.
func newTimeWheelWithClock[K comparable, P any](interval time.Duration, slotNum int, clk clockSource, handleExpireFunc expireFunc[K, P]) *timeWheel[K, P] {
	tw := &timeWheel[K, P]{
		stopped:          make(chan struct{}),
		interval:         interval,
		slots:            make([]map[K]timeWheelItem[K, P], slotNum),
		index:            make(map[K]int),
		current:          0,
		slotNum:          slotNum,
		handleExpireFunc: handleExpireFunc,
	}

	for i := 0; i < slotNum; i++ {
		tw.slots[i] = make(map[K]timeWheelItem[K, P])
	}

	if clk == nil {
		clk = defaultClock
	}
	tw.clk = clk
	// Read before the ticker exists, so the first tick is no sooner than
	// this.
	tw.nextTick = clk.Now().Add(interval)
	tw.ticker = clk.NewTicker(interval)
	// A virtual clock must not advance while a tick is being processed.
	// Most ticks expire nothing and reach no connection, so the wheel
	// reports its own activity to the clock.
	tw.barrier, _ = clk.(idleBarrier)
	if tw.barrier != nil {
		tw.barrier.Register()
	}
	go tw.run()
	return tw
}

// wheelResolution is the precision to which a deadline is honored. See put.
const wheelResolution = time.Millisecond

// put schedules value to expire after delay. Putting an existing key
// reschedules it.
//
// The item goes in the slot of the first tick due at or after the deadline,
// rounded to the millisecond, so it never fires early and fires less than
// one interval late. Rounding keeps a chain of retransmissions, each re-armed
// from the tick that fired the last, from drifting a tick later every time.
func (tw *timeWheel[K, P]) put(key K, value P, delay time.Duration) {
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
	tw.slots[idx][key] = timeWheelItem[K, P]{
		key:    key,
		value:  value,
		rounds: ticks / tw.slotNum,
	}
	tw.index[key] = idx
}

func (tw *timeWheel[K, P]) remove(key K) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if idx, exists := tw.index[key]; exists {
		delete(tw.slots[idx], key)
		delete(tw.index, key)
	}
}

// contains reports whether key is currently scheduled.
func (tw *timeWheel[K, P]) contains(key K) bool {
	tw.mu.RLock()
	defer tw.mu.RUnlock()
	_, exists := tw.index[key]
	return exists
}

func (tw *timeWheel[K, P]) run() {
	var expired []timeWheelItem[K, P]
	for {
		if tw.barrier != nil {
			tw.barrier.MarkIdle()
		}
		select {
		case at := <-tw.ticker.C():
			if tw.barrier != nil {
				tw.barrier.MarkBusy()
			}
			// Collect the expired items under the lock and dispatch them with
			// it released: the callback may block on a connection's event
			// loop, which itself calls put and remove.
			expired = expired[:0]
			tw.mu.Lock()
			currentSlot := tw.slots[tw.current]
			for key, item := range currentSlot {
				if item.rounds > 0 {
					item.rounds--
					currentSlot[key] = item
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
			// This goroutine is exiting, so unregister rather than mark busy;
			// a participant that never parks would block the clock.
			if tw.barrier != nil {
				if u, ok := tw.barrier.(interface{ Unregister() }); ok {
					u.Unregister()
				}
			}
			return
		}
	}
}

func (tw *timeWheel[K, P]) Len() int {
	tw.mu.RLock()
	defer tw.mu.RUnlock()
	return len(tw.index)
}

// stop halts the wheel. It is safe to call more than once.
func (tw *timeWheel[K, P]) stop() {
	tw.stopOnce.Do(func() {
		tw.ticker.Stop()
		close(tw.stopped)
	})
}
