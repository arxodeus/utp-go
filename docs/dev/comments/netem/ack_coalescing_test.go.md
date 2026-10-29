# netem/ack_coalescing_test.go

Every comment in `netem/ack_coalescing_test.go` at e56c9aa, verbatim.

## Line 9 — func TestAckCoalescingUnderLoad

```
// TestAckCoalescingUnderLoad measures how many acknowledgements the receiver
// puts on the return path per data packet it receives, on a real transfer.
//
// The connection defers acks -- connection.ackPending is set when data arrives
// and flushAck sends one once the read that brought the data has been handed
// out, matching libutp's schedule_ack / utp_issue_deferred_acks
// (utp_internal.cpp:2377, :3796-3808) -- and lets one wait to cover a few
// packets only once its acknowledgements have been seen to queue on the way
// back (connection.ackEvery). Where the return path keeps up, it acknowledges
// as libutp does; this network hands each datagram to the receiver on its
// own, so that is about one acknowledgement per data packet.
//
// So the assertion is on the path that cannot keep up: 20 Mb/s of data
// draws about 1,750 acknowledgements a second, and 160 kb/s carries about
// 1,000. Measured 0.61-0.62 acknowledgements per data packet there, three
// runs on Linux at 69e2b75 (0.51-0.52 when first written), and 1.00 with
// ackEvery returning 1; once 0.83 on a macOS CI runner. The symmetric rows
// are reported only: 1.00 at 20 Mb/s, 100 Mb/s and 1 Gb/s.
```

## Line 31 — func TestAckCoalescingUnderLoad

```
// 0: the same as bps
```

## Line 33 — func TestAckCoalescingUnderLoad

```
// 0 means "report only, do not assert"
```

## Line 61 — func ackRatioTransfer

```
// ackRatioTransfer runs one verified transfer and returns the number of packets
// offered to the forward link and to the return link. The forward link carries
// data, the return link carries acks; neither link loses anything here, so
// "offered" is what was sent.
```
