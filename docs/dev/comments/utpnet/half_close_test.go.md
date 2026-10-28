# utpnet/half_close_test.go

Every comment in `utpnet/half_close_test.go` at e56c9aa, verbatim.

## Line 13 — func TestCloseWriteKeepsTheReadSideOpen

```
// A half-close: one end finishes sending, both keep going.
//
// libutp has had this all along -- `utp_shutdown(s, SHUT_WR)`, with the socket
// held in CS_GOT_FIN while its application keeps reading. This library did
// not: on reaching the peer's FIN the connection tore itself down, so a peer
// that closed its sending side took the whole connection with it, along with
// whatever it had not finished sending back. That was the one place the
// reference could do something an application here could not.
//
// The shape below is the one that matters in practice, and the one that used
// to fail: a client sends a request, says it has nothing more to send, and
// then reads a reply that is larger than everything before it.
```

## Line 43 — func TestCloseWriteKeepsTheReadSideOpen

```
// Read to end of stream: the peer has closed its sending side, which
		// must not close ours.
```

## Line 54 — func TestCloseWriteKeepsTheReadSideOpen

```
// And now answer, long after the peer said it was finished.
```

## Line 78 — func TestCloseWriteKeepsTheReadSideOpen

```
// Writing after CloseWrite is an error, as it is on a TCP connection.
```

## Line 105 — func TestCloseWriteLooksLikeEndOfStreamNotFailure

```
// The far end must see end of stream, not an error, when its peer half-closes.
//
// A reader that cannot tell "the peer has finished sending" from "the
// connection broke" cannot use a half-close for anything: the whole point is
// that the reply is still worth sending.
```

## Line 121 — func TestCloseWriteLooksLikeEndOfStreamNotFailure

```
// io.ReadAll reports io.EOF as success; anything else is a failure.
```

## Line 145 — func dialTo

```
// dialTo opens a connection from one socket to another's address.
```
