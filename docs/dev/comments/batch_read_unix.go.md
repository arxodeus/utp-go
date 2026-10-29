# batch_read_unix.go

Every comment in `batch_read_unix.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly
```

## Line 10 — const maxReadBatch

```
// maxReadBatch bounds one batch. libutp's embedders read until the socket
// would block, with no bound (the bridge in native/libutp does exactly that);
// this one only keeps a pathological burst from holding acknowledgements back
// indefinitely. A 4 MiB socket buffer holds about 3000 full-size datagrams.
```

## Line 16 — type udpConnReader

```
// readBatch returns every datagram the kernel already has queued, waiting only
// for the first one: libutp's embedder loop of recvfrom until EWOULDBLOCK,
// after which it calls utp_issue_deferred_acks once (utp.h:512-517;
// native/libutp/bridge.cpp). See Socket.readLoop. drain does the reading:
// recvmmsg on Linux (batch_drain_linux.go), recvfrom elsewhere
// (batch_drain_recvfrom.go).
// udpConnReader is what udpConn's batched reads keep between reads.
```

## Line 24 — type udpConnReader

```
// peers caches the peer for each source address the read loop has seen.
	// See peerForKey.
```

## Line 27 — type udpConnReader

```
// arena holds the datagrams of the last batch read, and batch the list of
	// them: both are reused by the next. See lend.
```

## Line 31 — type udpConnReader

```
// drainState is what this platform's drain reads into.
```

## Line 35 — type peerKey

```
// peerKey is a source address as the kernel reports it, comparable so it can
// key the reader's peer cache (udpConn.peerForKey).
```

## Line 44 — const maxCachedPeers

```
// maxCachedPeers bounds the peer cache. Past it the cache starts again, which
// costs one address formatting per peer that sends again.
```

## Line 53 — func (*udpConn).readBatch

```
// The socket dispatches every datagram of a batch before it reads the
	// next, so the list and the bytes are both free again by then.
```

## Line 62 — func (*udpConn).lend

```
// lend copies a datagram into the batch's arena and returns it there.
//
// Each datagram was copied into a slice of its own, which the decoded packet
// then kept as its body: an allocation for every datagram received, for an
// acknowledgement as much as for data. A packet decoded from a borrowed
// buffer copies its body only if it is kept (packet.own).
```

## Line 74 — func (*udpConn).peerForKey

```
// peerForKey returns the peer a datagram came from, by its address's key,
// reusing the one made for the last datagram from the same address. Making one
// per datagram converted the address and formatted it as text -- the key
// every packet is routed by -- for every packet: the largest single cost of
// routing one, measured on a reader just woken. Only the read loop calls
// this, so the cache needs no lock.
```

## Line 92 — func (peerKey).udpAddr

```
// udpAddr is the address net.UDPConn.ReadFrom would have returned for the
// datagram this key was made from, so a peer read in a batch hashes exactly as
// one read singly does.
```

## Line 106

```
// uint32 -> string
```

## Line 108 — func zoneName

```
// zoneName is the interface name for an IPv6 scope id, as net formats it: the
// name when the interface exists, the number otherwise.
```
