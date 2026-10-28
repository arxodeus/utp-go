package utp

import (
	"context"
	"errors"
	"sync"
)

type streamResult struct {
	stream *Stream
	err    error
}

type acceptRequest struct {
	ctx    context.Context
	stream chan *streamResult
	config *ConnectionConfig
	cid    *ConnectionID
	hasCid bool
	// mu orders the handover against the caller giving up: a connection
	// is either queued for a caller still waiting, or closed. See hand.
	mu     sync.Mutex
	gaveUp bool
	// onHand, when set, takes the result in place of a caller: the socket's
	// own request for a SYN it answered with nobody waiting (holdAnswered).
	onHand func(*streamResult)
}

func newAccept(ctx context.Context, cid *ConnectionID, config *ConnectionConfig) *acceptRequest {
	return &acceptRequest{
		ctx:    ctx,
		stream: make(chan *streamResult, 1),
		config: config,
		cid:    cid,
		hasCid: cid != nil,
	}
}

// hand gives the caller its result or, if the caller has already given up,
// closes the connection the result carries. An accepted connection runs on
// the socket's context, so one handed to a departed caller would otherwise
// run unattended.
func (a *acceptRequest) hand(res *streamResult) {
	if a.onHand != nil {
		a.onHand(res)
		return
	}
	a.mu.Lock()
	if !a.gaveUp {
		select {
		case a.stream <- res:
			a.mu.Unlock()
			return
		default:
		}
	}
	a.mu.Unlock()
	if res != nil && res.stream != nil {
		res.stream.Close()
	}
}

// giveUp records that the caller has stopped waiting and returns a result
// already handed over, if any, so the caller takes it rather than leaving it
// behind.
func (a *acceptRequest) giveUp() *streamResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gaveUp = true
	select {
	case res := <-a.stream:
		return res
	default:
		return nil
	}
}

// wait is what Accept and AcceptWithCID do once the request is queued. It
// returns when the caller's context ends or the socket closes.
func (a *acceptRequest) wait(ctx, sockCtx context.Context) (*Stream, error) {
	var res *streamResult
	select {
	case res = <-a.stream:
	case <-ctx.Done():
		if res = a.giveUp(); res == nil || res.stream == nil {
			return nil, ctx.Err()
		}
	case <-sockCtx.Done():
		if res = a.giveUp(); res == nil || res.stream == nil {
			return nil, sockCtx.Err()
		}
	}
	if res == nil {
		return nil, errors.New("utp: stream creation failed")
	}
	if res.err != nil {
		return nil, res.err
	}
	return res.stream, nil
}

func (s *Socket) eventLoop() {
	barrier, _ := s.clock().(idleBarrier)
	if barrier != nil {
		barrier.Register()
		defer func() {
			if u, ok := barrier.(interface{ Unregister() }); ok {
				u.Unregister()
			}
		}()
	}
	for {
		// Receive without acting, so a virtual clock can be told the loop is
		// running before any case can create a connection. See idleBarrier.
		var gotAcceptCid, gotAccept *acceptRequest
		if barrier != nil {
			barrier.MarkIdle()
		}
		select {
		case gotAcceptCid = <-s.acceptsWithCidCh:
		case gotAccept = <-s.accepts:
		case <-s.ctx.Done():
			// Every connection's context ends with this one, and a resting
			// connection has no loop to see it.
			s.wakeConns()
			return
		}
		if barrier != nil {
			barrier.MarkBusy()
		}
		// An Accept changes the routing tables the reader consults, so it is
		// served under the same lock.
		s.dispatchMu.Lock()
		if gotAcceptCid != nil {
			s.handleNewAcceptWithCidEvent(gotAcceptCid)
		} else {
			s.handleNewAcceptEvent(gotAccept)
		}
		s.dispatchMu.Unlock()
	}
}

