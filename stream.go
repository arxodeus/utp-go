package utp_go

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"
)

var (
	ErrNotConnected = errors.New("not connected")
	// ErrReadClosed is returned by Read after CloseRead. The connection is
	// still alive and still sending; this end has simply stopped taking
	// delivery.
	ErrReadClosed = errors.New("read side closed")
)

type UtpStream struct {
	streamCtx    context.Context
	streamCancel context.CancelFunc
	logger       log.Logger
	cid          *ConnectionId
	reads        chan *readOrWriteResult
	writes       chan *queuedWrite
	streamEvents chan *streamEvent
	shutdown     *atomic.Bool
	// writeClosed is set by CloseWrite: this end has finished sending, but is
	// still reading. Distinct from shutdown, which means finished entirely.
	writeClosed atomic.Bool
	// readClosed is set by CloseRead: this end has finished reading, but is
	// still sending. The mirror of writeClosed, and libutp's read_shutdown.
	readClosed atomic.Bool
	connHandle *sync.WaitGroup
	conn       *connection
	closeOnce  sync.Once
	// abandoned is closed by Close, telling the connection that whatever is
	// still buffered for this reader is owed to nobody.
	abandoned  chan struct{}
	readLocker sync.Mutex
	// readRemainder holds bytes from a chunk that a Read call could not fit
	// in the caller's buffer. Guarded by readLocker.
	readRemainder []byte
	// readErr is the terminal read error, kept so every Read after the first
	// one returns it rather than blocking. Guarded by readLocker.
	readErr error
}

func NewUtpStream(
	ctx context.Context,
	logger log.Logger,
	cid *ConnectionId,
	config *ConnectionConfig,
	syn *packet,
	socketEvents chan *socketEvent,
	streamEvents chan *streamEvent,
	connected chan error,
	timers *retransmitTimers,
) *UtpStream {
	if logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		logger.Trace("new a utp stream", "dst.peer", cid.Peer, "dst.send", cid.Send, "dst.recv", cid.Recv)
	}
	connHandle := &sync.WaitGroup{}
	connHandle.Add(1)
	streamCtx, cancel := context.WithCancel(ctx)

	utpStream := &UtpStream{
		streamCtx:    streamCtx,
		streamCancel: cancel,
		logger:       logger,
		cid:          cid,
		reads:        make(chan *readOrWriteResult, 100),
		writes:       make(chan *queuedWrite, 100),
		streamEvents: streamEvents,
		connHandle:   connHandle,
		shutdown:     &atomic.Bool{},
		abandoned:    make(chan struct{}),
	}

	utpStream.conn = newConnection(streamCtx, logger, cid, config, syn, connected, socketEvents, utpStream.reads, utpStream.abandoned, timers)
	go utpStream.start()
	return utpStream
}

// abandonDial tears down a connection attempt whose caller has given up.
//
// It exists because the stream's lifetime is the socket's rather than the
// dial's (see UtpSocket.Connect): cancelling the dial context no longer takes
// the connection with it, so a dial that times out or is cancelled has to say
// so explicitly, or a half-open attempt would go on retrying its SYN with
// nobody waiting for the answer.
//
// Close is the wrong tool here: it waits for the event loop to drain, and an
// unanswered SYN keeps that loop retrying until MaxConnAttempts. Cancelling
// the stream's own context is what the old behaviour did, and it is
// immediate; the event loop's deferred cleanup removes the socket's entry for
// the connection either way.
func (s *UtpStream) abandonDial() {
	s.shutdown.Store(true)
	s.streamCancel()
}

func (s *UtpStream) notifyRead() {
	if s.conn == nil {
		return
	}
	select {
	case s.conn.readable <- struct{}{}:
	default:
	}
}

// terminalErr reports the error the connection recorded when the stream ended,
// for a reader that learned of the end from the read channel closing rather
// than from the end-of-stream marker. A clean end reports nil.
func (s *UtpStream) terminalErr() error {
	if s.conn == nil {
		return nil
	}
	if boxed := s.conn.terminalErr.Load(); boxed != nil {
		return boxed.err
	}
	return nil
}

func (s *UtpStream) Cid() *ConnectionId {
	return s.cid
}

func (s *UtpStream) start() {
	defer s.connHandle.Done()

	err := s.conn.eventLoop(s)
	if err != nil {
		s.logger.Error("utp stream evenLoop has error and return", "err", err)
	}
}

