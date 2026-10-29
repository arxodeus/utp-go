# anacrolix/torrent integration

A separate Go module that tests [`utpnet.Socket`](../../../utpnet) and
[compat/golibutp](../../../compat/golibutp) inside
[github.com/anacrolix/torrent](https://github.com/anacrolix/torrent). It is
kept apart so that torrent's dependencies stay out of the library's module
graph.

```
cd internal/integration/anacrolix && go test ./...
```

- `TestSatisfiesTorrentsUTPSocketInterface` asserts that `*utpnet.Socket`
  implements torrent's unexported uTP socket interface, found by reflection,
  so a change to that interface fails the test.
- `TestNewUTPSocket` and `TestConcurrentPeerConnections` check `NewSocket`,
  which has the signature of torrent's own constructor, under many
  bidirectional streams on one port.
- `TestTorrentTransferOverUTP` transfers and hash-verifies a 4 MiB torrent
  between two clients whose only transport is this library, through
  `Client.AddListener` and `Client.AddDialer`.
- `TestTorrentOverTheGoLibutpDropIn` does the same through torrent's own
  socket layer, with go-libutp replaced by compat/golibutp in this module's
  go.mod, and checks that the DHT shares the socket.

Trackers, PEX and holepunching are disabled; what is tested is the
transport.
