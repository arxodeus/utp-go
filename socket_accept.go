package utp

import (
	"context"
	"fmt"
	"sync"
)

type streamResult struct {
	stream *UtpStream
	err    error
}

type acceptRequest struct {
	ctx    context.Context
	stream chan *streamResult
	config *ConnectionConfig
	cid    *ConnectionId
	hasCid bool
	// mu orders the handover against the caller giving up: a connection
	// is either queued for a caller still waiting, or closed. See hand.
	mu     sync.Mutex
	gaveUp bool
	// onHand, when set, takes the result in place of a caller: the socket's
	// own request for a SYN it answered with nobody waiting (holdAnswered).
	onHand func(*streamResult)
}

func newAccept(ctx context.Context, cid *ConnectionId, config *ConnectionConfig) *acceptRequest {
	return &acceptRequest{
		ctx:    ctx,
		stream: make(chan *streamResult, 1),
		config: config,
		cid:    cid,
		hasCid: cid != nil,
	}
}

// hand gives the caller its result, or, if the caller has already given up,
// closes the connection the result carries.
//
// An accepted connection runs on the socket's context, not the Accept call's
// (see selectAcceptHelper), so one handed to a caller that has gone would run
// for nobody -- with no idle timeout by default, for as long as the peer
// stayed. libutp hands each incoming connection to its application from
// inside the call that received the SYN (utp_call_on_accept,
// utp_internal.cpp:3001), so it has no such gap to close.
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

// giveUp records that the caller has stopped waiting, and returns a result
// already handed over, if there is one: the caller takes it rather than
// leaving it behind.
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

// wait is what Accept and AcceptWithCid do once the request is queued.
//
// It ends with the socket as well as with the caller. It used to watch only
// the caller's context, so an Accept on context.Background() outlived the
// socket it was queued on: the dispatcher that would have answered it was
// gone, and so, for AcceptWithCid, was the expiry wheel that would have timed
// it out. From the release review of 9c44c4f (R7).
func (a *acceptRequest) wait(ctx, sockCtx context.Context) (*UtpStream, error) {
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
		return nil, fmt.Errorf("stream creation failed")
	}
	if res.err != nil {
		return nil, res.err
	}
	return res.stream, nil
}

func (s *UtpSocket) eventLoop() {
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
		// Receive without acting, so that a virtual clock can be told the
		// loop is running again before any case body can create a
		// connection. See idleBarrier.
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
		// served under the same lock. Incoming packets no longer pass through
		// this loop: the reader routes them itself (dispatch).
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
func (s *UtpSocket) holdAnswered(st *UtpStream) {
	// Those that ended while waiting -- reset, timed out, closed by the
	// peer -- go now: they are no longer counted as connections, and kept
	// here they would pile up behind a socket nobody accepts on.
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
func (s *UtpSocket) takeAnswered(key string) *UtpStream {
	for i := 0; i < len(s.answered); i++ {
		st := s.answered[i]
		select {
		case <-st.Done():
			s.answered = append(s.answered[:i], s.answered[i+1:]...)
			i--
			continue
		default:
		}
		if key != "" && st.Cid().Hash() != key {
			continue
		}
		s.answered = append(s.answered[:i], s.answered[i+1:]...)
		return st
	}
	return nil
}

func (s *UtpSocket) handleNewAcceptWithCidEvent(acceptWithCid *acceptRequest) {
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

func (s *UtpSocket) handleNewAcceptEvent(accept *acceptRequest) {
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
	// Otherwise wait for one, so that the ordinary server pattern -- call
	// Accept, then have a client connect -- works: the SYN that arrives a
	// moment later finds the request.
	//
	// Those already abandoned go first: pruned on each arrival, the list
	// holds at most the live requests and those abandoned since the last
	// one. History: HISTORY.md, "handleNewAcceptEvent: waiting".
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
func (s *UtpSocket) takePendingAccept() *acceptRequest {
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

func (s *UtpSocket) getAwaiting(key string) (*acceptRequest, bool) {
	accept, ok := s.awaiting.get(key)
	return accept, ok
}

func (s *UtpSocket) putAwaiting(key string, acceptWithCid *acceptRequest) {
	s.awaiting.put(key, acceptWithCid)
	s.awaitingExpirations.put(key, acceptWithCid, awaitingConnectionTimeout)
}

func (s *UtpSocket) removeAwaiting(key string) {
	s.awaiting.remove(key)
	s.awaitingExpirations.remove(key)
}

func (s *UtpSocket) putIncomingConn(key string, incomingConn *incomingPacket) {
	s.incomingConns.put(key, incomingConn)
	s.incomingConnsExpirations.put(key, incomingConn, awaitingConnectionTimeout)
}

func (s *UtpSocket) removeIncomingConn(key string) (*incomingPacket, bool) {
	incomingConn, exists := s.incomingConns.get(key)
	if exists {
		s.incomingConns.remove(key)
		s.incomingConnsExpirations.remove(key)
	}
	return incomingConn, exists
}

func (s *UtpSocket) nextIncomingConn() *incomingPacket {
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
func (s *UtpSocket) AcceptBacklog() int {
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

func (s *UtpSocket) Accept(ctx context.Context, config *ConnectionConfig) (*UtpStream, error) {
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

func (s *UtpSocket) AcceptWithCid(ctx context.Context, cid *ConnectionId, config *ConnectionConfig) (*UtpStream, error) {
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

// selectAcceptHelper makes the connection for an incoming SYN and hands it to
// accept, once connected.
//
// The connection runs on the socket's context, whichever came first, the SYN
// or the Accept: an accepted connection outlives the Accept that produced it,
// as a dialled one outlives its dial. Where the SYN came first this used the
// Accept call's context, and a timeout on Accept that fired after it had
// returned ended the connection it had returned.
func (s *UtpSocket) selectAcceptHelper(
	cid *ConnectionId,
	syn *packet,
	accept *acceptRequest,
) *UtpStream {
	if _, exists := s.getConnStream(cid.Hash()); exists {
		accept.hand(&streamResult{err: fmt.Errorf("connection ID unavailable")})
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