func (s *UtpStream) ReadToEOF(ctx context.Context, buf *[]byte) (int, error) {
	if s.readClosed.Load() {
		return 0, ErrReadClosed
	}
	s.readLocker.Lock()
	defer s.readLocker.Unlock()
	n := 0
	data := make([]byte, 0)
	for {
		select {
		case <-ctx.Done():
			s.logger.Error("ctx has been canceled", "err", ctx.Err(), "readLength", n)
			return n, ctx.Err()
		case <-s.streamCtx.Done():
			s.logger.Error("streamCtx has been canceled", "err", s.streamCtx.Err(), "readLength", n)
			return 0, s.streamCtx.Err()
		case res, ok := <-s.reads:
			if !ok {
				return n, s.terminalErr()
			}
			s.notifyRead()
			if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				s.logger.Trace("read a new buf", "len", res.Len)
			}
			if len(res.Data) == 0 {
				return n, res.Err
			}
			n += res.Len
			data = append(data, res.Data[:res.Len]...)
			*buf = data
		}
	}
}

// Read reads the next available bytes from the stream into buf, returning as
// soon as anything is available. It returns io.EOF once the peer has closed
// its sending side and everything it sent has been read.
//
// This is the streaming counterpart to ReadToEOF, which only returns once the
// whole transfer is complete and so cannot be used for a connection that
// stays open. Anything that treats a uTP stream as an ordinary byte stream --
// net.Conn, io.Reader, a BitTorrent peer connection -- needs this one.
//
// Do not mix Read and ReadToEOF on the same stream. They share the same
// underlying channel and each would consume chunks the other expected.
func (s *UtpStream) Read(ctx context.Context, buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	if s.readClosed.Load() {
		return 0, ErrReadClosed
	}
	s.readLocker.Lock()
	defer s.readLocker.Unlock()

	// Anything left over from the last chunk comes first.
	if len(s.readRemainder) > 0 {
		n := copy(buf, s.readRemainder)
		s.readRemainder = s.readRemainder[n:]
		return n, nil
	}
	if s.readErr != nil {
		return 0, s.readErr
	}

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-s.streamCtx.Done():
		// The stream is gone. Report it as end of stream rather than as a
		// context error: a reader that has already had everything the peer
		// sent should see io.EOF, which is what every io.Reader caller
		// expects, not "context canceled".
		s.readErr = io.EOF
		return 0, io.EOF
	case res, ok := <-s.reads:
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
		return n, nil
	}
}

func (s *UtpStream) Write(ctx context.Context, buf []byte) (int, error) {
	if s.shutdown.Load() || s.writeClosed.Load() {
		return 0, ErrNotConnected
	}
	resCh := make(chan *readOrWriteResult, 1)
	// The connection goroutine keeps hold of what is queued here until it has
	// copied it into the send buffer, which may be long after this call
	// returns: a Write that hits its deadline returns on ctx.Done below with
	// its entry still in the connection's pending list. The caller is then
	// free to reuse its buffer -- net.Conn makes no promise otherwise -- and
	// the connection would copy whatever it had become.
	//
	// That is not only a race detector complaint. The bytes actually sent
	// would be the mutated ones, silently, on a write the caller had already
	// been told failed. Found by nettest.RacyWrite under -race, which mutates
	// the buffer immediately after every Write; nothing in this repository had
	// reason to do that.
	//
	// libutp does not retain either: utp_writev copies out of the caller's
	// iovec into packet buffers before it returns (utp_internal.cpp:1057-1066),
	// so an embedder may reuse its buffer straight away. One copy per Write is
	// what that costs, and on the fastest link the harness models it does not
	// show up at all -- see "What the write copy cost" in BENCHMARKS.md.
	queued := make([]byte, len(buf))
	copy(queued, buf)
	return s.writeQueued(ctx, queued, resCh)
}

