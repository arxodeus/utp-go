# batch_read_other.go

Every comment in `batch_read_other.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)
```

## Line 5 — type udpConnReader

```
// udpConnReader keeps nothing: there are no batched reads here.
```

## Line 8 — func (udpConnReader).readBatch

```
// readBatch is unsupported here; Socket.readLoop falls back to one
// datagram per read, each its own batch.
```
