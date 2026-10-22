package utp

import (
	"time"
)

// lazyTimer is a Timer made the first time it is armed. Until then its
// channel is nil, which a select never takes, so a connection holds only
// the timers it has used.
//
// A timer made while the event loop is parked is one its select is not
// watching: the loop chose its channels before the timer existed, and the
// socket's reader arms timers in passes it runs inline. So making one wakes
// the loop (kickLoop), which goes round and selects on it. History:
// HISTORY.md, "lazyTimer".
type lazyTimer struct {
	c *connection
	t Timer
	// pending is set while the timer is armed and its fire has not been
	// taken: a resting connection (dormant.go) must have none of these.
	pending bool
}

func (c *connection) newLazyTimer() *lazyTimer {
	return &lazyTimer{c: c}
}

// C is the timer's channel, or nil before it is first armed.
func (l *lazyTimer) C() <-chan time.Time {
	if l.t == nil {
		return nil
	}
	return l.t.C()
}

// arm sets the timer to fire after d, dropping a fire not yet received.
//
// A timer armed while the connection rests has no loop to fire into, so
// arming one wakes it (wake). A connection that is not resting does nothing
// for that but a load.
func (l *lazyTimer) arm(d time.Duration) {
	l.pending = true
	if l.t == nil {
		l.t = l.c.timeSource().NewTimer(d)
		l.c.kickLoop()
		return
	}
	if !l.t.Stop() {
		select {
		case <-l.t.C():
		default:
		}
	}
	l.t.Reset(d)
	l.c.wake()
}

// fired records that the event loop has taken the timer's fire.
func (l *lazyTimer) fired() { l.pending = false }

// stop stops the timer, if it has been made.
func (l *lazyTimer) stop() {
	l.pending = false
	if l.t != nil {
		l.t.Stop()
	}
}

// loopTimers are the timers the event loop waits on. They are the
// connection's rather than the loop's locals so that the loop can stop and
// start again (see dormant.go), and so that the frame it waits in stays small
// (see wait).
type loopTimers struct {
	// keepAlive is made with the loop and always armed while it runs.
	keepAlive Timer
	// The rest are made the first time they are armed. See lazyTimer.
	idle, probe, ackHold, idleRto, lossProbe, rack *lazyTimer
}

// wakeResult is what the event loop's blocking select received: which case
// fired, and what it carried.
type wakeResult struct {
	kind  wakeKind
	event *streamEvent
	write *queuedWrite
	ok    bool
	timer *packet
}

// eventLoop sets the connection up and runs it until it ends or rests. It
// reports the error the connection ended with, and whether it ended: a
// connection that rests (dormant.go) returns with final unset, and its loop
// is started again by whatever next needs it.
func (c *connection) eventLoop(stream *UtpStream) (error, bool) {
	c.setupLoop(stream)
	return c.runLoop()
}

