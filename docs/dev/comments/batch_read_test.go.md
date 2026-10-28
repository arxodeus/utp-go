# batch_read_test.go

Every comment in `batch_read_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly
```

## Line 16 — func batchPair

```
// batchPair is a udpConn and a socket sending to it.
```

## Line 34 — func readQueued

```
// readQueued reads the k datagrams just sent to c, copied out of its arena.
//
// Linux delivers on loopback before the send returns, so all k are queued
// and one batch must take them. Darwin may still be delivering -- CI saw 11
// of 16 -- so there they are read until all have come, still in order and
// each whole.
```

## Line 60 — func TestReadBatchTakesEverythingQueued

```
// readBatch hands over every datagram already queued, in order, each whole,
// however many system calls that takes.
```

## Line 89 — func TestReadBatchTakesLargeDatagramWhole

```
// A datagram as large as loopback carries arrives whole: MaxPacketSize may be
// set that high, and a truncated packet would be lost.
//
// On Darwin the sender's send buffer also bounds the datagram it may send --
// net.inet.udp.maxdgram, 9216 by default -- so the sender's is raised first,
// and the size is the largest of a few the platform then accepts. That
// refusal is the sender's, before the receive path runs at all.
```

## Line 130 — func TestReadBatchPeerMatchesReadFrom

```
// A peer read in a batch is the one net.UDPConn.ReadFrom names: connections
// are keyed by the peer's text, so any difference would misroute a packet.
```
