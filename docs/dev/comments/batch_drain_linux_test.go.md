# batch_drain_linux_test.go

Every comment in `batch_drain_linux_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build linux
```

## Line 12 — func TestMmsghdrLayout

```
// mmsghdr is laid out as the kernel's struct mmsghdr: a msghdr, the length
// received, and padding to the word size. Written for any word size: a
// 32-bit build has no padding at all.
```

## Line 26 — func TestRawPeerKeyMatchesPeerFor

```
// rawPeerKey reads an address as the kernel writes it -- the port in network
// byte order -- into the key peerFor makes from the same address.
```

## Line 32 — func TestRawPeerKeyMatchesPeerFor

```
// 8080
```

## Line 38 — func TestRawPeerKeyMatchesPeerFor

```
// 50000
```
