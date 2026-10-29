# conn_receive.go

Every comment in `conn_receive.go` at e56c9aa, verbatim.

## Line 10 — const reorderBufferMaxSize

```
// reorderBufferMaxSize bounds how far past the next expected sequence number
// a packet may name and still be processed.
//
// libutp: `#define REORDER_BUFFER_MAX_SIZE 1024` (utp_internal.cpp:54),
// applied at :1890. It is the limit on how far ahead a peer can push this
// connection's pending state, and therefore on what a peer can make it hold.
```

## Line 18 — const reorderOldPacketFloor

```
// reorderOldPacketFloor is the other end of the same test: a distance at or
// above this has wrapped, so the packet is an *old* one within
// reorderBufferMaxSize behind us rather than a wildly future one. libutp
// writes it as `(SEQ_NR_MASK + 1) - REORDER_BUFFER_MAX_SIZE`
// (utp_internal.cpp:1891).
```

## Line 25 — func (*connection).outsideReorderWindow

```
// outsideReorderWindow reports whether a packet names a sequence number too
// far from the one expected to be worth processing, and answers it the way
// libutp does if so.
//
// libutp (utp_internal.cpp:1886-1899):
//
//	const uint seqnr = (pk_seq_nr - conn->ack_nr - 1) & SEQ_NR_MASK;
//	if (seqnr >= REORDER_BUFFER_MAX_SIZE) {
//	    if (seqnr >= (SEQ_NR_MASK + 1) - REORDER_BUFFER_MAX_SIZE
//	        && pk_flags != ST_STATE) conn->schedule_ack();
//	    return 0;
//	}
//
// `seqnr` counts how far past the next expected packet this one is, so 0 means
// "exactly the packet we are waiting for". Past 1024 the packet is either
// absurdly far in the future or, by wrapping, an old one already consumed.
// Either way libutp drops it without buffering it, acking it, or letting it
// touch congestion control. An old packet -- within 1024 behind, and not a
// STATE -- additionally re-triggers an ack, because the peer evidently missed
// the one already sent. Without the bound a peer could make this connection
// hold pending state anywhere in the 16-bit space.
//
// The check covers ST_DATA, ST_STATE and ST_FIN only. libutp reaches it
// through utp_process_incoming, which a SYN returns from before the check
// (utp_internal.cpp:1878) and which a RESET never enters at all: RESET is
// handled in its own branch at the socket level and returns
// (utp_internal.cpp:2855-2880). FuzzDifferentialResponder holds both to
// libutp's; history: HISTORY.md, "outsideReorderWindow".
```

## Line 63 — func (*connection).outsideReorderWindow

```
// wrapping
```

## Line 69 — func (*connection).outsideReorderWindow

```
// An old packet the peer is still retransmitting: re-ack, as libutp
		// does, so it can stop.
		//
		// Emitted directly rather than through transmit(), which registers
		// the packet with SentPackets and arms a retransmission timer. A
		// STATE is neither retransmitted nor numbered: libutp's send_ack
		// writes the current seq_nr without consuming it
		// (utp_internal.cpp:781, with :1088-1089 showing where one is
		// consumed). Going through transmit() advanced our sequence number
		// on every re-ack, so the next STATE carried a number libutp's never
		// would -- caught by FuzzDifferentialResponder comparing consecutive
		// acks.
```

## Line 104 — func (*connection).onPacket

```
// A SYN that is not this connection's own, retransmitted, is ignored,
	// and this connection is left as it was. libutp drops any SYN for a
	// connection it already has -- "rejected incoming connection, connection
	// already exists" (utp_internal.cpp:2957-2965) -- so the connection never
	// sees it. This took one with another sequence number, or any SYN at a
	// connection we dialled, for a protocol violation and reset itself: a
	// peer's new connection that happened to draw the ids of one this side
	// still held -- closing, or left behind -- killed the established one.
	// Measured in a two-hour soak: 41 connections in 880,000. A SYN with
	// this connection's own sequence number is the peer not having heard
	// the SYN-ACK, and is still answered (the st_syn case at the end).
```

## Line 123 — func (*connection).onPacket

```
// Every packet from the peer is evidence the connection is still alive,
	// which is what Close watches to decide whether waiting for the flush is
	// still worth anything.