// setupLoop is everything the event loop does once, before its first pass.
// Called with mu held, from newUtpStream; the loop releases it the first time
// it waits.
func (c *connection) setupLoop(stream *UtpStream) {
	if c.logger.Enabled(baseContext, LevelTrace) {
		c.logger.Trace("uTP conn starting", "dst.peer", c.cid.Peer, "cid.Send", c.cid.Send, "cid.Recv", c.cid.Recv)
	}
	// mu is held for setup, from newUtpStream, and for every pass; it is
	// released only while the loop waits. See connection.mu.
	// Initialize connection based on endpoint type
	if c.endpoint.Type == initiator {
		synSeqNum := c.endpoint.SynNum
		synPkt := c.synPacket(synSeqNum)
		c.emit(synPkt)
		if c.logger.Enabled(baseContext, LevelTrace) {
			c.logger.Trace("put a initial syn packet to delay map", "socketEvents.len", len(c.socketEvents), "dst.peer", c.cid.Peer, "synSeqNum", synSeqNum)
		}
		c.synTimeout = c.config.InitialTimeout
		c.armRetransmit(synPkt, c.synTimeout)
		c.synSentAt = c.now()

		c.endpoint.Attempts = 1
	} else {
		// Acceptor connection
		syn := c.endpoint.SynNum
		synAck := c.endpoint.SynAck

		c.synState = c.statePacket()

		c.logger.Debug("a initial state packet", "peer", c.cid.Peer, "cid.Send", c.cid.Send, "cid.Recv", c.cid.Recv)
		c.emit(c.synState)

		recvBuf := newReceiveBufferWithLogger(c.config.BufferSize, syn, c.logger)
		sendBuf := newSendBuffer(c.config.BufferSize)
		congestionCtrl := newDefaultController(c.controllerConfig())
		sentPacketsHolder := newSentPackets(synAck-1, congestionCtrl, c.logger) // wrapping subtraction
		sentPacketsHolder.rack.active = c.config.RACK

		if c.state != nil && c.state.connectedCh != nil {
			c.state.connectedCh <- nil
		} else {
			panic("connection in invalid statePacket prior to event loop beginning")
		}

		c.state.stateType = connConnected
		c.state.RecvBuf = recvBuf
		c.state.SendBuf = sendBuf
		c.state.SentPackets = sentPacketsHolder
	}

	t := &c.loopTimers
	// No idle timeout unless one is configured, and no timer for it either:
	// this and the other timers below that most connections never use are
	// made when first armed (lazyTimer).
	t.idle = c.newLazyTimer()
	if c.config.MaxIdleTimeout > 0 {
		t.idle.arm(c.config.MaxIdleTimeout)
	}
	c.lastActivity = c.now()

	// The zero-window probe needs a wake-up of its own.
	//
	// Every other reason this loop runs is an event: a packet arrived, the
	// application wrote, a retransmission timer expired. A closed peer window
	// is the absence of all three -- nothing is outstanding, so no
	// retransmission timer exists, and nothing will arrive until the peer
	// chooses to speak. Without this the probe would be armed and never
	// checked.
	//
	// An established connection that has gone quiet still has to say
	// something occasionally, or a NAT drops its mapping and the peer's own
	// idle timeout eventually kills it. libutp checks
	// `current_ms - last_sent_packet >= KEEPALIVE_INTERVAL` on every timeout
	// pass (utp_internal.cpp:1271-1274), so its keep-alive leaves one
	// interval after the last packet, to within the 500ms libutp allows
	// between passes however often its embedder calls it
	// (TIMEOUT_CHECK_INTERVAL, :37, :3284). This is a timer aimed at that
	// instant, re-aimed from lastSentPacket every time it fires. History:
	// HISTORY.md, "setupLoop: the zero-window probe and keep-alive timers".
	t.keepAlive = c.timeSource().NewTimer(c.keepAliveIntervalOrDefault())

	t.probe = c.newLazyTimer()
	t.ackHold = c.newLazyTimer()
	c.armAckHold = t.ackHold.arm
	c.armProbeTimer = t.probe.arm
	// An idle connection's window has to decay, and nothing else here would
	// wake the loop to do it.
	//
	// Retransmission timers are armed per packet, so a connection with
	// nothing outstanding has none, and its event loop is completely
	// quiescent. libutp keeps one deadline per socket and never clears it when
	// the window empties (it is set at utp_internal.cpp:997, reset on each
	// acknowledged packet at :1389, re-armed on each expiry at :1204); when it
	// passes with nothing in flight the idle branch decays the window by a
	// third (:1216-1222). Because `retransmit_count` is only incremented when
	// something *is* outstanding (:1240), that repeats without ever killing
	// the connection. netem.TestLibutpIdleWindowDecay holds this one to it.
	//
	// A resting connection has no loop to wake: its rest timer is already
	// set for this deadline (armDormant), and is moved only if a pass the
	// reader ran has brought the deadline forward, which costs a comparison
	// to be sure of rather than a timer operation per read batch. History:
	// HISTORY.md, "setupLoop: the idle RTO".
	t.idleRto = c.newLazyTimer()
	c.armIdleRto = func(d time.Duration) {
		if c.asleep {
			if c.rtoDeadline.Before(c.restAt) {
				c.armDormant()
			}
			return
		}
		t.idleRto.arm(d)
	}

	t.lossProbe = c.newLazyTimer()
	c.armLossProbe = func(d time.Duration) {
		if d > 0 {
			t.lossProbe.arm(d)
		} else {
			t.lossProbe.stop()
		}
	}

	t.rack = c.newLazyTimer()
	c.armRack = func(d time.Duration) {
		if d > 0 {
			t.rack.arm(d)
		} else {
			t.rack.stop()
		}
	}

	// A virtual clock needs to know when this loop has finished reacting, so
	// that it can move time without racing the reaction. Resolved once here
	// rather than per pass; nil for the real clock. See idleBarrier.
	if c.barrier != nil {
		c.barrier.Register()
	}
	c.setupDormancy()

	c.stream = stream
	c.ready = true
}

