# temporary_error.go

Every comment in `temporary_error.go` at e56c9aa, verbatim.

## Line 3 — func isTemporaryError

```
// isTemporaryError reports whether a read error is one the socket should
// read past: one that says it is temporary, or, on Windows, a datagram
// larger than the buffer (see isPacketTooBig). go-ethereum's
// netutil.IsTemporaryError, which this replaced, decides it the same way.
```
