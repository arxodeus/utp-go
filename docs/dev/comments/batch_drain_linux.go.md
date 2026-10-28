# batch_drain_linux.go

Every comment in `batch_drain_linux.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build linux
```

## Line 12 — const recvmmsgSlotCount

```
// recvmmsgSlotCount is how many datagrams one recvmmsg can take.
```

## Line 15 — const recvmmsgSlotSize

```
// recvmmsgSlotSize holds any UDP datagram whole: the largest IPv4 payload is
// 65,507 bytes and the largest IPv6 one without a jumbogram 65,527, and
// MaxPacketSize may be set that high on loopback, whose path MTU is 65,488.
// The slots are 1 MiB of address space, but the kernel writes only what
// arrives and fresh pages are not zeroed, so a full-size packet uses its
// slot's first page and nothing more is ever touched.
```

## Line 23 — type mmsghdr

```
// mmsghdr is Linux's struct mmsghdr: a msghdr and the length received. Go
// pads it to the word size as C does (TestMmsghdrLayout).
```

## Line 30 — type drainState

```
// drainState is what drain receives into.
```

## Line 35 — type recvmmsgSlots

```
// recvmmsgSlots is what drain receives into, made once per udpConn. Only the
// socket's read loop reads, so one set is enough.
```

## Line 56 — func (*udpConn).drain

```
// drain appends what the socket holds to out, waiting for the first datagram
// if there is none: libutp's embedder reads until the socket would block, and
// this does the same, up to sixteen datagrams a system call.
//
// Until it would block, not until a read comes back short. A short read has
// emptied the queue, and stopping there saves the last call, the one that
// only reports there is nothing left -- 0.3-0.8 us of a datagram's way to its
// acknowledgement, too little for TestAckTurnaround to see. But datagrams
// arriving meanwhile then start the next batch instead of joining this one,
// so under load batches were smaller than libutp's (TestManyConcurrentTransfers:
// 18.7-24.5 datagrams against 22.9-29.6), and every batch ends with an
// acknowledgement for each connection it reached.
```

## Line 89 — func (*udpConn).drain

```
// Nothing yet: wait, unless something has come.
```

## Line 99 — func (*udpConn).drain

```
// Larger than any UDP datagram; cannot happen.
```

## Line 120 — func rawPeerKey

```
// rawPeerKey is the peer cache's key for a source address as the kernel
// wrote it, without making a syscall.Sockaddr of it first.
```
