# conn_close.go

Every comment in `conn_close.go` at e56c9aa, verbatim.

## Line 8 — func (*connection).scheduleIdleRto

```
// scheduleIdleRto arms the idle wake-up when there is nothing outstanding, so
// the retransmission deadline is still honoured on a connection that has gone
// quiet. It is a no-op while packets are in flight, where the per-packet
// timers already cover it.
```

## Line 24 — func (*connection).onIdleRto

```
// onIdleRto applies libutp's idle branch: the retransmission deadline passed
// with nothing in flight, so the window decays by a third rather than
// collapsing (utp_internal.cpp:1216-1222), and the deadline is set again.
//
// The decay itself is the controller's, reached by the same call the
// in-flight path uses: sentPackets.OnTimeout asks HasUnackedPackets, which is
// false here, which is exactly the distinction libutp draws with
// `cur_window_packets == 0`.
```

## Line 38 — func (*connection).onIdleRto

```
// libutp re-arms from the doubled timeout (:1204), which OnTimeout has
	// just applied.
```

## Line 48 — func freshlyStamped

```
// freshlyStamped is ack as if it were built now, answering in: libutp stamps
// every packet as it sends it, with the time and with the delay it last
// measured of the peer's (send_data, utp_internal.cpp:733-738). An
// acknowledgement kept to be sent again -- a FIN's, a lingering one -- kept
// the stamps it was built with, and the peer's delay estimate took that old
// sample from every copy. The delay is the one in, the packet being answered,
// measured: a zero timestamp is none, as for a live connection (onPacket).
// Review of 224c363.
```

## Line 68 — func (*connection).lingerAck

```
// lingerAck is the acknowledgement the socket should re-send if the peer keeps
// retransmitting its FIN after this connection is gone.
//
// A transfer can arrive whole and still end as a failure for the peer: we
// acknowledge the peer's FIN, hand the application its end of stream and tear
// down; if that acknowledgement is lost the peer retransmits the FIN, the
// socket no longer has the connection, and it would answer with a RESET.
// libutp cannot do this: it keeps the socket in CS_GOT_FIN until its own
// application closes, so a retransmitted FIN is simply acknowledged again
// (utp_internal.cpp:2369-2370).
//
// Only a connection that reached the peer's FIN has anything to linger for.
// Anything else -- a reset, an idle timeout, a local close with no FIN
// received -- should still draw a RESET, which is what libutp does for a
// packet it has no socket for. History: HISTORY.md, "lingerAck".
```

## Line 84 — func (*connection).lingerAck

```
// Only a connection that ended gracefully -- a FIN was sent, received, or
	// both -- has anything to linger for. One killed by a RESET, an idle
	// timeout or an error should still draw a RESET for whatever arrives
	// afterwards, which is what libutp does for a packet it has no socket for.
```

## Line 93 — func (*connection).lingerAck

```
// We sent the FIN and never received one, so there is no FIN
		// acknowledgement to repeat. The last acknowledgement we did send
		// serves: it tells a peer retransmitting what we already have, and its
		// ack number is what distinguishes a retransmission from data written
		// past our FIN.
```

## Line 100 — func (*connection).lingerAck

```
// Numbered past our FIN, as every packet after it is: libutp's send_ack
	// stamps seq_nr, the next to send (utp_internal.cpp:783). Both sources
	// can be older -- the last state is captured as the FIN goes out, the
	// FIN's acknowledgement may precede it -- and a peer that has our FIN
	// takes an acknowledgement numbered at or before it for an old packet
	// and drops it: re-acknowledging its FIN never ended it.
```

## Line 119 — func (*connection).rememberLingerState

```
// rememberLingerState captures an acknowledgement for the socket to repeat
// after this connection is gone, while there is still live state to build one
// from.
//
// It is called as the local FIN goes out. By the time the connection actually
// shuts down, statePacket() has nothing left to build from -- the same reason
// the acknowledgement for a peer's FIN cannot be deferred -- and an initiator
// that only ever sent data has no earlier STATE to fall back on, because its
// acknowledgements ride on the data packets.
```

## Line 137 — func (*connection).notifySocketShutdown

```
// notifySocketShutdown asks the socket to forget this connection.
//
// The wait is bounded because the socket's event loop may already be gone --
// if the socket is closing, it drops its whole table anyway, so there is
// nothing left to clean up and blocking here would leak the very goroutine
// this is trying to tidy up after.
```

## Line 156 — func (*connection).finFits

```
// finFits reports whether the FIN may go now.
//
// libutp queues its FIN in the outgoing buffer like any data packet
// (`write_outgoing_packet(0, ST_FIN, NULL, 0)`, utp_internal.cpp:3377, :3419) and
// flush_packets sends it only while !is_full(), whose default is room for a
// whole packet (:931-985) -- behind anything owed a resend, since the walk
// is oldest first. This sent the FIN as soon as the send buffer was empty,
// whatever the window said. With bytes in flight above a window a loss had
// just halved, the controller refused even the FIN's zero bytes, and
// transmit panicked: 1 run in 6 of fifteen two-way transfers at 5% loss
// took the process down. Shutdown runs on every pass of the event loop, so
// a FIN held back here goes on the pass that finds room.
```

