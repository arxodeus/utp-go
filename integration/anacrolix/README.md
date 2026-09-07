# anacrolix/torrent integration

This is a separate Go module. Its only job is to check, against the real
thing rather than against a copy of it, that
[`utpnet.Socket`](../../utpnet) can stand in for a uTP implementation in
[github.com/anacrolix/torrent](https://github.com/anacrolix/torrent).

It is separate because the interface being satisfied is unexported, so proving
the claim means importing the torrent package, and that pulls in a large
dependency tree. Keeping it here means nothing a caller of this library
depends on grows because the check exists.

```
cd integration/anacrolix && go test ./...
```

## What is checked

`TestSatisfiesTorrentsUtpSocketInterface` reaches torrent's unexported
`utpSocket` interface through `reflect.TypeOf(torrent.NewUtpSocket).Out(0)`
and asserts that `*utpnet.Socket` implements it. Ten methods, all present.
If torrent changes the interface, this fails — which a hand-written interface
declaration would not.

`TestConcurrentPeerConnections` runs the traffic shape a torrent client
produces: many simultaneous streams multiplexed on one UDP port per side,
each exchanging data in both directions.

That test is why this module exists rather than only a compile-time
assertion. It found a defect nothing else had: the send buffer retained the
caller's slice, so any full-duplex exchange corrupted its own data. Every
unidirectional test in the repository passed throughout. See
[KNOWN-LIMITATIONS.md](../../KNOWN-LIMITATIONS.md).

## Wiring it into a torrent client

torrent chooses its uTP implementation at **build time**, not at run time:
`utp_go.go` (`//go:build !cgo`) uses `github.com/anacrolix/utp`, and
`utp_libutp.go` (`//go:build cgo`) uses `github.com/anacrolix/go-libutp`.
There is no injection point, so a client cannot be handed this socket without
one of:

1. **A `replace` on the uTP module.** Point `github.com/anacrolix/utp` at a
   shim whose `NewSocket` returns a `*utpnet.Socket`. The pure-Go path is
   already what a `CGO_ENABLED=0` build selects, so nothing else changes.
2. **A patched `utp_go.go`** in a fork of torrent, calling
   `anacrolix.NewUtpSocket` from this package.
3. **Bypassing torrent's socket layer**, by giving the client its own
   `Dialer` and `Listener` built on `utpnet.Socket`. This is the least
   invasive, and works because torrent accepts both as configuration.

`NewUtpSocket` in this package has the signature `torrent.NewUtpSocket` has,
so options 1 and 2 are a one-line substitution.

## What is not covered

- **The firewall callback.** torrent uses it to refuse connections from
  blocked addresses before any state is created for them. This library has no
  equivalent hook, so `NewUtpSocket` accepts one and ignores it. A caller
  relying on IP blocking would not get it.
- **A real torrent transfer.** Running one would require applying one of the
  wiring options above inside this repository, which means either vendoring a
  fork of torrent or a `replace` that would leak into anyone building this
  module. The traffic-shape test is what stands in for it, and its limits are
  stated rather than glossed.