// finishLoop is everything the event loop does once it has ended, on
// whichever goroutine ran its last pass. Called without mu.
func (c *connection) finishLoop() {
	if c.barrier != nil {
		// A connection that ends -- reset by its peer, timed out, closed --
		// can never park again, and a virtual clock waiting for it would
		// stop. Found by the initiator corpus: a peer that answers a SYN
		// with a RESET tore the connection down and hung the clock.
		//
		// Wakes queued for this loop that it will never take: each was
		// noted as a handoff, and a clock still counting one in flight
		// never moves again. A loop ended by a RESET the reader
		// processed inline left the reader's kick behind, and a test
		// waiting on the clock waited for ever.
		for queued := true; queued; {
			select {
			case <-c.kick:
				c.barrier.TakeHandoff()
			case <-c.unackTimeoutCh:
				c.barrier.TakeHandoff()
			default:
				queued = false
			}
		}
		if u, ok := c.barrier.(interface{ Unregister() }); ok {
			u.Unregister()
		}
	}
	t := &c.loopTimers
	t.rack.stop()
	t.lossProbe.stop()
	t.idleRto.stop()
	t.ackHold.stop()
	t.probe.stop()
	t.keepAlive.Stop()
	t.idle.stop()
	c.endDormancy()
	// Tell the socket to drop this connection on *every* exit, not only the
	// one where the state machine reached connClosed: a connection torn down
	// by a cancelled context -- which is what an abandoned or failed
	// connection attempt is -- must leave the socket's table too. History:
	// HISTORY.md, "finishLoop: reporting to the socket".
	c.notifySocketShutdown()
	// Report the end of the stream to the reader on every exit.
	//
	// Nothing else sends on c.reads by now -- the passes the socket's reader
	// runs stop once finished is set, before teardown -- so closing it here is
	// safe, and it is what lets the loop refuse to block: the end-of-stream
	// marker is best-effort, and a reader that never got it -- because its
	// queue was full when the connection ended -- learns from the close
	// instead, with the reason available in terminalErr.
	//
	// Without this a reader draining a full queue after the loop had gone
	// would wait for a marker that no one was left to send.
	if c.terminalErr.Load() == nil {
		c.terminalErr.Store(&terminalError{err: c.readEndErr()})
	}
	c.endErr.Store(&terminalError{err: c.state.Err})
	close(c.reads)
	// The shared wheel outlives the connection, so anything left armed
	// would linger until it expired and then be delivered to a dead loop.
	c.disarmAll()
}

// runLoop runs passes until the connection ends or rests. Called with mu
// held; returns without it.
//
// It and wait are kept small on purpose: they are the frames a parked loop
// leaves on its goroutine's stack, and Go's garbage collector halves a stack
// only while less than a quarter of it is in use. With everything inline, as
// it was, the parked loop's own frame was 1,760 bytes, and with the frames
// beneath it more than a quarter of the 8 KB it kept: 8.3 KB a connection
// end, measured. Split, it is 4.1-4.3 KB; the handling lives in react, which
// has returned by the time the loop parks. See KNOWN-LIMITATIONS.md, "An
// idle connection held a goroutine".
func (c *connection) runLoop() (err error, final bool) {
	for {
		var w wakeResult
		if c.takeQueued() {
			w.kind = wakeQueued
		} else if c.mayRest() {
			if c.rest() {
				return nil, false
			}
			w.kind = c.onResume()
		} else {
			w = c.wait()
		}
		if err, final := c.react(w); final {
			return err, true
		}
	}
}