```

## Line 131 — func (*connection).onPacket

```
// A RESET ends the connection whatever its numbers say. libutp acts on
	// one at the socket, before anything about the packet is checked
	// (utp_internal.cpp:2850-2873). It went through the checks below here,
	// and a RESET acknowledges the packet that drew it: one answering our
	// acknowledgement or keep-alive names our next, unsent sequence number,
	// which the acknowledgement check reads as acknowledging a packet never
	// sent, and its random sequence number can fall outside the reorder
	// window. Either way it was dropped, so a connection whose peer had gone
	// heard the peer's socket say so and carried on: in the long soak,
	// server connections left behind by clients that had given up, never
	// ending.
```

## Line 147 — func (*connection).onPacket

```
// libutp validates the acknowledgement number before anything else, and
	// so does this. Order matters: a packet rejected here must not first
	// draw a re-ack from the reorder-window check below.
```

## Line 158 — func (*connection).onPacket

```
// An "extension bits" extension must be exactly 8 bytes, or libutp drops
	// the packet (utp_internal.cpp:1850-1856). Not for a SYN: libutp creates
	// the connection and answers it whatever its extensions say
	// (:2984-2990), and a SYN does not reach here once the connection
	// exists.
```

## Line 171 — func (*connection).onPacket

```
// The peer's window, and the zero-window probe, from a packet that has
	// passed both checks above. libutp reads `pf1->windowsize` only there
	// (utp_internal.cpp:2144, reached after :1794-1807 and :1886-1899); this
	// used to take it first thing, so a packet the reference discards -- one
	// anybody who can guess a connection id can send -- could close our view
	// of the peer's window.
```

## Line 178 — func (*connection).onPacket

```
// libutp restarts the deadline on *every* packet reporting a zero window
	// (`zerowindow_time = current_ms + 15000`, :2148-2151), so the probe goes
	// 15 seconds after the last one, not the first; this used to keep the
	// first deadline. The next packet overwrites the forced one-packet
	// window with whatever the peer now reports (:2145).
```

## Line 194 — func (*connection).onPacket

```
// Measure how long ago the peer stamped this packet. That value is what we
	// echo back in timestamp_difference_microseconds, and it is the peer's
	// only delay signal for congestion control. libutp computes reply_micro
	// the same way, as a uint32 subtraction of the packet's timestamp from the
	// current time (utp_internal.cpp:2000-2002).
	//
	// **It is measured here, after the validation above, and not before.**
	// libutp reaches `conn->reply_micro = their_delay` only once the packet
	// has passed the acknowledgement-number rule (:1794-1807) and the
	// reorder-window check (:1886-1899); a packet either of those rejects
	// never touches it, and so a packet bad enough to be dropped cannot change
	// the peer's delay signal.
	//
	// Not capped: libutp echoes `time - p` raw, however it wraps
	// (utp_internal.cpp:2000). A peer whose clock is ahead of ours produces a
	// value that wraps to something enormous, and that is what the reference
	// passes on; the receiving end has its own rule for it.
	//
	// A zero timestamp is no timestamp: `their_delay = (p == 0 ? 0 : time -
	// p)` (:2000), and a zero is neither echoed as a delay nor sampled
	// (:2002). FuzzDifferentialResponder compares the echoed field with
	// libutp's; history: HISTORY.md, "onPacket: measuring the peer's
	// delay".
```

## Line 218 — func (*connection).onPacket

```
// One reading of the microsecond clock for both uses below.
```

## Line 226 — func (*connection).onPacket

```
// The delay in the peer's direction is not a congestion signal for this
	// sender -- it describes the other half of the path -- but it is how
	// clock drift between the two ends becomes visible. libutp feeds its
	// `their_hist` from exactly this value, on every packet that passes the
	// checks above (utp_internal.cpp:2000-2004).
	//
	// The *uncapped*, raw wrapping value is what goes to the controller,
	// which is what libutp feeds its their_hist (`their_delay`, :2000-2004).
	// The cap above exists to stop an unusable clock reading being echoed
	// back to the peer, and applying it here would break the drift detection
	// at exactly the point it starts to matter: a clock drifting slow makes
	// this measurement shrink until it passes zero and wraps, and a wrapped
	// value capped to one second is a base that has stopped moving. See
	// peerDelayHist.
