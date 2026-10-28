# socket_receive.go

Every comment in `socket_receive.go` at e56c9aa, verbatim.

## Line 15 — func (*Socket).readLoop

```
// The inbound path starts here, so a virtual clock must not move while a
	// packet is between the wire and a connection. See idleBarrier.
```

## Line 27 — func (*Socket).readLoop

```
// Each read is a batch. A connection holds the acknowledgement owed for
	// a packet until the batch has been dispatched, so a run of datagrams that
	// were already waiting in the kernel draws one acknowledgement, not one
	// each -- libutp's embedder loop of recvfrom until EWOULDBLOCK, then
	// utp_issue_deferred_acks (utp.h:512-517). A Conn that cannot read in
	// batches but is a QueuedReader has its batch end when nothing more is
	// waiting; any other Conn makes every datagram its own batch, which holds
	// nothing back.
```

## Line 38 — func (*Socket).readLoop

```
// run counts the datagrams read since the last batch ended, while a
	// QueuedReader says more are waiting.
```

## Line 46 — func (*Socket).readLoop

```
// Part way through a run the read cannot block, so this loop is
			// not idle -- and must not be seen to be, with acknowledgements
			// still owed for the run. See idleBarrier.
```

## Line 86 — func (*Socket).readLoop

```
// Decoded from buf, which the next read reuses: a packet
			// processed before then needs no copy, and one kept takes its
			// body (packet.own). The whole datagram was copied, an
			// allocation for every packet received.
```

## Line 100 — func (*Socket).dispatch

```
// dispatch routes one datagram, on the reader's goroutine. A packet for an
// established connection is handled there and then, under that
// connection's lock (connection.receiveInline), and the connection is
// recorded in touched, to be acknowledged at the end of the batch.
//
// This handed every datagram to a socket event loop, which handed it on to
// the connection's own event loop: two goroutine hand-offs before the packet
// was looked at, and a third, to a write loop, before its acknowledgement
// left. libutp's embedder calls utp_process_udp from its read loop and the
// acknowledgement is sent from the same call stack.
```

## Line 120 — type readBatch

```
// readBatch is the connections one read batch delivered to, each once, in the
// order the batch first reached them. A list rather than a set: a batch
// usually reaches one connection, and a map's iteration and deletion were a
// tenth of a packet's cost on a reader woken with its caches cold.
```

## Line 128 — func (*Socket).endBatch

```
// endBatch acknowledges, for each connection a read batch delivered to, what
// the batch brought it, and empties touched. libutp's
// utp_issue_deferred_acks, which its embedder calls once the socket would
// block (utp.h:512-517).
```

## Line 141 — const maxConsecutiveReadErrors

```
// maxConsecutiveReadErrors is how many read errors in a row the socket
// tolerates before it gives up and closes. go-libutp, go-utp's pure Go port
// and rust-utp all use 100, for the same reason: some platforms report errors
// on a UDP socket that do not mean it is finished.
```

## Line 147 — func (*Socket).readFailed

```
// readFailed handles a read error, and reports whether the read loop should
// stop. failures counts the errors since the last successful read.
//
// The loop used to stop at the first error that did not say it was temporary,
// and leave the socket open. Nothing read from it again: every connection on
// it retransmitted into silence until it timed out, new dials and accepts
// waited out their contexts, and nothing told the application why. Now a run
// of errors is tolerated, and a socket that cannot read closes, so everything
// waiting on it fails at once with ErrClosed-shaped errors instead.
```

## Line 175 — func (*Socket).readFailed

```
// In the background: Close waits for this loop's peers to settle, and
	// this loop is one of the things it tears down.
```

## Line 181 — func (*Socket).handleIncomingBuf

```
// handleIncomingBuf routes one datagram, and returns the connection that
// took it inline, or nil. Called with dispatchMu held.
```

## Line 184 — func (*Socket).handleIncomingBuf

```
// Handle incoming packets
```

## Line 187 — func (*Socket).handleIncomingBuf

```
// Not uTP, or not well formed. Ordinary on a port shared with
		// another protocol -- go-libutp, go-utp and rust-utp all hand such
		// datagrams on rather than complain -- and anyone can send them, so
		// a warning per datagram is a way to flood the log.
```

