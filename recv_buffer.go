package utp

import (
	"errors"
	"github.com/google/btree"
)

const (
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
	selectiveAckWindow int = 14 + 16
)

type receiveBuffer struct {
	logger *ulogger
	// capacity is the configured size: what the window is advertised from
	// and admission is checked against. buf grows towards it as data
	// arrives, and holds the offset bytes not yet read at buf[head:], as
	// libutp's buffers grow as they are used (SizableCircularBuffer::grow,
	// utp_internal.cpp:185-194, behind inbuf and outbuf at :549). History:
	// HISTORY.md, "receiveBuffer.capacity".
	capacity   int
	buf        []byte
	head       int
	offset     int
	pending    *btree.BTree
	initSeqNum uint16
	consumed   uint16
	// largestPacket is the biggest payload this peer has sent, and so the
	// most the gap-filling retransmission can be. See gapReserve.
	largestPacket int
	// pendingBytes is the total held out of order in pending, kept as it
	// changes so Available and Pending need not walk the tree for it.
	pendingBytes int
}

type pendingItem struct {
	seqNum uint16
	data   []byte
}

func (i *pendingItem) Less(other btree.Item) bool {
	return i.seqNum < other.(*pendingItem).seqNum
}

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
func (rb *receiveBuffer) gapReserve() int {
	return rb.largestPacket
}

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
func (rb *receiveBuffer) Admits(seqNum uint16, n int) bool {
	if seqNum != rb.initSeqNum+1+rb.consumed {
		return n <= rb.Available()-rb.gapReserve()
	}
	return n <= rb.Available() || (rb.offset == 0 && n <= maxUdpPayloadSize)
}

func newReceiveBuffer(size int, initSeqNum uint16) *receiveBuffer {
	return newReceiveBufferWithLogger(size, initSeqNum, nil)
}

func newReceiveBufferWithLogger(size int, initSeqNum uint16, logger *ulogger) *receiveBuffer {
	return &receiveBuffer{
		logger:     logger,
		capacity:   size,
		pending:    btree.NewWithFreeList(2, pendingFreeList),
		initSeqNum: initSeqNum,
	}
}

// pendingFreeList is the node free list every receive buffer's reorder tree
// shares: each would otherwise allocate a list of its own, per connection,
// that sits empty while packets arrive in order. btree's free lists are safe
// to share between trees.
var pendingFreeList = btree.NewFreeList(btree.DefaultFreeListSize)

// minRecvBufAlloc is the first allocation, enough for a few packets.
const minRecvBufAlloc = 4 * 1024

// appendInOrder puts data after what is held, making room first: the unread
// bytes move to the front if that is enough, and otherwise the array doubles,
// never past the capacity. Admission has already checked that offset plus
// pending plus data fits the capacity, so the room is always there.
func (rb *receiveBuffer) appendInOrder(data []byte) {
	need := rb.offset + len(data)
	if rb.head+need > len(rb.buf) {
		if need <= len(rb.buf) {
			copy(rb.buf, rb.buf[rb.head:rb.head+rb.offset])
		} else {
			size := max(len(rb.buf)*2, minRecvBufAlloc, need)
			size = min(size, max(rb.capacity, need))
			grown := make([]byte, size)
			copy(grown, rb.buf[rb.head:rb.head+rb.offset])
			rb.buf = grown
		}
		rb.head = 0
	}
	copy(rb.buf[rb.head+rb.offset:], data)
	rb.offset = need
}

// releaseStorage frees the in-order storage when it holds nothing, so an idle
// connection keeps none of what a busy one grew to. The next in-order data
// allocates it again from minRecvBufAlloc. libutp keeps no bytes once it has
// delivered them: each packet is its own allocation, freed when its payload
// is handed to the embedder (utp_internal.cpp:2339-2343, :2407-2417).
func (rb *receiveBuffer) releaseStorage() {
	if rb.offset == 0 {
		rb.buf, rb.head = nil, 0
	}
}

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
func (rb *receiveBuffer) Window() int {
	// Never negative: the in-order data can exceed the capacity by the one
	// packet Admits lets past it.
	return max(rb.capacity-rb.offset, 0)
}

// Available is the room left for bytes actually arriving, counting data held
// out of order because that data occupies the buffer when its gap fills.
//
// This is admission control, not the advertised window -- see Window. The two
// differ exactly by the out-of-order bytes, and the difference is load-bearing
// in both directions: advertising this figure stalls a peer that has done
// nothing wrong, and admitting on Window's figure would let offset plus
// pending exceed the capacity and panic the collapse loop in Write.
func (rb *receiveBuffer) Available() int {
	return rb.capacity - rb.offset - rb.pendingBytes
}

// Pending reports how many bytes have been received -- contiguous or held
// out of order -- but not yet read by the application.
func (rb *receiveBuffer) Pending() int {
	return rb.offset + rb.pendingBytes
}

func (rb *receiveBuffer) IsEmpty() bool {
	return rb.offset == 0 && rb.pending.Len() == 0
}

func (rb *receiveBuffer) InitSeqNum() uint16 {
	return rb.initSeqNum
}

func (rb *receiveBuffer) WasWritten(seqNum uint16) bool {
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
	if !wrappingLessThan(rb.AckNum(), seqNum) {
		return true
	}
	exists := rb.pending.Len() > 0 && rb.pending.Has(&pendingItem{seqNum: seqNum})
	if rb.logger != nil && rb.logger.Enabled(baseContext, LevelTrace) {
		rb.logger.Trace("checking written", "seqNum", seqNum, "initSeqNum", rb.initSeqNum, "consumed", rb.consumed, "exists", exists)
	}
	return exists
}

