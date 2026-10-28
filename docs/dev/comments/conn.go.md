# conn.go

Every comment in `conn.go` at e56c9aa, verbatim.

## Line 26 — var ErrReset, ErrTimedOut, ErrConnRefused

```
// ErrConnRefused is libutp's UTP_ECONNREFUSED: an ICMP error arrived for
	// a connection that had only sent its SYN, so nothing is listening.
	//
	//	const int err = (conn->state == CS_SYN_SENT) ? UTP_ECONNREFUSED
	//	                                             : UTP_ECONNRESET;
	//	                                       (utp_internal.cpp:3122)
	//
	// The other half of that expression is ErrReset, which is also what a
	// received ST_RESET produces -- libutp reports both as UTP_ECONNRESET,
	// so they are deliberately not distinguished here either.
```

## Line 78 — type connection

```
// windowCeiling, when set, replaces config.WindowSize for this
	// connection: UtpStream.SetWindowSize, libutp's per-socket
	// utp_setsockopt(UTP_SNDBUF). config may be shared with other
	// connections, so it is not written. Guarded by mu.
```

## Line 87 — type connection

```
// timers is the socket-wide retransmission wheel; timerScope namespaces
	// this connection's keys within it.
```

## Line 91 — type connection

```
// armed tracks which sequence numbers this connection currently has
	// scheduled. It exists so acking a range can disarm only this
	// connection's timers instead of scanning a wheel shared by every
	// connection on the socket. Guarded by mu.
```

## Line 97 — type connection

```
// undoSeq and undoStamp are the packet whose loss made the controller's
	// last cut and the wire timestamp of its first resend, once undoResent
	// is set. See checkSpuriousLoss.
```

## Line 103 — type connection

```
// payloadScratch is processWrites' list of payloads composed in a pass,
	// kept between passes: growing a new one cost an allocation or two on
	// every pass that sent anything.
```

## Line 113 — type connection

```
// readsTerminated records that the single end-of-stream marker has been
	// handed to the reader. Readers block on c.reads until they see it.
```

## Line 116 — type connection

```
// readsTruncated records that teardown dropped received bytes the reader
	// had not been given. See readEndErr.
```

## Line 119 — type connection

```
// drainCredit is what the pass under way will hand the reader once its
	// acknowledgement has gone, counted into the window that acknowledgement
	// advertises. See afterPass and recvWindow.
```

## Line 124 — type connection

```
// abandoned is closed when the consumer closes the stream. It is the one
	// thing that distinguishes "the reader is behind" from "there is no reader
	// any more", which the final drain has to know: bytes owed to a reader
	// must be delivered, and bytes owed to nobody must not hold the loop open.
```

## Line 130 — type connection

```
// lastStateSent is the most recent STATE packet emitted, kept for the same
	// reason as finAck: after this connection is gone the socket may still
	// need to tell a peer what we acknowledged.
```

## Line 135 — type connection

```
// peerActivity counts packets received from the peer. It exists for
	// UtpStream.Close, which needs to tell a connection that is still
	// flushing from one whose peer has stopped answering, and is read from
	// without mu -- hence the atomic.
```

## Line 141 — type connection

```
// closeRequested records that the application asked to close the
	// connection entirely, rather than only its sending side.
	//
	// libutp's `close_requested` (utp_internal.cpp:441), set by utp_close
	// (:3374) and not by utp_shutdown. It is what decides whether the
	// acknowledgement of our own FIN ends the connection: "else if (conn->
	// fin_sent && conn->cur_window_packets == acks) { fin_sent_acked = true;
	// if (conn->close_requested) conn->state = CS_DESTROY; }" (:2178-2182).
	// Without it a half-close would end the moment its FIN came back, which is
	// the opposite of the point.
```

## Line 152 — type connection

```
// writeShut records that the application has closed the write side, so
	// nothing more will join a packet the Nagle rule is holding back. libutp
	// queues its FIN behind the held packet, which is then no longer the last
	// and goes out (utp_close, utp_internal.cpp:3232-3247; flush_packets).
```

## Line 158 — type connection

```
// readShutdown records that the application has finished reading, while
	// the connection carries on.
	//
	// libutp's `read_shutdown` (utp_internal.cpp:439), set by
	// utp_shutdown(SHUT_RD) and by utp_close. Its whole effect is that a
	// payload is not handed upwards -- the acknowledgement number advances
	// either way (:2344-2355, :2392-2395), so the peer keeps sending at full
	// rate into a window that never closes.
```

