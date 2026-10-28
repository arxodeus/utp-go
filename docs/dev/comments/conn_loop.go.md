# conn_loop.go

Every comment in `conn_loop.go` at e56c9aa, verbatim.

## Line 7 — type lazyTimer

```
// lazyTimer is a Timer made the first time it is armed. Until then its
// channel is nil, which a select never takes, so a connection holds only
// the timers it has used.
//
// A timer made while the event loop is parked is one its select is not
// watching: the loop chose its channels before the timer existed, and the
// socket's reader arms timers in passes it runs inline. So making one wakes
// the loop (kickLoop), which goes round and selects on it. History:
// HISTORY.md, "lazyTimer".
```

## Line 19 — type lazyTimer

```
// pending is set while the timer is armed and its fire has not been
	// taken: a resting connection (dormant.go) must have none of these.
```

## Line 28 — func (*lazyTimer).C

```
// C is the timer's channel, or nil before it is first armed.
```

## Line 36 — func (*lazyTimer).arm

```
// arm sets the timer to fire after d, dropping a fire not yet received.
//
// A timer armed while the connection rests has no loop to fire into, so
// arming one wakes it (wake). A connection that is not resting does nothing
// for that but a load.
```

## Line 58 — func (*lazyTimer).fired

```
// fired records that the event loop has taken the timer's fire.
```

## Line 61 — func (*lazyTimer).stop

```
// stop stops the timer, if it has been made.
```

## Line 69 — type loopTimers

```
// loopTimers are the timers the event loop waits on. They are the
// connection's rather than the loop's locals so that the loop can stop and
// start again (see dormant.go), and so that the frame it waits in stays small
// (see wait).
```

## Line 74 — type loopTimers

```
// keepAlive is made with the loop and always armed while it runs.
```

## Line 76 — type loopTimers

```
// The rest are made the first time they are armed. See lazyTimer.
```

## Line 80 — type wakeResult

```
// wakeResult is what the event loop's blocking select received: which case
// fired, and what it carried.
```

## Line 90 — func (*connection).eventLoop

```
// eventLoop sets the connection up and runs it until it ends or rests. It
// reports the error the connection ended with, and whether it ended: a
// connection that rests (dormant.go) returns with final unset, and its loop
// is started again by whatever next needs it.
```

## Line 99 — func (*connection).setupLoop

```
// setupLoop is everything the event loop does once, before its first pass.
// Called with mu held, from newUtpStream; the loop releases it the first time
// it waits.
```

## Line 106 — func (*connection).setupLoop

```
// mu is held for setup, from newUtpStream, and for every pass; it is
	// released only while the loop waits. See connection.mu.
	// Initialize connection based on endpoint type
```

## Line 122 — func (*connection).setupLoop

```
// Acceptor connection
```

## Line 134 — func (*connection).setupLoop

```
// wrapping subtraction
```

## Line 137 — func (*connection).setupLoop

```
// Every acceptor is made with a channel to report on (newUtpStream's
		// callers in the socket), and nothing waits on one made without.
		// This panicked when there was none, which said nothing worth losing
		// the process for.
```

## Line 152 — func (*connection).setupLoop

```
// No idle timeout unless one is configured, and no timer for it either:
	// this and the other timers below that most connections never use are
	// made when first armed (lazyTimer).
```

## Line 161 — func (*connection).setupLoop

```
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
```

## Line 186 — func (*connection).setupLoop

```
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
```

## Line 233 — func (*connection).setupLoop

```
// A virtual clock needs to know when this loop has finished reacting, so
	// that it can move time without racing the reaction. Resolved once here
	// rather than per pass; nil for the real clock. See idleBarrier.
```

## Line 245 — func (*connection).finishLoop

```
// finishLoop is everything the event loop does once it has ended, on
// whichever goroutine ran its last pass. Called without mu.
```

## Line 248 — func (*connection).finishLoop

```
// Timers first, while a virtual clock still counts this loop as busy: it
	// moves time only once every participant is parked, so nothing fires
	// until they are stopped. Stopped after the loop unregistered, a timer
	// could fire in between into a loop no longer there -- the rest timer's
	// wake does nothing to a connection that is not resting -- and the clock
	// waited for a reaction for ever: TestDeadConnection*, under load.
```

## Line 264 — func (*connection).finishLoop

```
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
```

## Line 288 — func (*connection).finishLoop

```
// Tell the socket to drop this connection on *every* exit, not only the
	// one where the state machine reached connClosed: a connection torn down
	// by a cancelled context -- which is what an abandoned or failed
	// connection attempt is -- must leave the socket's table too. History:
	// HISTORY.md, "finishLoop: reporting to the socket".
```

## Line 294 — func (*connection).finishLoop

```
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
```

## Line 310 — func (*connection).finishLoop

```
// The shared wheel outlives the connection, so anything left armed
	// would linger until it expired and then be delivered to a dead loop.
```