// WriteV writes several buffers as one stream write, without the caller
// having to join them first.
//
// libutp's utp_writev (utp_internal.cpp:3154-3239); utp_write is that function
// with a single iovec (:3241-3245). What it saves is a copy: Write already
// copies the caller's buffer once, so a caller holding a header and a payload
// would otherwise concatenate them (one copy) and hand the result to Write
// (a second). This makes one allocation of the exact total and copies each
// piece into it once.
//
// It returns the number of bytes written, which on success is their sum, and
// follows Write's contract rather than libutp's in one respect worth stating:
// libutp's writev is non-blocking and returns however much fitted in the
// window, leaving the rest to the caller. Write here blocks until everything
// is queued, and two different contracts for the same operation in one package
// would be worse than the difference. That deviation is Write's, not this
// one's, and is already recorded.
//
// Empty and nil buffers are skipped. A call with nothing in it writes nothing
// and returns nil, as a zero-length Write does.
func (s *UtpStream) WriteV(ctx context.Context, bufs [][]byte) (int, error) {
	if s.shutdown.Load() || s.writeClosed.Load() {
		return 0, ErrNotConnected
	}
	total := 0
	for _, b := range bufs {
		total += len(b)
	}
	if total == 0 {
		return 0, nil
	}
	// One allocation, one copy per byte -- the whole point of the call.
	queued := make([]byte, 0, total)
	for _, b := range bufs {
		queued = append(queued, b...)
	}
	return s.writeQueued(ctx, queued, make(chan *readOrWriteResult, 1))
}

// writeQueued hands an already-copied buffer to the connection and waits for
// it to be accepted. Shared by Write and WriteV so the two cannot drift.
func (s *UtpStream) writeQueued(ctx context.Context, queued []byte, resCh chan *readOrWriteResult) (int, error) {
	select {
	case s.writes <- &queuedWrite{queued, 0, resCh}:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
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
		return 0, ctx.Err()
	case <-s.streamCtx.Done():
		return 0, s.streamCtx.Err()
	}
	return writtenLen, err
}

// CloseWrite finishes this end's sending side and leaves the reading side
// open: everything already written is flushed, a FIN goes out behind it, and
// the peer may go on sending for as long as it likes.
//
// This is the half-close, and libutp has had it all along -- `utp_shutdown(s,
// SHUT_WR)` (utp.h:176), with the socket sitting in CS_GOT_FIN while its
// application keeps reading. This library did not, which was the one place the
// reference could do something an application here could not: on reaching the
// peer's FIN the connection tore itself down, so a peer that closed its
// sending side took the whole connection with it.
//
// The name follows net.TCPConn.CloseWrite rather than libutp's shutdown(how),
// because that is what Go callers reach for and what libraries type-assert:
// anything holding a net.Conn will look for `interface{ CloseWrite() error }`.
//
// After it returns, Write reports ErrNotConnected. Read is unaffected and
// keeps returning data until the peer closes too. Close still does what it
// always did, and is what actually ends the connection.
func (s *UtpStream) CloseWrite() error {
	if s.shutdown.Load() {
		return ErrNotConnected
	}
	if s.writeClosed.Swap(true) {
		return nil
	}
	// Its own event, not the one Close uses: that one means "finished
	// entirely" and the loop sets stream.shutdown from it, which would turn
	// every half-close into a full one. This only has to wake the loop.
	select {
	case s.streamEvents <- &streamEvent{Type: streamCloseWrite}:
	default:
	}
	return nil
}

// CloseRead closes the receiving half of the stream: this end stops taking
// delivery of what the peer sends, while everything it still has to send goes
// out normally.
//
// libutp's utp_shutdown(s, SHUT_RD), which sets read_shutdown
// (utp_internal.cpp:3405-3411). What that flag does is precise and worth
// stating, because the obvious implementation is the wrong one: incoming data
// is still **acknowledged** -- `conn->ack_nr++` runs whether or not the bytes
// are delivered (:2344-2355, and again for the reorder buffer at :2392-2395)
// -- it is simply never handed to the application.
//
// So this is not "stop reading and let the window fill". A connection that let
// its receive window close would stall the peer, and a peer that cannot finish
// sending cannot finish closing. The peer keeps transmitting at full rate and
// the bytes are dropped on arrival.
//
// Data already buffered here is discarded for the same reason: it is owed to a
// reader that has said it will not come back. libutp never faces the question
// because it has no receive buffer of its own -- the bytes go straight to the
// embedder's callback or nowhere.
//
// Read reports ErrReadClosed afterwards. Write is unaffected. The name follows
// net.TCPConn.CloseRead, as CloseWrite follows its counterpart.
func (s *UtpStream) CloseRead() error {
	if s.shutdown.Load() {
		return ErrNotConnected
	}
	if s.readClosed.Swap(true) {
		return nil
	}
	select {
	case s.streamEvents <- &streamEvent{Type: streamCloseRead}:
	default:
	}
	return nil
}

