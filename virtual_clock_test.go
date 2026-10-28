package utp

import (
	"sync"
	"time"
)

// virtualClock is a Clock whose time moves only when a test says so, so a
// connection's emissions happen at exact, known instants, as the reference
// driver's do.
//
// Moving time is trivial; knowing that the connection has finished reacting
// to the previous instant is not, since it runs on its own goroutine. So the
// connection cooperates: its event loop marks itself idle just before it
// blocks and busy as soon as it wakes, and Advance waits for every
// registered participant to be idle before it moves.
type virtualClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*virtualWaiter
	// parked counts participants currently blocked and registered how many
	// exist. Advance moves only while they are equal.
	parked     int
	registered int
	// wakes counts every MarkBusy. Delivering into a participant's channel
	// is asynchronous, so "everything is parked" immediately afterwards is
	// stale, and acting on it would drop the next expiry on a full channel.
	// Waiting for this to move first shows the delivery has been picked up.
	wakes uint64
	// inflight counts things handed to a participant that has not yet woken
	// to take them. See idleBarrier.NoteHandoff.
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
	// fn, when set, is run by Advance instead of a send on ch, which is then
	// nil: a timer created by AfterFunc.
	fn func()
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

func (c *virtualClock) NewTimer(d time.Duration) clockTimer {
	return c.add(d, 0)
}

// AfterFunc implements funcTimerClock. Advance runs f on its own goroutine at
// the deadline and then waits for the reaction, as it does for a send.
func (c *virtualClock) AfterFunc(d time.Duration, f func()) clockTimer {
	w := c.add(d, 0)
	c.mu.Lock()
	w.ch, w.fn = nil, f
	c.mu.Unlock()
	return w
}

func (c *virtualClock) NewTicker(d time.Duration) clockTicker {
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
	// Drain a pending expiry, as (*time.Timer).Reset callers expect after
	// Stop.
	select {
	case <-w.ch:
	default:
	}
	return was
}

// Register implements idleBarrier.
func (c *virtualClock) Register() {
	c.mu.Lock()
	c.registered++
	c.cond.Broadcast()
	c.mu.Unlock()
}

// Unregister removes a participant that is going away for good. A goroutine
// that has exited can never park again, and would stop the clock.
func (c *virtualClock) Unregister() {
	c.mu.Lock()
	if c.registered > 0 {
		c.registered--
	}
	c.cond.Broadcast()
	c.mu.Unlock()
}

// Participants returns how many participants are registered. A connection's
// event loop unregisters as it ends, inside the step that ended it, so this
// shows the exact moment a connection gave up.
func (c *virtualClock) Participants() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.registered
}

// MarkIdle implements idleBarrier: the caller is about to block.
func (c *virtualClock) MarkIdle() {
	c.mu.Lock()
	c.parked++
	c.cond.Broadcast()
	c.mu.Unlock()
}

// MarkBusy implements idleBarrier: the caller has woken.
func (c *virtualClock) MarkBusy() {
	c.mu.Lock()
	if c.parked > 0 {
		c.parked--
	}
	c.wakes++
	c.cond.Broadcast()
	c.mu.Unlock()
}

// NoteHandoff implements idleBarrier: the caller is about to queue something
// for another participant.
func (c *virtualClock) NoteHandoff() {
	c.mu.Lock()
	c.inflight++
	c.mu.Unlock()
}

// TakeHandoff implements idleBarrier: the caller now holds the item, or the
// sender is canceling a handoff that did not happen. It is separate from
// MarkBusy because participants wake for other reasons, and letting any wake
// consume a handoff would clear the count while the item was still queued.
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

// wakeCount returns the number of wake-ups so far.
func (c *virtualClock) wakeCount() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wakes
}

// AwaitReactionTo runs an action that wakes the system, typically injecting a
// packet, and blocks until the reaction to it is complete. The test's
// goroutine is not a participant, so checking afterwards whether everything
// is parked would describe the state before the action; this captures the
// wake count first.
func (c *virtualClock) AwaitReactionTo(f func()) {
	before := c.wakeCount()
	f()
	c.awaitReaction(before)
}

// AwaitParticipants blocks until at least n participants have registered.
// It is needed before the first AwaitQuiet, since "everything registered is
// parked" is trivially true before anything has registered.
func (c *virtualClock) AwaitParticipants(n int) {
	c.mu.Lock()
	for c.registered < n {
		c.cond.Wait()
	}
	c.mu.Unlock()
}

// AwaitQuiet blocks until every registered participant is parked. A harness
// calls it after injecting a packet to learn that the reaction is over.
func (c *virtualClock) AwaitQuiet() {
	c.mu.Lock()
	for c.registered == 0 || c.parked < c.registered || c.inflight > 0 {
		c.cond.Wait()
	}
	c.mu.Unlock()
}

// Advance moves time forward by d, firing every waiter due along the way and
// letting the participants react to each before moving on. Firing in deadline
// order and waiting between matters: a timer armed in reaction to an expiry
// must be scheduled against that instant, not the end of the advance.
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
		fn := next.fn
		c.mu.Unlock()

		if fn != nil {
			// A function timer reacts by waking a participant, such as a resting
			// connection's loop, and is waited for as a delivered send is.
			fn()
			c.awaitReaction(wakesBefore)
			continue
		}
		select {
		case ch <- fireAt:
			// Wait for the delivery to be picked up and for everything to be
			// parked again before considering the next deadline.
			c.awaitReaction(wakesBefore)
		default:
			// A timer channel holds one pending expiry, as time.Timer's does; a
			// participant that has not drained the last one is not owed a
			// second, and nothing was delivered to wait for.
		}
	}
}
