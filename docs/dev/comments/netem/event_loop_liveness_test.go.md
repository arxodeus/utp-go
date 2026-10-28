# netem/event_loop_liveness_test.go

Every comment in `netem/event_loop_liveness_test.go` at e56c9aa, verbatim.

## Line 12 — func TestEventLoopRunsWhileReaderIsStalled

```
// A connection's event loop must keep running while its application is not
// reading.
//
// This is the defect's general form; TestCloseWithoutReadingDoesNotHang is one
// consequence of it. libutp cannot reach this state: utp_call_on_read hands the
// embedder its bytes and returns, and a slow application closes the advertised
// receive window rather than stopping the protocol.
//
// Liveness is measured directly rather than inferred from the peer's traffic.
// The metrics callback runs on the event-loop goroutine (see sampleMetrics),
// and a short KeepAliveInterval guarantees the loop wakes on its own even with
// nothing arriving, so the callback firing *is* the loop running. An earlier
// version of this test watched the return path for packets instead, which
// depends on the peer choosing to probe the closed window, and it was flaky in
// both directions.
```

## Line 50 — func TestEventLoopRunsWhileReaderIsStalled

```
// Every sample is one pass of the receiver's event loop.
```

## Line 72 — func TestEventLoopRunsWhileReaderIsStalled

```
// Never read. Hold the stream open so the connection stays up.
```

## Line 91 — func TestEventLoopRunsWhileReaderIsStalled

```
// Let the sender fill the receive buffer and the read queue behind it.
```

## Line 100 — func TestEventLoopRunsWhileReaderIsStalled

```
// With a 100ms keep-alive the loop should wake ~20 times in 2 seconds. One
	// is enough to prove it is not blocked.
```
