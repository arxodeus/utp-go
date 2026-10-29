# recv_buffer.go

Every comment in `recv_buffer.go` at e56c9aa, verbatim.

## Line 9 — const selectiveAckWindow

```
// selectiveAckWindow is how many sequence numbers past `ack_nr + 1` a
	// selective ack reports on, and so how wide the bitfield is.
	//
	// libutp scans `min(14+16, inbuf.size())` entries and always writes
	// exactly one 4-byte word (utp_internal.cpp:797, :805-818). 30 is the
	// constant it uses; the `inbuf.size()` term is the capacity of its
	// circular reorder buffer, an implementation detail we have no equivalent
	// of -- our pending set is a btree with no fixed capacity -- so we always
	// use 30. That makes our window at least as wide as libutp's, never
	// wider, and the extension is the same four bytes either way.
```

## Line 24 — type receiveBuffer

```
// capacity is the configured size: what the window is advertised from
	// and admission is checked against. buf grows towards it as data
	// arrives, and holds the offset bytes not yet read at buf[head:], as
	// libutp's buffers grow as they are used (SizableCircularBuffer::grow,
	// utp_internal.cpp:185-194, behind inbuf and outbuf at :549). History:
	// HISTORY.md, "receiveBuffer.capacity".
```

## Line 37 — type receiveBuffer

```
// largestPacket is the biggest payload this peer has sent, and so the
	// most the gap-filling retransmission can be. See gapReserve.
```

## Line 40 — type receiveBuffer

```
// pendingBytes is the total held out of order in pending, kept as it
	// changes so Available and Pending need not walk the tree for it.
```

## Line 54 — func (*receiveBuffer).gapReserve

```
// gapReserve is the room held back from data arriving out of order, so that
// the packet which fills the gap can always be admitted.
//
// Without it a gap is a deadlock. Data behind the gap is accepted until the
// buffer is full, and then the one packet that would release all of it -- the
// retransmission of the missing packet -- is refused for want of space. It is
// refused every time the peer sends it, and the peer sends it forever.
//
// The figure is the largest packet this peer has sent so far, which is what
// a retransmission of a packet already received will be; for a packet that
// never arrived, Admits takes it when nothing is readable whatever its size.
// It costs that much of the buffer, once. History: HISTORY.md,
// "receiveBuffer.gapReserve".
```

## Line 71 — func (*receiveBuffer).Admits

```
// Admits reports whether a packet of n bytes numbered seqNum may be taken.
//
// Out of order, it must leave gapReserve free. In order, it must fit what is
// free -- or, when nothing is readable, it is taken whatever its size.
//
// The reserve is the largest packet received, and the packet that fills a
// gap can be larger: it is one that never arrived. With a 4,096-byte buffer
// holding twelve 300-byte packets behind a gap, 496 bytes are free and a
// missing 1,400-byte packet never fit; nothing was readable, so no read could
// free room, and the connection stalled for good. A buffer smaller than one
// packet stalled the same way at its first packet. A peer that keeps within
// the advertised window cannot cause either -- everything it may have in
// flight, the missing packet included, fits what that window leaves -- but
// one that sends past it can, and the stall it caused was permanent.
//
// libutp has no receive buffer and never refuses the packet it is waiting
// for. Here the buffer may exceed its capacity by that one packet, and only
// while nothing is readable -- once the packet is in, something is, and the
// rule stops applying until the reader has taken it. See the release review
// of 9c44c4f (R5).
```

## Line 111 — var pendingFreeList

```
// pendingFreeList is the node free list every receive buffer's reorder tree
// shares: each would otherwise allocate a list of its own, per connection,
// that sits empty while packets arrive in order. btree's free lists are safe
// to share between trees.
```

## Line 117 — const minRecvBufAlloc

```
// minRecvBufAlloc is the first allocation, enough for a few packets.
```

## Line 120 — func (*receiveBuffer).appendInOrder

```
// appendInOrder puts data after what is held, making room first: the unread
// bytes move to the front if that is enough, and otherwise the array doubles,
// never past the capacity. Admission has already checked that offset plus
// pending plus data fits the capacity, so the room is always there.
```

## Line 142 — func (*receiveBuffer).releaseStorage

```
// releaseStorage frees the in-order storage when it holds nothing, so an idle
// connection keeps none of what a busy one grew to. The next in-order data
// allocates it again from minRecvBufAlloc. libutp keeps no bytes once it has
// delivered them: each packet is its own allocation, freed when its payload
// is handed to the embedder (utp_internal.cpp:2339-2343, :2407-2417).
```

## Line 153 — func (*receiveBuffer).Window

