# dead_conn_paths_test.go

Every comment in `dead_conn_paths_test.go` at e56c9aa, verbatim.

## Line 10

```
// The dead-connection rules -- a write to a connection that has ended fails at
// once with the reason, and a read reports the reason rather than a clean end
// -- were tested only for a connection the idle timeout had closed. These are
// the other two ways a connection ends without its application: the peer
// resets it, and the sender gives up after consecutive retransmission
// timeouts (utp_internal.cpp:1191).
```

## Line 17 — func deadAccepted

```
// deadAccepted returns an accepted stream that has read one packet of data,
// after kill has ended its connection.
```

## Line 23 — func deadAccepted

```
// The loop leaves the clock as it returns, a moment before the stream
	// records that it has: quiet is not yet ended.
```

## Line 33 — func liveAccepted

```
// liveAccepted returns an accepted stream, on a virtual clock and a scripted
// peer, that has read one packet of data.
```

## Line 100 — func deadByGivingUp

```
// Data the peer never acknowledges: the sender retransmits, backs
		// off, and gives up.
```

## Line 152 — func TestDeadConnectionReadsReportTheReason

```
// Twice per run, with the socket's context cancelled first, so
			// that Read has the cancelled context and the closed queue to
			// choose between.
```
