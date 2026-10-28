package utp

import "time"

// A connection with nothing to do releases its goroutine.
//
// Each connection's event loop runs on its own goroutine, and a parked
// goroutine keeps its stack. Most connections in a large swarm are idle, so
// an established connection with nothing in flight, nothing queued and only
// the timers every quiet connection keeps lets its loop return: it rests.
//
// Anything that needs the loop again restarts it (wake): a write, a read that
// frees room in the read queue, a close, an event from the socket, a timer
// armed by a pass the reader runs inline, the socket's context ending
// (UtpSocket.wakeConns), an abandoned dial, or the connection's single rest
// timer, set for the earliest of:
//
//   - the keep-alive, one interval after the last packet sent;
//   - the idle timeout, if configured;
//   - the idle window decay, which re-arms at a doubling interval.
//
// Resting does not change what is sent: a resting connection resumes at the
// instant its parked loop would have woken and does what that loop would
// have done. Packets for a resting connection are handled by the reader
// inline (receiveInline), as for one whose loop is parked.
//
// dormant is set while the loop is gone. It is cleared by exactly one
// compare-and-swap, either by whoever starts the next loop (wake) or by the
// resting loop itself if a wake arrived as it left (rest). asleep records the
// same state under mu, for passes the reader runs inline.

// funcTimerClock is a Clock that can run a function when a timer expires. A
// resting connection has no goroutine to receive from a timer channel, so
// connections rest only on such a clock. RealClock is one.
type funcTimerClock interface {
	AfterFunc(d time.Duration, f func()) Timer
}

// restingEnabled allows tests to run connections that never rest.
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

// mayRest reports whether the loop can release its goroutine. Called with mu
// held, where the loop would otherwise wait.
//
// Only an established connection rests: one being set up or closing has a
// retransmission timer armed or soon will, and the socket must have attached
// the connection for inline delivery.
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
	// Nor one holding bytes for a reader whose queue is full: each read
	// needs a pass, and resting between them would cost a goroutine start
	// and two timers per read.
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

// wakeQueued reports whether anything is waiting for the loop. rest reads
// it without mu after setting dormant: anything queued before that is seen
// here, and anything queued after finds dormant set and starts the loop
// itself.
func (c *connection) wakeQueued() bool {
	stream := c.stream
	return len(stream.streamEvents) != 0 || len(stream.writes) != 0 ||
		len(c.readable) != 0 || len(c.writable) != 0 || len(c.kick) != 0 ||
		len(c.unackTimeoutCh) != 0 || c.ctx.Err() != nil
}

// rest releases the loop's goroutine. Called with mu held once mayRest
// allows it. It reports whether the goroutine should return; false means a
// wake arrived meanwhile, and the loop continues with mu held and resumes
// the connection (onResume).
func (c *connection) rest() bool {
	t := &c.loopTimers
	// Stop these rather than leave them armed: nobody receives from their
	// channels while resting. The rest timer covers their deadlines.
	t.keepAlive.Stop()
	t.idle.stop()
	t.idleRto.stop()
	c.asleep = true
	// From here a wake starts the loop. A swap rather than a store, for its
	// full barrier: the queues are read below without a lock.
	c.dormant.Swap(true)
	c.armDormant()
	// Last, so a virtual clock does not advance before the rest timer is
	// set.
	if c.barrier != nil {
		c.barrier.MarkIdle()
	}
	c.mu.Unlock()
	if !c.wakeQueued() || !c.dormant.CompareAndSwap(true, false) {
		// Nothing arrived, or something did and has already restarted the
		// loop.
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

// resume restarts a resting connection's loop on a new goroutine.
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

// onResume does what the parked loop's timers would have done and re-arms
// them. Called with mu held. It returns the pass to run next: wakeResumed,
// or wakeCtxDone if the stream's context has ended.
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