## Line 205 — func (*Socket).handleIncomingBuf

```
// Each derivation is looked up by its key alone, built into a reused
	// buffer: no ConnectionID and no string per attempt. This ran three
	// cidFromPacket calls for every datagram -- for an accepted connection the
	// match is the third -- each formatting the peer's address and the key
	// anew: 9 of the 23 allocations a data packet cost, and most of the time
	// routing took. A ConnectionID is built below only for a SYN.
```

## Line 217 — func (*Socket).handleIncomingBuf

```
// The last hop of the inbound path: a virtual clock must not
		// consider the system quiet while this packet is queued for a
		// connection that has not woken to take it.
		//
		// Noted and taken on streamIncoming only, and that is what keeps
		// the accounting honest rather than approximately right. A
		// connection's event channel has several writers -- this loop,
		// the ICMP entry points, and the application's own CloseWrite,
		// CloseRead and Close -- and only this one is a clock
		// participant. Keying both halves on the event type makes the
		// pairing structural: nothing else notes, and the connection
		// takes for nothing else. See idleBarrier.
```

## Line 233 — func (*Socket).handleIncomingBuf

```
// Queued, so kept past this read. See packet.own.
```

## Line 244 — func (*Socket).handleIncomingBuf

```
// See droppedFullConnQueue. This is a real packet loss that the
			// peer has to recover from, and it is ours, not the network's.
```

## Line 268 — func (*Socket).handleIncomingBuf

```
// An unmatched RESET is dropped, never answered: replying to a reset
		// with a reset lets two hosts trade them forever. libutp handles
		// ST_RESET in its own branch and returns without responding
		// (utp_internal.cpp:2850-2881).
```

## Line 273 — func (*Socket).handleIncomingBuf

```
// A FIN or data packet retransmitted after we closed is
			// re-acknowledged rather than reset. Checked here, once every
			// lookup above has missed, so a connection that still exists
			// always wins.
```

## Line 285 — func (*Socket).handleIncomingBuf

```
// A SYN is kept, by the connection it starts or until an Accept takes
	// it. See packet.own.
```

## Line 291 — func (*Socket).handleIncomingBuf

```
// A connection we dialled may already receive on the id this one would:
	// accepting it would leave two connections a packet could be for. libutp
	// refuses the SYN without an answer -- "rejected incoming connection,
	// connection already exists" (utp_internal.cpp:2957-2965) -- and the
	// dialler, hearing nothing, gives up or retries on its own terms.
```

## Line 308 — func (*Socket).handleIncomingBuf

```
// The connection cap is checked here, where libutp checks it: after the
	// duplicate lookup above and before the firewall
	// (utp_internal.cpp:2967-2974). A SYN for a connection already parked
	// waiting for Accept is the peer retransmitting, not a new connection,
	// and does not count against the cap.
```

## Line 324 — func (*Socket).handleIncomingBuf

```
// The firewall is asked here: every lookup above has missed, so this is a
	// connection the socket does not have, and nothing has been created for it
	// yet. libutp asks in the same place, after its duplicate-connection check
	// and before utp_create_socket (utp_internal.cpp:2975-2982), and a refusal
	// there is `return 1` -- the SYN is dropped without an answer. Not a
	// RESET: a RESET would confirm to a refused peer that something is
	// listening, which is the opposite of what a blocklist is for.
```

## Line 363 — func (*Socket).handleIncomingBuf

```
// The socket's context, not the Accept call's: an accepted connection
		// outlives the Accept that produced it, exactly as a dialled one
		// outlives its dial. The sibling branch above already used s.ctx.
```

## Line 368 — func (*Socket).handleIncomingBuf

```
// Nobody is waiting, and the socket has a configuration of its own:
		// answer now, as libutp does, and hold the connection for an Accept.
		//
		// Held from here, under the lock an Accept takes it under, not once
		// its handshake reports from another goroutine: by then the dialler
		// can have the answer, and an AcceptBacklog asked of this socket
		// found nothing -- TestDialCompletesWithNoAcceptWaiting, 9 runs in
		// 200. libutp hands its embedder the connection from inside the call
		// that received the SYN (utp_call_on_accept, utp_internal.cpp:3001).
```