// HoldsOutOfOrder reports whether seqNum is held past a gap, waiting for it
// to fill.
func (rb *receiveBuffer) HoldsOutOfOrder(seqNum uint16) bool {
	return rb.pending.Has(&pendingItem{seqNum: seqNum})
}

// Readable reports how many contiguous bytes are ready to be read.
//
// It is what a caller needs to size a buffer to the data rather than to the
// largest packet the connection might carry. Bytes held out of order are not
// counted: they cannot be read until the gap before them is filled.
func (rb *receiveBuffer) Readable() int {
	return rb.offset
}

func (rb *receiveBuffer) Read(buf []byte) int {
	if len(buf) == 0 {
		return 0
	}

	n := minInt(len(buf), rb.offset)
	copy(buf, rb.buf[rb.head:rb.head+n])
	// The rest stays where it is: it used to be moved to the front on every
	// read, all of it, which costs the whole buffer per small read.
	rb.head += n
	rb.offset -= n
	if rb.offset == 0 {
		rb.head = 0
	}

	return n
}

func (rb *receiveBuffer) Write(data []byte, seqNum uint16) error {
	if rb.WasWritten(seqNum) {
		return nil
	}
	if rb.logger != nil && rb.logger.Enabled(baseContext, LevelTrace) {
		rb.logger.Trace("will put a data to recv buffer", "seq", seqNum)
	}
	// The packet that fills the gap is admitted against the whole of what is
	// free. Anything arriving out of order leaves gapReserve behind for it.
	//
	// Without the distinction a gap deadlocks the connection: data behind it
	// fills the buffer, and the retransmission that would release all of it is
	// then refused for want of space, every time, forever. See gapReserve.
	next := rb.initSeqNum + 1 + rb.consumed
	if !rb.Admits(seqNum, len(data)) {
		return errors.New("insufficient space in buffer")
	}
	if len(data) > rb.largestPacket {
		rb.largestPacket = len(data)
	}

	// The next packet in order, with nothing held behind a gap: straight into
	// the buffer. The tree is for packets that arrive ahead of a gap; an
	// in-order one went in and came straight back out of it, allocating each
	// way.
	if seqNum == next && rb.pending.Len() == 0 {
		rb.appendInOrder(data)
		rb.consumed++
		return nil
	}

	// Held until the gap before it fills, so it is copied: data may be the
	// socket reader's buffer, reused by the next read (see packet.own).
	// In-order data is copied into the buffer as it is appended.
	data = append([]byte(nil), data...)
	if old := rb.pending.ReplaceOrInsert(&pendingItem{seqNum: seqNum, data: data}); old != nil {
		rb.pendingBytes -= len(old.(*pendingItem).data)
	}
	rb.pendingBytes += len(data)

	if rb.logger != nil && rb.logger.Enabled(baseContext, LevelTrace) {
		rb.logger.Trace("will handle pending data in recv buffer", "startSeq", next)
	}

	for {
		item := rb.pending.Get(&pendingItem{seqNum: next})
		if item == nil {
			break
		}

		pending := item.(*pendingItem)

		rb.appendInOrder(pending.data)
		rb.consumed += 1
		rb.pending.Delete(pending)
		rb.pendingBytes -= len(pending.data)
		if rb.logger != nil && rb.logger.Enabled(baseContext, LevelTrace) {
			rb.logger.Trace("will delete a pending data in recv buffer", "seq", next, "pending.len", rb.pending.Len())
		}
		next += 1
	}
	if rb.logger != nil && rb.logger.Enabled(baseContext, LevelTrace) {
		rb.logger.Trace("handled pending data in recv buffer", "endSeq", next)
	}
	return nil
}

func (rb *receiveBuffer) AckNum() uint16 {
	return rb.initSeqNum + rb.consumed
}

func (rb *receiveBuffer) SelectiveAck() *selectiveAck {
	if rb.pending.Len() == 0 {
		return nil
	}

	// The bitfield starts at `ack_nr + 2`: `ack_nr + 1` is by definition the
	// packet we are waiting for, so reporting on it would say nothing
	// (utp_internal.cpp:802-807).
	base := rb.AckNum() + 2

	// A fixed window, as libutp uses, rather than one sized to reach the
	// highest pending sequence number: libutp sends no more, and recovers by
	// letting the window slide forward as the gap fills.
	//
	// Only the window is visited, as libutp's fixed-size scan of its own
	// (utp_internal.cpp:800-830) visits only its window. History:
	// HISTORY.md, "receiveBuffer.SelectiveAck: the window".
	acked := make([]bool, selectiveAckWindow)
	mark := func(i btree.Item) bool {
		acked[i.(*pendingItem).seqNum-base] = true
		return true
	}
	// The tree is ordered by sequence number as an integer, so a window that
	// wraps past 65535 is two ranges.
	end := base + uint16(selectiveAckWindow)
	if end > base {
		rb.pending.AscendRange(&pendingItem{seqNum: base}, &pendingItem{seqNum: end}, mark)
	} else {
		rb.pending.AscendGreaterOrEqual(&pendingItem{seqNum: base}, mark)
		rb.pending.AscendLessThan(&pendingItem{seqNum: end}, mark)
	}

	if rb.logger != nil && rb.logger.Enabled(baseContext, LevelTrace) {
		rb.logger.Trace("will new selective ack", "base", base, "acked.len", len(acked))
	}

	return newSelectiveAck(acked)
}

func (rb *receiveBuffer) close() {
}
