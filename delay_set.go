package utp_go

import (
	"sync"
	"time"
)

type expireFunc[P any] func(key any, value P)

type timeWheelItem[P any] struct {
	value P
	key   any
}

type timeWheel[P any] struct {
	stopped          chan struct{}
	interval         time.Duration
	slots            []map[any]*timeWheelItem[P]
	ticker           *time.Ticker
	current          int
	slotNum          int
	maxDelay         time.Duration
	handleExpireFunc expireFunc[P]
	mu               sync.RWMutex
	stopOnce         sync.Once
}

// 0.25s                   8
func newTimeWheel[P any](interval time.Duration, slotNum int, handleExpireFunc expireFunc[P]) *timeWheel[P] {
	tw := &timeWheel[P]{
		stopped:          make(chan struct{}),
		interval:         interval,
		slots:            make([]map[any]*timeWheelItem[P], slotNum),
		current:          0,
		slotNum:          slotNum,
		maxDelay:         time.Duration(slotNum) * interval,
		handleExpireFunc: handleExpireFunc,
	}

	for i := 0; i < slotNum; i++ {
		tw.slots[i] = make(map[any]*timeWheelItem[P])
	}

	tw.ticker = time.NewTicker(interval)
	go tw.run()
	return tw
}

func (tw *timeWheel[P]) put(key any, value P, delay time.Duration) {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	if delay > tw.maxDelay {
		delay = tw.maxDelay
	}

	slots := int(delay / tw.interval)
	index := (tw.current + slots) % tw.slotNum
	tw.slots[index][key] = &timeWheelItem[P]{
		key:   key,
		value: value,
	}
}

func (tw *timeWheel[P]) retain(shouldRemove func(key any) bool) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	for i := 0; i < tw.slotNum; i++ {
		for key := range tw.slots[i] {
			if shouldRemove(key) {
				delete(tw.slots[i], key)
			}
		}
	}
}

func (tw *timeWheel[P]) remove(key any) {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	for i := 0; i < tw.slotNum; i++ {
		if _, exists := tw.slots[i][key]; exists {
			delete(tw.slots[i], key)
			return
		}
	}
}

func (tw *timeWheel[P]) run() {
	var expired []*timeWheelItem[P]
	for {
		select {
		case <-tw.ticker.C:
			// Collect the expired items under the lock, then dispatch them
			// with the lock released. handleExpireFunc blocks (it hands the
			// packet to the connection's event loop over a channel), and that
			// same event loop calls put/remove/retain, which need this lock.
			// Running the callback under the lock deadlocks the connection as
			// soon as the timeout channel fills up.
			expired = expired[:0]
			tw.mu.Lock()
			currentSlot := tw.slots[tw.current]
			for key, item := range currentSlot {
				delete(currentSlot, key)
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
	length := 0
	for i := 0; i < tw.slotNum; i++ {
		length += len(tw.slots[i])
	}
	return length
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
