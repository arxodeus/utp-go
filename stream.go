package utp_go

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/log"
)

var (
	ErrNotConnected = errors.New("not connected")
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
	connHandle   *sync.WaitGroup
	conn         *connection
	closeOnce    sync.Once
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
	if s.shutdown.Load() {
		return 0, ErrNotConnected
	}
	resCh := make(chan *readOrWriteResult, 1)
	select {
	case s.writes <- &queuedWrite{buf, 0, resCh}:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		s.logger.Trace("created a new queued write to writes channel",
			"dst.peer", s.cid.Peer,
			"buf.len", len(buf),
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
		// wait to consume write buffer and recv buffer
		s.connHandle.Wait()
		s.streamCancel()
	})
}