## Line 380 — func (*Socket).handleIncomingBuf

```
// Already held. A connection that fails ends, and is dropped
			// from answered once it has.
```

## Line 393 — func (*Socket).rememberLingerAck

```
// maybeSendReset answers a packet addressed to a connection this socket does
// not have, subject to the same rate limiting libutp applies.
//
// Answering every such packet is both an incompatibility and an amplification
// vector: a peer that keeps sending to a torn-down connection draws one RESET
// per packet. A 300-connection test run emitted over 3000 of them. libutp
// remembers what it has already answered and stays quiet
// (utp_internal.cpp:2907-2945).
// rememberLingerAck keeps one acknowledgement for a connection that has just
// closed, so a FIN the peer retransmits is answered rather than reset.
//
// lingerAckTimeout is generous against libutp's FIN retransmissions, which
// back off from its 1000ms RTO floor: it covers the first few, which is where
// a lost acknowledgement actually costs something. Past that the peer gives up
// on its own and a RESET is the right answer anyway.
```

## Line 416 — func (*Socket).reAckLingering

```
// reAckLingering answers a FIN or a data packet retransmitted for a connection
// that has just closed, and reports whether it did.
//
// Both arrive for the same reason: the peer finished sending, we acknowledged
// everything including its FIN and tore down, and one of those acknowledgements
// was lost. The peer is retransmitting something we have already received and
// already acknowledged. Sending it that acknowledgement again ends the
// connection cleanly on both sides; sending a RESET tells it a completed
// transfer failed.
//
// This is what libutp does in return, for the same reason it can: it keeps the
// socket in CS_GOT_FIN until its own application closes, and a socket in that
// state acknowledges duplicates of data it has already taken.
//
// Data arriving *past* the stored acknowledgement -- beyond anything we took --
// is the other half of the same case: the peer writing after our FIN. libutp,
// still in CS_GOT_FIN, hands that to its application; we have none left to hand
// it to, so we drop it, but silently, which is what libutp's wire looks like.
// Either way the answer is never a RESET, and this returns true for both.
```

## Line 445 — func (*Socket).reAckLingering

```
// Data past the stored acknowledgement number is not a retransmission
		// of anything we took: it is the peer writing after our FIN, which is
		// what its half-close is for. We have nobody to give it to -- the
		// application closed -- but there is a difference between discarding
		// it and telling the peer its connection is broken.
		//
		// libutp discards. Its socket sits in CS_GOT_FIN and simply ignores
		// what arrives past the FIN; DEVIATIONS.md recorded us answering with
		// a RESET as a deliberate divergence, and measuring it showed what
		// that costs: a libutp peer writing after our FIN gets
		// UTP_ECONNRESET, three runs out of three. Silence matches the
		// reference and costs the peer nothing but a few retransmissions it
		// was going to make anyway.
		// The peer's FIN, numbered just past everything we took, is the end
		// of a stream we have whole: it is acknowledged, and the stored
		// acknowledgement moves to cover it. Silence here -- the rule for data
		// below -- left a peer that closed second retransmitting its FIN for
		// ever, each retransmission refreshing this entry; ending a closed
		// connection only once its FIN is acknowledged, as libutp does, made
		// that every such connection (review of 294cc2e).
		// Stamped as it is sent (freshlyStamped), on the wire clock: the
		// connection, and any clock its config gave it, has gone.
```

## Line 480 — func (*Socket).reAckLingering

```
// Refresh, as below: a peer still writing is still there, and
			// letting the entry expire under it would hand it the RESET this
			// exists to avoid. The cost is one entry, held only by a peer
			// that completed a handshake with us, which is the more expensive
			// half of the exchange.
```

## Line 492 — func (*Socket).reAckLingering

```
// Refresh the entry: a peer still retransmitting is still waiting.
```

## Line 508 — func (*Socket).maybeSendReset

```
// Already answered this one. Refresh its expiry and stay quiet, as
		// libutp does at utp_internal.cpp:2918.
```

## Line 514 — func (*Socket).maybeSendReset

