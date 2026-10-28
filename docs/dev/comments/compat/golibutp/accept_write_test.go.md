# compat/golibutp/accept_write_test.go

Every comment in `compat/golibutp/accept_write_test.go` at e56c9aa, verbatim.

## Line 3

```
// Adapted from go-libutp's accept_write_test.go (v1.5.1). Its test asserts a
// libutp limitation this package does not have, so it cannot run unchanged:
// libutp's acceptor stays in CS_SYN_RECV, and refuses to write, until the
// initiator's first data packet arrives (utp_writev, utp_internal.cpp:3181).
// go-libutp v1.5.0 fixed the consequence -- a write that blocked for ever --
// and its test pins both halves: the write waits, then completes.
//
// Here an accepted connection is established by the handshake, so the write
// completes at once (DEVIATIONS.md, "Completing an incoming connection"), and
// what matters to a caller is checked instead: the bytes arrive, before the
// dialler has sent anything.
```

## Line 24 — type gatedPacketConn

```
// gatedPacketConn only lets packets be received when a token is available. It
// lets tests control exactly when the wrapped Socket processes what it's been
// sent. Closing tokens lets everything through.
```

## Line 43 — func TestWriteOnAcceptedConnBeforeEstablished

```
// Only the initiating SYN gets through until we release the gate.
```

## Line 69 — func TestWriteOnAcceptedConnBeforeEstablished

```
// The acceptor has seen nothing after the SYN, and the write completes
	// anyway.
```