func (s *UtpStream) Close() {
	s.closeOnce.Do(func() {
		if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			s.logger.Trace("call close utp stream", "dst.Peer", s.cid.Peer, "dst.send", s.cid.Send, "dst.recv", s.cid.Recv)
		}
		s.shutdown.Store(true)
		// Tell the connection's final drain not to wait for a reader that is
		// no longer there. Closing is what makes Close safe against its own
		// wait below: the loop would otherwise block handing over bytes that
		// this consumer has just said it does not want.
		close(s.abandoned)
		// Wake the event loop.
		//
		// Setting the flag alone is not enough: the loop is blocked in a
		// select, and nothing here is one of the cases it waits on. It would
		// only notice the shutdown when some unrelated packet or timer
		// happened to arrive, so Close took anywhere from about a second to
		// the full idle timeout depending on what else was in flight. The
		// shutdown event is the channel the socket already uses to tell a
		// connection to wind up.
		select {
		case s.streamEvents <- &streamEvent{Type: streamShutdown}:
		default:
			// The queue is full, so the loop has plenty to wake it and will
			// see the flag on its next pass.
		}
		// Wait for the connection to flush what is queued -- but only while it
		// is still getting somewhere.
		//
		// This was a bare connHandle.Wait(), which waits for the event loop to
		// exit. A loop with unacknowledged data does not exit until its
		// retransmission ladder runs out -- 1 + 2 + 4 + 8 + 16 seconds -- and
		// where that is not enough, the 60-second idle timeout behind it.
		// Measured, closing a connection whose peer had gone took 31 seconds
		// in the standard library's net.Conn suite and 60.001 seconds on a
		// blackholed link, with the caller held for all of it; the eleven
		// subtests that kernel TCP finishes in 0.43s took 140. A BitTorrent
		// client drops peers constantly, and a minute per dropped peer is not
		// a close, it is a leak with a timer on it.
		//
		// The wait cannot simply be capped. Write returns once the data is in
		// the send buffer rather than once it is acknowledged (conn.go,
		// processWrites), so a write followed by a close leaves a tail of up
		// to a send buffer still to go -- and how long that takes is a
		// property of the link, not of the caller: measured at 2.29s for a
		// 512 KB transfer over 2 Mbps, which is past any cap short enough to
		// be worth having. A flat timeout would truncate exactly the
		// transfers that need the wait most. What separates the two cases is
		// not elapsed time but whether the peer is still answering, so that
		// is what is measured.
		//
		// libutp does not wait at all: utp_close sends the FIN, sets
		// close_requested and returns (utp_internal.cpp:3232-3247), and the
		// socket is destroyed later by utp_check_timeouts. The deviation is
		// deliberate and recorded in DEVIATIONS.md -- a caller that writes,
		// closes and exits should not lose its tail, which libutp leaves to
		// the embedder to arrange and this library does not.
		if s.waitForFlush() {
			s.streamCancel()
		}
		// If the flush did not finish, the stream context is deliberately left
		// alone. Cancelling it here would stop the event loop mid-flight and
		// discard whatever it still had to send, which is the opposite of what
		// giving up on *waiting* should mean -- and it is not what libutp does
		// either: a socket closed with unacknowledged data stays alive in
		// CS_FIN_SENT until utp_check_timeouts retires it. The loop reaches the
		// same end on its own, and its deferred cleanup drops the connection
		// from the socket when it does. Closing the socket cancels everything
		// regardless.
	})
}

// closeStallTimeout is how long Close keeps waiting for a connection that has
// heard nothing from its peer.
//
// Two seconds is twice libutp's RTO floor (`rto = max(rtt + rtt_var * 4,
// 1000)`, utp_internal.cpp:1380), so a single lost FIN and its first
// retransmission do not read as a stall. Past that the peer has missed two
// chances to answer and waiting longer buys the caller nothing: the connection
// goes on retransmitting in the background either way, and reaches the same
// end whether or not anyone is watching.
const closeStallTimeout = 2 * time.Second

// closeStallPollInterval is how often that is checked. It only bounds how
// quickly a finished close is noticed, so it is small enough not to add
// latency to the common case -- where the connection ends in one round trip
// and the wait is over before the first tick.
const closeStallPollInterval = 20 * time.Millisecond

// waitForFlush waits for the connection's event loop to finish, giving up once
// the peer has gone quiet for closeStallTimeout. It reports whether the loop
// actually finished.
//
// Giving up means giving up on *waiting*, not on the connection: the caller
// stops being held, and the event loop keeps running, keeps retransmitting and
// exits on its own. Nothing queued is discarded by returning early -- which is
// why the caller must not cancel the stream context on this path.
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