## Line 168 — type connection

```
// armIdleRto schedules the wake-up that lets an idle connection's window
	// decay. Set by the event loop, which owns the timer.
```

## Line 171 — type connection

```
// armLossProbe schedules the loss probe, or stops it for a zero
	// duration. See onLossProbe.
```

## Line 174 — type connection

```
// armRack schedules RACK's reordering timer, or stops it for a zero
	// duration. See onRackTimer.
```

## Line 177 — type connection

```
// lossProbeSent is set once the probe has fired, and cleared only when
	// the cumulative acknowledgement moves past lossProbeAckNum: one probe
	// per episode, as RFC 8985 allows.
```

## Line 183 — type connection

```
// lossProbeSeq and lossProbeAt are the packet the probe resent and when.
	// probeRecovery is set while the packets the probe's acknowledgement
	// showed lost are being resent: see probeAnswered.
```

## Line 188 — type connection

```
// lossProbeStamp is the timestamp the probe carried on the wire, and
	// ackEcho the send timestamp of the packet that drew the acknowledgement
	// now being processed, when its sender reported a delay: see
	// probeAnswered.
```

## Line 197 — type connection

```
// finAck is the acknowledgement sent for the peer's FIN, kept so the
	// socket can send it again if the peer retransmits that FIN after this
	// connection has gone.
```

## Line 202 — type connection

```
// terminalErr carries the error that ended the stream to a reader that was
	// not handed the marker directly. Written under mu and read by the
	// stream's reader without it, so it is atomic.
```

## Line 206 — type connection

```
// endErr is the error the connection itself ended with, for a writer,
	// set as the event loop returns. It differs from terminalErr, the
	// reader's, when the connection failed after the peer's whole stream
	// had arrived: the reader has had everything and ends cleanly, and a
	// writer is still told why its connection ended.
```

## Line 213 — type connection

```
// Counters, guarded by mu.
```

## Line 222 — type connection

```
// duplicateAcks counts consecutive bare acknowledgements repeating the
	// same sequence number. libutp's conn->duplicate_ack; see
	// noteDuplicateAck.
```

## Line 226 — type connection

```
// duplicatesJudged records that the current run of duplicates has
	// already been used to judge an MTU probe. See judgeProbeFromDuplicates.
```

## Line 229 — type connection

```
// mtuProbesLostToDuplicateAcks counts probes the duplicate-acknowledgement
	// path concluded were too big. Exposed so a test can assert the mechanism
	// ran rather than inferring it from where the search happened to settle.
```

## Line 233 — type connection

```
// retransmitCount is consecutive retransmission timeouts with no
	// intervening ack. libutp calls this retransmit_count.
```

## Line 236 — type connection

```
// zeroWindowProbeDue is when a closed peer window should be probed. Zero
	// means the peer's window is open and nothing is pending.
	//
	// libutp's `zerowindow_time` (utp_internal.cpp:496), armed when an
	// acknowledgement reports a zero window (:2149-2151) and acted on in
	// check_timeouts (:1142-1145).
```

## Line 243 — type connection

```
// lastAdvertisedWindow is the receive window on the last packet this
	// connection put on the wire. libutp's last_rcv_win. See onReadDrained.
```

## Line 246 — type connection

```
// recvBufferDrops counts data packets refused because the receive buffer
	// had no room for them. See ConnectionMetrics.RecvBufferDrops.
```

## Line 249 — type connection

```
// readDrainedAcks counts the acknowledgements owed because handing bytes
	// up reopened a window narrower than what is now free. Exposed through
	// ConnectionMetrics so a test can assert the mechanism ran rather than
	// inferring it from timing, which many other things also move.
```

## Line 254 — type connection

```
// ackPending records that a received packet is owed an acknowledgement,
	// which flushAck sends once the socket read that brought it has been
	// handed out, or later if ackEvery lets it wait for more packets.
	// libutp's schedule_ack / utp_issue_deferred_acks.
```

## Line 259 — type connection

```
// mu guards every field of this connection. The event loop holds it for
	// each pass, and the socket's reader holds it while it handles a packet
	// for this connection inline (receiveInline, endBatch), which is what
	// lets an acknowledgement leave from the goroutine that read the packet
	// -- libutp's model, where the embedder's read loop runs the protocol.
```

