# utpnet/socket_test.go

Every comment in `utpnet/socket_test.go` at e56c9aa, verbatim.

## Line 40 — func TestDialAcceptTransfer

```
// The whole point of the package: a uTP connection that behaves like any
// other net.Conn, over a real UDP socket.
```

## Line 95 — func TestConnAddresses

```
// The addresses a caller sees have to be the real ones, or a BitTorrent
// client cannot tell its peers apart.
```

## Line 134 — func TestNonUtpDatagramsPassThrough

```
// A BitTorrent client runs the DHT on the same UDP port as uTP. Datagrams
// that are not uTP have to come back out of ReadFrom rather than being
// swallowed or answered.
```

## Line 146 — func TestNonUtpDatagramsPassThrough

```
// Shaped like a DHT query, which is what would really be sharing the port.
```

## Line 167 — func TestNonUtpDatagramsPassThrough

```
// And the reverse direction, which is how the DHT would reply.
```

## Line 182 — func TestUtpPacketsDoNotLeakToReadFrom

```
// uTP traffic must not leak out of ReadFrom. If it did, a caller sharing the
// port would try to parse uTP packets as its own protocol.
```

## Line 220 — func TestIsUtpPacket

```
// The sniff decides what the uTP machinery ever sees, so it is tested on its
// own rather than only through a transfer.
```

## Line 223 — func TestIsUtpPacket

```
// An ST_DATA header, version 1, no extension: connection id 1234,
	// timestamp 200000, window 1 MiB, sequence number 42 (BEP 29).
```

## Line 251 — func TestIsUtpPacket

```
// A version other than 1 is not ours, whatever else it looks like.
```

## Line 261 — func TestReadDeadline

```
// Deadlines have to work the way net.Conn's do, including being movable from
// another goroutine while a call is blocked. Callers rely on that to unblock
// readers.
```

## Line 291 — func TestReadDeadline

```
// Nothing has been sent, so this must time out rather than hang.
```

## Line 308 — func TestReadDeadline

```
// A deadline set from another goroutine unblocks a Read already in
	// progress: the standard library behaves this way and callers use it.
```

## Line 336 — func TestCloseIsIdempotent

```
// Close has to be safe to call twice and from several goroutines: net.Conn
// callers do both.
```

## Line 358 — func TestConcurrentFullDuplexConnections

```
// Many simultaneous connections on one port, each exchanging data in both
// directions: the traffic shape a BitTorrent client produces.
//
// This is what found the send buffer retaining the caller's slice. A
// unidirectional transfer never touches its buffer again after writing, so
// every test in this repository passed while any echo loop corrupted its own
// data.
```