// takeQueued handles whatever the socket has already queued for this
// connection, without blocking, and reports whether there was any.
func (c *connection) takeQueued() bool {
	stream := c.stream
	if c.logger.Enabled(baseContext, LevelTrace) {
		c.logger.Trace("connection event count",
			"streamEvents.len", len(stream.streamEvents),
			"readable.len", len(c.readable),
			"writeable.len", len(c.writable))
	}
	select {
	case event := <-stream.streamEvents:
		c.takeIncoming(event)
		c.handleIncoming(event)
		// Take whatever else has already arrived before answering, so one
		// acknowledgement covers it rather than one per packet. flushAck
		// also waits for the rest of the socket read the packets came in
		// (see there).
		//
		// Bounded so that a peer sending continuously cannot keep this
		// inner loop fed and starve writes and timers; the outer loop
		// returns here immediately anyway if more are waiting.
		for drained := 0; drained < maxAckCoalesce; drained++ {
			select {
			case more := <-stream.streamEvents:
				c.takeIncoming(more)
				c.handleIncoming(more)
			default:
				drained = maxAckCoalesce
			}
		}
		return true
	default:
		return false
	}
}

// wait blocks until the loop has something to do, with mu released, and
// reports what woke it.
//
// The select below only *receives*; every case body runs after it, in react.
// That shape is what lets a virtual clock be told the loop is running again
// before any of those bodies can emit a packet -- one MarkBusy, on the one
// path out, rather than one per case where missing a single one would put
// the nondeterminism straight back. See idleBarrier.
func (c *connection) wait() (w wakeResult) {
	stream := c.stream
	t := &c.loopTimers
	// The channels are read while mu is still held: the socket's reader makes
	// lazy timers under it.
	keepAlive, probe, ackHold := t.keepAlive.C(), t.probe.C(), t.ackHold.C()
	idleRto, lossProbe, rack, idle := t.idleRto.C(), t.lossProbe.C(), t.rack.C(), t.idle.C()
	// Tell a virtual clock this loop is about to block, so it knows the
	// reaction to the last instant is complete and time may move. A real
	// clock has no barrier and this is nil. See idleBarrier.
	//
	// Not while a wake this loop queued for itself is still waiting: an
	// incoming packet that reopens the peer's window signals writable, and
	// the data goes out on the next pass. Marked idle with that signal
	// buffered, the select returned at once, but in between the clock saw
	// every participant parked and nothing in flight, called the system
	// quiet, and the packet went out after the step that should have
	// seen it -- TestInitiatorZeroWindow, about once in 160 runs. Only
	// this goroutine queues those wakes before reaching here, so their
	// length is exact; a wake from another goroutine is that goroutine's
	// to account for.
	markedIdle := false
	if c.barrier != nil && len(c.writable) == 0 && len(c.readable) == 0 && len(c.kick) == 0 {
		c.barrier.MarkIdle()
		markedIdle = true
	}
	// The socket's reader may handle a packet for this connection while
	// the loop waits. See receiveInline.
	c.mu.Unlock()
	select {
	case w.event = <-stream.streamEvents:
		w.kind = wakeStreamEvent
	case w.write, w.ok = <-stream.writes:
		w.kind = wakeWrite
	case <-c.readable:
		w.kind = wakeReadable
	case <-c.writable:
		w.kind = wakeWritable
	case w.timer = <-c.unackTimeoutCh:
		w.kind = wakeRetransmit
	case <-keepAlive:
		w.kind = wakeKeepAlive
	case <-probe:
		w.kind = wakeProbe
	case <-ackHold:
		w.kind = wakeAckHold
	case <-idleRto:
		w.kind = wakeIdleRto
	case <-lossProbe:
		w.kind = wakeLossProbe
	case <-rack:
		w.kind = wakeRack
	case <-idle:
		w.kind = wakeIdleTimeout
	case <-c.kick:
		w.kind = wakeKick
	case <-c.ctx.Done():
		w.kind = wakeCtxDone
	}
	c.mu.Lock()
	if markedIdle {
		c.barrier.MarkBusy()
	}
	return w
}

