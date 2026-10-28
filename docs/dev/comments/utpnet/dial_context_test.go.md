# utpnet/dial_context_test.go

Every comment in `utpnet/dial_context_test.go` at e56c9aa, verbatim.

## Line 12 — func TestDialContextCancelDoesNotCloseTheConnection

```
// Cancelling the dial context must not close the connection the dial produced.
//
// This is what net.Dialer promises -- "Canceling ctx does not affect the
// connection after it is established" (net.Dialer.DialContext) -- and every Go
// caller is written against that promise. The standard shape is
//
//	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
//	defer cancel()
//	conn, err := dialer.DialContext(ctx, ...)
//
// where the cancel fires as soon as the dialling function returns, with the
// connection still in the caller's hands.
//
// This library gave the connection that context as its lifetime, so the
// connection died the moment the dial returned. It was invisible to every test
// here because they all dial with a context they keep alive for the duration.
// anacrolix/torrent does not: its outgoing connections cancel their dial
// context on the way out of establishOutgoingConn. What that produced was a
// connection that completed the whole BitTorrent handshake and then stopped
// mid-stream -- the peer read 796 bytes of a 1007-byte exchange and saw EOF,
// and the torrent never moved a piece.
```

## Line 50 — func TestDialContextCancelDoesNotCloseTheConnection

```
// Answer, so the exchange is full duplex the way a handshake is.
```

## Line 71 — func TestDialContextCancelDoesNotCloseTheConnection

```
// Exactly what a `defer cancel()` does, at exactly the moment it does it:
	// the dial is over, the connection is not.
```

## Line 76 — func TestDialContextCancelDoesNotCloseTheConnection

```
// Give the cancellation every chance to take the connection with it.
```
