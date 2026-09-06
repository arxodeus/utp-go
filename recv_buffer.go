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
}

type pendingItem struct {
	seqNum uint16
	data   []byte
}

func (i *pendingItem) Less(other btree.Item) bool {
	return i.seqNum < other.(*pendingItem).seqNum
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
	if len(data) > rb.Available() {
		return errors.New("insufficient space in buffer")
	}

	rb.pending.ReplaceOrInsert(&pendingItem{seqNum: seqNum, data: data})

	//start := rb.initSeqNum + 1
	next := rb.initSeqNum + 1 + rb.consumed
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
