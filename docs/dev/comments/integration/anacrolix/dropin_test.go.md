# integration/anacrolix/dropin_test.go

Every comment in `integration/anacrolix/dropin_test.go` at e56c9aa, verbatim.

## Line 13 — func TestTorrentOverTheGoLibutpDropIn

```
// The drop-in replacement for go-libutp, through torrent's own socket layer.
//
// This module's go.mod replaces github.com/anacrolix/go-libutp with
// compat/golibutp, which is all an application does to use it. torrent's
// default build (cgo on, no tags) then makes its uTP sockets with that
// package's NewSocket, in its own listenUtp, on the same port as its TCP
// listener would be, installs its firewall with SetSyncFirewallCallback, and
// runs its DHT on the same socket. Nothing here calls this library directly:
// the test is that torrent, configured as an application would configure it,
// transfers a torrent and answers DHT queries over it.
```

## Line 31 — func TestTorrentOverTheGoLibutpDropIn

```
// The DHT shares the uTP socket's port: one client's DHT server pings
	// the other's, and the reply comes back through the socket that is also
	// carrying uTP.
```

## Line 44 — func newDefaultUtpClient

```
// newDefaultUtpClient is a torrent.Client with torrent's own uTP and DHT on,
// TCP off so uTP is the only transport between peers, and nothing that
// reaches beyond this host.
```

## Line 71 — func newDefaultUtpClient

```
// torrent's listener is its utpSocketSocket around the uTP socket it
	// made; that socket must be the drop-in's, with torrent's firewall
	// installed. The drop-in's Socket holds a *utpnet.Socket where
	// go-libutp's holds a libutp context.
```
