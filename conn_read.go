package utp

import (
	"sync"
)

type readOrWriteResult struct {
	Err  error
	Len  int
	Data []byte
	// pooled marks a chunk from readChunks, which the reader returns once it
	// has copied it out. See newReadChunk.
	pooled bool
}

// readChunks pools the chunks received data is handed to the reader in.
var readChunks sync.Pool

// pooledReadChunkMin is the minimum size of a chunk taken from readChunks.
// A pooled buffer is as large as the largest packet, so smaller chunks are
// allocated to fit, and a reader that falls behind on a trickle of small
// writes does not hold a full packet's memory for each.
const pooledReadChunkMin = 512

// newReadChunk returns a result holding n bytes of received data. size is
// the largest any chunk on this connection holds.
func newReadChunk(n, size int) *readOrWriteResult {
	if n >= pooledReadChunkMin {
		if r, _ := readChunks.Get().(*readOrWriteResult); r != nil && cap(r.Data) >= n {
			r.Data, r.Len = r.Data[:n], n
			return r
		}
		return &readOrWriteResult{Data: make([]byte, n, max(n, size)), Len: n, pooled: true}
	}
	return &readOrWriteResult{Data: make([]byte, n), Len: n}
}

// release returns a chunk the reader has finished with to readChunks.
func (r *readOrWriteResult) release() {
	if r.pooled {
		r.Data, r.Len, r.Err = r.Data[:0], 0, nil
		readChunks.Put(r)
	}
}

func (c *connection) processReads() {
	if c.state.stateType == connConnecting {
		return
	}
	recvBuf := c.state.RecvBuf

	if recvBuf != nil && c.logger.Enabled(baseContext, LevelTrace) {
		c.logger.Trace("read data saving in the recvBuf, start...", "available", recvBuf.Available(), "isEmpty", recvBuf.IsEmpty())
	}
	// Drain contiguous data even after the connection has closed: bytes that
	// arrived before teardown are still owed to the reader.
	//
	// Never block the event loop on the application. A slow reader is
	// handled by the advertised receive window shrinking, since unread
	// bytes stay in the receive buffer.
	//
	// Every sender on c.reads holds mu, so a free slot seen here is still
	// free at the send below.
	drained := true
	for recvBuf != nil && !recvBuf.IsEmpty() {
		if len(c.reads) == cap(c.reads) {
			drained = false
			break
		}
		// Size the buffer to the data rather than the largest packet.
		readable := recvBuf.Readable()
		if readable == 0 {
			break
		}
		if maxSize := int(c.config.MaxPacketSize); readable > maxSize {
			readable = maxSize
		}
		chunk := newReadChunk(readable, int(c.config.MaxPacketSize))
		n := recvBuf.Read(chunk.Data)
		if n == 0 {
			chunk.release()
			break
		}
		chunk.Data, chunk.Len = chunk.Data[:n], n
		if !c.sendRead(chunk) {
			return
		}
	}
	if recvBuf != nil && c.logger.Enabled(baseContext, LevelTrace) {
		c.logger.Trace("read data saving in the recvBuf, end...", "available", recvBuf.Available(), "isEmpty", recvBuf.IsEmpty())
	}

	// At end of stream, deliver the end-of-stream marker, but only after
	// everything already received. eof() means every byte has arrived, not
	// that it has been handed to the reader; the drain above can stop early.
	if drained && c.eof() {
		c.deliverTerminalRead()
	}
	// Bytes waiting for a reader with a full queue wait until it makes
	// room; only then does a read reopen the receive window. See
	// Stream.notifyRead.
	c.readsHeld.Store(recvBuf != nil && recvBuf.Readable() > 0)

	c.onReadDrained()
}

// drainable returns how many bytes processReads would hand the reader now:
// the contiguous bytes in the receive buffer, up to MaxPacketSize for each
// free slot in the reader's queue.
func (c *connection) drainable() int {
	if c.state.stateType == connConnecting || c.state.RecvBuf == nil {
		return 0
	}
	free := cap(c.reads) - len(c.reads)
	if free <= 0 {
		return 0
	}
	return min(c.state.RecvBuf.Readable(), free*int(c.config.MaxPacketSize))
}

// recvWindow is the receive window an outgoing packet advertises: the
// buffer's, plus what this pass will hand the reader after the packet goes
// (drainCredit). It never exceeds the buffer: the credit is bytes the buffer
// holds.
func (c *connection) recvWindow() uint32 {
	return uint32(c.state.RecvBuf.Window() + c.drainCredit)
}

