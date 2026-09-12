# net.Conn conformance

This is a separate Go module. Its only job is to run the Go standard library's
own `net.Conn` conformance suite — `golang.org/x/net/nettest` — against
[`utpnet.Conn`](../../utpnet).

It is separate for the same reason the [anacrolix one](../anacrolix) is: the
library's own `go.mod` stays small, and nothing a caller depends on grows
because this check exists.

```sh
cd integration/nettest && go test -race -count=1 ./...
```

## Why it exists

`utpnet.Conn` claims to be a `net.Conn`, and everything that embeds this
library programs against that claim rather than against uTP. Nothing checked
it. The claim was already false in a way no test here could see: cancelling
the context a dial was made with closed the connection the dial had just
returned, which `net.Dialer` documents must not happen. It took running a real
torrent to find that.

`nettest.TestConn` covers what a hand-written test does not think to: reads
racing writes, a deadline set in the past, a deadline moved while a read is
blocked on it, `Close` while another goroutine is inside `Read`, and every
method called concurrently with every other. Its own documentation warns that
some issues surface only across repeated runs, so it is worth more than one
(`-count=5`).

## The TCP control

`TestTCPBaseline` runs the same eleven subtests against kernel TCP over
loopback, in the same process and the same container.

It is there because "the suite passes" is not the only thing worth knowing
from it. The subtest timings say what the deadline and teardown paths cost,
and a timing means nothing without something to compare it to — a slow machine
would otherwise read as a slow library. The first run made the case for it:
TCP finished in 0.43s and this library took 139.91s, which is not a machine
being slow at anything.

## What it has found

- **`Close` waited out the retransmission ladder.** `RacyRead` took 31.15s,
  `PresentTimeout` 75.10s, and almost all of it was in `Close` rather than in
  the test body. See KNOWN-LIMITATIONS.md.
- **`Write` retained the caller's buffer past a deadline.** Found by
  `RacyWrite` under `-race`, which is the only subtest that mutates the write
  buffer immediately after the call returns.

## What it does not cover

- **`net.PacketConn` and `net.Listener`.** `nettest` has `TestConn` and
  nothing equivalent for the other two, so `utpnet.Socket`'s packet-oriented
  and accept-side behaviour is still only covered by this repository's own
  tests.
- **Anything about uTP.** The suite tests the `net.Conn` contract. It says
  nothing about wire compatibility, congestion control, or libutp — those are
  the other harnesses' job.