## Line 265 — type connection

```
// ready is set once the event loop has finished setting the connection
	// up. newUtpStream holds mu from creation until then, so the reader never
	// sees it unset on a connection the socket made; a connection built by
	// hand, without an event loop, declines inline packets. finished is set
	// once the loop has stopped running passes; from then on nothing touches
	// the state but the loop's own teardown. Both under mu.
```

## Line 272 — type connection

```
// inBatch is set while the socket's reader is part way through a read
	// batch that has delivered packets to this connection, and holds the
	// acknowledgement back until the batch is done: libutp's embedder reads
	// until the socket would block and then calls utp_issue_deferred_acks
	// (utp.h:512-517). See endBatch.
```

## Line 278 — type connection

```
// wantWrite records, within a pass, that there may be data to send:
	// an acknowledgement opened the window, or a write moved bytes into the
	// send buffer. The pass sends it before it ends. These used to be
	// signals the loop sent itself on writable, for the next pass.
```

## Line 283 — type connection

```
// kick wakes the event loop when a pass the reader ran inline leaves
	// something only the loop can do: tear the connection down.
```

## Line 286 — type connection

```
// loopTimers are the timers the event loop waits on. Guarded by mu.
```

## Line 288 — type connection

```
// barrier is the connection's clock as an idleBarrier, or nil: a virtual
	// clock in a test. Resolved once, in newConnection.
```

## Line 291 — type connection

```
// What lets the loop rest: see dormant.go. dormant is the loop's
	// goroutine having gone, read and cleared without mu; asleep is the same
	// under mu. restClock is nil for a connection that never rests.
	// attached is set once the socket delivers this connection's packets
	// inline (UtpSocket.attachInline). wakeFn is wake, bound once.
```

## Line 303 — type connection

```
// readsHeld is set while the receive buffer holds bytes the reader's
	// full queue has no room for. Written under mu, read without it by
	// UtpStream.notifyRead.
```

## Line 307 — type connection

```
// stream is the stream this connection belongs to, for passes run off
	// the event loop. Set at setup.
```

## Line 310 — type connection

```
// lastActivity is when the connection last heard from the peer or the
	// application. The idle timer is not moved for each: when it fires, it
	// re-arms for what is left, and the connection closes only once a whole
	// MaxIdleTimeout has passed since this. Resetting it on every packet cost
	// a timer operation per packet on the reader's path, which libutp, with
	// no timer of its own per connection, does not pay.
```

## Line 317 — type connection

```
// out sends what this connection emits. The socket sets it to write
	// straight to the wire; when it is nil, as in unit tests that build a
	// connection by hand, emissions are queued on socketEvents instead.
```

## Line 321 — type connection

```
// outEvent is what emitPacket hands out, reused: out finishes with it
	// before returning. Guarded by mu, as every emission is.
```

## Line 324 — type connection

```
// inReadBatch is set while the socket's reader holds this connection in
	// its list for the read batch it is dispatching. The reader's alone.
```

## Line 327 — type connection

```
// dropUnacked is set while a packet is being handled when libutp would
	// discard it without scheduling an acknowledgement -- its early `return
	// 0`s in utp_process_incoming (utp_internal.cpp:2381-2386, :2425-2431).
	// Cleared at the start of every packet.
```

## Line 332 — type connection

```
// clk is this connection's time source: deadlines and timers. Never nil
	// after newConnection; read through c.now() for the nil-safe path that
	// struct-literal tests need.
```

## Line 337 — type connection

```
// clock reads the wall clock in the uint32 microseconds uTP puts on the
	// wire. ConnectionConfig.NowMicros when set, and nil otherwise -- read it
	// through nowMicros, never directly.
	//
	// Every timestamp this connection stamps and every one-way delay it
	// derives goes through that accessor, so a test can pin it. Scheduling
	// does not: see the note on the config field.
```

## Line 346 — type connection

```
// mtu is this connection's path-MTU search. See mtu.go.
```

## Line 348 — type connection

```
// lastSentPacket is when this connection last put a packet on the wire.
	// libutp's `last_sent_packet`, which its keep-alive compares against
	// (utp_internal.cpp:1272).
```

## Line 352 — type connection

```
// ackHeldSince is when the acknowledgement now pending was first held
	// back, and armAckHold wakes the loop when it is due. dataSinceAck counts
	// the data packets it would cover. See flushAck.
