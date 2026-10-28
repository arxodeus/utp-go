# recv_buffer_gap_test.go

Every comment in `recv_buffer_gap_test.go` at e56c9aa, verbatim.

## Line 5 — func TestGapFillingPacketIsAdmittedWhenTheBufferIsFull

```
// The packet that fills a gap is always admitted.
//
// Without room held back for it, a gap is a deadlock: data behind the gap is
// accepted until the buffer is full, and then the retransmission that would
// release all of it is refused for want of space -- every time the peer sends
// it, and the peer sends it forever. Nothing else can free the buffer, because
// nothing behind the gap can be delivered until the gap closes.
//
// This is that situation, built directly rather than waited for.
```

## Line 17 — func TestGapFillingPacketIsAdmittedWhenTheBufferIsFull

```
// initSeqNum is 100, so 101 is the gap
```

## Line 21 — func TestGapFillingPacketIsAdmittedWhenTheBufferIsFull

```
// Everything from firstSeq+1 onwards, leaving firstSeq missing. Stop when
	// the buffer refuses one, which is the state the deadlock needs.
```

## Line 40 — func TestGapFillingPacketIsAdmittedWhenTheBufferIsFull

```
// The buffer must be full enough that an out-of-order packet no longer
	// fits, or the reserve is not being tested.
```

## Line 47 — func TestGapFillingPacketIsAdmittedWhenTheBufferIsFull

```
// And now the one that matters: the gap.
```

## Line 53 — func TestGapFillingPacketIsAdmittedWhenTheBufferIsFull

```
// Filling it releases everything behind it.
```

## Line 64 — func TestGapReserveCostsOnePacket

```
// The reserve costs one packet's worth of buffer and no more.
```

## Line 80 — func TestGapReserveCostsOnePacket

```
// Out-of-order data is held to the capacity less one packet's reserve, so
	// the free space left over is at least the reserve and less than the
	// reserve plus another packet.
```

## Line 96 — func TestInOrderDataIsNotHeldBackByTheReserve

```
// In-order data is not subject to the reserve at all: a buffer filling up in
// order fills completely.
```
