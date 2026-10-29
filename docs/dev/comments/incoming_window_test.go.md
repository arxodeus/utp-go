# incoming_window_test.go

Every comment in `incoming_window_test.go` at e56c9aa, verbatim.

## Line 8 — func incomingWindowConn

```
// incomingWindowConn is a connected acceptor with one packet outstanding.
```

## Line 27 — func TestDiscardedPacketDoesNotSetThePeerWindow

```
// A packet libutp discards does not change what we think the peer's window
// is. libutp reads the window only after the acknowledgement-number and
// reorder-window checks (utp_internal.cpp:2144); this used to take it from
// every packet, first thing, so a forged packet with a bad acknowledgement
// could close the window.
```

## Line 35 — func TestDiscardedPacketDoesNotSetThePeerWindow

```
// Acknowledges a packet never sent: libutp drops it (:1794-1807).
```

## Line 49 — func TestDiscardedPacketDoesNotSetThePeerWindow

```
// The same window on a packet that passes is taken.
```

## Line 58 — func TestZeroWindowProbeDeadlineRestartsOnEachZeroWindow

```
// Every packet reporting a zero window restarts the probe's deadline: libutp
// sets `zerowindow_time = current_ms + 15000` on each (utp_internal.cpp:
// 2148-2151), so the probe goes 15 seconds after the last, not the first.
```

## Line 78 — func TestFinWaitsForRoomInTheWindow

```
// A FIN waits for room in the window like any packet. libutp queues it in its
// outgoing buffer and sends it only while !is_full() (utp_internal.cpp:
// 931-985). This library sent it the moment the send buffer was empty; with
// more in flight than a loss had just left the window, the controller refused
// even its zero bytes and transmit panicked. Found by a two-way transfer at 5%
// loss, one run in six.
```

## Line 102 — func TestFinWaitsForRoomInTheWindow

```
// A loss halves the window below what is in flight.
```

## Line 113 — func TestFinWaitsForRoomInTheWindow

```
// The window reopens; the next pass sends it.
```

## Line 121 — func TestAcknowledgementOnADataPacketWakesTheWriter

```
// An acknowledgement wakes the writer whichever packet carries it. libutp
// makes the socket writable after any incoming packet that leaves the window
// below full (utp_internal.cpp:2302-2308). This woke only for ST_STATE, so on
// a connection carrying data both ways -- where the peer's acknowledgements
// ride on its data packets -- the window opened and nothing used it until a
// timer ran: about 36 s instead of 5 for 1 MB each way at 5% loss, in 3 runs
// of 15.
```

## Line 133 — func TestAcknowledgementOnADataPacketWakesTheWriter

```
// Clear any wake left over from setting up.
```

## Line 150 — func TestOutstandingPacketsAreCappedAt1023

```
// No more than 1023 packets in flight, however small they are. libutp's
// is_full refuses at `cur_window_packets >= OUTGOING_BUFFER_MAX_SIZE - 1`
// before it counts bytes (utp_internal.cpp:939-947). There was no packet
// bound here at all.
```

## Line 157 — func TestOutstandingPacketsAreCappedAt1023

```
// Room for three whole packets, so that none is a short tail the
		// Nagle rule would hold back.
```

## Line 177 — func TestClosedConnectionAnswersEachWriteOnce

```
// A closed connection answers each queued write once, however many passes
// look at it. It used to leave the queue as it was, so the second pass sent
// every writer a second result; a result channel holds one, and that send
// blocked for ever -- with the connection's lock held, and the socket's
// reader, which serves every connection, waiting behind it.
```
