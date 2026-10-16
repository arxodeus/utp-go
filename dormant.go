package utp

import "time"

// A connection with nothing to do gives its goroutine back.
//
// Each connection's event loop runs on a goroutine of its own, and a goroutine
// parked in its select keeps its stack: 8 KB once a pass has grown it, 4 KB
// at the least (see runLoop), beside about 8.5 KB of heap. libutp has no
// thread per connection -- its embedder drives every socket from its own
// loop -- and holds about 10 KB per connection all told. Most of the
// connections a BitTorrent client holds are idle most of the time, so that
// stack was most of what an idle connection cost here.
//
// So a connection that is established and has nothing in flight, nothing
// queued and no timer armed but the ones every quiet connection keeps lets its
// loop return: it rests. Everything that can need the loop again restarts it
// (wake): a write, a read freeing room in the read queue, a close, an event
// the socket queues, a timer armed by a pass the socket's reader runs inline,
// the socket's context ending (UtpSocket.wakeConns) or a dial being abandoned,
// and a timer of its own -- one function timer, set for the earliest of the
// deadlines a quiet connection keeps:
//
//   - the keep-alive, one interval after the last packet sent
//     (utp_internal.cpp:1271-1274);
//   - the idle timeout, when one is configured;
//   - the idle window decay, libutp's idle branch of utp_check_timeouts
//     (:1216-1222), which re-arms for ever at a doubling interval and so can
//     never be waited out.
//
// None of this changes what happens on the wire: a resting connection is
// resumed at the instant its parked loop would have woken, and does what the
// woken loop would have done. A packet arriving for a resting connection is
// handled by the socket's reader, as it is for one whose loop is parked
// (receiveInline); only what the reader leaves to the loop restarts it.
//
// The protocol: dormant is set while the loop has gone and nothing has started
// another; it is cleared by exactly one compare-and-swap, by whoever starts
// the next (wake), or by the resting loop itself if it finds a wake arrived
// as it went (rest). asleep is the same fact under mu, for passes the reader
// runs inline.

// funcTimerClock is a Clock that can run a function when a timer expires. A
// connection rests only on one: a resting connection has no goroutine to
// receive from a timer's channel. The real clock is one; a Clock supplied
// without it gives connections that never rest.
type funcTimerClock interface {
	AfterFunc(d time.Duration, f func()) Timer
}

// restingEnabled lets a test or a measurement run connections that never
// rest, for comparison.
var restingEnabled = true

// setupDormancy is the part of the loop's setup that lets it rest. Called
// with mu held.
func (c *connection) setupDormancy() {
	clk, ok := c.timeSource().(funcTimerClock)
	if !ok || !restingEnabled {
		return
	}
	c.restClock = clk
}

// endDormancy undoes setupDormancy, once the loop has ended.
func (c *connection) endDormancy() {
	if c.restTimer != nil {
		c.restTimer.Stop()
	}
}

// mayRest reports whether the loop can let its goroutine go. Called with mu
// held, where the loop would otherwise wait.
//
// Only an established connection rests. One being set up, or closing, has a
// retransmission timer armed or is about to; and the socket must have
// attached the connection for inline delivery, or a packet arriving for it
// would be queued for a loop that is not there.
func (c *connection) mayRest() bool {
	if c.restClock == nil || !c.attached.Load() || c.ctx.Err() != nil {
		return false
	}
	stream := c.stream
	if c.state.stateType != connConnected || c.state.closing != nil ||
		c.closeRequested || c.writeShut ||
		stream.shutdown.Load() || stream.writeClosed.Load() {
		return false
	}
	// Nor one holding bytes back for a reader whose queue is full: that
	// reader is the bottleneck, every read it makes needs a pass, and resting
	// between them started a goroutine and set two timers per read. Measured
	// on go-libutp's loopback benchmark, where the reader is slower than the
	// network: 2.6% off 100 MB (40 interleaved runs, z = -2.35).
	if len(c.armed) != 0 || c.wantWrite || c.ackPending || c.inBatch ||
		c.readsHeld.Load() || len(c.pendingWrites) != 0 ||
		(c.state.SendBuf != nil && c.state.SendBuf.Pending() > 0) {
		return false
	}
	t := &c.loopTimers
	if t.probe.pending || t.ackHold.pending || t.lossProbe.pending || t.rack.pending {
		return false
	}
	return !c.wakeQueued()
}