```
// Window is the receive window to advertise to the peer: the capacity, less
// the bytes already delivered in order and not yet handed up.
//
// Deliberately not Available(). Data held out of order is not counted against
// it, which is libutp's accounting:
//
//	UTPSocket::get_rcv_window  (utp_internal.cpp:590-596)
//	    const size_t numbuf = utp_call_get_read_buffer_size(this->ctx, this);
//	    return opt_rcvbuf > numbuf ? opt_rcvbuf - numbuf : 0;
//
// libutp's window is what its embedder has not yet read. A packet held out of
// order sits in conn->inbuf and never reaches utp_call_on_read until the gap
// before it is filled, so it never enters that figure. Charging it would
// close the window on the peer for holding data the peer was entitled to
// send -- and the window is what permits the peer to retransmit the very
// packet that would fill the gap.
//
// # Why this does not overrun the buffer
//
// Available() still charges the out-of-order bytes, and admission still uses
// it, so offset plus pending stays within the capacity -- but for the one
// packet Admits takes while nothing is readable, which appendInOrder grows
// the storage for.
//
// A peer respecting this window cannot break it either: the window is the
// capacity less what has been delivered, so everything the peer is permitted
// to send fits in what is left, whether it lands in order or behind a gap. A
// peer ignoring the window is caught by admission, which drops rather than
// writes. Over-advertising therefore costs a retransmission, never a panic.
// History: HISTORY.md, "receiveBuffer.Window".
```

## Line 184 — func (*receiveBuffer).Window

```
// Never negative: the in-order data can exceed the capacity by the one
	// packet Admits lets past it.
```

## Line 189 — func (*receiveBuffer).Available

```
// Available is the room left for bytes actually arriving, counting data held
// out of order because that data occupies the buffer when its gap fills.
//
// This is admission control, not the advertised window -- see Window. The two
// differ exactly by the out-of-order bytes, and the difference is load-bearing
// in both directions: advertising this figure stalls a peer that has done
// nothing wrong, and admitting on Window's figure would let offset plus
// pending exceed the capacity and panic the collapse loop in Write.
```

## Line 201 — func (*receiveBuffer).Pending

```
// Pending reports how many bytes have been received -- contiguous or held
// out of order -- but not yet read by the application.
```

## Line 216 — func (*receiveBuffer).WasWritten

```
// At or before the acknowledgement number, in wrapping order: delivered.
	//
	// This was the range from the initial sequence number to the
	// acknowledgement number, which is the same thing until the connection
	// has received 32,768 packets and is everything after 65,535: from then
	// on every new packet was taken for one already delivered and dropped,
	// and the connection stalled. libutp decides it from ack_nr alone:
	// `(pk_seq_nr - conn->ack_nr - 1) & SEQ_NR_MASK` (utp_internal.cpp:1887).
	//
	// Checked first, and the reorder set only when it holds anything, so an
	// in-order packet never touches the tree.
```

## Line 237 — func (*receiveBuffer).HoldsOutOfOrder

```
// HoldsOutOfOrder reports whether seqNum is held past a gap, waiting for it
// to fill.
```

## Line 243 — func (*receiveBuffer).Readable

```
// Readable reports how many contiguous bytes are ready to be read.
//
// It is what a caller needs to size a buffer to the data rather than to the
// largest packet the connection might carry. Bytes held out of order are not
// counted: they cannot be read until the gap before them is filled.
```

## Line 259 — func (*receiveBuffer).Read

```
// The rest stays where it is: it used to be moved to the front on every
	// read, all of it, which costs the whole buffer per small read.
```

## Line 277 — func (*receiveBuffer).Write

```
// The packet that fills the gap is admitted against the whole of what is
	// free. Anything arriving out of order leaves gapReserve behind for it.
	//
	// Without the distinction a gap deadlocks the connection: data behind it
	// fills the buffer, and the retransmission that would release all of it is
	// then refused for want of space, every time, forever. See gapReserve.
```

## Line 291 — func (*receiveBuffer).Write

```
// The next packet in order, with nothing held behind a gap: straight into
	// the buffer. The tree is for packets that arrive ahead of a gap; an
	// in-order one went in and came straight back out of it, allocating each
	// way.
```

## Line 301 — func (*receiveBuffer).Write

```
// Held until the gap before it fills, so it is copied: data may be the
	// socket reader's buffer, reused by the next read (see packet.own).
	// In-order data is copied into the buffer as it is appended.
```

## Line 346 — func (*receiveBuffer).SelectiveAck

```
// The bitfield starts at `ack_nr + 2`: `ack_nr + 1` is by definition the
	// packet we are waiting for, so reporting on it would say nothing
	// (utp_internal.cpp:802-807).
```

## Line 351 — func (*receiveBuffer).SelectiveAck

```
// A fixed window, as libutp uses, rather than one sized to reach the
	// highest pending sequence number: libutp sends no more, and recovers by
	// letting the window slide forward as the gap fills.
	//
	// Only the window is visited, as libutp's fixed-size scan of its own
	// (utp_internal.cpp:800-830) visits only its window. History:
	// HISTORY.md, "receiveBuffer.SelectiveAck: the window".
```

## Line 363 — func (*receiveBuffer).SelectiveAck

```
// The tree is ordered by sequence number as an integer, so a window that
	// wraps past 65535 is two ranges.
```
