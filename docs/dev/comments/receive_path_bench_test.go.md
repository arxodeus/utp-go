# receive_path_bench_test.go

Every comment in `receive_path_bench_test.go` at e56c9aa, verbatim.

## Line 11 — const benchConnID, benchPeerSeq, benchOurSeq, benchWindow

```
// The connection the benchmark drives: the peer's ids and sequence numbers,
// and ours, pinned.
```

## Line 20 — type benchConn

```
// benchConn feeds a socket datagrams from a channel and discards what it
// writes, so the receive path can be measured without a kernel in the way.
```

## Line 27 — type benchConn

```
// wroteAt is when WriteTo last ran: on the benchmark's goroutine, since
	// the reader sends acknowledgements itself.
```

## Line 68 — const benchPayload

```
// benchPayload is the size of each data packet's payload.
```

## Line 71 — func benchReceiver

```
// benchReceiver is an established connection for the receive-path
// benchmarks, accepted from benchConn's peer, and the data packet they feed
// it.
//
// One datagram, its timestamp and sequence number rewritten for each packet:
// each is handled before the next is written, and the receive buffer copies
// the payload, so nothing holds on to it. Building b.N of them up front kept
// hundreds of megabytes alive and timed the garbage collector instead.
```

## Line 107 — func BenchmarkReceivePath

```
// BenchmarkReceivePath is the cost of one in-order data packet on an
// established connection: routing it, handling it, handing its bytes to the
// application and acknowledging it -- what the socket's reader does for each
// datagram, measured warm and without the wake-up that precedes it.
```

## Line 114 — func BenchmarkReceivePath

```
// The reader's own two steps, called here so nothing but the work is
	// timed: no channel between the datagram and the socket, no wake-up. The
	// socket's read loop sits idle meanwhile, waiting on conn.in. What the
	// connection hands up is taken at once, as by an application that never
	// falls behind; one that did would let the receive buffer fill, and the
	// packets after it -- never resent here -- would stall the connection.
```

## Line 140 — func BenchmarkReceivePathCold

```
// BenchmarkReceivePathCold is BenchmarkReceivePath with the caches evicted
// before each packet, as they are on a reader that wakes once a millisecond
// with the rest of the process running in between, and with the packets
// spaced as at 10 Mb/s. Each packet is timed on
// its own and the median reported (cold-ns/op), with the median time to the
// acknowledgement's write (cold-ack-ns/op); the benchmark's own ns/op includes
// the eviction and means nothing.
```

## Line 150 — func BenchmarkReceivePathCold

```
// About what other threads evict between two packets a millisecond apart:
	// with 4 MB evicted this measured 8-9us, against 10-12us timed on a live
	// receiver at 10 Mb/s. A full eviction (64 MB) measured 41-45us.
```

## Line 162 — func BenchmarkReceivePathCold

```
// Packets 1.2 ms apart, as at 10 Mb/s: the receiver acknowledges each
		// one, as it did in the runs this is compared with. Closer together,
		// it holds acknowledgements back and this would time that instead.
```
