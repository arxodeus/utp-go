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
//     about 6% of them on a lossless path. A delay is now rounded up to a
//     whole number of ticks and never fires early.
//   - Range. Delays beyond slotNum*interval used to be clamped to that
//     ceiling, so exponential RTO backoff stopped growing. The rounds
//     counter removes the ceiling.
//
// Removal is O(1): an index maps each key to the slot holding it. Scanning
// every slot was acceptable for a per-connection wheel but not for one shared
// across a whole socket.
type timeWheel[P any] struct {
	stopped          chan struct{}
	interval         time.Duration
	slots            []map[any]*timeWheelItem[P]
	index            map[any]int
	ticker           *time.Ticker
	current          int
	slotNum          int
	handleExpireFunc expireFunc[P]
	mu               sync.RWMutex
	stopOnce         sync.Once
}

func newTimeWheel[P any](interval time.Duration, slotNum int, handleExpireFunc expireFunc[P]) *timeWheel[P] {
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

	tw.ticker = time.NewTicker(interval)
	go tw.run()
	return tw
}

// put schedules value to expire after delay. Re-putting an existing key
// reschedules it.
//
// The delay is rounded up to a whole number of ticks, so an item never fires
// early. It may fire up to one interval late, which is the wheel's
// resolution.
func (tw *timeWheel[P]) put(key any, value P, delay time.Duration) {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	if old, exists := tw.index[key]; exists {
		delete(tw.slots[old], key)
		delete(tw.index, key)
	}

	ticks := int(delay / tw.interval)
	if delay%tw.interval != 0 {
		ticks++
	}
	if ticks < 1 {
		// Never schedule into the slot about to be processed: that would fire
		// immediately rather than after the requested delay.
		ticks = 1
	}

	// The next tick processes slots[current], and it may be about to happen:
	// the wheel ticks on its own schedule, not on ours, so the gap between
	// now and the next tick is anywhere from nothing to a full interval.
	//
	// An item placed at current+n-1 is therefore fired on the n'th tick from
	// now, which is between (n-1) and n intervals away -- up to a full
	// interval *early*, contradicting the promise above. Placing it at
	// current+n fires it on the (n+1)'th tick, between n and n+1 intervals
	// away: never early, at most one interval late.
	//
	// Measured against libutp before the fix: a SYN with a 200ms timeout was
	// retransmitted at 186ms. At the real 3000ms timeout the error is under
	// 1%, but early is the wrong direction -- it resends a packet the peer
	// was still going to acknowledge, and libutp cannot do it, because it
	// compares the clock against rto_timeout rather than trusting a timer
	// (utp_internal.cpp:1147-1148).
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
		select {
		case <-tw.ticker.C:
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
			tw.mu.Unlock()

			for _, item := range expired {
				tw.handleExpireFunc(item.key, item.value)
			}
		case <-tw.stopped:
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
