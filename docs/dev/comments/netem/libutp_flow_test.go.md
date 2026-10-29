# netem/libutp_flow_test.go

Every comment in `netem/libutp_flow_test.go` at e56c9aa, verbatim.

## Line 1 — package netem

```
//go:build cgo
```

## Line 20

```
// Real libutp, running as an endpoint on the emulated network.
//
// Everything in BENCHMARKS.md compares this library against a previous version
// of itself, and every congestion-control row in COMPATIBILITY.md is *cited* —
// code read against utp_internal.cpp and matched by hand — because libutp had
// never been run over the emulated network. This is what closes that gap: the
// same links, the same payload, the same measurements, with the reference
// implementation at one end.
//
// libutp.Driver is deliberately inert: no socket, no clock, no threads. That
// is what makes this possible. The loop below supplies all three — it hands
// the driver packets from a netem Endpoint, moves its clock to match the real
// one the rest of the harness runs on, and writes whatever it emits back to
// the link.
```

## Line 35 — const libutpTickInterval

```
// libutpTickInterval is how often the loop advances libutp's clock and calls
// utp_check_timeouts when no packet has arrived.
//
// libutp rate-limits itself internally to TIMEOUT_CHECK_INTERVAL, 500ms
// (utp_internal.cpp:37, :3284-3285), so calling more often costs a cgo call
// and changes nothing. It is this fine so that the *clock* is accurate: every
// timestamp libutp puts on a packet, and every delay sample it takes from one,
// is only as good as the last SetTime.
```

## Line 45 — type libutpRole

```
// libutpRole is which end of the transfer libutp plays.
```

## Line 53 — type libutpEndpoint

```
// libutpEndpoint drives a libutp.Driver over a netem Endpoint.
//
// The driver is not safe for concurrent use, so exactly one goroutine — run —
// ever touches it. Packets arrive through inbox from a reader goroutine, and
// results are read only after run has returned.
```

## Line 66 — type libutpEndpoint

```
// what a sender sends
```

## Line 67 — type libutpEndpoint

```
// how many bytes a receiver waits for
```

## Line 71 — type libutpEndpoint

```
// delivered is a receiver's bytes in hand over time, for the split
	// between flows sharing a link (SplitWhileBothRan).
```

## Line 75 — type libutpEndpoint

```
// linger keeps a receiver running after the last byte has arrived, until
	// its context ends, so that it is still there to acknowledge the FIN of a
	// libutp sender. This library's receiver keeps its socket open the same
	// way; a libutp receiver that stopped would leave the sender waiting.
```

## Line 83 — func newLibutpEndpoint

```
// newLibutpEndpoint pins libutp's random source to connSeed, which fixes the
// connection ids it uses: a libutp initiator takes recv_id = connSeed and
// send_id = connSeed + 1, so the other end must accept on the mirror of that.
```

## Line 87 — func newLibutpEndpoint

```
// libutp's clock is set to the same base this library stamps packets with,
	// time.Now().UnixMicro() (conn.go, nowMicros). It has to be: the two ends
	// exchange timestamps and each computes the other's queueing delay by
	// subtracting them, so a driver whose clock starts at zero hands libutp a
	// delay reading that is really the offset between two unrelated epochs.
```

## Line 108 — func (*libutpEndpoint).readLoop

```
// readLoop moves packets off the link into inbox. It is the only goroutine
// besides run, and it never touches the driver.
```

## Line 127 — func (*libutpEndpoint).run

```
// run drives the transfer to completion and returns how long it took, measured
// from the moment before the SYN is emitted to the moment the last byte is in
// hand.
```

## Line 139 — func (*libutpEndpoint).run

```
// The driver buffers the whole payload and feeds libutp as its send
		// window allows (drv_pump_writes, driver.cpp:87-97), pumping again on
		// every inject and timeout check. CloseStream only reaches utp_close
		// once that buffer has drained, so queueing both up front is safe.
```

## Line 168 — func (*libutpEndpoint).run

```
// Drain whatever else has already arrived into the same batch, so
			// libutp gets the batched delivery its embedder API assumes and
			// its deferred acks mean what they mean in production.
```

## Line 187 — func (*libutpEndpoint).run

```
// Drain received data every pass. libutp advertises its window from
		// what is still unread (drv_get_read_buffer_size, driver.cpp:275-278),
		// so a receiver that does not read closes its own window.
```

## Line 209 — func (*libutpEndpoint).finished

```
// finished reports whether this end has nothing left to do.
```

## Line 223 — func (*libutpEndpoint).finished

```
// The sender is done when libutp has torn the connection down, which
		// happens after the FIN it queued in CloseStream is acknowledged.
```

## Line 239 — func (*libutpEndpoint).flush

```
// flush writes everything libutp has emitted onto the link.
```

## Line 261

```
// --- flows joining libutp to this library -----------------------------------
```

## Line 263 — func libutpToGo

```
// libutpToGo runs a transfer with real libutp sending and this library
// receiving. It returns the elapsed time and the bytes the receiver read.
//
// The elapsed time runs from the moment before libutp's SYN to the moment our
// receiver holds the last byte, as for every other flow. It used to
// run to libutp's tearing the connection down, and libutp only does that on
// one of its 500ms timeout passes (utp_internal.cpp:37, :3284-3285) after the
// FIN is acknowledged: a 256 KB transfer delivered in 344ms was timed at
// 502ms. That rounding is libutp's teardown cadence, not its sending.
```

## Line 280 — func libutpToGo

```
// Our end reads in batches, as libutp's does here (Inject then IssueAcks
	// above) and as both do from a real socket.
```

## Line 288 — func libutpToGo

```
// A libutp initiator puts its recv_id in the SYN, so our acceptor mirrors
	// it: send on what arrived, receive on one more.
```

## Line 307 — func libutpToGo

```
// Read to the end of the stream, noting when the last byte arrived.
		// That instant, not the end of stream, is what the flow is timed to:
		// libutp's FIN can wait a round trip behind a full window, and a
		// libutp receiver is timed to the last byte (libutpToLibutp).
```

## Line 345 — func goToLibutp

```
// goToLibutp runs a transfer with this library sending and real libutp
// receiving.
```

## Line 355 — func goToLibutp

```
// as in libutpToGo
```

## Line 358 — func goToLibutp

```
// libutp is listening and will take whatever ids the SYN carries.
```

## Line 390 — func libutpToLibutp

```
// libutpToLibutp runs a transfer with real libutp at both ends, so that what
// libutp does on a link can be told apart from what our end contributes when
// it is joined to this library. The returned duration runs from the moment
// before the SYN to the moment the receiver holds the last byte, as for every
// other flow.
```

## Line 403 — func libutpToLibutp

```
// A listening libutp takes whatever ids the SYN carries; its own random
	// draws (its sequence numbers) come from a different seed.
```

## Line 426 — func libutpToLibutp

```
// The sender is finished; give its last packets a moment to be answered
	// before the receiver stops, as a socket would.
```

## Line 443 — func libutpErrName

```
// libutpErrName names libutp's error codes (utp.h:60-62).
```

## Line 456 — func flowLogger

```
// flowLogger is quiet unless UTP_FLOW_LOG asks otherwise, for diagnosing
// interop failures.
```

## Line 465 — var lastReceiverResets

```
// lastReceiverResets records how many RESETs the receiving socket sent during
// the last libutpToGo flow. A RESET tells the peer to give up, so a healthy
// transfer that draws one was killed by this side.
```

## Line 470 — var lastReceiverReadErr

```
// lastReceiverReadErr records how the receiving stream's read ended.
```