```

## Line 245 — func (*connection).onPacket

```
// Handle different packet types
```

## Line 263 — func (*connection).onPacket

```
// Process acknowledgments
```

## Line 266 — func (*connection).onPacket

```
// INT_MAX is the peer saying it has no measurement, not a delay of
		// 35 minutes. libutp:
		//
		//	const uint32 actual_delay = (uint32(pf1->reply_micro)==INT_MAX?0:uint32(pf1->reply_micro));
		//	                                        (utp_internal.cpp:2017)
		//
		// Without this the value goes into the delay history as a sample,
		// where being a maximum it changes nothing for the base but reports a
		// 35-minute queue for as long as it is the current sample.
```

## Line 280 — func (*connection).onPacket

```
// The peer's timestamp less the delay it measured is the timestamp of
		// the packet of ours it last received, in our own clock: the two
		// clocks' offset cancels, and holding the acknowledgement back only
		// makes it later. See probeAnswered.
```

## Line 286 — func (*connection).onPacket

```
// Before the acknowledgement retires anything: libutp counts
		// duplicates against the window as it stood when the packet arrived
		// (utp_internal.cpp:1921, with ack_packet not reached until :2194).
```

## Line 308 — func (*connection).onPacket

```
// Handle retransmissions
```

## Line 312 — func (*connection).onPacket

```
// Send STATE packet if appropriate
```

## Line 316 — func (*connection).onPacket

```
// The synState is generated at the beginning of the eventLoop
```

## Line 322 — func (*connection).onPacket

```
// Built as libutp's send_rst builds every reset, and as the
				// socket's own does: no timestamps, no window, and the
				// sequence number of the packet that drew it
				// (utp_internal.cpp:846-865). This one used to carry a
				// timestamp and a 100000-byte window.
```

## Line 333 — func (*connection).onPacket

```
// A SYN for a connection we have already accepted is the initiator
			// retransmitting because our SYN-ACK was lost. Re-send the same
			// SYN-ACK: nothing else ever retransmits it, so dropping this
			// duplicate silently strands the peer until it gives up.
			//
			// libutp does not do this. A SYN whose connection already exists
			// is dropped in utp_process_udp (utp_internal.cpp:2955-2958), and
			// a libutp acceptor whose SYN-ACK was lost answers none of the
			// retries. This comment used to say the opposite, citing a line
			// in the socket destructor. DEVIATIONS.md, "A retransmitted SYN
			// is answered"; TestConformanceDuplicateSynBeforeData.
```

## Line 353 — func (*connection).onPacket

```
// A FIN is acknowledged immediately, not deferred.
		//
		// libutp: `// if the other end wants to close, ack` followed by a
		// direct `conn->send_ack()` (utp_internal.cpp:2369-2370), separately
		// from the deferred one it also schedules at :2404.
		//
		// Deferring it does not work here for a reason worth recording:
		// reaching a FIN tears the connection down in the same pass of the
		// event loop, so by the time the deferred acknowledgement would be
		// sent there is no connection left to build one from, and the peer
		// gets nothing at all. Two corpus cases caught that immediately.
```

## Line 372 — func (*connection).onPacket

```
// Kept so the socket can send it again if the peer retransmits
			// this FIN after the connection has gone -- see lingerAck. It has
			// to be captured here: by the time the connection shuts down,
			// statePacket() has nothing left to build one from, which is the
			// same reason the acknowledgement above cannot be deferred.
```

## Line 381 — func (*connection).onPacket

```
// Note that an acknowledgement is owed; do not send it here.
		//
		// libutp calls `schedule_ack()` (utp_internal.cpp:2404, :2472), which
		// adds the socket to a list its embedder flushes once per pass of its
		// event loop. So a batch of packets arriving together draws one
		// acknowledgement, not one each. History: HISTORY.md, "onPacket:
		// deferring the acknowledgement".
```

## Line 406 — func (*connection).onPacket

```
// Wake the writer on any packet that carries an acknowledgement, not only
	// on ST_STATE. libutp makes the socket writable again whenever an incoming
	// packet leaves the window below full, before the early return that is
	// specific to ST_STATE (utp_internal.cpp:2302-2308), so its embedder
	// writes on the acknowledgements a data packet carries as well. On a
	// connection carrying data both ways the peer's acknowledgements ride on
	// its data packets (send_data, :768), and a window they open must be used.
	// History: HISTORY.md, "onPacket: waking the writer".
