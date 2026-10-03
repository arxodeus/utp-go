# go-utp interop

This is a separate Go module. It tests this library against go-utp's pure Go
engine, [`purego`](https://pkg.go.dev/github.com/anacrolix/go-utp/purego): a
port of libutp to Go, with no cgo, that departs from libutp in a few places
and so can catch what interop with libutp itself
([`native/libutp`](../../native/libutp)) cannot.

It is separate so that go-utp and what its module requires stay out of the
library's module graph: added to the library's own go.mod, it brought eight
modules and a newer `golang.org/x/exp` with it.

```
cd integration/goutp && go test ./...
```

About 45 seconds; no cgo needed.

## What is checked

Over loopback UDP (`interop_test.go`), every case with this library dialling
and with purego dialling:

- `TestPuregoTransfer`: 4 MB one way, in each direction, ended by the
  sender's close.
- `TestPuregoBothDirectionsAtOnce`: 2 MB each way on one connection at once,
  each side half-closing when done.
- `TestPuregoConcurrentConnections`: 16 connections between the same two
  sockets, half sending each way.
- `TestPuregoSmallWrites`: 2,000 writes of 37 bytes, in each direction.
- `TestPuregoRequestResponse`: a request, a half-close, and the answer on
  the half still open.

Over [`netem`](../../netem), which purego reaches through a `net.PacketConn`
adapter (`netem_conn_test.go`): `TestPuregoTransferUnderAdverseConditions`,
in every combination of who dials and who sends, over seven links -- 20 ms
with jitter; 1% and 5% loss; 5% reordering; 5% duplication; 2 Mb/s with a
32 KB queue; and loss, reordering, duplication and jitter together.

It catches what it is for: with this library's selective-ack bits written
most significant first, all four lossy cases with this library receiving
stalled, purego never resending what it took for delivered.

## The dialler speaks first

libutp completes an incoming connection only when the first data packet
arrives (`utp_internal.cpp:2158-2161`) and writes nothing before that
(`utp_writev`, `:3181`). purego does the same: accepted and asked to write
before the dialler had sent anything, its `Write` was still blocked after 10
seconds. go-libutp fixed the same hang in v1.5.0; purego, at
`v0.0.0-20260908033909-9f1664acf866`, still has it. So where purego accepts
and is to send, this library dials and sends one byte first, as BitTorrent's
handshake does (`helloFirst`). This library completes a connection on the
handshake instead (DEVIATIONS.md, "Completing an incoming connection"), so
where it accepts it sends first, and the tests let it: against purego, which
dialled and said nothing, it delivered.
