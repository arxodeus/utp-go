package utp

import (
	"time"
)

// lazyTimer is a Timer created the first time it is armed. Until then its
// channel is nil, which a select never chooses, so a connection holds only
// the timers it has used.
//
// A timer created while the event loop is parked is one its select is not
// watching, since the reader arms timers in passes it runs inline. Creating
// one therefore wakes the loop (kickLoop), which then selects on it.
type lazyTimer struct {
	c *connection
	t Timer
	// pending is set while the timer is armed and its expiry not yet taken.
	// A resting connection (dormant.go) has none pending.
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

// arm sets the timer to fire after d, discarding an expiry not yet
// received. A timer armed while the connection rests wakes it (wake).
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

// stop stops the timer, if it has been created.
func (l *lazyTimer) stop() {
	l.pending = false
	if l.t != nil {
		l.t.Stop()
	}
}

// loopTimers are the timers the event loop waits on. They belong to the
// connection rather than the loop so the loop can stop and restart (see
// dormant.go), and so the frame it waits in stays small (see wait).
type loopTimers struct {
	// keepAlive is created with the loop and always armed while it runs.
	keepAlive Timer
	// The rest are created when first armed. See lazyTimer.
	idle, probe, ackHold, idleRto, lossProbe, rack *lazyTimer
}

// wakeResult is what the event loop's blocking select received: which case
// fired and what it carried.
type wakeResult struct {
	kind  wakeKind
	event *streamEvent
	write *queuedWrite
	ok    bool
	timer *packet
}

// eventLoop sets the connection up and runs it until it ends or rests. It
// returns the error the connection ended with and whether it ended. A
// connection that rests (dormant.go) returns with final unset, and its loop
// is restarted by whatever next needs it.
func (c *connection) eventLoop(stream *UtpStream) (error, bool) {
	c.setupLoop(stream)
	return c.runLoop()
}

// setupLoop does the event loop's one-time setup before its first pass.
// Called with mu held, from newUtpStream; the loop releases mu the first
// time it waits.
func (c *connection) setupLoop(stream *UtpStream) {
	if c.logger.Enabled(baseContext, LevelTrace) {
		c.logger.Trace("uTP conn starting", "dst.peer", c.cid.Peer, "cid.Send", c.cid.Send, "cid.Recv", c.cid.Recv)
	}
	// mu is held for setup and for every pass, and released only while the
	// loop waits. See connection.mu. Initialize by endpoint type.
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
		// Acceptor.
		syn := c.endpoint.SynNum
		synAck := c.endpoint.SynAck

		c.synState = c.statePacket()

		c.logger.Debug("a initial state packet", "peer", c.cid.Peer, "cid.Send", c.cid.Send, "cid.Recv", c.cid.Recv)
		c.emit(c.synState)

		recvBuf := newReceiveBufferWithLogger(c.config.BufferSize, syn, c.logger)
		sendBuf := newSendBuffer(c.config.BufferSize)
		congestionCtrl := newDefaultController(c.controllerConfig())
		sentPacketsHolder := newSentPackets(synAck-1, congestionCtrl, c.logger) // wrapping sub
		sentPacketsHolder.rack.active = c.config.RACK

		// Every acceptor is created with a channel to report on. Nothing waits
		// on one created without, so that is not an error.
		if c.state.connectedCh != nil {
			c.state.connectedCh <- nil
		}

		c.state.stateType = connConnected
		c.state.RecvBuf = recvBuf
		c.state.SendBuf = sendBuf
		c.state.SentPackets = sentPacketsHolder
	}

	t := &c.loopTimers
	// No idle timeout unless configured, and no timer for it: this and the
	// other rarely used timers below are created when first armed
	// (lazyTimer).
	t.idle = c.newLazyTimer()
	if c.config.MaxIdleTimeout > 0 {
		t.idle.arm(c.config.MaxIdleTimeout)
	}
	c.lastActivity = c.now()

	// The zero-window probe needs its own wake-up: with the peer's window
	// closed, nothing is outstanding, so no retransmission timer exists and
	// nothing arrives until the peer speaks.
	//
	// A quiet established connection must still send occasionally, or a
	// NAT drops its mapping. The keep-alive timer is aimed one interval
	// after the last packet sent, and re-aimed from lastSentPacket each time
	// it fires.
	t.keepAlive = c.timeSource().NewTimer(c.keepAliveIntervalOrDefault())

	t.probe = c.newLazyTimer()
	t.ackHold = c.newLazyTimer()
	c.armAckHold = t.ackHold.arm
	c.armProbeTimer = t.probe.arm
	// An idle connection's window must decay, and nothing else would wake
	// the loop to do it: with nothing outstanding there are no
	// retransmission timers. When the retransmission deadline passes with
	// nothing in flight, the window decays by a third; this repeats without
	// ever counting as a timeout.
	//
	// A resting connection has no loop to wake: its rest timer is already
	// set for this deadline (armDormant), and is moved only if a pass the
	// reader ran has brought the deadline forward.
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

	// A virtual clock needs to know when this loop has finished reacting,
	// so it can advance time without racing it. Resolved once; nil for the
	// real clock. See idleBarrier.
	if c.barrier != nil {
		c.barrier.Register()
	}
	c.setupDormancy()

	c.stream = stream
	c.ready = true
}

