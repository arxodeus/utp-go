# integration/nettest/conn_test.go

Every comment in `integration/nettest/conn_test.go` at e56c9aa, verbatim.

## Line 16 — func TestUtpConnSatisfiesNetConn

```
// The standard library's own net.Conn conformance suite, run against ours.
//
// utpnet.Conn claims to be a net.Conn, and everything that embeds this library
// -- a BitTorrent client above all -- programs against that claim rather than
// against uTP. Until now nothing checked it. The claim was already false in a
// way no test here could see: cancelling the context a dial was made with
// closed the connection the dial had just returned, which net.Dialer documents
// must not happen, and which took a real torrent to find.
//
// nettest.TestConn is the suite the standard library holds its own
// implementations to. It covers the things a hand-written test does not think
// to: reads racing writes, a deadline set in the past, a deadline moved while
// a read is blocked on it, Close while another goroutine is inside Read, and
// every method called concurrently with every other. It is written to be run
// under the race detector, and its own documentation warns that some issues
// surface only across repeated runs -- so this is worth running more than once
// (`go test -race -count=5 ./...`).
```

## Line 37 — func makeUtpPipe

```
// makeUtpPipe gives nettest a connected pair, one end from Dial and one from
// Accept, on two separate sockets -- which is the shape a real peer connection
// has, rather than two ends of one socket talking to themselves.
```
