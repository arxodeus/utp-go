package utp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrNotConnected is returned by Write after CloseWrite or Close, and
	// for a connection that ended without an error of its own.
	ErrNotConnected = errors.New("not connected")
	// ErrReadClosed is returned by Read after CloseRead. The connection is
	// still alive and may still send; this end has stopped taking delivery.
	ErrReadClosed = errors.New("read side closed")
	// ErrSocketClosed is reported by a connection whose socket has been
	// closed. It wraps net.ErrClosed.
	ErrSocketClosed = fmt.Errorf("utp: socket closed: %w", net.ErrClosed)
)

// UtpStream is one uTP connection, as returned by UtpSocket's Connect and
// Accept methods.
//
// It lives as long as the socket, not the call that created it: canceling a
// dial's or accept's context after it returns does not end the stream. Read
// and Write each take a context that governs that one call. CloseWrite sends
// a FIN once what is written has been sent, CloseRead stops delivery, and
// Close does both and returns at once while the connection delivers what it
// holds in the background; UtpSocket.Close waits for that while the peer
// still answers. Done is closed when the connection has ended.
type UtpStream struct {
	// socketCtx is the context of the socket the stream runs on; streamCtx
	// is the stream's own, which ends with it. See endWait.
	socketCtx    context.Context
	streamCtx    context.Context
	streamCancel context.CancelFunc
	logger       *ulogger
	cid          *ConnectionId
	reads        chan *readOrWriteResult
	writes       chan *queuedWrite
	streamEvents chan *streamEvent
	shutdown     *atomic.Bool
	// writeClosed is set by CloseWrite: this end has finished sending, but is
	// still reading. Distinct from shutdown, which means finished entirely.
	writeClosed atomic.Bool
	// readClosed is set by CloseRead: this end has finished reading but may
	// still send. The mirror of writeClosed.
	readClosed atomic.Bool
	// readShutCh is closed by CloseRead to wake a Read already waiting. It
	// is created by the first read that waits (readClosedSignal). Guarded by
	// readShutMu.
	readShutMu sync.Mutex
	readShutCh chan struct{}
	// readBusy is set while a read is in progress, and readWake, created only
	// by a read that must wait, is closed when it ends. Guarded by
	// readShutMu. See lockRead.
	readBusy bool
	readWake chan struct{}
	// writeSem keeps one write's chunks together: a write holds it while
	// queued, and waits for it under its own context. See lockWrite.
	writeSem   chan struct{}
	connHandle *sync.WaitGroup
	conn       *connection
	// linger, set by the socket that created this stream, takes it over at
	// Close. See UtpSocket.linger.
	linger    func(*UtpStream)
	closeOnce sync.Once
	// abandoned is closed by Close, telling the connection that whatever is
	// still buffered for this reader is owed to nobody.
	abandoned chan struct{}
	// readRemainder holds bytes from a chunk that a Read call could not fit
	// in the caller's buffer. Guarded by the read lock (lockRead).
	readRemainder []byte
	// readErr is the terminal read error, kept so every Read after the first
	// one returns it rather than blocking. Guarded by the read lock.
	readErr error
	// ended is closed once the connection's event loop has returned, after
	// publishing terminalErr. Nothing serves writes after that, so a writer
	// learns of it here.
	ended chan struct{}
}

// readQueueLen is how many chunks of received data, at most a packet each,
// wait for the application to read them; further data stays in the receive
// buffer.
const readQueueLen = 100

// writeQueueLen is how many writes can wait for a connection to take them.
// A Write blocks until its bytes are accepted, so more than one waits only
// when several goroutines write to one stream at once.
const writeQueueLen = 16

