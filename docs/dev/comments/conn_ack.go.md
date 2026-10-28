# conn_ack.go

Every comment in `conn_ack.go` at e56c9aa, verbatim.

## Line 9 — const maxAckCoalesce

```
// maxAckCoalesce bounds how many already-queued packets one pass of the event
// loop will take before answering them.
//
// libutp has no explicit bound: its embedder drains its socket and then calls
// utp_issue_deferred_acks. The bound here exists because this loop also
// services writes and timers, and an unbounded inner drain would let a peer
// sending continuously starve them.
```

## Line 18 — const ackQueueStep, ackRatePeriod, maxAckRateRun, maxAckQueueRun, maxAckWait,...

```
// Acknowledgement coalescing. See ackEvery.
```

## Line 20 — const ackQueueStep, ackRatePeriod, maxAckRateRun, maxAckQueueRun, maxAckWait,...

```
// ackQueueStep is how much queueing delay on the way back adds one packet
	// to the run an acknowledgement waits for.
```

## Line 23 — const ackQueueStep, ackRatePeriod, maxAckRateRun, maxAckQueueRun, maxAckWait,...

```
// ackRatePeriod is the shortest spacing this receiver keeps between its
	// acknowledgements when data packets arrive closer together than that,
	// up to maxAckRateRun packets apiece.
```

## Line 29 — const ackQueueStep, ackRatePeriod, maxAckRateRun, maxAckQueueRun, maxAckWait,...

```
// maxAckWait is the longest an acknowledgement waits for the rest of its
	// run. It waits no longer than two of the latest gap between packets per
	// packet it is waiting for, so the end of a burst is acknowledged once
	// the packets stop coming. Waiting by a smoothed gap, or with a 1 ms
	// floor, held each slow-start burst's acknowledgement for packets that
	// could not come until it was sent.
```

## Line 36 — const ackQueueStep, ackRatePeriod, maxAckRateRun, maxAckQueueRun, maxAckWait,...

```
// ackRateGate is how much queueing delay the way back must show before
	// the rate term applies, and ackRateMemory how long it goes on applying
	// after the last time it did. On paths where nothing queues, the delay
	// this receiver reads for its acknowledgements still reaches 2-6 ms: the
	// sender stamps an acknowledgement when it reads it, and a sender busy
	// sending reads late. A return path that cannot carry the
	// acknowledgements queues them by tens of milliseconds within a round
	// trip or two.
```

## Line 46 — const ackQueueStep, ackRatePeriod, maxAckRateRun, maxAckQueueRun, maxAckWait,...

```
// A queue on the way back is taken for this receiver's own
	// acknowledgements once it has climbed twice in a row: three windows of
	// ackQueueWindow samples, ackQueueSampleEvery apart and the starts of
	// consecutive windows ackQueueSpan samples apart, each window's least
	// above the greatest of the one before by ackQueueClimb -- over 400 ms.
	// See ackPathSelfQueued.
```

## Line 58 — func (*connection).ackEvery

```
// ackEvery is how many data packets an acknowledgement may wait to cover.
//
// libutp acknowledges once per pass of its embedder's loop: whatever arrived
// while it was busy shares one acknowledgement, and a packet that arrives
// alone gets its own -- on a link, one per packet, at any rate the return
// path can or cannot carry.
//
// This receiver acknowledges as libutp does until its acknowledgements are
// seen to queue on the way back, and then lets one wait for more packets, by
// two terms, the larger winning:
//
//   - The return path: the peer reports on every packet how long ours take to
//     reach it, and for a connection that only receives, ours are its
//     acknowledgements. Each ackQueueStep of filtered queueing delay on that
//     path (libutp's our_hist.get_value()) adds one packet to the run.
//   - The rate, for ackRateMemory after that delay last reached ackRateGate:
//     while packets arrive closer together than ackRatePeriod, round(1 ms /
//     gap) of them share one, at most maxAckRateRun. The gap is the smaller
//     of the latest and a smoothed one, so a burst after a pause is coalesced
//     from its second packet.
//
// Never while a gap is open: the selective ack is how the peer learns of a
// loss, and holding it back would delay recovery.
//
// And only when the queue is this receiver's own (ackPathSelfQueued).
// Coalescing helps when the way back is clogged with its acknowledgements;
// when the queue is other traffic's, fewer acknowledgements change nothing
// there and cost the sender's yielding. Otherwise it acknowledges every
// read, as libutp does.
//
// DEVIATIONS.md, "Acknowledgements: one per read, fewer when they would
// crowd the way back"; the measurements: HISTORY.md, "ackEvery".
```

## Line 118 — func (*connection).ackPathSelfQueued

