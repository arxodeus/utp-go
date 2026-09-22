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
	logger     log.Logger
	buf        []byte
	offset     int
	pending    *btree.BTree
	initSeqNum uint16
	consumed   uint16
	// largestPacket is the biggest payload this peer has sent, and so the
	// most the gap-filling retransmission can be. See gapReserve.
	largestPacket int
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
	buf := make([]byte, size)
	return &receiveBuffer{
		buf:        buf,
		offset:     0,
		pending:    btree.New(2),
		initSeqNum: initSeqNum,
		consumed:   0,
	}
}

func newReceiveBufferWithLogger(size int, initSeqNum uint16, logger log.Logger) *receiveBuffer {
	buf := make([]byte, size)
	return &receiveBuffer{
		logger:     logger,
		buf:        buf,
		offset:     0,
		pending:    btree.New(2),
		initSeqNum: initSeqNum,
		consumed:   0,
	}
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
// offset plus pending never exceeding the capacity, or `rb.buf[rb.offset:end]`
// panics -- is unchanged.
//
// A peer respecting this window cannot break it either: the window is the
// capacity less what has been delivered, so everything the peer is permitted
// to send fits in what is left, whether it lands in order or behind a gap. A
// peer ignoring the window is caught by admission, which drops rather than
// writes. Over-advertising therefore costs a retransmission, never a panic.
func (rb *receiveBuffer) Window() int {
	return len(rb.buf) - rb.offset
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
	available := len(rb.buf) - rb.offset

	rb.pending.Ascend(func(i btree.Item) bool {
		item := i.(*pendingItem)
		available -= len(item.data)
		return true
	})
	return available
}

// Pending reports how many bytes have been received -- contiguous or held
// out of order -- but not yet read by the application.
func (rb *receiveBuffer) Pending() int {
	pending := rb.offset
	rb.pending.Ascend(func(i btree.Item) bool {
		pending += len(i.(*pendingItem).data)
		return true
	})
	return pending
}

func (rb *receiveBuffer) IsEmpty() bool {
	return rb.offset == 0 && rb.pending.Len() == 0
}

func (rb *receiveBuffer) InitSeqNum() uint16 {
	return rb.initSeqNum
}

func (rb *receiveBuffer) WasWritten(seqNum uint16) bool {
	exists := rb.pending.Has(&pendingItem{seqNum: seqNum})
	if rb.logger != nil && rb.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		rb.logger.Trace("checking written", "seqNum", seqNum, "initSeqNum", rb.initSeqNum, "consumed", rb.consumed, "exists", exists)
	}
	writtenRange := circularRangeInclusive{start: rb.initSeqNum, end: rb.initSeqNum + rb.consumed}
	return exists || writtenRange.Contains(seqNum)
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
	copy(buf, rb.buf[:n])

	remaining := rb.offset - n
	copy(rb.buf, rb.buf[n:n+remaining])
	rb.offset = remaining

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

	rb.pending.ReplaceOrInsert(&pendingItem{seqNum: seqNum, data: data})

	if rb.logger != nil && rb.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		rb.logger.Trace("will handle pending data in recv buffer", "startSeq", next)
	}

	for {
		item := rb.pending.Get(&pendingItem{seqNum: next})
		if item == nil {
			break
		}

		pending := item.(*pendingItem)

		end := rb.offset + len(pending.data)
		copy(rb.buf[rb.offset:end], pending.data)
		rb.offset = end
		rb.consumed += 1
		rb.pending.Delete(pending)
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