func newUtpStream(
	ctx context.Context,
	logger *ulogger,
	cid *ConnectionId,
	config *ConnectionConfig,
	syn *packet,
	socketEvents chan *socketEvent,
	out func(*socketEvent),
	streamEvents chan *streamEvent,
	connected chan error,
	timers *retransmitTimers,
) *UtpStream {
	if logger.Enabled(baseContext, LevelTrace) {
		logger.Trace("new a utp stream", "dst.peer", cid.Peer, "dst.send", cid.Send, "dst.recv", cid.Recv)
	}
	connHandle := &sync.WaitGroup{}
	connHandle.Add(1)
	streamCtx, cancel := context.WithCancel(ctx)

	utpStream := &UtpStream{
		socketCtx:    ctx,
		streamCtx:    streamCtx,
		streamCancel: cancel,
		logger:       logger,
		cid:          cid,
		reads:        make(chan *readOrWriteResult, readQueueLen),
		writes:       make(chan *queuedWrite, writeQueueLen),
		streamEvents: streamEvents,
		connHandle:   connHandle,
		shutdown:     &atomic.Bool{},
		abandoned:    make(chan struct{}),
		ended:        make(chan struct{}),
		writeSem:     make(chan struct{}, 1),
	}

	utpStream.conn = newConnection(streamCtx, logger, cid, config, syn, connected, socketEvents, utpStream.reads, utpStream.abandoned, timers)
	utpStream.conn.out = out
	// The connection's lock is taken here and released by its event loop
	// once setup is done, so a packet the reader brings before then waits
	// rather than finding the setup half done, and nothing here waits for
	// the loop while holding the socket's dispatch lock.
	utpStream.conn.mu.Lock()
	go utpStream.start()
	return utpStream
}

// abandonDial tears down a connection attempt whose caller has given up.
// The stream lives on the socket's context, not the dial's, so a dial that
// times out or is canceled must say so explicitly, or the attempt would keep
// retrying its SYN. Canceling the stream's context is immediate; Close would
// wait for the event loop, which keeps retrying until MaxConnAttempts.
func (s *UtpStream) abandonDial() {
	s.shutdown.Store(true)
	s.streamCancel()
	// Nothing watches the context of a resting connection. A dial is never
	// resting, since its SYN is in flight, but make sure.
	s.wakeLoop()
}

func (s *UtpStream) notifyRead() {
	if s.conn == nil {
		return
	}
	// Wake a resting connection only if the read has something to release:
	// bytes held back for lack of room in the read queue, the only case in
	// which a read grows the receive window. Otherwise a resting receiver
	// would have its loop started for every read, to do nothing.
	if s.conn.dormant.Load() && !s.conn.readsHeld.Load() {
		return
	}
	select {
	case s.conn.readable <- struct{}{}:
	default:
	}
	s.conn.wake()
}

// wakeLoop starts the connection's event loop if it is resting, for an event
// just queued for it. See dormant.go.
func (s *UtpStream) wakeLoop() {
	if s.conn != nil {
		s.conn.wake()
	}
}

// terminalErr returns the error the connection recorded when the stream
// ended, for a reader that learned of the end from the read channel closing
// rather than from the end-of-stream marker. A clean end returns nil.
func (s *UtpStream) terminalErr() error {
	if s.conn == nil {
		return nil
	}
	if boxed := s.conn.terminalErr.Load(); boxed != nil {
		return boxed.err
	}
	return nil
}

// endedErr returns the error the connection ended with once it has ended, or
// nil if it is still running or ended without one. The reader's
// ErrReadClosed for a clean but incomplete end (readEndErr) is not an error
// here.
func (s *UtpStream) endedErr() error {
	select {
	case <-s.ended:
	default:
		return nil
	}
	if boxed := s.conn.endErr.Load(); boxed != nil && boxed.err != nil {
		return boxed.err
	}
	if err := s.terminalErr(); err != nil && !errors.Is(err, ErrReadClosed) {
		return err
	}
	return nil
}

// writeErr returns what a write is told once the connection has ended: the
// error it ended with, or ErrNotConnected if none.
func (s *UtpStream) writeErr() error {
	if err := s.endedErr(); err != nil {
		return err
	}
	return ErrNotConnected
}

