package utp_go

import (
	"sort"
	"sync"
	"time"
)

// virtualClock is a Clock whose time moves only when a test says so.
//
// It exists to take a connection off the real clock entirely. The conformance
// corpus drives real libutp through a driver that is synchronous and has a
// virtual clock, so libutp's emissions happen at exact, known instants. Ours
// happened whenever a goroutine was scheduled, which is why every timing
// comparison in this repository is either coarse, a bound, or absent -- and
// why two harness defects this session were timing assumptions rather than
// library faults.
//
// # The hard part is not the clock, it is knowing when to advance it
//
// Moving time forward is trivial. Knowing that the connection has *finished*
// reacting to the previous instant is not: it runs on its own goroutine, and
// from outside there is no way to tell "parked in select with nothing to do"
// from "about to emit a packet".
//
// So the connection cooperates. Its event loop marks itself idle just before
// it blocks and busy as soon as it wakes, and Advance waits for every
// registered participant to be idle before it moves. That turns "wait 25ms
// and hope" into an actual barrier.
type virtualClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*virtualWaiter
	// parks counts how many times a participant has reported that it is
	// about to block, and registered how many participants exist. Advance
	// waits for parks to rise rather than for a state to be true, so a
	// wake-up that is never followed by another park blocks it rather than
	// letting it race ahead.
	// parked counts participants currently blocked and registered how many
	// exist. Advance moves only while they are equal.
	parked     int
	registered int
	// wakes counts every MarkBusy. Delivering into a participant's channel
	// is asynchronous -- the send returns long before the participant runs --
	// so "everything is parked" immediately afterwards is a *stale* answer,
	// and acting on it drops the next fire on a full channel. Waiting for
	// this to move first is how the clock knows the delivery has actually
	// been picked up.
	//
	// Measured before it existed: the retransmission wheel ticks every 25ms,
	// so a two-second advance should fire it eighty times; with the stale
	// check it took delivery of one and dropped seventy-nine, and nothing
	// ever retransmitted.
	wakes uint64
	// inflight counts things handed to a participant that has not yet woken
	// to take them. See IdleBarrier.NoteHandoff.
	inflight int
	cond     *sync.Cond
}

type virtualWaiter struct {
	at       time.Time
	period   time.Duration // non-zero for a ticker
	ch       chan time.Time
	stopped  bool
	repeats  bool
	clockRef *virtualClock
}

func newVirtualClock(start time.Time) *virtualClock {
	c := &virtualClock{now: start}
	c.cond = sync.NewCond(&c.mu)
	return c
}

func (c *virtualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *virtualClock) NewTimer(d time.Duration) Timer {
	return c.add(d, 0)
}

func (c *virtualClock) NewTicker(d time.Duration) Ticker {
	return &virtualTicker{w: c.add(d, d)}
}

// virtualTicker adapts a waiter to Ticker, whose Stop returns nothing.
type virtualTicker struct{ w *virtualWaiter }

func (t *virtualTicker) C() <-chan time.Time { return t.w.C() }
func (t *virtualTicker) Stop()               { t.w.Stop() }

func (c *virtualClock) add(d, period time.Duration) *virtualWaiter {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := &virtualWaiter{
		at:       c.now.Add(d),
		period:   period,
		repeats:  period > 0,
		ch:       make(chan time.Time, 1),
		clockRef: c,
	}
	c.waiters = append(c.waiters, w)
	return w
}

func (w *virtualWaiter) C() <-chan time.Time { return w.ch }

func (w *virtualWaiter) Stop() bool {
	w.clockRef.mu.Lock()
	defer w.clockRef.mu.Unlock()
	was := !w.stopped
	w.stopped = true
	return was
}

func (w *virtualWaiter) Reset(d time.Duration) bool {
	w.clockRef.mu.Lock()
	defer w.clockRef.mu.Unlock()
	was := !w.stopped
	w.stopped = false
	w.at = w.clockRef.now.Add(d)
	// Drain a pending fire, as (*time.Timer).Reset callers expect after Stop.
	select {
	case <-w.ch:
	default:
	}
	return was
}

// Register implements IdleBarrier.
func (c *virtualClock) Register() {
	c.mu.Lock()
	c.registered++
	c.cond.Broadcast()
	c.mu.Unlock()
}

// Unregister removes a participant that is going away for good. A goroutine
// that has exited can never park again, and one that never parks would stop
// the clock permanently.
func (c *virtualClock) Unregister() {
	c.mu.Lock()
	if c.registered > 0 {
		c.registered--
	}
	c.cond.Broadcast()
	c.mu.Unlock()
}

// MarkIdle implements IdleBarrier: the caller is about to block.
func (c *virtualClock) MarkIdle() {
	c.mu.Lock()
	c.parked++
	c.cond.Broadcast()
	c.mu.Unlock()
}

