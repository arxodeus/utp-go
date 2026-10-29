# conn_send.go

Every comment in `conn_send.go` at e56c9aa, verbatim.

## Line 16 — type queuedWrite

```
// mu orders the connection taking bytes from this write against its
	// writer giving up on it. written is the loop's, read by the writer
	// under mu; canceled is the writer's, read by the loop under mu.
```

## Line 23 — func (*queuedWrite).cancel

```
// cancel withdraws what the connection has not yet taken, and reports what
// it has: the count of bytes already in the send buffer, which will be sent,
// or the result, if the whole write was taken before the writer gave up.
//
// A write used to return 0 on cancellation while the connection went on
// taking the rest of it, so its bytes were sent after the caller had been
// told none were -- and utpnet, which retries a write whose deadline moved
// and that reported nothing written, sent them twice. From the release
// review of 9c44c4f (R1).
```

## Line 44 — func (*connection).emit

```
// emit hands a packet to the socket and records when this connection last
// spoke.
//
// Every outbound packet goes through here so that the keep-alive can tell
// whether the connection has been silent, which is libutp's
// `last_sent_packet` (utp_internal.cpp:1272).
```

## Line 54 — func (*connection).emitPacket

```
// emitPacket sends a packet, through the socket. dontFragment marks
// it as an MTU probe; see DontFragmentWriter.
```

## Line 65 — func (*connection).emitPacket

```
// Kept for the socket to repeat after this connection is gone.
```

## Line 69 — func (*connection).emitPacket

```
// Straight to the wire, on this goroutine: nothing is in flight
		// between deciding to send and sending, so a virtual clock has
		// nothing to wait for. The event is done with when out returns, so
		// one serves every packet.
```

## Line 82 — func (*connection).emitPacket

```
// A virtual clock must not consider the system quiet while this packet is
	// queued but not yet taken by whoever reads socketEvents. See
	// idleBarrier.NoteHandoff.
```

## Line 91 — func (*connection).keepAlivePacket

```
// keepAlivePacket is a STATE acknowledging one less than we actually have.
//
// libutp: `ack_nr--; send_ack(); ack_nr++` (utp_internal.cpp:834-844). Acking
// one behind makes the packet look like a stale acknowledgement to the peer,
// which answers it -- so the exchange proves both directions still work
// without consuming a sequence number or delivering anything.
```

## Line 102 — func (*connection).keepAlivePacket

```
// wrapping
```

## Line 103 — func (*connection).keepAlivePacket

```
// The selective ack must be read against the decremented number too.
	// libutp decrements ack_nr before send_ack builds the mask
	// (utp_internal.cpp:836-843, :804-808), so its bit i is ack_nr + 1 + i
	// of the real acknowledgement; ours was built against the real one, bit i
	// being ack_nr + 2 + i. Shift it up one: the new bit 0 is ack_nr + 1, the
	// packet a selective ack exists because we lack, and the top bit falls
	// out of the window as it does in libutp. Left unshifted, the peer read
	// every bit one packet early and could free a packet we never received.
	// TestKeepAliveSelectiveAckMatchesItsAckNumber.
```

## Line 121 — func (*connection).keepAliveIntervalOrDefault

```
// keepAliveIntervalOrDefault is the configured interval, or libutp's.
```

## Line 129 — func (*connection).zeroWindowProbeIntervalOrDefault

```
// zeroWindowProbeIntervalOrDefault is the configured interval, or libutp's.
```

## Line 137 — func (*connection).effectivePeerWindow

```
// effectivePeerWindow is how much the peer says it can accept, with the
// zero-window probe applied.
//
// libutp does this by overwriting max_window_user with PACKET_SIZE once
// zerowindow_time has passed (utp_internal.cpp:1142-1145); the next
// acknowledgement overwrites it again with whatever the peer reports. The
// effect is the same: exactly one packet gets through a closed window per
// interval, and the peer's answer to it carries a fresh window.
```

## Line 158 — func (*connection).processWrites

```
// LEDBAT++'s slowdowns are driven by the clock, not by acks. Without
		// this, a connection whose application goes quiet mid-slowdown would
		// stay pinned at two packets until an ack happened to arrive.
```

## Line 171 — func (*connection).processWrites