// onReadDrained tells the peer when handing bytes to the reader has opened a
// receive window larger than the one last advertised. Without it, a peer
// blocked on a window too small to use sends nothing, and so draws no
// acknowledgement that would tell it the window opened. The acknowledgement
// is sent by flushAck at the end of this pass.
func (c *connection) onReadDrained() {
	if c.state == nil || c.state.stateType != connConnected || c.state.RecvBuf == nil {
		return
	}
	if uint32(c.state.RecvBuf.Window()) > c.lastAdvertisedWindow {
		c.ackPending = true
		c.readDrainedAcks++
	}
}

// drainReadsForTeardown hands over everything left in the receive buffer as
// the connection ends.
//
// Unlike processReads it waits for room in the read queue, since anything
// not delivered now is lost. The wait ends when the connection's context
// ends or the consumer closes the stream: a consumer that closed without
// reading is not owed the bytes, and Stream.Close is itself waiting for
// this goroutine.
func (c *connection) drainReadsForTeardown() {
	recvBuf := c.state.RecvBuf
	for recvBuf != nil && !recvBuf.IsEmpty() {
		buf := make([]byte, c.config.MaxPacketSize)
		n := recvBuf.Read(buf)
		if n == 0 {
			break
		}
		select {
		case c.reads <- &readOrWriteResult{Data: buf, Len: n}:
		case <-c.ctx.Done():
			c.readsTruncated = true
			return
		case <-c.abandoned:
			c.logger.Debug("stream closed by its consumer; dropping undelivered received bytes",
				"bytes", n)
			c.readsTruncated = true
			return
		}
	}
	if c.eof() {
		c.deliverTerminalRead()
	}
}

// deliverTerminalRead delivers the end-of-stream marker, a result with an
// empty Data slice, at most once. Every path that closes the connection must
// reach it, including idle timeout, RESET and a local close.
func (c *connection) deliverTerminalRead() {
	if c.readsTerminated {
		return
	}
	c.readsTerminated = true
	// A clean end of stream carries no error, as with io.ReadAll. Only an
	// abnormal close (RESET, idle timeout) carries one. See readEndErr.
	err := c.readEndErr()
	c.logger.Debug("read eof...", "err", err)

	// Publish the reason before delivery, so it is available however the
	// reader learns that the stream ended.
	c.terminalErr.Store(&terminalError{err: err})

	// Do not block the event loop here either: Stream.Close waits for
	// this goroutine, and only the reader drains c.reads. If there is no
	// room the marker is not sent; closing c.reads when the loop exits
	// signals the end just as well, with the error in terminalErr.
	select {
	case c.reads <- &readOrWriteResult{Err: err, Data: make([]byte, 0)}:
	default:
		c.readsTerminated = false // let a later pass deliver it if room appears
		c.logger.Debug("read queue full at end of stream; deferring the marker", "err", err)
	}
}

// readEndErr returns what the reader is told when the stream ends: the error
// that ended the connection, or nil for a clean end. A clean end means the
// reader has received everything the peer sent before its FIN. A connection
// that ended without error short of that reports ErrReadClosed.
//
// Once everything before the peer's FIN has arrived and been delivered, the
// read side has ended cleanly whatever happens afterwards. A peer that closed
// first may answer a later window update with a RESET; that is reported as an
// error but does not take back the data.
func (c *connection) readEndErr() error {
	if !c.readsTruncated && c.receivedThroughFin() {
		return nil
	}
	if c.state.Err != nil {
		return c.state.Err
	}
	return ErrReadClosed
}

// receivedThroughFin reports whether every byte the peer sent before its FIN
// has arrived. eof() asks the same of a connection still open.
func (c *connection) receivedThroughFin() bool {
	st := c.state
	return st.closing != nil && st.closing.RemoteFin != nil && st.RecvBuf != nil &&
		st.RecvBuf.AckNum() == *st.closing.RemoteFin
}

// terminalError boxes the end-of-stream error so it can be stored atomically,
// including the nil case that means a clean end of stream.
type terminalError struct{ err error }

// sendRead delivers a read result, giving up if the connection's context is
// canceled, so a reader that has gone away cannot block the event loop.
func (c *connection) sendRead(res *readOrWriteResult) bool {
	select {
	case c.reads <- res:
		return true
	case <-c.ctx.Done():
		return false
	}
}

func (c *connection) eof() bool {
	switch c.state.stateType {
	case connConnecting:
		return false
	case connConnected:
		if c.state.closing != nil && c.state.closing.RemoteFin != nil {
			return c.state.RecvBuf.AckNum() == *c.state.closing.RemoteFin
		}
		return false
	case connClosed:
		return true
	default:
		return false
	}
}