```
// Past this point libutp stops answering entirely rather than let the
		// table grow without bound (utp_internal.cpp:2928).
```

## Line 526 — func (*Socket).maybeSendReset

```
// Fields match libutp's send_rst (utp_internal.cpp:846-863) exactly:
	//
	//   - ack_nr is the sequence number of the packet being rejected, so the
	//     peer can tell which of its packets drew the reset. We previously
	//     left it at zero, which named no packet at all.
	//   - seq_nr is random, so an off-path attacker cannot predict it.
	//   - windowsize is zero: there is no connection, so there is no receive
	//     window to advertise. We previously advertised 100000.
	//   - the timestamp fields are left at zero. libutp zeroes the whole
	//     header and never fills them in for a reset.
```

## Line 543 — func (*Socket).ProcessICMPFragmentation

```
// ProcessICMPFragmentation reports an ICMP "fragmentation needed" message
// (IPv4 type 3 code 4, or ICMPv6 "packet too big") for a datagram this socket
// sent, so the path-MTU search can take the router's word for it instead of
// discovering the same limit by losing a probe.
//
// quoted is the *uTP datagram the ICMP message quoted* -- the original UDP
// payload -- not the ICMP packet and not the IP or UDP headers around it.
// peer is the address that datagram was sent to. nextHopMTU is the router's
// figure, which may be zero: older routers do not supply one, and the search
// falls back to halving the gap when it is missing or implausible.
//
// It reports whether the quoted bytes were recognised as a uTP packet
// belonging to a live connection, matching what libutp's return value means
// (utp_internal.cpp:3071-3072).
//
// This library does not open a raw socket to collect ICMP itself, and neither
// does libutp: on Linux the usual source is IP_RECVERR on the UDP socket, and
// feeding it in is the embedder's job.
```

## Line 568 — func (*Socket).ProcessICMPError

```
// ProcessICMPError reports an ICMP message that should tear a connection
// down: destination unreachable other than fragmentation-needed, time
// exceeded, parameter problem. The connection ends with ErrConnRefused if it
// had only sent its SYN and ErrReset otherwise, which is libutp's
// UTP_ECONNREFUSED / UTP_ECONNRESET distinction (utp_internal.cpp:3122).
//
// quoted and peer mean what they mean for ProcessICMPFragmentation, and the
// return value likewise. Deciding which ICMP messages are fatal is the
// caller's: libutp draws that line in its callers too.
```

## Line 581 — func (*Socket).deliverICMP

```
// deliverICMP is libutp's parse_icmp_payload (utp_internal.cpp:3019-3067)
// followed by the delivery its two callers share.
```

## Line 588 — func (*Socket).deliverICMP

```
// "ICMP packets are only required to quote the first 8 bytes of the
	// layer4 payload. The UDP payload is 8 bytes, and the UTP header is
	// another 20 bytes. So, in order to find the entire UTP header, we need
	// the ICMP packet to quote 28 bytes." -- and then libutp checks for 20,
	// because by this point the UDP header is already off (:3032-3041).
```

## Line 597 — func (*Socket).deliverICMP

```
// libutp reads the version and connection id straight out of the quoted
	// bytes and rejects anything that is not version 1 (:3043-3052). Its
	// UTP_Version also requires a known packet type and a first extension
	// below 3, which is exactly what this decoder checks. Only the fixed
	// header is parsed: a router that quoted 20 bytes has cut the extensions
	// off, and requiring them would throw away the report.
```

## Line 626 — func (*Socket).deliverICMP

```
// The connection's queue is full. Dropping the report costs a probe,
		// not correctness: the fragmentation case is rediscovered by the lost
		// probe and the error case by the retransmission timeout. Reported as
		// unrecognised would be wrong -- the connection was found.
```

## Line 638 — func (*Socket).getConnStreamForQuotedId

```
// recv == id (the quoted packet is our SYN), either role
```

## Line 641 — func (*Socket).getConnStreamForQuotedId

```
// recv == id+1 && send == id: we accepted this connection
```

## Line 643 — func (*Socket).getConnStreamForQuotedId

```
// recv == id-1 && send == id: we initiated it
```