// MarkBusy implements IdleBarrier: the caller has woken.
func (c *virtualClock) MarkBusy() {
	c.mu.Lock()
	if c.parked > 0 {
		c.parked--
	}
	c.wakes++
	c.cond.Broadcast()
	c.mu.Unlock()
}

// NoteHandoff implements IdleBarrier: the caller is about to queue something
// for another participant.
func (c *virtualClock) NoteHandoff() {
	c.mu.Lock()
	c.inflight++
	c.mu.Unlock()
}

// TakeHandoff implements IdleBarrier: the caller now holds the item, or the
// sender is cancelling a handoff that did not happen.
//
// Deliberately not folded into MarkBusy. A participant wakes for reasons that
// have nothing to do with a handoff -- the wheel wakes on every tick, most of
// which expire nothing -- and letting any wake consume an outstanding handoff
// would clear the count while the item was still queued, which is the state
// this exists to detect.
func (c *virtualClock) TakeHandoff() {
	c.mu.Lock()
	if c.inflight > 0 {
		c.inflight--
	}
	c.cond.Broadcast()
	c.mu.Unlock()
}

// awaitReaction blocks until at least one participant has woken since the
// given wake count and everything is parked again.
func (c *virtualClock) awaitReaction(since uint64) {
	c.mu.Lock()
	for c.wakes <= since || c.registered == 0 || c.parked < c.registered || c.inflight > 0 {
		c.cond.Wait()
	}
	c.mu.Unlock()
}

// wakeCount is the number of wake-ups seen so far.
func (c *virtualClock) wakeCount() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wakes
}

// AwaitReactionTo runs an action that wakes the system -- injecting a packet,
// typically -- and blocks until the reaction to it is complete.
//
// A test's own goroutine is not a participant, so it cannot simply act and
// then ask whether everything is parked: the answer would describe the state
// before the action, and every participant would still be parked from the
// moment before. This captures the wake count first, so the wait cannot be
// satisfied by the past.
func (c *virtualClock) AwaitReactionTo(f func()) {
	before := c.wakeCount()
	f()
	c.awaitReaction(before)
}

// AwaitParticipants blocks until at least n participants have registered.
//
// Necessary before the first AwaitQuiet, because "everything registered so
// far is parked" is trivially true when nothing has registered yet. The
// socket's retransmission wheel registers as soon as it is built and a
// connection's event loop only when its goroutine runs, so a test that waited
// for quiet without this would be told the connection was idle before it had
// started -- which it was, and which is not what the question meant.
func (c *virtualClock) AwaitParticipants(n int) {
	c.mu.Lock()
	for c.registered < n {
		c.cond.Wait()
	}
	c.mu.Unlock()
}

// AwaitQuiet blocks until every registered participant is parked.
//
// A harness calls this after injecting a packet: the injection wakes the
// connection, and this is how it learns the reaction is over. It is the same
// question Advance asks, and the reason neither needs a settle loop.
func (c *virtualClock) AwaitQuiet() {
	c.mu.Lock()
	for c.registered == 0 || c.parked < c.registered || c.inflight > 0 {
		c.cond.Wait()
	}
	c.mu.Unlock()
}

// Advance moves time forward by d, firing every waiter due along the way and
// letting the participants react to each before moving on.
//
// Firing in deadline order and waiting between matters: a connection that
// reacts to a retransmission timeout by arming another timer must have that
// timer scheduled against the right instant, not against the end of the whole
// advance.
func (c *virtualClock) Advance(d time.Duration) {
	c.AwaitQuiet()
	target := c.Now().Add(d)
	for {
		c.mu.Lock()
		var next *virtualWaiter
		for _, w := range c.waiters {
			if w.stopped || w.at.After(target) {
				continue
			}
			if next == nil || w.at.Before(next.at) {
				next = w
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		c.now = next.at
		fireAt := next.at
		if next.repeats {
			next.at = next.at.Add(next.period)
		} else {
			next.stopped = true
		}
		ch := next.ch
		wakesBefore := c.wakes
		c.mu.Unlock()

		select {
		case ch <- fireAt:
			// Wait for the delivery to be picked up *and* for everything to
			// be parked again before considering the next deadline. Without
			// the first half the answer is stale and the next fire is
			// dropped; without the second, a connection that answers a
			// retransmission timeout by arming another timer would have that
			// timer scheduled against the end of the whole advance rather
			// than against the instant it was armed at.
			c.awaitReaction(wakesBefore)
		default:
			// A timer channel holds one pending fire, exactly as
			// time.Timer's does; a participant that has not drained the last
			// one is not owed a second, and nothing was delivered to wait
			// for.
		}
	}
}

// sortedDeadlines is a debugging aid: what is armed, in order.
func (c *virtualClock) sortedDeadlines() []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []time.Time
	for _, w := range c.waiters {
		if !w.stopped {
			out = append(out, w.at)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}