```
// ackPathSelfQueued reports whether the queue on the way back is this
// receiver's own acknowledgements, as far as can be told: it has been seen,
// within ackRateMemory, climbing steadily out of its own noise. The delay is
// sampled every ackQueueSampleEvery; it is climbing steadily when it has
// climbed twice in a row: the least of a window of ackQueueWindow samples
// above the greatest of the window ackQueueSpan samples before it by
// ackQueueClimb or more, and that window likewise above the one before it.
// See ackEvery.
//
// Acknowledgements a return path cannot carry pile up steadily. Another
// flow's queue does not climb like that under this one's acknowledgements: it
// swings from one sample to the next, and a window of those holds a sample
// near nothing. Twice in a row, because the delay also steps once when a host
// is busy -- every timestamp it is read from is taken by a process that may be
// waiting for a CPU -- and a step is one climb and then level, where a queue
// of acknowledgements climbs the whole time.
// netem.TestReceiverDoesNotCoalesceBehindAnotherFlow holds it to that; the
// measurements, and the simpler tests tried first: HISTORY.md,
// "connection.ackPathSelfQueued".
```

## Line 145 — func (*connection).ackPathSelfQueued

```
// window returns the least and greatest of the ackQueueWindow
			// samples starting k spans after the oldest.
```

## Line 166 — func (*connection).flushAck

```
// flushAck sends the acknowledgement owed for whatever was received, if any,
// unless ackEvery says it should wait for more.
```

## Line 172 — func (*connection).flushAck

```
// One acknowledgement per socket read, as libutp's embedder sends it: the
	// datagrams already waiting in the kernel are all processed, and then
	// utp_issue_deferred_acks runs once (utp.h:512-517). A read that is still
	// being handed out holds the acknowledgement; the reader ends the batch
	// (endBatch) once it has dispatched the whole read, and the
	// acknowledgement then covers everything the read brought. History:
	// HISTORY.md, "flushAck: one per read".
```

## Line 203 — const ackNrAllowedWindow

```
// ackNrAllowedWindow is how far behind the last sent sequence number a peer's
// acknowledgement may point and still be believed.
//
// libutp: `#define ACK_NR_ALLOWED_WINDOW DUPLICATE_ACKS_BEFORE_RESEND`, which
// is 3 (utp_internal.cpp:64, :69).
```

## Line 210 — func (*connection).invalidAckNum

```
// invalidAckNum reports whether a packet acknowledges something this
// connection never sent, or something so old it cannot be meaningful.
//
// libutp (utp_internal.cpp:1794-1807), which is unusually explicit about why:
//
//	// ignore packets whose ack_nr is invalid. This would imply a spoofed
//	// address or a malicious attempt to attach the uTP implementation.
//	// acking a packet that hasn't been sent yet!
//
//	const uint16 curr_window = max<uint16>(
//	    conn->cur_window_packets + ACK_NR_ALLOWED_WINDOW, ACK_NR_ALLOWED_WINDOW);
//	if ((pk_flags != ST_SYN || conn->state != CS_SYN_RECV) &&
//	    (wrapping_compare_less(conn->seq_nr - 1, pk_ack_nr, ACK_NR_MASK)
//	     || wrapping_compare_less(pk_ack_nr, conn->seq_nr - 1 - curr_window, ACK_NR_MASK)))
//	    return 0;
//
// The acceptable range is a short window ending at the last sequence number
// actually sent. Anything above it acknowledges a packet that does not exist
// yet; anything below is too stale to act on. FuzzDifferentialResponder holds
// it to libutp's; history: HISTORY.md, "invalidAckNum".
```

## Line 232 — func (*connection).invalidAckNum

```
// libutp's exception for a SYN arriving in CS_SYN_RECV: there are no
		// previous packets for it to be acking.
```

## Line 248 — func (*connection).invalidAckNum

```
// Acknowledges a packet we have not sent.
```

## Line 252 — func (*connection).invalidAckNum

```
// Too far behind to mean anything.
```

## Line 268 — func (*connection).processAck

```
// An acknowledgement that names nothing new and carries no selective
	// ack -- every data packet a receiver with nothing in flight is sent --
	// still gives the delay sample libutp takes from every packet
	// (our_hist, utp_internal.cpp:2116-2127), and the MTU search still
	// checks whether it is due. Nothing else below has work to do, and on a
	// reader just woken, walking it was 3.5-4us of each packet's path to its
	// acknowledgement.
	//
	// Except a timer still armed for that number: an initiator's SYN, whose
	// timer the first acknowledgement after the handshake retires. That one
	// takes the full path.
