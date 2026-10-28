# utpnet/packetconn_test.go

Every comment in `utpnet/packetconn_test.go` at e56c9aa, verbatim.

## Line 11

```
// The net.PacketConn side of a Socket, held to its contract.
//
// This is not a decorative interface. A BitTorrent client runs its DHT on the
// same UDP port as its uTP connections, and ReadFrom/WriteTo are how it does
// that: every DHT query and reply the client sends passes through here. The
// uTP side of the socket has the standard library's own conformance suite
// behind it now (integration/nettest); this side had four tests and no
// contract.
```

## Line 20 — func goroutinesSettle

```
// goroutinesSettle returns the goroutine count once it has stopped moving, so
// a count is not taken while an unrelated goroutine is still winding down.
```

## Line 36 — func TestSharedPortDoesNotLeakGoroutines

```
// Sending and receiving on the shared port must not accumulate goroutines.
//
// Each ReadFrom and WriteTo asked the deadline for a timer channel, and the
// deadline started a goroutine to serve it. With no deadline set -- which is
// the normal case, and the only case for a DHT -- that goroutine parks until
// the deadline is next *changed*, which for most callers is never. WriteTo
// discarded the channel outright and leaked one per call regardless.
//
// A client doing a few hundred DHT messages a second accumulates a few hundred
// parked goroutines a second, for the life of the process. Nothing here would
// have noticed: every existing test sends a handful of datagrams.
//
// Two details of the measurement matter, and both were got wrong first.
// Sending in bulk and draining afterwards loses datagrams, because the
// passthrough queue is bounded and a UDP socket is entitled to drop -- so the
// send and the read are interleaved, each read satisfied by the datagram just
// sent. And no deadline is set anywhere in this test, because SetReadDeadline
// releases every goroutine parked on the old deadline: a drain loop bounded by
// a deadline would free the very leak it was there to count.
```

## Line 60 — func TestSharedPortDoesNotLeakGoroutines

```
// Warm up: the first calls create the socket's own machinery, which is not
	// what is being counted.
```

## Line 85 — func TestSharedPortDoesNotLeakGoroutines

```
// A handful either way is scheduling noise. Hundreds is the leak.
```

## Line 95 — func TestPacketConnPastDeadline

```
// A deadline already in the past must fail both directions immediately, and
// must keep failing until it is moved.
```

## Line 115 — func TestPacketConnPastDeadline

```
// Clearing the deadline puts both back in service.
```

## Line 127 — func TestPacketConnDeadlineMovesUnderABlockedRead

```
// A deadline moved while ReadFrom is blocked must take effect, in both
// directions: bringing it forward times the call out, and pushing it back
// keeps the call waiting.
```

## Line 144 — func TestPacketConnDeadlineMovesUnderABlockedRead

```
// Let the read block on the hour-long deadline.
```

## Line 152 — func TestPacketConnDeadlineMovesUnderABlockedRead

```
// Bring it into the past; the blocked read must notice.
```

## Line 166 — func TestPacketConnCloseUnblocksAndStaysClosed

```
// Close must unblock a ReadFrom that is already waiting, and every call after
// it must report the socket is closed rather than block or panic.
```

## Line 207 — func TestConnIODoesNotLeakGoroutines

```
// The connection side has its own deadline path, and it must not leak either.
//
// Conn.Read and Conn.Write go through withDeadline/deadline.context rather
// than deadline.wait, which also starts a goroutine per call. That one is
// cancelled by the caller on every path, so it should be clean -- but it is
// the same class of defect as the one above, in the code next door, so it is
// checked rather than assumed.
```

## Line 242 — func TestConnIODoesNotLeakGoroutines

```
// Warm up, then measure. No deadline is set, for the same reason as above.
```
