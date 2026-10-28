# conformance_ack_test.go

Every comment in `conformance_ack_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build cgo
```

## Line 11 — func TestConformanceAckCoalescing

```
// TestConformanceAckCoalescing compares how many STATE packets each stack
// emits for a batch of ST_DATA packets delivered back to back.
//
// libutp does not acknowledge a packet at the point it processes it. It calls
// schedule_ack() (utp_internal.cpp:2377), which only sets a flag, and the
// embedder later calls utp_issue_deferred_acks() once per read batch
// (utp.h:512-517, utp_internal.cpp:3796-3808). An entire batch therefore costs
// libutp exactly one STATE packet, however many data packets it holds — and
// that is asserted here on every run rather than hard-coded, so the test fails
// if the reference ever changes instead of pinning a stale assumption.
//
// We match it. The socket's reader handles each packet as it reads it and
// sends the acknowledgement once nothing more is waiting (UtpSocket.readLoop,
// QueuedReader), which is libutp's embedder loop.
//
// This used to be unmatchable, and asserted only "no more acks than data
// packets": the packets crossed three goroutines -- read loop, dispatcher,
// connection event loop -- so how many shared a pass depended on the
// scheduler. A fixed bound of two held on a plain run and failed under -race.
// Handled on the reader, the batch is what was queued, not what the scheduler
// happened to group.
```

## Line 51 — func libutpAcksForBatch

```
// libutpAcksForBatch drives a real libutp responder through the handshake,
// injects batch ST_DATA packets, and returns the number of packets it emits
// when the embedder flushes deferred acks.
```

## Line 76 — func ourAcksForBatch

```
// ourAcksForBatch drives our responder through the same sequence.
```

## Line 92 — func ourAcksForBatch

```
// Queue the whole batch before any of it is delivered, so what is measured
	// is our coalescing and not the test goroutine's injection speed. This is
	// the same starting position libutp's embedder gives it.
```

## Line 101 — func ourAcksForBatch

```
// settle() returns after one quiet period; a batch may still span two
	// passes, so wait long enough that a third would have happened.
```

## Line 107 — func ackBatchPackets

```
// ackBatchPackets builds batch in-order single-byte ST_DATA packets following
// the corpus handshake, each acknowledging the responder's own SYN-ACK.
```