## Line 315 — func (*connection).runLoop

```
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
```

## Line 345 — func (*connection).takeQueued

```
// takeQueued handles whatever the socket has already queued for this
// connection, without blocking, and reports whether there was any.
```

## Line 359 — func (*connection).takeQueued

```
// Take whatever else has already arrived before answering, so one
		// acknowledgement covers it rather than one per packet. flushAck
		// also waits for the rest of the socket read the packets came in
		// (see there).
		//
		// Bounded so that a peer sending continuously cannot keep this
		// inner loop fed and starve writes and timers; the outer loop
		// returns here immediately anyway if more are waiting.
```

## Line 382 — func (*connection).wait

```
// wait blocks until the loop has something to do, with mu released, and
// reports what woke it.
//
// The select below only *receives*; every case body runs after it, in react.
// That shape is what lets a virtual clock be told the loop is running again
// before any of those bodies can emit a packet -- one MarkBusy, on the one
// path out, rather than one per case where missing a single one would put
// the nondeterminism straight back. See idleBarrier.
```

## Line 393 — func (*connection).wait

```
// The channels are read while mu is still held: the socket's reader makes
	// lazy timers under it.
```

## Line 397 — func (*connection).wait

```
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
```

## Line 416 — func (*connection).wait

```
// The socket's reader may handle a packet for this connection while
	// the loop waits. See receiveInline.
```

## Line 456 — func (*connection).react

```
// react handles one wake and ends the pass. It reports whether the
// connection has ended, and with what; when it has, mu has been released.
```

## Line 462 — func (*connection).react

```
// Already handled, by takeQueued and by onResume.
```

## Line 469 — func (*connection).react

```
// Nothing to do but wake: the drain happens in afterPass, for
		// every pass, so that it precedes the acknowledgement.
		//
		// The wake itself is the point. UtpStream.notifyRead signals this
		// when the application takes a chunk, which is the only thing
		// that frees room once the read queue has filled -- without it
		// processReads gives up and waits for a packet that a blocked
		// peer will not send.
```

## Line 480 — func (*connection).react

```
// This is the handoff the wheel noted when it queued the timeout.
```

## Line 489 — func (*connection).react

```
// Nothing more to do but wake: a held acknowledgement goes out from
		// flushAck in afterPass.
```

## Line 492 — func (*connection).react

```
// The handoff endBatch noted when it kicked. What it wants done
		// happens in afterPass.
```

## Line 499 — func (*connection).react

```
// The peer's window has been closed for a whole interval. Let one
		// packet through, so its acknowledgement carries a fresh window.
```

## Line 524 — func (*connection).react

```
// More to send than one pass sends: come round again.
```

## Line 535 — func (*connection).react

```
// No more passes: the reader leaves this connection alone from
		// here, so the teardown below can block for the application
		// without holding up the socket.
```

## Line 540 — func (*connection).react

```
// Always drain and terminate the read side here. The previous
		// `if !c.eof()` guard was inverted in effect: eof() is true by
		// definition once stateType is connClosed, so processReads was
		// never called and the end-of-stream marker was never delivered.
		// Readers reaching this path blocked in ReadToEOF forever.
```

## Line 550 — func (*connection).react

```
// A final sample, so the last state of a connection is always
		// observed rather than lost to the throttle.
```

## Line 558 — func (*connection).takeIncoming

```
// takeIncoming takes the handoff the socket noted when it queued an inbound
// packet. Only streamIncoming is noted -- see handleIncomingBuf -- so only
// streamIncoming is taken. Every receive from streamEvents calls it: the
// non-blocking ones in takeQueued took the packet without it, and left a
// virtual clock waiting for ever.
```

## Line 591 — func (*connection).handleIncoming

```
// streamCloseWrite needs no handling beyond having woken the loop:
	// the flag it refers to is already set, and the pass this event ends
	// will see it and send the FIN.
```

## Line 605 — func (*connection).handleTimeout

```
// The wheel has already removed this one; keep the armed set in step.
```

## Line 615 — func (*connection).checkIdleTimeout

```
// checkIdleTimeout closes the connection once MaxIdleTimeout has passed since
// lastActivity. Activity since the timer was set moves the deadline on: it
// arms for what is left of it. See lastActivity.
```

## Line 636 — func (*connection).handleCtxDone

```
// The socket was closed under a connection still running: that is
	// why it ended, and what its reader and writers are told. A stream
	// whose own context was cancelled -- an abandoned dial -- keeps
	// whatever it had.
```

## Line 646 — func (*connection).onKeepAliveTimer

```
// onKeepAliveTimer sends a keep-alive if the connection has been silent for
// the interval, and aims the timer at the moment the silence next will be.
```

## Line 651 — func (*connection).onKeepAliveTimer

