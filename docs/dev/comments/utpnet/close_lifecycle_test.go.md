# utpnet/close_lifecycle_test.go

Every comment in `utpnet/close_lifecycle_test.go` at e56c9aa, verbatim.

## Line 13 — func TestSocketCloseAfterWriteDeliversEverything

```
// Closing a connection and then its socket straight after a Write delivers
// everything the Write accepted. The socket stopped reading at the start of
// Close, while the connection still had most of a send buffer to deliver, so
// the acknowledgements that would have let it out were never read: the peer
// got what was in flight and the rest was abandoned. From the release review
// of 9c44c4f (R2).
```

## Line 62 — type failingPacketConn

```
// failingPacketConn is a real UDP socket whose reads fail, with err, once
// told to.
```

## Line 76 — func (*failingPacketConn).ReadFrom

```
// Short reads, so the failure is seen soon after it is asked for.
```

## Line 90 — func TestTerminalReadErrorClosesTheSocket

```
// A read error that will not clear closes the socket: Accept and ReadFrom,
// already waiting, return at once with it. The reader used to stop and
// nothing else, leaving both waiting for ever. Release review of 9c44c4f
// (R9).
```