```

## Line 419 — func (*connection).onPacket

```
// Data or a FIN is handed up by processReads, which every pass runs.
```

## Line 424 — func (*connection).isOwnSyn

```
// isOwnSyn reports whether a SYN is this connection's own, retransmitted by a
// peer that has not heard the SYN-ACK.
```

## Line 430 — func (*connection).onSyn

```
// onSyn handles a SYN that reached the connection. Only its own does (see
// onPacket), and that one changes nothing here: it is answered with the
// SYN-ACK again. Anything else used to reset the connection with
// ErrInvalidSyn or ErrSynFromAcceptor; libutp never lets such a SYN reach
// one (utp_internal.cpp:2957-2965).
```

## Line 443 — func (*connection).onState

```
// NOTE: In a deviation from the specification, we initialize the ACK num
		// to the sequence number of the SYN-ACK minus 1. This is consistent with
		// the reference implementation and the libtorrent implementation.
```

## Line 450 — func (*connection).onState

```
// wrapping subtraction for uint16
```

## Line 454 — func (*connection).onState

```
// The SYN's round trip is the first RTT sample, as it is in libutp:
		// the SYN sits in its outgoing buffer like any packet, and the
		// SYN-ACK acknowledges it through the same ack_packet, which samples
		// a packet sent once (`if (pkt->transmissions == 1)`,
		// utp_internal.cpp:1362). A SYN sent more than once gives no sample,
		// because the answer cannot be matched to one transmission.
		//
		// Without it a connection's first timeout was the 3-second initial
		// value where libutp's was computed from the handshake -- 1 second on
		// any path under about 330ms. Measured against libutp:
		// TestConformanceRetransmissionTimeoutComputation.
```

## Line 471 — func (*connection).onState

```
// Report success the same way the acceptor path does: send nil, not
		// close the channel, which a receiver checking `ok` reads as failure.
		//
		// The channel is buffered with room for one, and the failure path at
		// onTimeout only fires while the state is still connConnecting, so
		// this send cannot block and cannot be followed by another. History:
		// HISTORY.md, "onState: reporting a connection".
```

## Line 493 — func (*connection).onData

```
// An empty payload is not an error. libutp delivers only non-empty data
	// to the application but still advances ack_nr, so the packet is acked
	// and the sequence space moves on (utp_internal.cpp:2342-2355). The
	// receive buffer below does the same: a zero-length write advances the
	// ack number without copying anything.
```

## Line 504 — func (*connection).onData

```
// connection being established.
```

## Line 513 — func (*connection).onData

```
// connection is closing
```

## Line 517 — func (*connection).onData

```
// From the acknowledgement number, not the connection's first
			// sequence number: once 65,536 packets have arrived, that is no
			// lower bound at all, and a FIN that overtook the last data
			// left a packet just behind the first number outside the range
			// -- dropped every time it was resent, holding everything after
			// it behind a gap that never filled.
```

## Line 526 — func (*connection).onData

```
// Numbered past the end of the stream: dropped, unacknowledged,
				// and the connection carries on. libutp: `if (conn->got_fin &&
				// pk_seq_nr > conn->eof_pkt) return 0;` (utp_internal.cpp:
				// 2381-2386). This used to close the connection with
				// ErrInvalidSeqNum, which handed anyone able to guess a
				// connection id a way to end it once the peer's FIN was in.
				//
				// libutp compares the two numbers without wrapping; this
				// range does wrap, so a stream whose FIN is numbered just
				// past 65535 is not cut short by it.
```

## Line 540 — func (*connection).onData

```
// The application has finished reading, so the bytes go nowhere --
		// but the sequence number still has to advance, or the peer is
		// left retransmitting a packet that arrived. libutp runs
		// `conn->ack_nr++` outside the `!read_shutdown` guard for exactly
		// this reason (utp_internal.cpp:2344-2355).
		//
		// A zero-length write is how that is expressed here: the receive
		// buffer records the sequence number, copies nothing, and consumes
		// no space, so the advertised window stays open. Out-of-order
		// packets still reorder correctly -- an empty entry fills its gap
		// like any other.
```

## Line 554 — func (*connection).onData

```
// An out-of-order packet that is already held is discarded without
		// an acknowledgement: libutp returns before schedule_ack
		// (utp_internal.cpp:2425-2431). An in-order duplicate never gets here
		// -- it is behind the acknowledgement number, and
		// outsideReorderWindow re-acknowledges it, as libutp does (:1891).