```
// libutp: `if (state >= CS_CONNECTED && !fin_sent)` and the
	// connection has been silent for the interval
	// (utp_internal.cpp:1271-1274).
```

## Line 659 — func (*connection).onKeepAliveTimer

```
// Aim at the moment the silence will reach the interval. Anything
	// sent since the timer was armed has moved that later; nothing sent,
	// or nothing sendable in this state, means one interval from now.
```

## Line 667 — func (*connection).onKeepAliveTimer

```
// The connection has been silent for the interval: whatever the
	// receive buffer grew to while it was busy is held for nothing.
```

## Line 674 — func (*connection).afterPass

```
// afterPass is the work that ends every pass, whoever ran it: hand bytes up,
// send what the window allows, acknowledge, and see whether the application
// has asked to close. It reports whether there is still data the window would
// let out, which one pass stops short of sending.
//
// Called with mu held, by the event loop and by endBatch.
```

## Line 682 — func (*connection).afterPass

```
// CloseRead is applied from its flag, not its event, which is dropped
	// when the event queue is full. Taken from the event alone, a CloseRead
	// that lost its event left Read refusing data the connection went on
	// buffering, until the window closed on the peer. Release review of
	// 9c44c4f (R6).
```

## Line 690 — func (*connection).afterPass

```
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
```

## Line 709 — func (*connection).afterPass

```
// One reading of the clock for the whole pass, as libutp reads
	// ctx->current_ms once per call into it.
```

## Line 712 — func (*connection).afterPass

```
// Send before acknowledging, as libutp does: an acknowledgement that
	// opened the window is followed by the data it lets out within
	// utp_process_incoming, and the deferred acknowledgement after it is
	// dropped if a data packet already carried it (send_data, :768).
```

## Line 725 — func (*connection).afterPass

```
// shutdown() sends the local FIN once everything queued has drained.
	// Both flags reach it: Close means finished entirely, CloseWrite means
	// finished sending. The difference is not here -- it is that a
	// CloseWrite connection is not torn down when the peer's FIN arrives,
	// because its reader is still there.
```

## Line 740 — func (*connection).afterPass

```
// And re-check: shutdown may have just sent the FIN that finishes
		// this connection, and no packet need ever arrive to notice.
```

## Line 747 — const maxWritesPerPass

```
// maxWritesPerPass bounds how many times one pass goes back to processWrites
// for data it moved into the send buffer. Each time either sends or finds the
// window full, so this is reached only by an application writing faster than
// the loop can turn round, and then the next pass carries on.
```

## Line 753 — func (*connection).receiveInline

```
// receiveInline handles a packet for this connection on the caller's
// goroutine -- the socket's reader -- and reports whether it did. It does not
// acknowledge: that waits for endBatch, at the end of the read the packet came
// in. A connection still being set up declines, and the packet goes through
// streamEvents; one that has closed takes the packet and drops it, as its
// channel did once the loop had stopped reading it. Closed but not yet
// finished is the moment between the pass that closed it and the event loop
// waking to tear it down.
```

## Line 765 — func (*connection).receiveInline

```
// Except a FIN, which is acknowledged: the peer closing second is
		// waiting for it, and dropped here, between the teardown and the
		// socket forgetting the connection, it was answered only by its
		// retransmission, a timeout on. Sent without touching the
		// connection, which the teardown reads unlocked.
```

## Line 793 — func (*connection).receiveInline

```
// Except a FIN, which is acknowledged, as above. Kept as the FIN's
		// acknowledgement, so the socket's linger state covers it too.
```

## Line 813 — func (*connection).endBatch

```
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
```

## Line 844 — func (*connection).endBatch

```
// Resting is the loop's to do, and a loop parked behind this pass
		// would otherwise wait for its next timer to find it has nothing
		// left: with the loss probe stopped once nothing is outstanding, a
		// sender whose last acknowledgement the reader handled would never
		// rest.
```

## Line 853 — func (*connection).hasSendWork

```
// hasSendWork reports whether processWrites has anything it might send: bytes
// buffered or queued by the application, or packets a timeout marked for
// resending.
```

## Line 871 — func (*connection).kickLoop

```
// kickLoop wakes the event loop from another goroutine, accounting for the
// wake with a virtual clock. See idleBarrier.NoteHandoff.
```

## Line 881 — func (*connection).kickLoop

```
// A wake is already pending, and it is enough.
```

## Line 889 — type wakeKind

```
// wakeKind names which case of the event loop's blocking select fired.
//
// It exists so the select can receive without acting: the bodies run from a
// switch afterwards, which gives one place to tell a virtual clock that the
// loop is no longer parked.
```

## Line 911 — const wakeStreamEvent, wakeWrite, wakeReadable, wakeWritable, wakeRetransmit,...

```
// wakeQueued is a pass for events takeQueued has already handled, and
	// wakeResumed one for a connection coming back from rest (dormant.go).
	// Neither came from the select.
```
