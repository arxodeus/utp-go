# batch_read_bench_test.go

Every comment in `batch_read_bench_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly
```

## Line 12 — func BenchmarkReadBatch

```
// BenchmarkReadBatch is the cost of udpConn.readBatch taking k datagrams the
// kernel already holds, per datagram (ns/datagram): k = 1 is a reader woken
// for each packet, as on a paced path, and larger k a reader that fell behind
// a burst. Only the read is timed; the datagrams are queued before it.
```

## Line 45 — func BenchmarkReadBatch

```
// Loopback delivers inside the sender's write, so all k are
				// queued by now and one read takes them.
```