```
// A writer still waiting has not had all its bytes taken, so it is
		// told why -- with ErrNotConnected for a connection that closed
		// cleanly, which has no error of its own -- and how many were taken
		// before the end. Both used to be wrong: a nil error with nothing
		// written, which an io.Writer may not return, and 0 for a writer
		// part of whose bytes were already in the send buffer.
```

## Line 181 — func (*connection).processWrites

```
// Each writer is answered once. The queue used to be left as it was,
		// so the next call sent to every writer again, and a result channel
		// holds one: the second send blocked for ever, with the connection's
		// lock held -- and the socket's reader, which serves every connection,
		// waiting behind it.
```

## Line 194 — func (*connection).processWrites

```
// Compose data packets.
	//
	// libutp queues a write as packets of up to packet_size and flush_packets
	// sends the next one while !is_full() (utp_internal.cpp:3197-3212,
	// :963-985). is_full is called there with no argument, so it charges a
	// whole packet_size whatever the queued packet actually holds (:933-934),
	// and refuses when cur_window + packet_size > min(max_window, opt_sndbuf,
	// max_window_user) (:936, :956). Two things follow:
	//
	//   - Bytes in flight count against the peer's window, not only against
	//     the congestion window. The peer's advertisement is how much it can
	//     take beyond what it has already acknowledged; everything we have
	//     sent since is already spending it.
	//   - Nothing is sent into less than a full packet of room, and a packet
	//     is never cut down to fit what room there is.
	//
	// opt_sndbuf has no counterpart: our send buffer bounds what the
	// application may queue, not what may be in flight. History: HISTORY.md,
	// "processWrites: composing packets".
```

## Line 215 — func (*connection).processWrites

```
// And never more than maxOutstandingPackets in flight, however small
	// they are. libutp's is_full refuses at `cur_window_packets >=
	// OUTGOING_BUFFER_MAX_SIZE - 1` before it looks at bytes
	// (utp_internal.cpp:939-947), so the flush stops there, resends
	// included. There was no such bound here: a run of small writes into a
	// large window could put more packets in flight than a libutp receiver
	// will reorder (1024, :1890) -- and, past 32768, more than 16-bit
	// sequence numbers can order at all.
```

## Line 225 — func (*connection).processWrites

```
// Packets a retransmission timeout gave up as lost go first, oldest
	// first, under the same rule as new data. libutp's flush_packets walks
	// its outgoing buffer from the oldest packet and sends each one that is
	// unsent or need_resend while !is_full() (utp_internal.cpp:970-985), so
	// a resend always precedes the new data behind it.
```

## Line 238 — func (*connection).processWrites

```
// No room even for what is owed; new data waits behind it.
```

## Line 245 — func (*connection).processWrites

```
// Taken, not shared: nothing here should come back into processWrites,
	// but if it did it would start a slice of its own.
```

## Line 251 — func (*connection).processWrites

```
// libutp's `is_full` marks the connection application-limited or not
	// every time it considers sending a packet (utp_internal.cpp:945, :957),
	// and the congestion controller refuses to grow a window the application
	// never fills (:1681-1686). The equivalent signal here is "we had data
	// and no room for it".
```

## Line 265 — func (*connection).processWrites

```
// libutp's Nagle rule (flush_packets, utp_internal.cpp:974-982): the
		// last packet waits while it is short of a full one and anything is
		// ahead of it in the window -- sent and unacknowledged, or composed in
		// this pass -- and what is written next joins it. The acknowledgement
		// that empties the window releases it, as libutp's "flush Nagle" does
		// (:2246-2252): every acknowledgement brings this loop round again.
```

## Line 283 — func (*connection).processWrites

```
// Write pending data to send buffer
```

## Line 287 — func (*connection).processWrites

```
// A write its caller gave up on is dropped whether or not there is
		// room, so that it does not hold a FIN behind it.
```

## Line 301 — func (*connection).processWrites

```
// The queued data is the stream's own copy of what was written
		// (writeQueued), so the send buffer takes it as it is.
```

## Line 323 — func (*connection).processWrites

```
// transmit data packets
```

## Line 329 — func (*connection).processWrites

```
// No selective ack: see statePacket.
```

## Line 339 — func (*connection).processWrites

```
// wrapping add in uint16
```

## Line 348 — func (*connection).onWrite

```
// There are 0 bytes written so far
```

## Line 359 — func (*connection).onWrite

```
// Our FIN is out: nothing more may be sent. libutp's
				// utp_writev refuses the same (`if (conn->fin_sent)
				// return 0`, utp_internal.cpp:3188). This answered Len 0
				// with no error, which an io.Writer may not do: a write
				// queued just before Close or CloseWrite reported success
				// and its bytes were never sent.
