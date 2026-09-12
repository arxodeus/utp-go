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

`TestTorrentTransferOverUtp` runs a real torrent: two `torrent.Client`s, a
generated 4 MiB torrent in 128 pieces, and BitTorrent's own piece hashes
deciding whether what arrived is what was sent. Both clients have TCP,
torrent's built-in uTP and the DHT disabled, so the only transport between
them is this library.

It too found a defect on its first run, and a worse one: `UtpSocket.Connect`
made the caller's dial context the *connection's* lifetime, so the standard
`defer cancel()` after a dial killed the connection it had just returned.
torrent does exactly that, and the result was a BitTorrent connection that
completed its handshake and then died mid-stream. Every test in the repository
dials with a context it keeps alive for the whole transfer, so none of them
could see it.

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
- **A torrent through torrent's own socket layer.** `TestTorrentTransferOverUtp`
  takes option 3 above -- `Client.AddListener` and `Client.AddDialer`, both
  exported, both taking what `utpnet.Socket` already provides -- so it needs
  neither a fork nor a `replace`. What it does not exercise is options 1 and 2:
  torrent choosing this implementation through its own `listenUtp`, which is
  selected at build time and cannot be reached from here.

  This entry previously said a real transfer was not covered because wiring it
  up required a fork. That was true of torrent's built-in socket layer and not
  of the client's exported hooks; the gap was in the reading of the API.
- **Trackers, the DHT, PEX and holepunching**, all disabled: the leecher is
  handed the seeder's address directly. What is tested is the transport, not
  peer discovery.
- **Encryption.** `HeaderObfuscationPolicy` is left at its default, so what the
  two clients negotiate is whatever they would negotiate over TCP. Nothing here
  pins it.
