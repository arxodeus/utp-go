# native/libutp/relay_test.go

Every comment in `native/libutp/relay_test.go` at e56c9aa, verbatim.

## Line 1 — package libutp_test

```
//go:build cgo
```

## Line 15

```
// A userspace impairment relay between two real UDP sockets.
//
// The adverse-conditions tests in netem damage packets inside an emulated
// network, where libutp is driven by a Go loop on a clock the loop sets. Here
// both implementations run as they would in production -- libutp on its own
// kernel socket, its own thread and the real clock, this library on a real
// utpnet socket -- and only the path between them is artificial.
//
// It is artificial in userspace because it has to be: this host's kernel is
// built without netem (CONFIG_NET_SCH_NETEM is not set) and has no tc, so
// loopback cannot be told to lose or reorder anything. Every datagram still
// leaves one kernel socket and arrives at another; the relay only decides
// which of them arrive, and when.
//
// Topology: libutp <-> relay.sideA ... relay.sideB <-> ours. Whatever arrives
// on sideA is forwarded from sideB to our socket, and whatever arrives on
// sideB is forwarded from sideA to libutp's, so each implementation sees the
// relay as its peer.
```

## Line 34 — type pathConfig

```
// pathConfig is one direction's impairment.
```

## Line 36 — type pathConfig

```
// one-way propagation
```

## Line 37 — type pathConfig

```
// uniform in [-Jitter, +Jitter] around Delay, as in netem
```

## Line 38 — type pathConfig

```
// bottleneck rate; zero means unlimited
```

## Line 39 — type pathConfig

```
// bottleneck queue; a datagram that does not fit is dropped
```

## Line 40 — type pathConfig

```
// independent random loss
```

## Line 41 — type pathConfig

```
// fraction held back by ReorderHold, so later ones overtake
```

## Line 45 — type pathStats

```
// pathStats counts what one direction did.
```

## Line 74 — type relayPath

```
// relayPath is one direction: datagrams read from in are written from out to
// dst after the impairment decides their fate.
```

## Line 85 — type relayPath

```
// when the bottleneck finishes serializing its queue
```

## Line 86 — type relayPath

```
// bytes waiting in the bottleneck queue
```

## Line 90 — type relayPath

```
// trace, when non-nil, records every datagram offered and whether it
	// was dropped, for diagnosing a transfer after the fact.
```

## Line 93 — type relayPath

```
// delivered, when non-nil, records every datagram as it is written to
	// its destination.
```

## Line 97 — type relayPath

```
// child and name, for a relay in its own process: whom to ask for this
	// path's counts, and which path this is.
```

## Line 103 — func parseRelayEvent

```
// parseRelayEvent reads the uTP header fields, and the selective ack if there
// is one, out of a datagram.
```

## Line 109 — func parseRelayEvent

```
// Walk the extension chain for a selective ack (type 1).
```

## Line 124 — type relayEvent

```
// relayEvent is one offered datagram, as the relay saw it.
```

## Line 127 — type relayEvent

```
// uTP packet type, the high nibble of the first byte
```

## Line 130 — type relayEvent

```
// advertised receive window
```

## Line 132 — type relayEvent

```
// Sack is the selective-ack bitmask, if the packet carries one: bit i of
	// the whole mask, least significant first, is ack_nr + 2 + i (BEP 29).
```

## Line 156 — func (*relayPath).offer

```
// Bytes still waiting to be serialized are the queue.
```

## Line 192 — const relaySpin

```
// relaySpin is how long before a datagram is due deliver stops sleeping and
// spins, so that it is written within microseconds of its time.
```

## Line 196 — func (*relayPath).deliver

```
// deliver writes out each datagram when it is due, until done closes.
```

## Line 215 — func (*relayPath).deliver

```
// Stamped before the write, not after: on loopback the datagram
			// is in the receiver's socket before WriteToUDP returns, and a
			// receiver quick enough could acknowledge it before an
			// after-the-write stamp. turnaround then skipped that
			// acknowledgement as earlier than the delivery and paired the
			// packet with the next one, a packet interval later.
```

## Line 229 — func (*relayPath).deliver

```
// A Go timer under a millisecond fires after about 1.1 ms when the
		// runtime is otherwise idle (epoll waits in whole milliseconds), which
		// would deliver a 20 Mb/s path's packets in pairs and a 100 Mb/s
		// path's in clumps of ten. Packets on a link arrive spaced, so the
		// last stretch before a datagram is due is spun instead.
```

## Line 269 — type relay

```
// relay joins libutp's socket to ours through two impaired paths.
```

## Line 271 — type relay

```
// sideA faces libutp, sideB faces us
```

## Line 276 — type relay

```
// child, when not nil, is the process this relay runs in: see
	// relayInOwnProcess. sideA, sideB and the paths' sockets are then the
	// child's, and the paths here only receive what it recorded.
```

## Line 282 — func newRelay

```
// newRelay starts a relay between libutp at libPort and our socket at goPort.
// toGo impairs what libutp sends; toLib what we send.
```

## Line 299 — func startRelay

```
// startRelay is newRelay in this process. hook, if not nil, sees the relay
// before it starts forwarding.
```

## Line 329 — func startRelay

```
// A datagram is taken as arriving when the kernel received it, not when
	// this goroutine woke to read it. On loopback that is the instant the
	// sender's sendto put it there -- when an acknowledgement left the
	// receiver, which is what TestAckTurnaround means to time. This
	// goroutine's own wake-up was in it before, and it is not the same for
	// both receivers: it shares the Go runtime with this library's receiver,
	// still busy finishing the pass that sent the acknowledgement, and not
	// with libutp's thread. Measured at the receivers themselves, kernel
	// arrival to the acknowledgement's sendto, the gap between the two was
	// 12-15us at the median where this relay reported 30.
```

## Line 364 — var onNewRelay

```
// onNewRelay, when set, sees every relay before it starts forwarding: a
// diagnostic can switch tracing on (relayPath.trace) and keep the relay.
```

## Line 368 — func (*relay).LibutpFacingPort

```
// LibutpFacingPort is where libutp should send: the relay's side facing it.
```

## Line 376 — func (*relay).GoFacingAddr

```
// GoFacingAddr is where our socket should send.
```

## Line 384 — func (*relay).Close

```
// Close stops the relay once everything in flight has been read, and returns
// when its goroutines have: what they recorded (relayPath.delivered, trace)
// may be read after it. Closing again does nothing.
```

## Line 396 — func (*relay).close

```
// Let what is already on its way in be read first. A test ends the
	// moment its receiver sees the end of the stream, and closing the
	// sockets then discards whatever they hold unread: an acknowledgement
	// the receiver sent microseconds before was lost from the trace, and
	// TestAckTurnaround counted its packets as never acknowledged -- in
	// go->go about one run in fifteen, every time the last data packet and
	// the FIN, read together, whose acknowledgement was the last thing sent.
	// Closing waits for 20 ms with nothing offered, a second at most.
```