// finishLoop does the event loop's teardown once the connection has ended,
// on whichever goroutine ran its last pass. Called without mu.
func (c *connection) finishLoop() {
	// Stop timers first, while a virtual clock still counts this loop as
	// busy, so nothing fires into a loop that has unregistered.
	t := &c.loopTimers
	t.rack.stop()
	t.lossProbe.stop()
	t.idleRto.stop()
	t.ackHold.stop()
	t.probe.stop()
	t.keepAlive.Stop()
	t.idle.stop()
	c.endDormancy()
	if c.barrier != nil {
		// A connection that has ended can never park again, so a virtual
		// clock waiting for it would stall. Wakes queued for this loop that
		// it will never take were each noted as handoffs; settle them, or
		// the clock would count them in flight forever.
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
	// Tell the socket to drop this connection on every exit, not only when
	// the state machine reached connClosed: a connection ended by a
	// canceled context, such as an abandoned dial, must leave the table too.
	c.notifySocketShutdown()
	// Report the end of the stream to the reader on every exit.
	//
	// Nothing else sends on c.reads by now (the reader's inline passes stop
	// once finished is set), so closing it here is safe. The end-of-stream
	// marker is best-effort; a reader that missed it because its queue was
	// full learns from the close instead, with the reason in terminalErr.
	if c.terminalErr.Load() == nil {
		c.terminalErr.Store(&terminalError{err: c.readEndErr()})
	}
	c.endErr.Store(&terminalError{err: c.state.Err})
	close(c.reads)
	// The shared wheel outlives the connection, so anything left armed
	// would expire into a dead loop.
	c.disarmAll()
}

// runLoop runs passes until the connection ends or rests. Called with mu
// held; returns without it.
//
// runLoop and wait are kept small because they are the frames a parked loop
// leaves on its goroutine's stack, and the garbage collector shrinks a stack
// only while less than a quarter of it is in use. The handling lives in
// react, which has returned by the time the loop parks.
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
		// Take whatever else has already arrived before acknowledging, so one
		// acknowledgement covers it. flushAck also waits for the rest of
		// the socket read the packets arrived in. Bounded, so a peer sending
		// continuously cannot starve writes and timers.
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

// wait blocks with mu released until the loop has something to do, and
// reports what woke it.
//
// The select only receives; every case body runs afterwards, in react. That
// gives one place to tell a virtual clock the loop is running again before
// any body can send a packet. See idleBarrier.
func (c *connection) wait() (w wakeResult) {
	stream := c.stream
	t := &c.loopTimers
	// Read the channels while mu is still held: the reader creates lazy
	// timers under it.
	keepAlive, probe, ackHold := t.keepAlive.C(), t.probe.C(), t.ackHold.C()
	idleRto, lossProbe, rack, idle := t.idleRto.C(), t.lossProbe.C(), t.rack.C(), t.idle.C()
	// Tell a virtual clock this loop is about to block, so the reaction to
	// the last instant is complete and time may advance. The real clock has
	// no barrier.
	//
	// Not while a wake this loop queued for itself is still pending:
	// otherwise the clock could see every participant parked with work
	// still queued. Only this goroutine queues such wakes before reaching
	// here, so their count is exact.
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
		// Already handled by takeQueued or onResume.
	case wakeStreamEvent:
		c.takeIncoming(w.event)
		c.handleIncoming(w.event)
	case wakeWrite:
		c.handleWrite(w.write)
	case wakeReadable:
		// Nothing to do but wake: the drain happens in afterPass, before the
		// acknowledgement. UtpStream.notifyRead signals this when the
		// application takes a chunk, which is the only thing that frees
		// room once the read queue has filled.
	case wakeWritable:
		c.processWrites(c.now())
	case wakeRetransmit:
		// The handoff the wheel noted when it queued the timeout.
		if c.barrier != nil {
			c.barrier.TakeHandoff()
		}
		c.handleTimeout(w.timer)
	case wakeKeepAlive:
		c.onKeepAliveTimer()
	case wakeAckHold:
		t.ackHold.fired()
		// Nothing more to do but wake: a held acknowledgement is sent by
		// flushAck in afterPass.
	case wakeKick:
		// The handoff endBatch noted when it kicked. The work happens in
		// afterPass.
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
		// More to send than one pass sends: go round again.
		select {
		case c.writable <- struct{}{}:
		default:
		}
	}

	if c.state.stateType == connClosed {
		if c.logger.Enabled(baseContext, LevelTrace) {
			c.logger.Trace("uTP conn closing...", "err", c.state.Err, "c.cid.Send", c.cid.Send, "c.cid.Recv", c.cid.Recv)
		}
		// No more passes: the reader leaves this connection alone from here,
		// so the teardown below can block on the application without
		// holding up the socket.
		c.finished = true
		c.mu.Unlock()
		// Always drain and terminate the read side here.
		c.drainReadsForTeardown()
		c.processWrites(c.now())
		if c.state.RecvBuf != nil {
			c.state.RecvBuf.close()
		}
		// A final sample, so a connection's last state is always observed.
		c.sampleMetrics(c.now(), true)
		return c.state.Err, true
	}
	return nil, false
}