```

## Line 358 — type connection

```
// lastDataAt, lastDataGap and dataGap time the data packets arriving:
	// when the last came, the gap before it, and a smoothed gap. See
	// ackEvery.
```

## Line 364 — type connection

```
// ackPathQueuedAt is when the way back last showed a queue; the rate
	// term in ackEvery applies for ackRateMemory after it.
```

## Line 367 — type connection

```
// ackQueueSamples holds the way back's queueing delay, sampled every
	// ackQueueSampleEvery (the next at ackQueueNext), and ackSelfQueuedAt
	// is when it was last seen climbing as only this receiver's own
	// acknowledgements make it climb. See ackPathSelfQueued.
```

## Line 375 — type connection

```
// armProbeTimer wakes the event loop when a closed window has gone
	// unprobed for long enough. Installed by the event loop, which owns the
	// timer.
```

## Line 379 — type connection

```
// probingZeroWindow allows one packet through a window the peer has
	// closed. libutp expresses the same thing by writing PACKET_SIZE into
	// max_window_user, which the next acknowledgement then overwrites.
```

## Line 384 — type connection

```
// rtoDeadline is when this connection's retransmission timeout expires.
	//
	// libutp has exactly one of these per connection (`rto_timeout`,
	// utp_internal.cpp:494): armed when the first packet enters an empty
	// window (:994-998), reset on every acknowledgement of new data
	// (:1388-1389), and compared against the clock before a timeout is
	// declared (:1147-1148). This connection arms a timer per outstanding
	// packet, and this deadline is what makes their expiries one timeout,
	// not several, and never early. conformance_timing_test.go; history:
	// HISTORY.md, "rtoDeadline".
```

## Line 396 — type connection

```
// fastTimeout is libutp's fast_timeout (utp_internal.cpp:444): set by a
	// retransmission timeout (:1247), it makes each acknowledgement that
	// leaves the oldest outstanding packet next in line for a fast resend
	// send that packet again at once (:2256-2282). It is how the packets a
	// timeout gave up as lost come back one per round trip, rather than all
	// at the timeout. See onFastTimeout.
```

## Line 404 — type connection

```
// synTimeout is the current retransmission timeout for the SYN, doubled
	// on each attempt. libutp calls this retransmit_timeout.
```

## Line 407 — type connection

```
// synSentAt is when the SYN first went out, for the RTT sample the
	// SYN-ACK gives. See onState.
```

## Line 446 — func newConnection

```
// No delay is measured from the SYN, and the SYN-ACK therefore
		// echoes a timestamp difference of zero.
		//
		// libutp processes an incoming SYN with `utp_process_incoming(conn,
		// packet, len, /*syn=*/true)`, and that call returns at
		//
		//	if (syn) {
		//	    return 0;
		//	}
		//
		// before ever reaching `conn->reply_micro = their_delay`
		// (utp_internal.cpp:2002). So reply_micro keeps the zero it was
		// initialised with (:2617) until a non-SYN packet arrives, and zero
		// is a defined signal rather than a measurement: "if the actual delay
		// is 0, it means the other end hasn't received a sample from us yet".
		// History: HISTORY.md, "newConnection: no delay from the SYN".
```

## Line 500 — func newConnection

```
// The ceiling is the largest packet this library will ever try, and
		// the first it sends, as libutp does; a lost probe brings it down.
		// See newMtuSearch.
```

## Line 510 — func (*connection).sampleMetrics

```
// sampleMetrics hands a snapshot to the configured observer, throttled to
// MetricsInterval.
//
// It runs with mu held -- on the event loop, or on the socket's reader in
// endBatch -- which is the whole point: reading the connection's state
// without it would be a race.
```

## Line 584 — func (*connection).now

```
// nowMicros reads this connection's wall clock, in the uint32 microseconds
// uTP puts on the wire.
//
// The fallback is deliberate rather than an oversight: a connection built as
// a struct literal -- which several tests do -- would otherwise carry a nil
// function and panic on its first packet. A clock is not optional behaviour
// that can be left unset; only its *source* is configurable.
// now is this connection's current time.
//
// The fallback mirrors nowMicros: several tests build a connection as a
// struct literal, and a nil clock would panic on the first deadline
// comparison. Time is not optional; only its source is.
```

## Line 603 — func (*connection).timeSource

```
// clock returns this connection's Clock, never nil.
```
