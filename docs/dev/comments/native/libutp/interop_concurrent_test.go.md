# native/libutp/interop_concurrent_test.go

Every comment in `native/libutp/interop_concurrent_test.go` at e56c9aa, verbatim.

## Line 1 — package libutp_test

```
//go:build cgo
```

## Line 20

```
// Interop with more than one libutp peer at a time.
//
// The M7 gate proves a single connection completes in each direction. That is
// not the shape a BitTorrent client runs: it holds tens of connections on one
// UDP port, all live at once, and every packet arriving on that port has to be
// routed to the right one by connection id alone. A demultiplexing bug, an
// accept that starves under inbound load, or state shared between connections
// that should not be, shows up here and nowhere in the single-connection gate.
//
// Each peer gets a payload stamped with its own index, so a byte delivered to
// the wrong connection is a failure with a name rather than a length mismatch.
```

## Line 37 — func stampedPayload

```
// stampedPayload builds a payload no other index could produce: every 8-byte
// word carries the index, so a fragment delivered to the wrong stream names
// the stream it came from.
```

## Line 48 — func payloadIndex

```
// payloadIndex recovers the index a payload was stamped with.
```

## Line 56 — func TestInteropConcurrentLibutpInitiators

```
// libutp initiating, all at once, into one socket of ours.
//
// This is the direction a seeding client sees: many peers arrive on one port
// and every one of them has to be accepted and kept apart.
```

## Line 75 — func TestInteropConcurrentLibutpInitiators

```
// Accept loop: it cannot know which peer it is answering, so it decides
	// from the payload.
```

## Line 110 — func TestInteropConcurrentLibutpInitiators

```
// Let the accepts register before any SYN arrives; an accept that has not
	// been posted yet is a different case, covered elsewhere.
```

## Line 196 — func TestInteropConcurrentGoInitiators

```
// The other direction: one socket of ours initiating to many libutp peers at
// once. This is the leeching side, and it exercises our connection-id
// allocation and send path under concurrency rather than our accept path.
```

## Line 228 — func TestInteropConcurrentGoInitiators

```
// Distinct connection ids per peer: the pair is what routes a
			// packet back to its connection, and reusing one across peers is
			// exactly the allocation bug this looks for.
```

## Line 280 — func describeStamps

```
// describeStamps reports what a payload actually contains, word by word, so a
// mismatch says which streams contributed and where.
```