// endWait waits, once the stream's context has ended, for its event loop to
// return, so the caller can report why it ended rather than the bare context
// error. The loop returns promptly; ctx bounds the wait regardless. It
// returns ctx's error if that comes first. This is what makes a connection
// whose socket was closed report ErrSocketClosed consistently, after any
// data that had already arrived.
func (s *UtpStream) endWait(ctx context.Context) error {
	select {
	case <-s.ended:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// shutErr returns what a write is told on a stream already shut down: why its
// connection ended, if it is ending, or ErrNotConnected after Close.
func (s *UtpStream) shutErr(ctx context.Context) error {
	if s.streamCtx.Err() == nil {
		return ErrNotConnected
	}
	if err := s.endWait(ctx); err != nil {
		return err
	}
	return s.writeErr()
}

// Cid returns the stream's connection id.
func (s *UtpStream) Cid() *ConnectionId {
	return s.cid
}

func (s *UtpStream) start() {
	if err, final := s.conn.eventLoop(s); final {
		s.conn.finishLoop()
		s.loopEnded(err)
	}
}

// loopEnded runs when the connection's event loop ends, on whichever
// goroutine ran it last.
func (s *UtpStream) loopEnded(err error) {
	if err != nil {
		s.logger.Error("utp stream evenLoop has error and return", "err", err,
			"cid.send", s.cid.Send, "cid.recv", s.cid.Recv)
	}
	close(s.ended)
	s.connHandle.Done()
}

// ReadToEOF reads until the peer ends the stream, replacing the contents of
// *buf. It reuses the capacity *buf already has, so a caller that knows how
// much is coming can size it once.
func (s *UtpStream) ReadToEOF(ctx context.Context, buf *[]byte) (int, error) {
	if s.readClosed.Load() {
		return 0, ErrReadClosed
	}
	if err := s.lockRead(ctx); err != nil {
		return 0, err
	}
	defer s.unlockRead()
	n := 0
	data := (*buf)[:0]
	streamDone := s.streamCtx.Done()
	readShut := s.readClosedSignal()
	for {
		select {
		case <-ctx.Done():
			s.logger.Error("ctx has been canceled", "err", ctx.Err(), "readLength", n)
			return n, ctx.Err()
		case <-readShut:
			return n, ErrReadClosed
		case <-streamDone:
			// The connection is ending. Its loop closes the read queue behind what
			// had arrived; read on to that.
			if err := s.endWait(ctx); err != nil {
				return n, err
			}
			streamDone = nil
		case res, ok := <-s.reads:
			if !ok {
				return n, s.terminalErr()
			}
			s.notifyRead()
			if s.logger.Enabled(baseContext, LevelTrace) {
				s.logger.Trace("read a new buf", "len", res.Len)
			}
			if len(res.Data) == 0 {
				return n, res.Err
			}
			n += res.Len
			data = append(data, res.Data[:res.Len]...)
			res.release()
			*buf = data
		}
	}
}

// Read reads the next available bytes into buf, returning as soon as any are
// available. It returns io.EOF once the peer has closed its sending side and
// everything it sent has been read.
//
// Do not mix Read and ReadToEOF on the same stream; each would consume
// chunks the other expected.
func (s *UtpStream) Read(ctx context.Context, buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	if s.readClosed.Load() {
		return 0, ErrReadClosed
	}
	if err := s.lockRead(ctx); err != nil {
		return 0, err
	}
	defer s.unlockRead()

	// Anything left over from the last chunk comes first.
	if len(s.readRemainder) > 0 {
		n := copy(buf, s.readRemainder)
		s.readRemainder = s.readRemainder[n:]
		return n, nil
	}
	if s.readErr != nil {
		return 0, s.readErr
	}

	var res *readOrWriteResult
	var ok bool
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-s.readClosedSignal():
		return 0, ErrReadClosed
	case <-s.streamCtx.Done():
		// The connection is ending. Its loop closes the read queue behind what
		// had arrived, with the reason in terminalErr: deliver what arrived,
		// then the reason.
		if err := s.endWait(ctx); err != nil {
			return 0, err
		}
		res, ok = <-s.reads
	case res, ok = <-s.reads:
	}
	if !ok {
		if err := s.terminalErr(); err != nil {
			s.readErr = err
			return 0, err
		}
		s.readErr = io.EOF
		return 0, io.EOF
	}
	s.notifyRead()
	if res.Len == 0 || len(res.Data) == 0 {
		if res.Err != nil {
			s.readErr = res.Err
			return 0, res.Err
		}
		s.readErr = io.EOF
		return 0, io.EOF
	}
	n := copy(buf, res.Data[:res.Len])
	if n < res.Len {
		s.readRemainder = append([]byte(nil), res.Data[n:res.Len]...)
	}
	res.release()
	return n, nil
}

// Write queues all of buf to be sent and returns once the connection has
// taken every byte into its send buffer, which may be before any has been
// sent; delivery continues in the background, through Close.
//
// The count is what the connection took, all of which will be sent. If ctx
// ends first, Write returns the count taken with ctx's error and withdraws
// the rest, so retrying with the remainder sends each byte once. Concurrent
// writes are each sent whole, one after another.
//
// After CloseWrite or Close it returns ErrNotConnected, and once the
// connection has ended, the reason it ended.
func (s *UtpStream) Write(ctx context.Context, buf []byte) (int, error) {
	if s.writeClosed.Load() {
		return 0, ErrNotConnected
	}
	if s.shutdown.Load() {
		return 0, s.shutErr(ctx)
	}
	select {
	case <-s.ended:
		return 0, s.writeErr()
	default:
	}
	// The connection keeps what is queued here until it has moved it into
	// the send buffer, possibly after this call returns, when the caller may
	// reuse its buffer. So copy the bytes first.
	if len(buf) <= s.writeChunk() {
		if err := s.lockWrite(ctx); err != nil {
			return 0, err
		}
		defer s.unlockWrite()
		queued := make([]byte, len(buf))
		copy(queued, buf)
		return s.writeQueued(ctx, queued, make(chan *readOrWriteResult, 1))
	}
	return s.writeChunked(ctx, len(buf), func(dst []byte, off int) { copy(dst, buf[off:]) })
}

// lockWrite waits for the preceding write to finish, for as long as ctx, the
// stream and its write side allow.
func (s *UtpStream) lockWrite(ctx context.Context) error {
	select {
	case s.writeSem <- struct{}{}:
	default:
		select {
		case s.writeSem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		case <-s.ended:
			return s.writeErr()
		}
	}
	// The write side may have closed while this waited.
	if s.writeClosed.Load() {
		s.unlockWrite()
		return ErrNotConnected
	}
	if s.shutdown.Load() {
		s.unlockWrite()
		return s.shutErr(ctx)
	}
	return nil
}

func (s *UtpStream) unlockWrite() { <-s.writeSem }

// lockRead waits for the reads ahead of this one under its own context, as
// lockWrite does for writes. A waiting read gives up if the read side
// closes, and one admitted after it has closed is told so.
//
// It uses a flag and a channel created only for a read that must wait,
// rather than a semaphore, so an idle stream carries no channel.
func (s *UtpStream) lockRead(ctx context.Context) error {
	for {
		s.readShutMu.Lock()
		if !s.readBusy {
			s.readBusy = true
			s.readShutMu.Unlock()
			break
		}
		if s.readWake == nil {
			s.readWake = make(chan struct{})
		}
		wake := s.readWake
		s.readShutMu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return ctx.Err()
		case <-s.readClosedSignal():
			return ErrReadClosed
		}
	}
	if s.readClosed.Load() {
		s.unlockRead()
		return ErrReadClosed
	}
	return nil
}