```

## Line 563 — func (*connection).onData

```
// not closing should send data
```

## Line 581 — func (*connection).onData

```
// do nothing
```

## Line 586 — func (*connection).closedFinAck

```
// closedFinAck acknowledges a peer's FIN that reached this connection after
// it closed, or closed it: one whose FIN also acknowledged ours, so that
// processAck ended the connection before this packet's own acknowledgement
// was built, and statePacket, which builds nothing for a closed connection,
// left the FIN unanswered. The peer then waited for its retransmission three
// seconds on to be re-acknowledged from the socket's linger state. libutp
// acknowledges a FIN whatever its socket's state (send_ack,
// utp_internal.cpp:2369-2370). It is the linger acknowledgement, covering the
// FIN if it completes the stream, stamped now (freshlyStamped). Review of
// 294cc2e.
```

## Line 619 — func (*connection).onFin

```
// A FIN may carry a final payload, and it reaches the application by
		// the same route as any other, so a shut-down read side drops it the
		// same way. The sequence number still has to be registered: it is
		// what marks the end of the stream.
```

## Line 628 — func (*connection).onFin

```
// If we have already received a FIN, a subsequent FIN with a different
				// sequence number is incorrect behavior
```

## Line 631 — func (*connection).onFin

```
// The end of the stream is the first FIN's. A later one
					// with another number is handled as data: libutp records
					// eof_pkt only `if (pk_flags == ST_FIN && !conn->got_fin)`
					// (utp_internal.cpp:2316) and the packet then takes the
					// data path below it. This used to reset the connection.
```

## Line 644 — func (*connection).onFin

```
// Register the FIN with the receive buffer
```

## Line 659 — func (*connection).onICMP

```
// onICMP applies an ICMP report about a packet this connection sent.
//
// libutp's two entry points, utp_process_icmp_fragmentation and
// utp_process_icmp_error (utp_internal.cpp:3079-3150), differ only in what
// they do once the connection is found, so the lookup lives in the socket and
// the two decisions live here.
```

## Line 677 — func (*connection).onICMPFragmentationNeeded

```
// onICMPFragmentationNeeded lowers the path-MTU ceiling on a router's say-so.
// See mtuSearch.icmpFragmentationNeeded for the libutp text and for the one
// deliberate difference, which is a unit conversion.
```

## Line 682 — func (*connection).onICMPFragmentationNeeded

```
// libutp logs exactly this line (utp_internal.cpp:3104).
```

## Line 687 — func (*connection).onICMPUnreachable

```
// onICMPUnreachable tears the connection down after an ICMP error.
//
// libutp:
//
//	const int err = (conn->state == CS_SYN_SENT) ? UTP_ECONNREFUSED : UTP_ECONNRESET;
//	switch(conn->state) {
//	    case CS_IDLE: return 1;                    // don't pass on errors for idle/closed
//	    default:
//	        conn->state = conn->close_requested ? CS_DESTROY : CS_RESET;
//	}
//	utp_call_on_error(conn->ctx, conn, err);
//	                                        (utp_internal.cpp:3117-3150)
//
// Two mappings are worth stating.
//
// CS_IDLE has no counterpart: libutp's socket object exists before connect or
// accept is called, and ours does not -- the connection is created by the
// call. The nearest thing is a connection that has already finished, and that
// is skipped for the same stated reason ("don't pass on errors for
// idle/closed connections").
//
// CS_DESTROY and CS_RESET both land on connClosed here, because that is the
// only terminal state this connection has. The distinction libutp draws is
// how soon the socket object goes away, not what the application is told:
// both branches fall through to the same utp_call_on_error, so the error is
// reported either way.
```

## Line 719 — func (*connection).onICMPUnreachable

```
// CS_SYN_SENT is reachable only by the initiator, and only before the
	// handshake completes.
```

## Line 728 — func (*connection).onICMPUnreachable

```
// Deliberately not c.reset(): that treats a reset arriving after our own
	// FIN as a clean close and discards the error. libutp has no such branch
	// on this path -- it reports the error whatever state the connection was
	// in -- and the difference is load-bearing, because "the peer went away"
	// is exactly what an application half way through a close needs to hear.
	// A connection still in the handshake has a Connect call waiting on this
	// channel; without it the caller waits out its own context for a failure
	// the network already reported. Same handling as the connect-attempt
	// timeout at onTimeout.
```
