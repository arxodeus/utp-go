# batch_drain_recvfrom.go

Every comment in `batch_drain_recvfrom.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build darwin || freebsd || netbsd || openbsd || dragonfly
```

## Line 10 — type drainState

```
// drainState is drain's receive buffer.
```

## Line 15 — func (*udpConn).drain

```
// drain appends what the socket holds to out, waiting for the first datagram
// if there is none: recvfrom until it would block, as libutp's embedder does.
```

## Line 31 — func (*udpConn).drain

```
// Nothing yet: wait, unless something has come.
```

## Line 52 — func (*udpConn).peerFor

```
// peerFor returns the peer a datagram came from, as recvfrom reports its
// address. See peerForKey.
```
