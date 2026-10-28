# netem/libutp_halfclose_test.go

Every comment in `netem/libutp_halfclose_test.go` at e56c9aa, verbatim.

## Line 1 — package netem

```
//go:build cgo
```

## Line 15 — func TestPeerMayWriteAfterOurFin

```
// What a peer's half-close actually costs, measured rather than assumed.
//
// DEVIATIONS.md records that this library has no half-close: on reaching the
// peer's FIN it tears the connection down, where libutp keeps its socket in
// CS_GOT_FIN until its own application closes too. That entry had no
// measurement behind it, and "libutp has a feature we lack" is not by itself a
// reason to build the feature.
//
// This drives the case from the only side that can start it. Our API has no
// CloseWrite -- Close means done entirely -- so an application using this
// library cannot half-close. Only the peer can, and only after we have sent
// our FIN. So: we write, we Close, and libutp then writes back, which is
// exactly what its half-close is for.
//
// It did cost compatibility, and this is what found it: before the socket
// learned to stay silent for data past a closed connection's FIN, this failed
// three times out of three with libutp reporting UTP_ECONNRESET and one RESET
// sent from here. A peer doing something entirely legitimate was told its
// connection had broken. Both assertions below are for that, and they are the
// reason this test is worth its runtime.
//
// What is left is the half-close itself. libutp writes and we discard, because
// our application said it was finished and there is nobody to give it to. That
// costs an API capability on our side, not compatibility on the wire.
// Implementing it means a new public method, a connection that keeps sending
// after the peer's FIN, and a reader that sees end of stream while the writer
// carries on -- a feature, not a conformance fix. It stays unbuilt, and this
// is the evidence for that being a reasonable choice rather than an oversight.
```

## Line 68 — func TestPeerMayWriteAfterOurFin

```
// The driver is a C object freed by Close, and the pump goroutine below is
	// the only thing that touches it. It has to be stopped and joined first --
	// a deferred drv.Close() alone runs while the pump is still ticking, and
	// the next SetTime dereferences a freed handle. (It does: this test
	// segfaulted in 5 of 12 runs before the goroutine was joined.) ctx is not
	// enough either, because its cancel is deferred after this and so runs
	// later.
```

## Line 138 — func TestPeerMayWriteAfterOurFin

```
// StateEOF means libutp has taken our FIN. Its application is
			// still entitled to write, which is the half-close.
```

## Line 166 — func TestPeerMayWriteAfterOurFin

```
// Long enough for libutp to notice the FIN, write back, and retransmit if
	// it thought that had failed.
```

## Line 182 — func TestPeerMayWriteAfterOurFin

```
// The point of the measurement: the peer writing after our FIN is not an
	// error for it, and does not draw a reset from us.
```