// react handles one wake and ends the pass. It reports whether the
// connection has ended, and with what; when it has, mu has been released.
func (c *connection) react(w wakeResult) (err error, final bool) {
	t := &c.loopTimers
	switch w.kind {
	case wakeQueued, wakeResumed:
		// Already handled, by takeQueued and by onResume.
	case wakeStreamEvent:
		c.takeIncoming(w.event)
		c.handleIncoming(w.event)
	case wakeWrite:
		c.handleWrite(w.write)
	case wakeReadable:
		// Nothing to do but wake: the drain happens in afterPass, for
		// every pass, so that it precedes the acknowledgement.
		//
		// The wake itself is the point. UtpStream.notifyRead signals this
		// when the application takes a chunk, which is the only thing
		// that frees room once the read queue has filled -- without it
		// processReads gives up and waits for a packet that a blocked
		// peer will not send.
	case wakeWritable:
		c.processWrites(c.now())
	case wakeRetransmit:
		// This is the handoff the wheel noted when it queued the timeout.
		if c.barrier != nil {
			c.barrier.TakeHandoff()
		}
		c.handleTimeout(w.timer)
	case wakeKeepAlive:
		c.onKeepAliveTimer()
	case wakeAckHold:
		t.ackHold.fired()
		// Nothing more to do but wake: a held acknowledgement goes out from
		// flushAck in afterPass.
	case wakeKick:
		// The handoff endBatch noted when it kicked. What it wants done
		// happens in afterPass.
		if c.barrier != nil {
			c.barrier.TakeHandoff()
		}
	case wakeProbe:
		t.probe.fired()
		// The peer's window has been closed for a whole interval. Let one
		// packet through, so its acknowledgement carries a fresh window.
		c.processWrites(c.now())
	case wakeIdleRto:
		t.idleRto.fired()
		c.onIdleRto(c.now())
	case wakeLossProbe:
		t.lossProbe.fired()
		c.onLossProbe(c.now())
	case wakeRack:
		t.rack.fired()
		c.onRackTimer(c.now())
	case wakeIdleTimeout:
		t.idle.fired()
		c.checkIdleTimeout()
	case wakeCtxDone:
		c.handleCtxDone()
		if c.logger.Enabled(baseContext, LevelTrace) {
			c.logger.Trace("stream context done, will force stop...", "c.cid.peer", c.cid.Peer, "c.cid.Send", c.cid.Send, "c.cid.Recv", c.cid.Recv)
		}
		c.finished = true
		c.mu.Unlock()
		return c.ctx.Err(), true
	}
	if c.afterPass() {
		// More to send than one pass sends: come round again.
		select {
		case c.writable <- struct{}{}:
		default:
		}
	}

	if c.state.stateType == connClosed {
		if c.logger.Enabled(baseContext, LevelTrace) {
			c.logger.Trace("uTP conn closing...", "err", c.state.Err, "c.cid.Send", c.cid.Send, "c.cid.Recv", c.cid.Recv)
		}
		// No more passes: the reader leaves this connection alone from
		// here, so the teardown below can block for the application
		// without holding up the socket.
		c.finished = true
		c.mu.Unlock()
		// Always drain and terminate the read side here. The previous
		// `if !c.eof()` guard was inverted in effect: eof() is true by
		// definition once stateType is connClosed, so processReads was
		// never called and the end-of-stream marker was never delivered.
		// Readers reaching this path blocked in ReadToEOF forever.
		c.drainReadsForTeardown()
		c.processWrites(c.now())
		if c.state.RecvBuf != nil {
			c.state.RecvBuf.close()
		}
		// A final sample, so the last state of a connection is always
		// observed rather than lost to the throttle.
		c.sampleMetrics(c.now(), true)
		return c.state.Err, true
	}
	return nil, false
}

// takeIncoming takes the handoff the socket noted when it queued an inbound
// packet. Only streamIncoming is noted -- see handleIncomingBuf -- so only
// streamIncoming is taken. Every receive from streamEvents calls it: the
// non-blocking ones in takeQueued took the packet without it, and left a
// virtual clock waiting for ever.
func (c *connection) takeIncoming(event *streamEvent) {
	if c.barrier != nil && event != nil && event.Type == streamIncoming {
		c.barrier.TakeHandoff()
	}
}