## Line 182 — func (*connection).finFits

```
// libutp's is_full records this too (:945, :957).
```

## Line 194 — func (*connection).shutdown

```
// ignore
```

## Line 198 — func (*connection).shutdown

```
// If we have not sent our FIN, and there are no pending writes, and there is no
			// pending data in the send buffer, then send our FIN
```

## Line 205 — func (*connection).shutdown

```
// No selective ack: see statePacket.
```

## Line 228 — func (*connection).shutdown

```
// No selective ack: see statePacket.
```

## Line 256 — func (*connection).updateClosingState

```
// updateClosingState decides whether the connection has finished.
//
// It is called both when a packet arrives and once per pass of the event loop.
// Only the first of those used to happen, and the second is not decoration: the
// side that closes second sends its FIN into a peer whose connection is
// already gone, so nothing ever arrives to trigger the check again. Measured
// when it was packet-driven only -- 29 connections still tracked after 60
// closed cycles and 48 goroutines grown, every one of them that second closer.
```

## Line 265 — func (*connection).updateClosingState

```
// Handle connection closing cases
```

## Line 271 — func (*connection).updateClosingState

```
// Only when the application asked to close entirely. A FIN sent by
		// CloseWrite is acknowledged like any other packet and means nothing
		// about the reading side, which is still someone's (:2178-2182).
```

## Line 280 — func (*connection).updateClosingState

```
// A connection whose peer has closed its sending side is *not* finished.
	//
	// libutp holds the socket in CS_GOT_FIN and keeps delivering
	// (utp_internal.cpp:2314 drops only what arrives past the FIN it has
	// reached), until its application closes too. So does this: the reader
	// gets its end-of-stream marker from processReads when the receive buffer
	// catches up to the FIN, and the writer carries on.
	//
	// Once the application *has* closed, this end is finished when everything
	// it sent, its FIN included, has been acknowledged and the reader has
	// everything up to the peer's FIN: libutp destroys a socket with
	// close_requested once its FIN is acknowledged (fin_sent_acked,
	// utp_internal.cpp:2174-2181). A peer that closed fully has gone by then,
	// and answers this FIN with a RESET, which ends this connection too.
	//
	// It used to end the moment both FINs existed as well, in a second block
	// with this one's conditions and none of its checks: whatever was still
	// unacknowledged -- the end of the data, and the FIN -- was abandoned,
	// and a peer that had only half-closed, still reading, never got it.
	// TestCloseAfterThePeersFinDeliversWhatWasWritten (netem); review of
	// 294cc2e.
	//
	// LocalFin != nil is doing real work in this condition. shutdown() only
	// emits the FIN once the send buffer and the pending writes have drained,
	// so its presence is the proof that there is nothing left to send; without
	// it, "nothing unacknowledged" is true at every momentary gap between a
	// window emptying and the next packet going out. History: HISTORY.md,
	// "updateClosingState: the half-close".
```

## Line 321 — func (*connection).onReset

```
// With what had arrived: a RESET before the peer's FIN has been reached
	// takes the rest of the stream with it, and is worth telling apart from
	// the routine one that follows a peer closing first (readEndErr).
```

## Line 344 — func (*connection).onReset

```
// A reset answering our SYN is a refusal: nothing on the other end will
	// take the connection. libutp means to say so --
	//
	//	if (conn->close_requested) conn->state = CS_DESTROY;
	//	else conn->state = CS_RESET;
	//	const int err = (conn->state == CS_SYN_SENT) ? UTP_ECONNREFUSED : UTP_ECONNRESET;
	//	                                        (utp_internal.cpp:2865-2870)
	//
	// -- but it overwrites the state before testing it, so the refusal is
	// unreachable and every reset reads as one. go-utp's pure Go port
	// corrects the same line. Its ICMP path, which tests the state first
	// (onICMPUnreachable), already distinguished the two here.
```

## Line 363 — func (*connection).onCloseRead

```
// onCloseRead applies libutp's read_shutdown: from here on, what arrives is
// acknowledged and dropped rather than delivered. See Stream.CloseRead for
// why the acknowledgement half matters.
```

## Line 374 — func (*connection).onCloseRead

```
// Whatever is already buffered is owed to a reader that has said it will
	// not come back, and holding it would keep the advertised window closed
	// by exactly that much.
```

## Line 389 — func (*connection).reset

```
// If we already sent our fin and got a reset we assume the receiver already got our fin
	// and has successfully closed their connection, hence mark this as a successful close.
```

## Line 398 — func (*connection).reset

```
// A Connect still waiting for the handshake learns why it failed. This
	// did not: a SYN answered with a reset closed the connection and left
	// the dial to wait out its own context, where libutp reports the error
	// at once (utp_call_on_error, utp_internal.cpp:2870). The timeout and
	// ICMP paths already signalled it; see onICMPUnreachable. Only during the
	// handshake: afterwards nobody waits on the channel, and its one slot may
	// still hold the success already sent.
```