// unlockRead ends a read, and lets the reads waiting for it try again.
func (s *UtpStream) unlockRead() {
	s.readShutMu.Lock()
	s.readBusy = false
	if s.readWake != nil {
		close(s.readWake)
		s.readWake = nil
	}
	s.readShutMu.Unlock()
}

// writeChunk returns the most a write copies at once: the send buffer's
// size, which is the most the connection can take from it at once.
func (s *UtpStream) writeChunk() int {
	if n := s.conn.config.BufferSize; n > 0 {
		return n
	}
	return DefaultBufferSize
}

// writeChunked writes total bytes a chunk at a time, fill copying each chunk
// from the caller's buffers once the previous one has been taken. A write
// holds at most one send buffer's worth beyond the send buffer itself.
// lockWrite keeps the chunks of one write together.
func (s *UtpStream) writeChunked(ctx context.Context, total int, fill func(dst []byte, off int)) (int, error) {
	if err := s.lockWrite(ctx); err != nil {
		return 0, err
	}
	defer s.unlockWrite()
	chunk := s.writeChunk()
	written := 0
	// One answer per chunk: each is answered before the next is queued, and
	// a chunk not answered ends the write.
	resCh := make(chan *readOrWriteResult, 1)
	for written < total {
		queued := make([]byte, min(chunk, total-written))
		fill(queued, written)
		n, err := s.writeQueued(ctx, queued, resCh)
		written += n
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

// WriteV writes several buffers as one stream write, without the caller
// joining them first, saving a copy. It returns the number of bytes written,
// which on success is their sum, and otherwise behaves like Write. Empty and
// nil buffers are skipped; a call with nothing to write returns 0, nil.
func (s *UtpStream) WriteV(ctx context.Context, bufs [][]byte) (int, error) {
	if s.writeClosed.Load() {
		return 0, ErrNotConnected
	}
	if s.shutdown.Load() {
		return 0, s.shutErr(ctx)
	}
	select {
	case <-s.ended:
		return 0, s.writeErr()
	default:
	}
	total := 0
	for _, b := range bufs {
		total += len(b)
	}
	if total == 0 {
		return 0, nil
	}
	// Copy each byte once, into one allocation, or one per chunk for more
	// than a send buffer. Chunks are filled in order, so a cursor (which
	// buffer, how far into it) continues where the last chunk stopped.
	bi, bo := 0, 0
	return s.writeChunked(ctx, total, func(dst []byte, _ int) {
		for len(dst) > 0 && bi < len(bufs) {
			n := copy(dst, bufs[bi][bo:])
			dst = dst[n:]
			if bo += n; bo == len(bufs[bi]) {
				bi, bo = bi+1, 0
			}
		}
	})
}

// writeQueued hands an already copied buffer to the connection and waits for
// it to be accepted. Shared by Write and WriteV. It also waits on the
// connection's end, so a write to a connection that has ended returns the
// reason at once.
func (s *UtpStream) writeQueued(ctx context.Context, queued []byte, resCh chan *readOrWriteResult) (int, error) {
	write := &queuedWrite{data: queued, resultCh: resCh}
	select {
	case s.writes <- write:
		s.wakeLoop()
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-s.ended:
		return 0, s.writeErr()
	}
	if s.logger.Enabled(baseContext, LevelTrace) {
		s.logger.Trace("created a new queued write to writes channel",
			"dst.peer", s.cid.Peer,
			"buf.len", len(queued),
			"len(s.writes)", len(s.writes),
			"ptr(s)", fmt.Sprintf("%p", s),
			"ptr(writes)", fmt.Sprintf("%p", s.writes))
	}
	var writtenLen int
	var err error
	select {
	case writeRes := <-resCh:
		if writeRes != nil {
			return writeRes.Len, writeRes.Err
		}
	case <-ctx.Done():
		// What the connection has taken will be sent, so it is reported; the
		// rest is withdrawn, and the caller can retry without sending
		// anything twice.
		n, res := write.cancel()
		if res != nil {
			return res.Len, res.Err
		}
		return n, ctx.Err()
	case <-s.ended:
		// An answer sent before the end still counts.
		select {
		case writeRes := <-resCh:
			if writeRes != nil {
				return writeRes.Len, writeRes.Err
			}
		default:
		}
		return 0, s.writeErr()
	case <-s.streamCtx.Done():
		if err := s.endWait(ctx); err != nil {
			return 0, err
		}
		// An answer sent before the end still counts.
		select {
		case writeRes := <-resCh:
			if writeRes != nil {
				return writeRes.Len, writeRes.Err
			}
		default:
		}
		return 0, s.writeErr()
	}
	return writtenLen, err
}

// CloseWrite shuts down the sending side and leaves the receiving side open:
// everything written is flushed, a FIN follows it, and the peer may continue
// sending. It is named after net.TCPConn.CloseWrite, so callers holding a
// net.Conn can detect it with a type assertion.
//
// Afterwards Write returns ErrNotConnected. Read is unaffected and returns
// data until the peer closes too. Close ends the connection.
func (s *UtpStream) CloseWrite() error {
	if s.shutdown.Load() {
		return ErrNotConnected
	}
	if s.writeClosed.Swap(true) {
		return nil
	}
	// Its own event rather than Close's, which means finished entirely.
	// This only needs to wake the loop.
	select {
	case s.streamEvents <- &streamEvent{Type: streamCloseWrite}:
	default:
	}
	s.wakeLoop()
	return nil
}

// CloseRead shuts down the receiving side: this end stops delivering what
// the peer sends, while everything it has to send goes out normally.
//
// Incoming data is still acknowledged and then discarded, so the peer is not
// stalled by a closing window and can finish sending and closing. Data
// already buffered is discarded too.
//
// Afterwards Read returns ErrReadClosed. Write is unaffected. It is named
// after net.TCPConn.CloseRead.
func (s *UtpStream) CloseRead() error {
	if s.shutdown.Load() {
		return ErrNotConnected
	}
	if s.readClosed.Swap(true) {
		return nil
	}
	s.readShutMu.Lock()
	if s.readShutCh != nil {
		close(s.readShutCh)
	}
	s.readShutMu.Unlock()
	// The event only wakes the loop; the flag is what it acts on (see
	// afterPass), so an event dropped from a full queue loses nothing.
	select {
	case s.streamEvents <- &streamEvent{Type: streamCloseRead}:
	default:
	}
	s.wakeLoop()
	return nil
}

// readClosedSignal returns a channel closed once CloseRead has been called,
// so a Read already waiting returns.
func (s *UtpStream) readClosedSignal() <-chan struct{} {
	s.readShutMu.Lock()
	defer s.readShutMu.Unlock()
	if s.readShutCh == nil {
		s.readShutCh = make(chan struct{})
		if s.readClosed.Load() {
			close(s.readShutCh)
		}
	}
	return s.readShutCh
}

// Done returns a channel closed once the connection has ended: its close
// handshake finished or gave up, it was reset or timed out, or the socket
// closed. Data delivered before then can still be read.
func (s *UtpStream) Done() <-chan struct{} {
	return s.ended
}

// SetWindowSize sets the upper bound on this connection's congestion window,
// in bytes, overriding ConnectionConfig.WindowSize. It takes effect at once:
// nothing more is sent beyond it, and a larger window is cut to it. Zero
// restores the default.
func (s *UtpStream) SetWindowSize(bytes uint32) {
	s.conn.setWindowCeiling(bytes)
}

// WindowSize returns the upper bound set by SetWindowSize or the
// connection's config.
func (s *UtpStream) WindowSize() uint32 {
	return s.conn.windowCeilingSetting()
}

// Close ends the stream and returns at once. What has been written continues
// to be delivered in the background, followed by the FIN, until the peer has
// acknowledged everything or stopped answering; UtpSocket.Close waits for
// that.
func (s *UtpStream) Close() {
	s.closeOnce.Do(func() {
		if s.logger.Enabled(baseContext, LevelTrace) {
			s.logger.Trace("call close utp stream", "dst.Peer", s.cid.Peer, "dst.send", s.cid.Send, "dst.recv", s.cid.Recv)
		}
		s.shutdown.Store(true)
		// Tell the final drain not to wait for a reader that is gone.
		close(s.abandoned)
		// Wake the event loop, which is blocked in a select and would
		// otherwise notice the flag only when some packet or timer arrived.
		select {
		case s.streamEvents <- &streamEvent{Type: streamShutdown}:
		default:
			// The queue is full, so the loop will wake anyway and see the flag on
			// its next pass.
		}
		s.wakeLoop()
		// Return and let the connection finish in the background. Closing
		// the socket waits for it (UtpSocket.awaitLingering), so a program
		// that closes the socket before exiting loses nothing in flight.
		if s.linger != nil {
			s.linger(s)
			return
		}
		go func() {
			s.connHandle.Wait()
			s.streamCancel()
		}()
	})
}

// closeStallTimeout is how long Close keeps waiting for a connection that
// has heard nothing from its peer. Two seconds is twice the minimum RTO, so
// one lost FIN and its first retransmission do not count as a stall. Beyond
// that, waiting gains nothing: the connection keeps retransmitting in the
// background either way.
const closeStallTimeout = 2 * time.Second

// closeStallPollInterval is how often that is checked. It only bounds how
// quickly a finished close is noticed.
const closeStallPollInterval = 20 * time.Millisecond

// waitForFlush waits for the connection's event loop to finish, giving up
// once the peer has been silent for closeStallTimeout, and reports whether
// the loop finished. Giving up stops only the waiting: the event loop keeps
// retransmitting and exits on its own, so the caller must not cancel the
// stream's context on this path.
func (s *UtpStream) waitForFlush() bool {
	done := make(chan struct{})
	go func() {
		s.connHandle.Wait()
		close(done)
	}()

	ticker := time.NewTicker(closeStallPollInterval)
	defer ticker.Stop()

	lastActivity := s.peerActivity()
	quietSince := time.Now()
	for {
		select {
		case <-done:
			return true
		case now := <-ticker.C:
			if activity := s.peerActivity(); activity != lastActivity {
				lastActivity = activity
				quietSince = now
				continue
			}
			if now.Sub(quietSince) >= closeStallTimeout {
				s.logger.Debug("close stopped waiting for a peer that went quiet",
					"dst.peer", s.cid.Peer, "dst.send", s.cid.Send, "dst.recv", s.cid.Recv,
					"quiet", now.Sub(quietSince))
				return false
			}
		}
	}
}

// peerActivity reports how many packets the connection has received, or zero
// if there is no connection to ask.
func (s *UtpStream) peerActivity() uint64 {
	if s.conn == nil {
		return 0
	}
	return s.conn.peerActivity.Load()
}
