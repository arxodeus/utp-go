# utpnet/half_close_read_test.go

Every comment in `utpnet/half_close_read_test.go` at e56c9aa, verbatim.

## Line 13 — func TestCloseReadKeepsThePeerSending

```
// The other half-close: this end has finished reading, the peer has not
// finished sending.
//
// libutp's utp_shutdown(s, SHUT_RD) sets read_shutdown, and what that flag
// does is narrower than it looks: `conn->ack_nr++` runs whether or not the
// bytes are delivered (utp_internal.cpp:2344-2355, and again for the reorder
// buffer at :2392-2395). Incoming data is acknowledged and dropped, not
// refused.
//
// The distinction is the whole test. The obvious implementation -- stop
// delivering and let the receive buffer fill -- closes the advertised window,
// and a peer that cannot finish sending cannot finish closing either. So this
// sends far more than the receive buffer holds and requires it to go through.
```

## Line 29 — func TestCloseReadKeepsThePeerSending

```
// Four times the default receive buffer. A connection that merely stopped
	// delivering would stall a quarter of the way in.
```

## Line 54 — func TestCloseReadKeepsThePeerSending

```
// And the peer's own data still arrives, in the other direction.
```

## Line 85 — func TestCloseReadKeepsThePeerSending

```
// Reading is over, and says so rather than blocking or reporting the end
	// of a stream that has not ended.
```

## Line 95 — func TestCloseReadKeepsThePeerSending

```
// Sending is untouched.
```

## Line 114 — func TestCloseReadIsNotAClose

```
// CloseRead does not end the connection, and Close after it still does.
```

## Line 141 — func TestCloseReadIsNotAClose

```
// Twice is not an error: net.TCPConn tolerates it and so must this.
```

## Line 160 — func TestCloseReadIsNotAClose

```
// Now really close, and the peer reaches end of stream.
```
