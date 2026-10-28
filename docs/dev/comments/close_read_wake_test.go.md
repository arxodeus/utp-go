# close_read_wake_test.go

Every comment in `close_read_wake_test.go` at e56c9aa, verbatim.

## Line 10 — func TestCloseReadWakesABlockedRead

```
// CloseRead wakes a Read already waiting on an open, silent stream, as
// net.TCPConn's does. The flag was checked only before the wait, so such a
// Read waited through it for as long as the peer stayed quiet. From the
// release review of 9c44c4f (R6).
```

## Line 29 — func TestCloseReadWakesABlockedRead

```
// both waiting
```

## Line 45 — func TestCloseReadAppliesWithoutItsEvent

```
// A CloseRead whose event was dropped -- the event queue full -- still shuts
// the read side in the connection: data arriving afterwards is acknowledged
// and discarded, not buffered for a reader that will refuse it.
```

## Line 53 — func TestCloseReadAppliesWithoutItsEvent

```
// What CloseRead does when the event cannot be queued: the flag, and a
	// wake, but no streamCloseRead.
```