```

## Line 374 — func (*connection).onWrite

```
// A connection that closed cleanly has no error of its own, and a
		// write that sends nothing must still report one.
```

## Line 386 — const maxOutstandingPackets

```
// maxOutstandingPackets is how many packets may be in flight at once:
// libutp's `OUTGOING_BUFFER_MAX_SIZE - 1` (utp_internal.cpp:52, applied in
// is_full at :939). See processWrites.
```

## Line 391 — const defaultZeroWindowProbeInterval

```
// defaultZeroWindowProbeInterval is how long libutp tolerates a closed peer
// window before forcing a packet through: "Reset max_window_user to 1 every 15
// seconds" (utp_internal.cpp:2150-2151).
```

## Line 396 — const defaultKeepAliveInterval

```
// defaultKeepAliveInterval is how long libutp lets an established connection
// stay silent before sending a keep-alive: `#define KEEPALIVE_INTERVAL 29000`
// (utp_internal.cpp:74), applied at :1271-1274.
//
// 29 seconds is chosen to sit under the 30-second UDP mapping timeout common
// in NATs. A connection that goes quiet for longer than that loses its
// mapping and cannot be reached again from the outside.
```

## Line 416 — func (*connection).transmit

```
// transmit sends a packet and arms its retransmission timer.
//
// firstTransmission distinguishes a packet going out for the first time from
// one being resent. Only the former can serve as an MTU probe: libutp excludes
// retransmissions because an oversized packet being resent needs to fragment
// just to get through, which is the opposite of what a probe is for
// (utp_internal.cpp:900-904).
```

## Line 425 — func (*connection).transmit

```
// Ended by a failure below, with packets of the same pass still to go.
```

## Line 450 — func (*connection).transmit

```
// A probe sent again goes without the don't-fragment bit, and
		// fragmented if it has to be, so its acknowledgement no longer says
		// the size fits: forget the probe and let the search send another.
		// libutp's only resends of a probe come after it has been judged
		// or cleared -- the third duplicate acknowledgement
		// (utp_internal.cpp:1927-1938), the timeout (:1152-1167) -- and so
		// did ours until the loss probe, which resends the oldest packet
		// outstanding and can pick the probe. Its fragmented copy was
		// acknowledged and the search settled on 1402 bytes for a path that
		// had just dropped a 1402-byte probe
		// (netem.TestDontFragmentBringsTheSearchWithinThePath, under load).
```

## Line 466 — func (*connection).transmit

```
// Use this packet as an MTU probe if the search wants one.
	//
	// libutp decides the same thing in send_packet (utp_internal.cpp:906-925)
	// and sends the probe with fragmentation disabled, by passing
	// UTP_UDP_DONTFRAG to its embedder's sendto callback (:925-929). This
	// carries the same bit the same way: the flag travels with the datagram to
	// the socket, which asks the Conn to honour it if it can.
	//
	// A Conn that cannot -- the libutp driver, an emulated network that does
	// not model fragmentation, an operating system with no such socket option
	// -- sends it normally. On IPv4 a router may then fragment an oversized
	// probe rather than drop it, so the probe is acknowledged, the floor rises,
	// and the search settles on a size that works only because every packet at
	// it is being fragmented. History: HISTORY.md, "transmit: the MTU
	// probe".
```

## Line 492 — func (*connection).transmit

```
// This connection's accounts no longer agree with what it is
		// sending. It ends, with the reason; nothing else does. See
		// sentPackets.OnTransmit.
```

## Line 506 — func (*connection).transmit

```
// A packet carrying the current acknowledgement -- and the current
	// selective ack, which every packet this connection builds carries --
	// says everything a deferred ST_STATE would have, so that one is no
	// longer owed. libutp takes the socket off its ack list whenever it sends
	// anything (send_data, utp_internal.cpp:768).
	//
	// It rarely applies here: data received and data sent mostly fall in
	// different event-loop passes. Measured in a two-way transfer against
	// libutp, before socket reads were batched, pure acknowledgements per data
	// packet were 0.90 before and after it (DEVIATIONS.md, "Acknowledgements:
	// one per read, fewer when they would crowd the way back").
```