func (c *connection) handleIncoming(event *streamEvent) {
	stream := c.stream
	if event.Type == streamIncoming {
		if c.logger.Enabled(baseContext, LevelTrace) {
			c.logger.Trace("incoming packet",
				"stream.streamEvents.len", len(stream.streamEvents),
				"src.peer", c.cid.Peer,
				"packet.type", event.Packet.Header.PacketType.String(),
				"packet.seqNum", event.Packet.Header.SeqNum,
				"packet.ackNum", event.Packet.Header.AckNum,
				"buf.len", len(event.Packet.Body))
		}
		now := c.now()
		c.lastActivity = now
		c.onPacket(event.Packet, now)
	} else if event.Type == streamShutdown {
		stream.shutdown.Store(true)
	} else if event.Type == streamICMP {
		c.onICMP(event.ICMP, c.now())
	} else if event.Type == streamCloseRead {
		c.onCloseRead()
	}
	// streamCloseWrite needs no handling beyond having woken the loop:
	// the flag it refers to is already set, and the pass this event ends
	// will see it and send the FIN.
}

func (c *connection) handleWrite(write *queuedWrite) {
	if c.logger.Enabled(baseContext, LevelTrace) {
		c.logger.Trace("get queued write from writes", "dst.peer", c.cid.Peer, "content", len(write.data))
	}
	c.lastActivity = c.now()
	c.onWrite(write)
}

func (c *connection) handleTimeout(timeoutPkt *packet) {
	// The wheel has already removed this one; keep the armed set in step.
	delete(c.armed, timeoutPkt.Header.SeqNum)
	c.logger.Debug("unack timeout",
		"seq", timeoutPkt.Header.SeqNum,
		"ack", timeoutPkt.Header.AckNum,
		"item.key", timeoutPkt.Header.SeqNum,
		"type", timeoutPkt.Header.PacketType.String())
	c.onTimeout(timeoutPkt, c.now())
}

// checkIdleTimeout closes the connection once MaxIdleTimeout has passed since
// lastActivity. Activity since the timer was set moves the deadline on: it
// arms for what is left of it. See lastActivity.
func (c *connection) checkIdleTimeout() {
	if left := c.config.MaxIdleTimeout - c.now().Sub(c.lastActivity); left > 0 {
		c.loopTimers.idle.arm(left)
		return
	}
	if c.state.stateType != connClosed {
		c.logger.Trace("idle timeout, closing...")
		c.state.stateType = connClosed
		c.state.Err = ErrTimedOut
	}
}

func (c *connection) handleCtxDone() {
	stream := c.stream
	if !stream.shutdown.Load() {
		c.logger.Trace("ctx done, uTP conn initiating shutdown...", "err", c.ctx.Err())
		stream.shutdown.Store(true)
	}
	// The socket was closed under a connection still running: that is
	// why it ended, and what its reader and writers are told. A stream
	// whose own context was cancelled -- an abandoned dial -- keeps
	// whatever it had.
	if c.state.Err == nil && c.state.stateType != connClosed &&
		stream.socketCtx != nil && stream.socketCtx.Err() != nil {
		c.state.Err = ErrSocketClosed
	}
}

// onKeepAliveTimer sends a keep-alive if the connection has been silent for
// the interval, and aims the timer at the moment the silence next will be.
func (c *connection) onKeepAliveTimer() {
	interval := c.keepAliveIntervalOrDefault()
	now := c.now()
	// libutp: `if (state >= CS_CONNECTED && !fin_sent)` and the
	// connection has been silent for the interval
	// (utp_internal.cpp:1271-1274).
	if c.state.stateType == connConnected &&
		(c.state.closing == nil || c.state.closing.LocalFin == nil) &&
		now.Sub(c.lastSentPacket) >= interval {
		c.emit(c.keepAlivePacket())
	}
	// Aim at the moment the silence will reach the interval. Anything
	// sent since the timer was armed has moved that later; nothing sent,
	// or nothing sendable in this state, means one interval from now.
	next := c.lastSentPacket.Add(interval).Sub(now)
	if next <= 0 {
		next = interval
	}
	c.loopTimers.keepAlive.Reset(next)
	// The connection has been silent for the interval: whatever the
	// receive buffer grew to while it was busy is held for nothing.
	if c.state.RecvBuf != nil {
		c.state.RecvBuf.releaseStorage()
	}
}