```

## Line 303 — func (*connection).processAck

```
// An acknowledgement number outside what this connection has
			// outstanding acknowledges nothing. It does not end the
			// connection.
			//
			// libutp computes how many packets an acknowledgement covers and
			// then throws the answer away when it exceeds the window:
			//
			//	int acks = (pk_ack_nr - (conn->seq_nr - 1 - conn->cur_window_packets)) & ACK_NR_MASK;
			//	// this happens when we receive an old ack nr
			//	if (acks > conn->cur_window_packets) acks = 0;
			//	                                (utp_internal.cpp:1904-1907)
			//
			// A delayed or duplicated acknowledgement arriving after the window
			// has moved on is an ordinary event on a lossy path, and a forged one
			// is cheap; neither may end a transfer.
			//
			// The selective-ack extension on such a packet is dropped with it,
			// where libutp would still apply it: ours is applied relative to this
			// same acknowledgement number, which has just been found outside the
			// window. History: HISTORY.md, "processAck: an acknowledgement outside
			// the window".
```

## Line 338 — func (*connection).processAck

```
// A probe that came back acknowledged proves the path carries its size.
	// libutp: utp_internal.cpp:1969-1974.
```

## Line 356 — func (*connection).processAck

```
// A converged search stands for a while and is then redone, because paths
	// change. libutp: 30 minutes (utp_internal.cpp:1310).
```

## Line 366 — func (*connection).processAck

```
// Restart the retransmission timeout, but only when this ack actually
	// retired something.
	//
	// libutp resets rto_timeout from inside its RTT-sample path
	// (utp_internal.cpp:1388-1389), which runs once per newly acknowledged
	// packet -- not once per ack received. Resetting on every ack is not
	// equivalent: duplicate acks are how a peer reports a hole, so on a lossy
	// path they arrive in a stream, each would push the deadline further out,
	// and a packet fast retransmit could not recover would never be resent.
	//
	// One deliberate difference: libutp's ack_packet also runs for a packet
	// acknowledged selectively (:1529), so a selective ack moves its deadline
	// too. Here only the cumulative acknowledgement does: DEVIATIONS.md, "A
	// selective ack does not restart the retransmission timeout".
	//
	// Not when this is the loss probe's acknowledgement and it has shown
	// other packets lost: probeAnswered has started resending them, and the
	// timeout stays where it was, so the probe never makes recovery wait
	// longer than libutp's timeout would. History: HISTORY.md, "processAck:
	// restarting the timeout".
```

## Line 403 — func (*connection).processAck

```
// The peer retired something we were still timing, so the path is
		// alive: reset the consecutive-timeout count. libutp resets
		// retransmit_count in ack_packet for the same reason
		// (utp_internal.cpp:1398).
		//
		// Selective acks count. Under reordering or loss the only progress
		// may be a SACK, and ignoring those would kill a connection that is
		// in fact delivering.
```

## Line 417 — func (*connection).statePacket

```
// statePacket builds an ST_STATE, the only packet that carries a selective
// ack. libutp attaches the extension in send_ack alone (`pfa.pf.ext = 1`,
// utp_internal.cpp:795); a data packet, a FIN and every resend go out with
// `ext = 0` (:1080, :2781), and its packet size leaves room for the 20-byte
// header and nothing else (:1757-1761). So does this library.
//
// On a connection carrying data both ways, that means the peer hears about a
// hole in what it sent only from an ST_STATE -- and a data packet that goes
// out carrying the same acknowledgement number cancels the pending one
// (send_data, :768; transmit here). libutp lives with that, and so does this
// library. History: HISTORY.md, "statePacket".
```

## Line 446 — func (*connection).statePacket

```
// NOTE: Consistent with the reference implementation and the libtorrent
		// implementation, STATE packets always include the next sequence number.
```

## Line 452 — func (*connection).statePacket

```
// No selective ack once the peer's FIN has been reached in order.
		//
		// libutp: "we never need to send EACK for connections that are
		// shutting down" (utp_internal.cpp:786-788, the `!got_fin_reached`
		// term). `eof()` is our equivalent of `got_fin_reached`. Anything
		// still pending past a reached FIN is data the peer sent after
		// declaring it had none left; naming it in a selective ack invites a
		// retransmission of data neither side will deliver.
		//
		// This is close to unreachable as the connection stands: `eof()` only
		// becomes true when the receive buffer has caught up to the FIN, and
		// the same event loop then tears the connection down (the RemoteFin
		// branch below). It is kept because it matches the reference and
		// because it becomes load-bearing the moment a half-close exists --
		// see TestConformanceDataAfterReachedFin.
```

## Line 478 — func (*connection).statePacket

```
// ClosedState
```