// wakeQueued reports whether anything is waiting for the loop. Read without
// mu by rest, after it has set dormant: anything queued before that is seen
// here, and anything queued after finds dormant set and starts the loop
// itself.
func (c *connection) wakeQueued() bool {
	stream := c.stream
	return len(stream.streamEvents) != 0 || len(stream.writes) != 0 ||
		len(c.readable) != 0 || len(c.writable) != 0 || len(c.kick) != 0 ||
		len(c.unackTimeoutCh) != 0 || c.ctx.Err() != nil
}

// rest lets the loop's goroutine go. Called with mu held, once mayRest has
// said it can. It reports true if the goroutine should return; false if a
// wake arrived as it went, in which case the loop carries on, mu held, and
// resumes the connection (onResume).
func (c *connection) rest() bool {
	t := &c.loopTimers
	// Stopped, not left armed: a fire into a channel nobody receives from
	// would be lost, and a virtual clock would wait for its reaction for
	// ever. Their deadlines are the rest timer's.
	t.keepAlive.Stop()
	t.idle.stop()
	t.idleRto.stop()
	c.asleep = true
	// Set before anything can need the loop: from here, a wake starts it.
	// A swap, not a store, for its full barrier: the queues are read below
	// without a lock, and a sender that queued before this must be seen.
	c.dormant.Swap(true)
	c.armDormant()
	// Last, so that a virtual clock does not move time while the rest timer
	// is not yet set.
	if c.barrier != nil {
		c.barrier.MarkIdle()
	}
	c.mu.Unlock()
	if !c.wakeQueued() || !c.dormant.CompareAndSwap(true, false) {
		// Nothing arrived as it went, or something did and has already
		// started the loop again.
		return true
	}
	c.mu.Lock()
	if c.barrier != nil {
		c.barrier.MarkBusy()
	}
	return false
}

// armDormant sets the rest timer for the earliest deadline a quiet connection
// keeps. Called with mu held, by rest and by a pass the reader runs inline
// that moves the idle-decay deadline (armIdleRto).
func (c *connection) armDormant() {
	now := c.now()
	at := c.lastSentPacket.Add(c.keepAliveIntervalOrDefault())
	if c.config.MaxIdleTimeout > 0 {
		if idle := c.lastActivity.Add(c.config.MaxIdleTimeout); idle.Before(at) {
			at = idle
		}
	}
	// The conditions under which scheduleIdleRto arms the idle wake-up.
	if c.state.SentPackets != nil && len(c.armed) == 0 && !c.rtoDeadline.IsZero() &&
		c.rtoDeadline.Before(at) {
		at = c.rtoDeadline
	}
	c.restAt = at
	d := max(at.Sub(now), 0)
	if c.restTimer == nil {
		c.restTimer = c.restClock.AfterFunc(d, c.wakeFn)
		return
	}
	c.restTimer.Reset(d)
}

// wake starts the loop of a resting connection, and does nothing to one that
// is not resting. Safe from any goroutine, with or without mu.
func (c *connection) wake() {
	if !c.dormant.Load() || !c.dormant.CompareAndSwap(true, false) {
		return
	}
	// The new goroutine is a handoff a virtual clock must wait for.
	if c.barrier != nil {
		c.barrier.NoteHandoff()
	}
	go c.resume()
}

// resume is a resting connection's loop starting again, on a goroutine of its
// own.
func (c *connection) resume() {
	c.mu.Lock()
	if c.barrier != nil {
		c.barrier.MarkBusy()
	}
	kind := c.onResume()
	if c.barrier != nil {
		c.barrier.TakeHandoff()
	}
	err, final := c.react(wakeResult{kind: kind})
	if !final {
		err, final = c.runLoop()
	}
	if final {
		c.finishLoop()
		c.stream.loopEnded(err)
	}
}

// onResume does what the parked loop's timers would have done had it not
// rested, and sets them again. Called with mu held. It reports the pass that
// follows: wakeResumed, which does the rest of what any pass does; or, if the
// stream's context has ended, wakeCtxDone, which ends the connection without
// one, as the parked loop's select would have.
func (c *connection) onResume() wakeKind {
	c.asleep = false
	c.restTimer.Stop()
	if c.ctx.Err() != nil {
		return wakeCtxDone
	}
	now := c.now()
	if due := c.lastSentPacket.Add(c.keepAliveIntervalOrDefault()); !now.Before(due) {
		c.onKeepAliveTimer()
	} else {
		c.loopTimers.keepAlive.Reset(due.Sub(now))
	}
	if c.config.MaxIdleTimeout > 0 {
		c.checkIdleTimeout()
	}
	// A no-op unless the deadline has passed; the pass re-arms it
	// (scheduleIdleRto).
	c.onIdleRto(now)
	return wakeResumed
}
