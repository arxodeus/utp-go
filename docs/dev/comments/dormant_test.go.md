# dormant_test.go

Every comment in `dormant_test.go` at e56c9aa, verbatim.

## Line 13 — func restingPair

```
// restingPair is two connected streams on two loopback sockets.
```

## Line 45 — func exchange

```
// exchange writes n bytes from one stream and reads them at the other.
```

## Line 61 — func awaitRest

```
// awaitRest waits for both streams' connections to have let their loops go.
```

## Line 75 — func TestIdleConnectionRests

```
// An established connection with nothing to do gives its goroutine back, and
// comes back for whatever needs it next: a write at either end, in turn.
```

## Line 86 — func TestIdleConnectionRests

```
// Woken by a write, from rest, at either end.
```

## Line 92 — func TestRestingConnectionIdleTimeout

```
// The idle timeout is kept while resting: the connection ends when it falls
// due, not at the next keep-alive 29 seconds on.
```

## Line 117 — func TestRestingConnectionEndsWithItsSocket

```
// Closing the socket ends a resting connection at once: there is no loop
// waiting on the context to notice.
```

## Line 127 — func TestRestingConnectionEndsWithItsSocket

```
// Well inside the second at which the connection's own idle-decay
	// deadline -- the retransmission timeout, at least libutp's 1s floor --
	// would wake it anyway.
```

## Line 134 — func TestRestingConnectionEndsWithItsContext

```
// Cancelling the context the socket was made with ends a resting connection
// at once, as it ended a parked one: there is no Close, so no event is queued,
// and no loop is waiting on the context to notice.
```

## Line 152 — func awaitEnded

```
// awaitEnded requires s to end within 300ms of what should end it at once.
// One that does not is waited for a while longer, to tell a slow end from a
// stuck one: how long it took is reported, and the goroutines if it is still
// running. Windows CI has missed the 300ms twice, and nothing here does.
```

## Line 174 — func TestRestingReceiverWokenByRead

```
// A receiver whose application stopped reading rests with the rest of the
// data held back, and the application's next read brings it back to hand
// that over: nothing else would, the sender having nothing left to send.
```

## Line 184 — func TestRestingReceiverWokenByRead

```
// more than the read queue holds
```

## Line 202 — func TestRestingReceiverWokenByRead

```
// Woken, and with bytes still held back it stays: a reader slower than
	// the network would otherwise have its connection started and rested
	// again for every read.
```

## Line 221 — func TestTimerArmedOnRestingConnectionFires

```
// A timer the socket's reader arms on a resting connection -- the held
// acknowledgement, when the return path is queued behind this receiver's own
// acknowledgements (ackEvery) -- starts the loop, which takes its fire. Left
// resting, the fire would wait in the timer's channel for the next thing to
// wake the loop, and the acknowledgement with it.
```

## Line 234 — func TestTimerArmedOnRestingConnectionFires

```
// Twice: the first arm makes the timer, which wakes the loop by its own
	// path (kickLoop); the second, after the loop has rested again, finds it
	// made.
```
