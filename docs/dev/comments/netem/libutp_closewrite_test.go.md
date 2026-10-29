# netem/libutp_closewrite_test.go

Every comment in `netem/libutp_closewrite_test.go` at e56c9aa, verbatim.

## Line 1 — package netem

```
//go:build cgo
```

## Line 16

```
// The half-close against real libutp, from the side that initiates it.
//
// TestPeerMayWriteAfterOurFin covers the same exchange with Close(), where the
// application has said it is finished entirely and what libutp sends back is
// discarded -- correctly, because there is nobody left to give it to. This is
// the same exchange with CloseWrite(), where there is: we finish sending, and
// libutp's reply has to arrive.
//
// That is what libutp's own applications have always been able to do
// (`utp_shutdown(s, SHUT_WR)`, utp.h:176) and what this library could not.
```

## Line 52 — func TestCloseWriteDeliversWhatThePeerSendsAfterIt

```
// The driver is a C object freed by Close, and the pump goroutine below is
	// the only thing that touches it. It has to be stopped and joined first --
	// a deferred drv.Close() alone runs while the pump is still ticking, and
	// the next SetTime dereferences a freed handle. (It does: this test
	// segfaulted in 5 of 12 runs before the goroutine was joined.) ctx is not
	// enough either, because its cancel is deferred after this and so runs
	// later.
```

## Line 122 — func TestCloseWriteDeliversWhatThePeerSendsAfterIt

```
// StateEOF means libutp has taken our FIN. Its application is
			// still entitled to write, which is the half-close.
```

## Line 152 — func TestCloseWriteDeliversWhatThePeerSendsAfterIt

```
// Read the reply libutp sent after our FIN. This is the whole point: the
	// connection is still ours to read from.
```