// holdAnswered keeps a connection the socket answered with nobody waiting,
// until an Accept takes it (takeAnswered). Called with dispatchMu held, as
// the SYN is answered: no Accept is waiting then, or it would have had the
// connection, and any that comes later looks here first.
func (s *Socket) holdAnswered(st *Stream) {
	// Drop connections that ended while waiting (reset, timed out,
	// closed by the peer), so they do not accumulate on a socket nobody
	// accepts on.
	live := s.answered[:0]
	for _, st := range s.answered {
		select {
		case <-st.Done():
		default:
			live = append(live, st)
		}
	}
	clear(s.answered[len(live):])
	s.answered = append(live, st)
}

// takeAnswered removes and returns the oldest answered connection still
// alive, or the one for key if key is not empty; nil if there is none.
// Called with dispatchMu held.
func (s *Socket) takeAnswered(key string) *Stream {
	for i := 0; i < len(s.answered); i++ {
		st := s.answered[i]
		select {
		case <-st.Done():
			s.answered = append(s.answered[:i], s.answered[i+1:]...)
			i--
			continue
		default:
		}
		if key != "" && st.CID().Hash() != key {
			continue
		}
		s.answered = append(s.answered[:i], s.answered[i+1:]...)
		return st
	}
	return nil
}

func (s *Socket) handleNewAcceptWithCidEvent(acceptWithCid *acceptRequest) {
	s.logger.Debug("get a accept event with cid",
		"accept.cid.Send", acceptWithCid.cid.Send,
		"accept.cid.Recv",
		acceptWithCid.cid.Recv, "accept.cid.Peer", acceptWithCid.cid.Peer,
		"hash", acceptWithCid.cid.Hash())
	incomingConnsKey := acceptWithCid.cid.Hash()

	if st := s.takeAnswered(incomingConnsKey); st != nil {
		acceptWithCid.hand(&streamResult{stream: st})
		return
	}
	if incomingConn, exists := s.removeIncomingConn(incomingConnsKey); exists {
		if s.logger.Enabled(baseContext, LevelTrace) {
			s.logger.Trace("conn has already accepted", "key", incomingConnsKey)
		}
		s.selectAcceptHelper(acceptWithCid.cid, incomingConn.pkt, acceptWithCid)
	} else {
		if s.logger.Enabled(baseContext, LevelTrace) {
			s.logger.Trace("wait for the syn pkt arrive", "key", incomingConnsKey)
		}
		s.putAwaiting(incomingConnsKey, acceptWithCid)
	}
}

func (s *Socket) handleNewAcceptEvent(accept *acceptRequest) {
	// A connection already answered satisfies this immediately, and so does
	// a SYN that already arrived.
	if st := s.takeAnswered(""); st != nil {
		accept.hand(&streamResult{stream: st})
		return
	}
	if incomingAccept := s.nextIncomingConn(); incomingAccept != nil {
		s.selectAcceptHelper(incomingAccept.cid, incomingAccept.pkt, accept)
		return
	}
	// Otherwise wait for one, so that calling Accept before a client
	// connects works. Abandoned requests are pruned first, so the list
	// holds at most the live requests and those abandoned since the last
	// one.
	live := s.pendingAccepts[:0]
	for _, a := range s.pendingAccepts {
		if !a.abandoned() {
			live = append(live, a)
		}
	}
	clear(s.pendingAccepts[len(live):])
	s.pendingAccepts = append(live, accept)
}