// afterPass is the work that ends every pass, whoever ran it: hand bytes up,
// send what the window allows, acknowledge, and see whether the application
// has asked to close. It reports whether there is still data the window would
// let out, which one pass stops short of sending.
//
// Called with mu held, by the event loop and by endBatch.
func (c *connection) afterPass() (moreToWrite bool) {
	stream := c.stream
	// CloseRead is applied from its flag, not its event, which is dropped
	// when the event queue is full. Taken from the event alone, a CloseRead
	// that lost its event left Read refusing data the connection went on
	// buffering, until the window closed on the peer. Release review of
	// 9c44c4f (R6).
	if !c.readShutdown && stream.readClosed.Load() {
		c.onCloseRead()
	}
	// Hand bytes up before acknowledging, so the acknowledgement carries
	// the window that results rather than the one from before the drain.
	//
	// This ordering is libutp's. It hands bytes to its embedder inside
	// utp_process_incoming and acknowledges afterwards, which is why
	// utp_read_drained finds nothing to report on an ordinary packet. A
	// drain on a later pass would look like the window growing, and report
	// it with a second acknowledgement. ackPending is a bool and flushAck
	// runs once per pass, so the drain and the data that prompted it share
	// one acknowledgement.
	//
	// What the drain will take is counted first (drainable) and advertised
	// as if already taken (drainCredit, recvWindow); the drain itself runs
	// once the acknowledgement has gone. The packets are the same either
	// way. What moves is the work -- copying the bytes out, allocating what
	// the reader is handed, and waking the reader -- off the path from
	// datagram to acknowledgement. History: HISTORY.md, "afterPass: drain
	// before acknowledging".
	c.drainCredit = c.drainable()
	// One reading of the clock for the whole pass, as libutp reads
	// ctx->current_ms once per call into it.
	now := c.now()
	// Send before acknowledging, as libutp does: an acknowledgement that
	// opened the window is followed by the data it lets out within
	// utp_process_incoming, and the deferred acknowledgement after it is
	// dropped if a data packet already carried it (send_data, :768).
	for i := 0; c.wantWrite && i < maxWritesPerPass; i++ {
		c.wantWrite = false
		c.processWrites(now)
	}
	c.flushAck()
	c.drainCredit = 0
	c.processReads()
	c.scheduleIdleRto(now)
	c.sampleMetrics(now, false)
	// shutdown() sends the local FIN once everything queued has drained.
	// Both flags reach it: Close means finished entirely, CloseWrite means
	// finished sending. The difference is not here -- it is that a
	// CloseWrite connection is not torn down when the peer's FIN arrives,
	// because its reader is still there.
	if stream.shutdown.Load() {
		c.closeRequested = true
	}
	if (stream.shutdown.Load() || stream.writeClosed.Load()) &&
		c.state.stateType != connClosed {
		if !c.writeShut {
			c.writeShut = true
			c.processWrites(now)
		}
		c.shutdown()
		// And re-check: shutdown may have just sent the FIN that finishes
		// this connection, and no packet need ever arrive to notice.
		c.updateClosingState()
	}
	return c.wantWrite
}

// maxWritesPerPass bounds how many times one pass goes back to processWrites
// for data it moved into the send buffer. Each time either sends or finds the
// window full, so this is reached only by an application writing faster than
// the loop can turn round, and then the next pass carries on.
const maxWritesPerPass = 64