// takeIncoming takes the handoff the socket noted when it queued an inbound
// packet. Only streamIncoming is noted (see handleIncomingBuf), so only
// streamIncoming is taken. Every receive from streamEvents calls it.
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
	// streamCloseWrite needs no handling beyond waking the loop: its flag is
	// already set, and this pass sends the FIN.
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
// lastActivity. If there has been activity since the timer was set, it
// re-arms for the remainder. See lastActivity.
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
	// The socket was closed under a running connection; that is why it
	// ended, and what its reader and writers are told. A stream whose own
	// context was canceled, such as an abandoned dial, keeps its own error.
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
	// Only an established connection that has not sent its FIN.
	if c.state.stateType == connConnected &&
		(c.state.closing == nil || c.state.closing.LocalFin == nil) &&
		now.Sub(c.lastSentPacket) >= interval {
		c.emit(c.keepAlivePacket())
	}
	// Aim at the moment the silence will reach the interval. Anything sent
	// since the timer was armed moves that later.
	next := c.lastSentPacket.Add(interval).Sub(now)
	if next <= 0 {
		next = interval
	}
	c.loopTimers.keepAlive.Reset(next)
	// The connection has been silent for the interval: release whatever
	// the receive buffer grew to while busy.
	if c.state.RecvBuf != nil {
		c.state.RecvBuf.releaseStorage()
	}
}

