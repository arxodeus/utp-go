# rest_after_ack_test.go

Every comment in `rest_after_ack_test.go` at e56c9aa, verbatim.

## Line 9 — func TestSenderRestsOnceEverythingIsAcknowledged

```
// Once everything a sender sent is acknowledged, no loss probe stays armed,
// and the connection rests without waiting for a timer. A probe left armed
// fired to find nothing outstanding, and until it did the connection could
// not rest: about 10 ms here, and past 400 ms on Windows CI, failing
// TestRestingConnectionIdleTimeout. Stopping it alone left a sender whose
// last acknowledgement the socket's reader handled with no reason to wake
// its loop at all, and it never rested.
```

## Line 42 — func TestSenderRestsOnceEverythingIsAcknowledged

```
// Promptly: without the wake it took about a second, until an
	// unrelated timer happened to run a pass.
```
