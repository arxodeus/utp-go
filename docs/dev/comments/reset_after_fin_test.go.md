# reset_after_fin_test.go

Every comment in `reset_after_fin_test.go` at e56c9aa, verbatim.

## Line 11 — func TestResetAfterTheFinTakesNothingBack

```
// A RESET that arrives after everything the peer sent, its FIN included,
// takes nothing back: the reader still gets every byte and a clean end of
// stream, as go-libutp's does. A writer is told the connection was reset.
//
// The side that closed first destroys its connection once its FIN is
// acknowledged and answers the next packet with a RESET, as libutp does
// (utp_internal.cpp:2179-2182, :2907-2944). Before this, the reset turned a
// complete transfer into "reset" whenever the reader had not yet taken
// everything: one 16 MB loopback transfer in ten under load
// (integrated.TestUdpTransfer).
```

## Line 27 — func TestResetAfterTheFinTakesNothingBack

```
// More than the read queue holds, so that some is still in the receive
	// buffer, not yet handed up, when the reset comes: what a reader that
	// has fallen behind leaves.
```

## Line 36 — func TestResetAfterTheFinTakesNothingBack

```
// Wait until the server has everything, FIN included, unread.
```

## Line 51 — func TestResetAfterTheFinTakesNothingBack

```
// The client's socket resets the server's connection, as it does when
	// the server's next packet reaches a connection already gone.
	// Built as maybeSendReset builds it: its ack number is the packet that
	// drew it, the server's latest.
```

## Line 60 — func TestResetAfterTheFinTakesNothingBack

```
// The connection ends once its reader has taken what it holds; wait
	// for the reset to land first.
```