// afterPass does the work that ends every pass, whoever ran it: deliver
// bytes, send what the window allows, acknowledge, and check whether the
// application has asked to close. It reports whether there is still data the
// window would let out, which one pass stops short of sending.
//
// Called with mu held, by the event loop and by endBatch.
func (c *connection) afterPass() (moreToWrite bool) {
	stream := c.stream
	// CloseRead is applied from its flag rather than its event, which is
	// dropped when the event queue is full.
	if !c.readShutdown && stream.readClosed.Load() {
		c.onCloseRead()
	}
	// Account for delivery before acknowledging, so the acknowledgement
	// carries the resulting window rather than the one before. ackPending is
	// a flag and flushAck runs once per pass, so the delivery and the data
	// that prompted it share one acknowledgement.
	//
	// What the drain will take is counted first (drainable) and advertised
	// as if already taken (drainCredit, recvWindow); the drain itself runs
	// after the acknowledgement is sent, which keeps copying and waking the
	// reader off the path from datagram to acknowledgement.
	c.drainCredit = c.drainable()
	// Read the clock once for the whole pass.
	now := c.now()
	// Send before acknowledging: the data an acknowledgement let out
	// carries the acknowledgement, and the deferred one is then dropped.
	for i := 0; c.wantWrite && i < maxWritesPerPass; i++ {
		c.wantWrite = false
		c.processWrites(now)
	}
	c.flushAck()
	c.drainCredit = 0
	c.processReads()
	c.scheduleIdleRto(now)
	c.sampleMetrics(now, false)
	// shutdown sends the local FIN once everything queued has drained. Both
	// flags reach it: Close means finished entirely, CloseWrite means
	// finished sending. The difference is that a CloseWrite connection is
	// not torn down when the peer's FIN arrives, since its reader remains.
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
		// Re-check: shutdown may have just sent the FIN that finishes this
		// connection, and no packet need arrive to notice.
		c.updateClosingState()
	}
	return c.wantWrite
}

// maxWritesPerPass bounds how many times one pass returns to processWrites
// for data it moved into the send buffer. Each time either sends or finds
// the window full, so the bound is reached only by an application writing
// faster than the loop turns round, and the next pass continues.
const maxWritesPerPass = 64

// receiveInline handles a packet for this connection on the caller's
// goroutine, the socket's reader, and reports whether it did. It does not
// acknowledge; that waits for endBatch at the end of the read. A connection
// still being set up declines, and the packet goes through streamEvents. One
// that has closed takes the packet and drops it.
func (c *connection) receiveInline(pkt *packet) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		// Except a FIN, which is acknowledged: the peer closing second is
		// waiting for it. Sent without touching the connection, which the
		// teardown reads unlocked.
		if pkt.Header.PacketType == st_fin {
			if ack := c.closedFinAck(pkt); ack != nil {
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
		// Except a FIN, which is acknowledged, as above, and kept as the FIN's
		// acknowledgement so the socket's linger state covers it.
		if pkt.Header.PacketType == st_fin {
			if ack := c.closedFinAck(pkt); ack != nil {
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
// pass that receiveInline left open runs now, including the acknowledgement.
//
// If the batch left data to send, the whole pass is left to the event loop
// instead, so the reader, which serves every connection on the socket, is
// not spent on sending. The acknowledgement goes out with the data.
//
// Otherwise the event loop is woken only for what it alone can do: tear the
// connection down, or keep sending past one pass's worth.
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
		// Resting is the loop's job, and a loop parked behind this pass would
		// otherwise wait for its next timer to discover it has nothing left
		// to do.
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
		// A wake is already pending, which suffices.
		if barrier != nil {
			barrier.TakeHandoff()
		}
	}
	c.wake()
}

// wakeKind names which case of the event loop's blocking select fired, so
// the select can receive without acting.
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
