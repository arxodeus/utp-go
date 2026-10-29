# integrated/test_utils.go

Every comment in `integrated/test_utils.go` at e56c9aa, verbatim.

## Line 36 — type DropFirstNSent

```
// DropFirstNSent drops the first targetDropsN datagrams. Safe for
// concurrent use: a socket writes from more than one goroutine.
```

## Line 80 — func (*MockUdpSocket).Queued

```
// Queued implements utp.QueuedReader, as a real socket's batched read would.
```

## Line 93 — func (*MockUdpSocket).WriteTo

```
// Like a UDP socket, never wait for the peer: a datagram that finds the
	// link's queue full is lost. The socket writes from its reader, as
	// libutp's embedder does, so a WriteTo that waited for the other side to
	// read would let two sockets each wait on the other for ever.
	// Copied, as a UDP socket copies: the caller reuses b once this returns.
```

## Line 112 — const linkQueue

```
// linkQueue is how many datagrams a mock link holds before it drops: more
// than any window the tests here open, so a drop is what a LinkDecider asks
// for and not an accident of the harness.
```

## Line 121 — func buildLinkPair

```
// A -> B
```

## Line 128 — func buildLinkPair

```
// B -> A
```
