# cid.go

Every comment in `cid.go` at e56c9aa, verbatim.

## Line 9 — type ConnectionPeer

```
// ConnectionPeer is an interface representing a remote peer.
```

## Line 14 — type UDPPeer

```
// SocketAddr is a simple implementation of the ConnectionPeer interface using net.UDPAddr.
```

## Line 17 — type UDPPeer

```
// hash is addr.String(), computed when the peer is made, so a peer the
	// socket's reader routes many datagrams for formats its address once.
```

## Line 33 — func (*UDPPeer).Addr

```
// Addr returns the UDP address this peer names. It exists so callers that
// need a net.Addr -- anything presenting a uTP stream as a net.Conn -- can
// get one back out without reparsing the hash.
```

## Line 56 — type ConnectionID

```
// ConnectionID represents a connection identifier with send and receive IDs and a peer.
```

## Line 74 — func genHash

```
// genHash is the connection's key: its two ids and its peer's key, as text.
// Two connections share a key exactly when all three match -- the ids come
// first and hold no colon, so the peer's key, whatever it contains, cannot
// make two different triples spell the same string.
//
// It was a SHA3-256 of that same text, cut to 20 bytes and hex-encoded, which
// bought nothing a map needs and cost most of the time a packet took: the
// socket's reader builds up to three candidate ids for every datagram it
// routes, and with the hash that was 27us of a 37us path from read to
// acknowledgement (10 Mb/s from libutp, TestAckTurnaround).
```

## Line 89 — func appendConnKey

```
// appendConnKey appends the key genHash makes for these ids and peer key. The
// socket's reader builds keys with it into a reused buffer, so a lookup
// allocates nothing.
```

## Line 100 — func (*ConnectionID).Hash

```
// Hash returns the key this connection is tracked under.
//
// The value is cached by NewConnectionID. ConnectionID is exported with
// exported fields, so callers legitimately build one as a struct literal --
// and such a value has an empty cached hash. Returning that empty string made
// every literal-built ConnectionID hash to "" and therefore collide with
// every other one, silently breaking connection lookup. Fall back to
// computing it rather than handing back a key that is wrong.
//
// Prefer NewConnectionID: it caches, and this fallback does not.
```
