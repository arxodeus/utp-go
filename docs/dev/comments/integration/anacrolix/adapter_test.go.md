# integration/anacrolix/adapter_test.go

Every comment in `integration/anacrolix/adapter_test.go` at e56c9aa, verbatim.

## Line 19 — func TestSatisfiesTorrentsUtpSocketInterface

```
// The check this module exists for, made against torrent's own interface
// rather than against a copy of it.
//
// torrent's `utpSocket` is unexported, so it cannot be named from here. But
// torrent.NewUtpSocket returns it, and reflection can reach the type through
// that function's signature. Comparing against the real thing means this test
// fails if torrent changes the interface -- which a hand-copied interface
// declaration would not.
```

## Line 58 — func TestNewUtpSocket

```
// And that the constructor this package offers really produces one.
```

## Line 75 — func TestConcurrentPeerConnections

```
// A BitTorrent-shaped workload over the adapter: many concurrent peer
// connections, each exchanging messages in both directions, over one UDP port
// on each side.
//
// This is not a torrent transfer -- torrent selects its uTP implementation at
// build time, so it cannot be handed one at run time without patching that
// package (see README.md for the recipe). What it does test is the traffic
// shape a torrent client produces, which is what would break first: many
// simultaneous streams multiplexed on a shared port, each bidirectional and
// short-lived.
```

## Line 105 — func TestConcurrentPeerConnections

```
// Each accepted connection echoes what it is sent, as a peer answering a
	// request does.
```
