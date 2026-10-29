# scripted_conn_test.go

Every comment in `scripted_conn_test.go` at e56c9aa, verbatim.

## Line 3

```
// The scripted transport and helpers the conformance harness drives our side
// with. None of it needs libutp, so it builds without cgo, and so do the
// tests that use it on their own (zerowindow_test.go, dont_fragment_test.go).
// What compares against libutp is in conformance_harness_test.go, which does.
```

## Line 25

```
// --- a scripted transport for our implementation ----------------------------
```

## Line 31 — type scriptedConn

```
// scriptedConn is a Conn whose input is injected by the test and whose output
// is captured, so our socket can be driven exactly as the libutp driver is.
```

## Line 38 — type scriptedConn

```
// inMu guards the queue of injected packets. notify carries a single
	// wake-up for a reader parked with nothing to read.
```

## Line 57 — func (*scriptedConn).Queued

```
// Queued implements QueuedReader: injected packets not held back are what
// ReadFrom returns at once, as a real socket's queue is.
```

## Line 129 — func (*scriptedConn).pendingCount

```
// pendingCount is how many injected packets our socket has not yet read.
//
// The settle loops below wait for a quiet period, and a quiet period that
// starts before our event loop has even been handed the packet measures
// nothing: under load the whole window can pass with the packet still sitting
// in this queue. Draining is the precondition for the wait to mean anything.
```

## Line 141 — func (*scriptedConn).awaitDelivered

```
// awaitDelivered waits until our socket has read everything injected so far,
// or the limit passes. It reports whether the queue drained; a caller that
// injected nothing sees an immediate true.
```

## Line 161 — func (*scriptedConn).settle

```
// settle waits until our implementation has stopped emitting.
//
// libutp is synchronous, so its side needs nothing like this. Ours is
// goroutine-driven with real timers, so the only honest way to know it has
// finished reacting is to watch until it goes quiet. That asymmetry is why
// the timing comparison in this corpus is coarse -- see CONFORMANCE.md.
```

## Line 188

```
// --- pinning our random source ----------------------------------------------
```

## Line 190 — func pinRandom

```
// pinRandom fixes the sequence numbers our implementation chooses, so they can
// be compared against libutp's scripted ones. It restores the real source on
// cleanup.
```

## Line 221 — func TestRandomSourcePinnedWhileInUse

```
// Pinning the random source while something draws from it is safe. A socket's
// reader can still be answering a datagram with a reset after Close has
// returned, and the next test pinning the source raced it -- the race
// detector caught it in 2 of 15 runs of this package. For the race
// detector; without it this passes either way.
```
