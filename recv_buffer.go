package utp_go

import (
	"errors"
	"github.com/ethereum/go-ethereum/log"
	"github.com/google/btree"
)

const (
	// SELECTIVE_ACK_WINDOW is how many sequence numbers past `ack_nr + 1` a
	// selective ack reports on, and so how wide the bitfield is.
	//
	// libutp scans `min(14+16, inbuf.size())` entries and always writes
	// exactly one 4-byte word (utp_internal.cpp:797, :805-818). 30 is the
	// constant it uses; the `inbuf.size()` term is the capacity of its
	// circular reorder buffer, an implementation detail we have no equivalent
	// of -- our pending set is a btree with no fixed capacity -- so we always
	// use 30. That makes our window at least as wide as libutp's, never
	// wider, and the extension is the same four bytes either way.
	SELECTIVE_ACK_WINDOW int = 14 + 16
)

type receiveBuffer struct {
	logger log.Logger
	// capacity is the configured size: what the window is advertised from
	// and admission is checked against. buf grows towards it as data
	// arrives, and holds the offset bytes not yet read at buf[head:].
	//
	// buf was allocated at the full capacity up front -- 1 MB by default --
	// so every connection held a megabyte from the moment it was set up,
	// whether it ever carried data or not: 1.08 MB per connection end that
	// exchanged 1 KB, measured, where libutp holds about 10 KB per socket,
	// its buffers growing as they are used (SizableCircularBuffer::grow,
	// utp_internal.cpp:185-194, behind inbuf and outbuf at :549). With no idle timeout
	// by default, that was a megabyte held for every vanished peer an
	// application did not close. Like libutp's, it grows and is kept.
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
// Measured before this existed, on a 32KB receive buffer with a reader that
// paused for two seconds: `pending 32613, readable 0, held-behind-gap 32613`,
// unchanged for the rest of the run while packets kept arriving and being
// dropped. A 512KB transfer delivered 152KB and stopped. It reached that state
// in about 3 runs in 20.
//
// The figure is the largest packet this peer has sent so far rather than a
// constant, because that is exactly what the gap-filling retransmission will
// be: the peer is resending a packet it already sent, and every packet it has
// sent is at most this. It costs that much of the buffer, once.
func (rb *receiveBuffer) gapReserve() int {
	return rb.largestPacket
}

func newReceiveBuffer(size int, initSeqNum uint16) *receiveBuffer {
	return newReceiveBufferWithLogger(size, initSeqNum, nil)
}

func newReceiveBufferWithLogger(size int, initSeqNum uint16, logger log.Logger) *receiveBuffer {
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

// Window is the receive window to advertise to the peer: the capacity, less
// the bytes already delivered in order and not yet handed up.
//
// Deliberately not Available(). Data held out of order is not counted against
// it, which is libutp's accounting and was not this library's:
//
//	UTPSocket::get_rcv_window  (utp_internal.cpp:590-596)
//	    const size_t numbuf = utp_call_get_read_buffer_size(this->ctx, this);
//	    return opt_rcvbuf > numbuf ? opt_rcvbuf - numbuf : 0;
//
// libutp's window is what its embedder has not yet read. A packet held out of
// order sits in conn->inbuf and never reaches utp_call_on_read until the gap
// before it is filled, so it never enters that figure.
//
// Charging it, as Available() does, closes the window on the peer for holding
// data the peer was entitled to send -- and the window is what permits the
// peer to send, including to retransmit the very packet that would fill the
// gap and let all of it be delivered. Measured before this split: 40,000
// bytes held behind a gap cost exactly 40,000 of advertised window where
// libutp lost none, and 800 packets drove it to 1,376 bytes, under the size of
// one packet and above the zero that would have armed the peer's zero-window
// probe. Both ends then sit silent. See KNOWN-LIMITATIONS.md.
//
// # Why this does not overrun the buffer
//
// Available() still charges the out-of-order bytes, and admission still uses
// Available(), so the invariant the collapse loop in Write depends on --
// offset plus pending never exceeding the capacity, which appendInOrder relies
// on to find its room -- is unchanged.
//
// A peer respecting this window cannot break it either: the window is the
// capacity less what has been delivered, so everything the peer is permitted
// to send fits in what is left, whether it lands in order or behind a gap. A
// peer ignoring the window is caught by admission, which drops rather than
// writes. Over-advertising therefore costs a retransmission, never a panic.
func (rb *receiveBuffer) Window() int {
	return rb.capacity - rb.offset
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
	if rb.logger != nil && rb.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
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
	if rb.logger != nil && rb.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		rb.logger.Trace("will put a data to recv buffer", "seq", seqNum)
	}
	// The packet that fills the gap is admitted against the whole of what is
	// free. Anything arriving out of order leaves gapReserve behind for it.
	//
	// Without the distinction a gap deadlocks the connection: data behind it
	// fills the buffer, and the retransmission that would release all of it is
	// then refused for want of space, every time, forever. See gapReserve.
	next := rb.initSeqNum + 1 + rb.consumed
	room := rb.Available()
	if seqNum != next {
		room -= rb.gapReserve()
	}
	if len(data) > room {
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

	if rb.logger != nil && rb.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
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
		if rb.logger != nil && rb.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			rb.logger.Trace("will delete a pending data in recv buffer", "seq", next, "pending.len", rb.pending.Len())
		}
		next += 1
	}
	if rb.logger != nil && rb.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		rb.logger.Trace("handled pending data in recv buffer", "endSeq", next)
	}
	return nil
}

func (rb *receiveBuffer) AckNum() uint16 {
	return rb.initSeqNum + rb.consumed
}

func (rb *receiveBuffer) SelectiveAck() *SelectiveAck {
	if rb.pending.Len() == 0 {
		return nil
	}

	// The bitfield starts at `ack_nr + 2`: `ack_nr + 1` is by definition the
	// packet we are waiting for, so reporting on it would say nothing
	// (utp_internal.cpp:802-807).
	base := rb.AckNum() + 2

	pendingSeqs := make(map[uint16]bool, rb.pending.Len())
	rb.pending.Ascend(func(i btree.Item) bool {
		item := i.(*pendingItem)
		pendingSeqs[item.seqNum] = true
		return true
	})

	// A fixed window, as libutp uses, rather than one sized to reach the
	// highest pending sequence number.
	//
	// This previously grew until every pending packet was covered, up to 2016
	// bits. A peer that leaves a gap open and then delivers a packet far past
	// it -- reordering, or deliberately -- made us answer every subsequent
	// packet with a 254-byte extension where libutp answers with six bytes.
	// The extra bits carry real information, but libutp does not send them
	// and recovers by letting the window slide forward as the gap fills, so
	// sending them buys nothing against a libutp peer and costs bandwidth on
	// the ack path exactly when the path is already in trouble.
	acked := make([]bool, SELECTIVE_ACK_WINDOW)
	for i := 0; i < SELECTIVE_ACK_WINDOW; i++ {
		acked[i] = pendingSeqs[base+uint16(i)]
	}

	if rb.logger != nil && rb.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		rb.logger.Trace("will new selective ack", "base", base, "acked.len", len(acked))
	}

	return NewSelectiveAck(acked)
}

func (rb *receiveBuffer) close() {
}
