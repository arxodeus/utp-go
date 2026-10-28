# icmp_test.go

Every comment in `icmp_test.go` at e56c9aa, verbatim.

## Line 18 — func quotedDatagram

```
// quotedDatagram builds the bytes an ICMP message would quote: a uTP datagram
// this side sent, carrying connId in its header. Routers quote the front of
// the offending datagram, so 20 bytes of header is all a caller can rely on
// having.
```

## Line 29 — func quotedDatagram

```
// Quote only the header, which is the least a router will send.
```

## Line 33 — type icmpPair

```
// icmpPair brings up a connected pair over loopback UDP and returns the
// initiator's socket, its stream, the acceptor's stream, and the initiator's
// connection id.
```

## Line 64 — func newICMPPair

```
// The observer runs on the connection's own goroutine, so it must not
	// block; a buffered channel with the oldest sample dropped keeps the
	// latest without ever stalling the connection.
```

## Line 119 — func latestCeiling

```
// latestCeiling waits for a metrics sample whose MTU ceiling satisfies want,
// and reports the last ceiling it saw either way.
```

## Line 138 — func TestProcessICMPFragmentationLowersTheCeiling

```
// A router's fragmentation-needed report must reach the connection that sent
// the offending datagram and lower its path-MTU ceiling. libutp's
// utp_process_icmp_fragmentation.
```

## Line 147 — func TestProcessICMPFragmentationLowersTheCeiling

```
// Establish what the ceiling was before the report, so a test that
	// asserts a drop cannot pass on a ceiling that was already low.
```

## Line 165 — func TestProcessICMPFragmentationLowersTheCeiling

```
// Keep the connection producing samples: metrics are sampled by the
	// event loop, which needs something to do.
```

## Line 192 — func TestProcessICMPErrorResetsAnEstablishedConnection

```
// An ICMP error for an established connection ends it, and the application
// finds out. libutp's utp_process_icmp_error, UTP_ECONNRESET half.
```

## Line 222 — func TestProcessICMPErrorRefusesAPendingConnect

```
// An ICMP error while only the SYN has been sent means nothing is listening,
// which is a different thing to tell the caller than a reset. libutp:
// `(conn->state == CS_SYN_SENT) ? UTP_ECONNREFUSED : UTP_ECONNRESET`.
//
// Without the ICMP path the caller waits out three SYN attempts and is told
// it timed out, so the test also asserts the timing: the answer arrives long
// before the connection attempts would have run out.
```

## Line 240 — func TestProcessICMPErrorRefusesAPendingConnect

```
// A plain UDP socket that is bound and silent: the SYN reaches it and
	// nothing ever answers, which is the state a real connect to a closed
	// port sits in until ICMP arrives.
```

## Line 260 — func TestProcessICMPErrorRefusesAPendingConnect

```
// A SYN carries the sender's own receive id, so that is what the router
	// would quote back.
```

## Line 264 — func TestProcessICMPErrorRefusesAPendingConnect

```
// Wait until the connection exists to be found, then report the error.
	// Retrying keeps the test from depending on how fast Connect gets going.
```

## Line 284 — func TestProcessICMPErrorRefusesAPendingConnect

```
// Three SYN attempts at the default timeout is a good deal longer
		// than this; if the answer took that long the ICMP path did nothing.
```

## Line 295 — func TestProcessICMPIgnoresWhatItCannotMatch

```
// What must be ignored. Each of these would otherwise be a way to tear down
// or shrink somebody else's connection from off the path, since ICMP is
// trivially forged.
```

## Line 343 — func TestProcessICMPIgnoresWhatItCannotMatch

```
// And after all that the connection is still alive and still carrying
	// data -- otherwise "ignored" would be indistinguishable from "acted on
	// and the test did not look".
```

## Line 358 — func TestProcessICMPMatchesAnAcceptedConnection

```
// The acceptor's side of the lookup: a datagram quoted from a connection this
// socket accepted has to match too. libutp's second lookup,
// `Lookup(UTPSocketKey(addr, id + 1)) && conn_id_send == id`.
```

## Line 367 — func TestProcessICMPMatchesAnAcceptedConnection

```
// sb accepted, so its send id is cidB.Send, which is cidA.Recv.
```