// abandoned reports whether the caller has stopped waiting for this request.
func (a *acceptRequest) abandoned() bool {
	if a.ctx.Err() != nil {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gaveUp
}

// takePendingAccept returns the oldest waiting cid-less Accept whose caller
// has not already given up, or nil.
func (s *Socket) takePendingAccept() *acceptRequest {
	for len(s.pendingAccepts) > 0 {
		accept := s.pendingAccepts[0]
		s.pendingAccepts = s.pendingAccepts[1:]
		// Skip callers that have already returned.
		if accept.abandoned() {
			continue
		}
		return accept
	}
	return nil
}

func (s *Socket) getAwaiting(key string) (*acceptRequest, bool) {
	accept, ok := s.awaiting.get(key)
	return accept, ok
}

func (s *Socket) putAwaiting(key string, acceptWithCid *acceptRequest) {
	s.awaiting.put(key, acceptWithCid)
	s.awaitingExpirations.put(key, acceptWithCid, awaitingConnectionTimeout)
}

func (s *Socket) removeAwaiting(key string) {
	s.awaiting.remove(key)
	s.awaitingExpirations.remove(key)
}

func (s *Socket) putIncomingConn(key string, incomingConn *incomingPacket) {
	s.incomingConns.put(key, incomingConn)
	s.incomingConnsExpirations.put(key, incomingConn, awaitingConnectionTimeout)
}

func (s *Socket) removeIncomingConn(key string) (*incomingPacket, bool) {
	incomingConn, exists := s.incomingConns.get(key)
	if exists {
		s.incomingConns.remove(key)
		s.incomingConnsExpirations.remove(key)
	}
	return incomingConn, exists
}

func (s *Socket) nextIncomingConn() *incomingPacket {
	var cidHash string
	var incomingAccept *incomingPacket
	s.incomingConns.Range(func(key any, accept *incomingPacket) bool {
		cidHash = key.(string)
		incomingAccept = accept
		return false
	})
	if incomingAccept != nil {
		s.removeIncomingConn(cidHash)
	}
	return incomingAccept
}

// AcceptBacklog reports how many incoming connections are waiting for an
// Accept to take them.
func (s *Socket) AcceptBacklog() int {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	n := s.incomingConns.len()
	for _, st := range s.answered {
		select {
		case <-st.Done():
		default:
			n++
		}
	}
	return n
}

// Accept waits for an incoming connection from any peer and returns it once
// connected. config applies to the connection; nil means the socket's accept
// configuration (WithAcceptConfig) or the defaults.
func (s *Socket) Accept(ctx context.Context, config *ConnectionConfig) (*Stream, error) {
	accept := newAccept(ctx, nil, config)
	select {
	case s.accepts <- accept:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
	return accept.wait(ctx, s.ctx)
}

// AcceptWithCID waits for the incoming connection with the given id and
// returns it once connected. It fails with ErrAcceptTimedOut if no matching
// SYN arrives within 20 seconds.
func (s *Socket) AcceptWithCID(ctx context.Context, cid *ConnectionID, config *ConnectionConfig) (*Stream, error) {
	accept := newAccept(ctx, cid, config)
	select {
	case s.acceptsWithCidCh <- accept:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
	return accept.wait(ctx, s.ctx)
}

// selectAcceptHelper creates the connection for an incoming SYN and hands it
// to accept once connected. The connection runs on the socket's context, so
// it outlives the Accept call that returned it, as a dialed connection
// outlives its dial.
func (s *Socket) selectAcceptHelper(
	cid *ConnectionID,
	syn *packet,
	accept *acceptRequest,
) *Stream {
	if _, exists := s.getConnStream(cid.Hash()); exists {
		accept.hand(&streamResult{err: errors.New("utp: connection ID unavailable")})
		return nil
	}

	connected := make(chan error, 1)
	streamEvents := make(chan *streamEvent, connEventQueueLen)

	if s.logger.Enabled(baseContext, LevelTrace) {
		s.logger.Trace("put a conn stream at selectAcceptHelper", "cid.Peer", cid.Peer, "cid", cid)
	}
	s.putConnStream(cid, streamEvents)

	stream := newUtpStream(
		s.ctx,
		s.logger,
		cid,
		s.configForAccept(accept, cid.Peer),
		syn,
		nil,
		s.sendEvent,
		streamEvents,
		connected,
		s.retransmitTimers,
	)
	s.attachInline(cid.Hash(), streamEvents, stream.conn)
	stream.linger = s.linger

	go s.awaitConnected(stream, accept, connected)
	return stream
}
