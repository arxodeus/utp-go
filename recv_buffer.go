package utp

import (
	"errors"
	"github.com/google/btree"
)

const (
	// selectiveAckWindow is how many sequence numbers past ack_nr+1 a
	// selective ack covers: 30, in one 4-byte bitfield whose last two bits
	// are padding.
	selectiveAckWindow int = 14 + 16
)

type receiveBuffer struct {
	logger *ulogger
	// capacity is the configured size, from which the window is advertised
	// and against which admission is checked. buf grows towards it as data
	// arrives, and holds the unread bytes at buf[head:].
	capacity   int
	buf        []byte
	head       int
	offset     int
	pending    *btree.BTree
	initSeqNum uint16
	consumed   uint16
	// largestPacket is the largest payload this peer has sent, and so the
	// largest a gap-filling retransmission can be. See gapReserve.
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

// gapReserve is the room held back from out-of-order data, so the packet
// that fills a gap can always be admitted. Without it, data behind a gap
// could fill the buffer and the retransmission that releases it would be
// refused every time.
//
// It is the largest packet this peer has sent, which is the size of a
// retransmission of a packet already seen. A packet that never arrived is
// admitted by Admits whatever its size when nothing is readable.
func (rb *receiveBuffer) gapReserve() int {
	return rb.largestPacket
}

// Admits reports whether a packet of n bytes numbered seqNum may be taken.
//
// Out of order, it must leave gapReserve free. In order, it must fit in the
// free space or, when nothing is readable, it is taken whatever its size.
// The latter lets the buffer exceed its capacity by one packet, so a
// connection cannot stall waiting for a packet larger than the reserve; once
// that packet is in, something is readable and the rule no longer applies.
func (rb *receiveBuffer) Admits(seqNum uint16, n int) bool {
	if seqNum != rb.initSeqNum+1+rb.consumed {
		return n <= rb.Available()-rb.gapReserve()
	}
	return n <= rb.Available() || (rb.offset == 0 && n <= maxUDPPayloadSize)
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

// pendingFreeList is the node free list shared by every receive buffer's
// reorder tree, rather than one per connection.
var pendingFreeList = btree.NewFreeList(btree.DefaultFreeListSize)

// minRecvBufAlloc is the first allocation, enough for a few packets.
const minRecvBufAlloc = 4 * 1024

// appendInOrder appends data after what is held, making room first: the
// unread bytes move to the front if that suffices, and otherwise the array
// doubles, up to the capacity. Admission has already checked that the data
// fits.
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

// releaseStorage frees the in-order storage when it is empty, so an idle
// connection keeps none of what a busy one grew to. The next in-order data
// reallocates it from minRecvBufAlloc.
func (rb *receiveBuffer) releaseStorage() {
	if rb.offset == 0 {
		rb.buf, rb.head = nil, 0
	}
}

// Window returns the receive window to advertise: the capacity less the
// bytes delivered in order and not yet read.
//
// Unlike Available, it does not count data held out of order. That data is
// not readable until its gap fills, and charging it would shrink the window
// the peer needs to retransmit the very packet that fills the gap.
//
// Admission uses Available, so the buffer still cannot overrun, except by
// the one packet Admits accepts when nothing is readable. A peer that
// respects this window cannot overrun it either; one that ignores it is
// caught by admission, which drops rather than writes.
func (rb *receiveBuffer) Window() int {
	// Never negative: the in-order data can exceed the capacity by the one
	// packet Admits lets through.
	return max(rb.capacity-rb.offset, 0)
}

// Available returns the room left for arriving bytes, counting data held out
// of order, which occupies the buffer once its gap fills. It is used for
// admission, not advertised; see Window.
func (rb *receiveBuffer) Available() int {
	return rb.capacity - rb.offset - rb.pendingBytes
}

// Pending reports how many bytes have been received, contiguous or held out
// of order, but not yet read.
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
	// At or before the acknowledgement number, in wrapping order: already
	// delivered. Check this first, and the reorder set only if it is not
	// empty, so an in-order packet never touches the tree.
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

// Readable reports how many contiguous bytes are ready to be read. Bytes
// held out of order are not counted.
func (rb *receiveBuffer) Readable() int {
	return rb.offset
}

func (rb *receiveBuffer) Read(buf []byte) int {
	if len(buf) == 0 {
		return 0
	}

	n := minInt(len(buf), rb.offset)
	copy(buf, rb.buf[rb.head:rb.head+n])
	// Leave the rest in place rather than moving it to the front on every
	// read.
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
	// The packet that fills the gap is admitted against all free space;
	// anything out of order must leave gapReserve free. See gapReserve.
	next := rb.initSeqNum + 1 + rb.consumed
	if !rb.Admits(seqNum, len(data)) {
		return errors.New("utp: insufficient space in buffer")
	}
	if len(data) > rb.largestPacket {
		rb.largestPacket = len(data)
	}

	// The next packet in order with nothing held behind a gap goes straight
	// into the buffer, bypassing the tree.
	if seqNum == next && rb.pending.Len() == 0 {
		rb.appendInOrder(data)
		rb.consumed++
		return nil
	}

	// Held until its gap fills, so copy it: data may be the reader's
	// buffer, which the next read reuses (see packet.own).
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

	// The bitfield starts at ack_nr+2: ack_nr+1 is the packet being waited
	// for, so reporting on it would say nothing.
	base := rb.AckNum() + 2

	// A fixed window, which slides forward as the gap fills. Only the
	// window is visited.
	acked := make([]bool, selectiveAckWindow)
	mark := func(i btree.Item) bool {
		acked[i.(*pendingItem).seqNum-base] = true
		return true
	}
	// The tree is ordered by sequence number as an integer, so a window
	// that wraps past 65535 is two ranges.
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