// receiveInline handles a packet for this connection on the caller's
// goroutine -- the socket's reader -- and reports whether it did. It does not
// acknowledge: that waits for endBatch, at the end of the read the packet came
// in. A connection still being set up declines, and the packet goes through
// streamEvents; one that has closed takes the packet and drops it, as its
// channel did once the loop had stopped reading it. Closed but not yet
// finished is the moment between the pass that closed it and the event loop
// waking to tear it down.
func (c *connection) receiveInline(pkt *packet) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		// Except a FIN, which is acknowledged: the peer closing second is
		// waiting for it, and dropped here, between the teardown and the
		// socket forgetting the connection, it was answered only by its
		// retransmission, a timeout on. Sent without touching the
		// connection, which the teardown reads unlocked.
		if pkt.Header.PacketType == st_fin {
			if ack := c.closedFinAck(pkt.Header.SeqNum); ack != nil {
				ev := newOutgoingSocketEvent(ack, c.cid)
				if c.out != nil {
					c.out(ev)
				} else {
					b, _ := c.timeSource().(idleBarrier)
					if b != nil {
						b.NoteHandoff()
					}
					select {
					case c.socketEvents <- ev:
					default:
						if b != nil {
							b.TakeHandoff()
						}
					}
				}
			}
		}
		return true
	}
	if c.ready && c.state.stateType == connClosed {
		// Except a FIN, which is acknowledged, as above. Kept as the FIN's
		// acknowledgement, so the socket's linger state covers it too.
		if pkt.Header.PacketType == st_fin {
			if ack := c.closedFinAck(pkt.Header.SeqNum); ack != nil {
				c.finAck = ack
				c.emit(ack)
			}
		}
		return true
	}
	if !c.ready {
		return false
	}
	c.inBatch = true
	now := c.now()
	c.lastActivity = now
	c.onPacket(pkt, now)
	return true
}

// endBatch ends a read batch that delivered packets to this connection: the
// pass that receiveInline left open runs now, acknowledgement included.
// libutp's utp_issue_deferred_acks, which its embedder calls once the socket
// would block (utp.h:512-517).
//
// Except when the batch has left data to send. Then the whole pass is the
// event loop's: one goroutine per connection does the sending, and the reader,
// which serves every connection on the socket, does not. libutp sends from
// inside utp_process_incoming, on its one thread; done here, the reader's
// write system calls for every sender on the socket would leave it no time
// to read. The acknowledgement this pass owes goes out with the data, as it
// would have here.
//
// Otherwise the event loop is woken only for what it alone can do: tear the
// connection down, or go on sending past one pass's worth. History:
// HISTORY.md, "endBatch".
func (c *connection) endBatch() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished || !c.ready || !c.inBatch {
		return
	}
	c.inBatch = false
	if c.wantWrite && c.hasSendWork() {
		c.kickLoop()
		return
	}
	more := c.afterPass()
	if more || c.state.stateType == connClosed {
		c.kickLoop()
	} else if !c.asleep && c.mayRest() {
		// Resting is the loop's to do, and a loop parked behind this pass
		// would otherwise wait for its next timer to find it has nothing
		// left: with the loss probe stopped once nothing is outstanding, a
		// sender whose last acknowledgement the reader handled would never
		// rest.
		c.kickLoop()
	}
}

// hasSendWork reports whether processWrites has anything it might send: bytes
// buffered or queued by the application, or packets a timeout marked for
// resending.
func (c *connection) hasSendWork() bool {
	if c.state.stateType != connConnected {
		return false
	}
	if len(c.pendingWrites) > 0 ||
		(c.state.SendBuf != nil && c.state.SendBuf.Pending() > 0) {
		return true
	}
	if c.state.SentPackets == nil {
		return false
	}
	_, resend := c.state.SentPackets.NextNeedingResend()
	return resend
}

// kickLoop wakes the event loop from another goroutine, accounting for the
// wake with a virtual clock. See idleBarrier.NoteHandoff.
func (c *connection) kickLoop() {
	barrier := c.barrier
	if barrier != nil {
		barrier.NoteHandoff()
	}
	select {
	case c.kick <- struct{}{}:
	default:
		// A wake is already pending, and it is enough.
		if barrier != nil {
			barrier.TakeHandoff()
		}
	}
	c.wake()
}

// wakeKind names which case of the event loop's blocking select fired.
//
// It exists so the select can receive without acting: the bodies run from a
// switch afterwards, which gives one place to tell a virtual clock that the
// loop is no longer parked.
type wakeKind int

const (
	wakeStreamEvent wakeKind = iota
	wakeWrite
	wakeReadable
	wakeWritable
	wakeRetransmit
	wakeKeepAlive
	wakeProbe
	wakeIdleRto
	wakeLossProbe
	wakeRack
	wakeAckHold
	wakeKick
	wakeIdleTimeout
	wakeCtxDone
	// wakeQueued is a pass for events takeQueued has already handled, and
	// wakeResumed one for a connection coming back from rest (dormant.go).
	// Neither came from the select.
	wakeQueued
	wakeResumed
)
