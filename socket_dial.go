package utp

import (
	"context"
	"fmt"
)

func (s *UtpSocket) generateCid(peer ConnectionPeer, isInitiator bool, eventCh chan *streamEvent) *ConnectionId {
	cid := &ConnectionId{
		Peer: peer,
	}

	generationAttemptCount := 0
	for {
		// Generate random recv ID
		recv := randomUint16()
		var send uint16
		// Calculate send ID based on initiator status
		if isInitiator {
			send = recv + 1
		} else {
			send = recv - 1
		}

		cid.Send = send
		cid.Recv = recv
		cid.hash = genHash(cid)

		// Free means no connection to this peer receives on the id yet,
		// whichever side opened it: packets are routed on the receive id
		// alone (routeCandidates). libutp draws until the (address, receive
		// id) key is free (utp_internal.cpp:2533-2538). This checked only the
		// exact pair, so a new connection could take the receive id an
		// accepted one already had.
		//
		// Nor one that ended in the last idQuarantine: what is still in
		// flight for it, or still coming from a peer finishing its side of
		// it, would reach the new connection. A RESET for the old one
		// arriving while the new one dialled refused it -- the long soak
		// drew that once a minute or so, the same ids picked 0.18s after
		// the last connection to hold them closed. TCP keeps a closed
		// connection's ports out of use for the same reason (TIME_WAIT);
		// libutp does not. See DEVIATIONS.md, "A closed connection's ids
		// are not reused at once".
		//
		// And neither of its ids may be one another connection to the peer
		// uses in either direction. A RESET is matched on the receive id
		// first, as libutp matches it, so a connection sending on the id
		// another receives on has its RESETs taken by the other: in the soak
		// a dialling connection was refused by a RESET meant for one that
		// sent on its receive id and had just closed. libutp draws only the
		// receive id free and has the same ambiguity. With every id used by
		// one connection at most, no packet can be for two.
		s.connsMutex.Lock()
		peerKey := peer.Hash()
		if !s.idInUseLocked(recv, peerKey) && !s.idInUseLocked(send, peerKey) {
			if eventCh != nil {
				s.conns[cid.Hash()] = eventCh
				s.connsGen.Add(1)
			}
			s.connsMutex.Unlock()
			break
		}
		s.connsMutex.Unlock()
		generationAttemptCount++
	}
	return cid
}

func (s *UtpSocket) Connect(ctx context.Context, peer ConnectionPeer, config *ConnectionConfig) (*UtpStream, error) {
	// Create channels for connection status and events
	connectedCh := make(chan error, 1)
	streamEvents := make(chan *streamEvent, connEventQueueLen)

	// Registered under the reader's lock, so the reader never finds the
	// connection's queue without the connection behind it. Nothing here
	// waits: a packet for the connection that arrives before its event loop
	// has set it up waits on the connection's own lock. See newUtpStream.
	s.dispatchMu.Lock()
	// Generate connection ID
	cid := s.generateCid(peer, true, streamEvents)
	s.logger.Info("connecting", "dst.peer.hash", peer.Hash(), "dst.send", cid.Send, "dst.recv", cid.Recv, "dst.hash", cid.Hash(), "dst.peer", peer)
	// Create new UTP stream
	s.putConnStream(cid.Hash(), streamEvents)
	// The connection's lifetime is the socket's, not the dial's.
	//
	// This passed `ctx` -- the caller's dial context -- and so gave every
	// connection a lifetime that ended when the dial did. net.Dialer
	// documents the opposite ("Canceling ctx does not affect the connection
	// after it is established") and Go callers rely on it: the standard shape
	// is a dial under a timeout with `defer cancel()`, which fires the moment
	// the dial returns. anacrolix/torrent does exactly that, and the result
	// was a connection that completed its BitTorrent handshake and then died
	// mid-stream with "context canceled" -- a torrent that never moved a
	// byte. See TestDialContextCancelDoesNotCloseTheConnection.
	//
	// The caller's context still governs the wait below, which is the part it
	// is for.
	stream := newUtpStream(
		s.ctx,
		s.logger,
		cid,
		s.configForPeer(config, cid.Peer),
		nil,
		nil,
		s.sendEvent,
		streamEvents,
		connectedCh,
		s.retransmitTimers,
	)
	s.attachInline(cid.Hash(), streamEvents, stream.conn)
	s.dispatchMu.Unlock()
	stream.linger = s.linger

	// Wait for connection result
	select {
	case err, ok := <-connectedCh:
		if ok && err == nil {
			return stream, nil
		} else if err != nil {
			return nil, err
		}
		// A closed channel with nothing in it means the connection went away
		// without ever reporting an outcome. Saying "timed out" here was
		// wrong twice over: it was not a timeout, and until the connection
		// started reporting success by sending rather than by closing, this
		// was the branch every successful connection took.
		return nil, fmt.Errorf("connection closed before it was established")
	case <-ctx.Done():
		// The attempt is the caller's to abandon even though the stream no
		// longer shares its context.
		stream.abandonDial()
		return nil, ctx.Err()
	case <-s.ctx.Done():
		stream.abandonDial()
		return nil, s.ctx.Err()
	}
}

func (s *UtpSocket) ConnectWithCid(
	ctx context.Context,
	cid *ConnectionId,
	config *ConnectionConfig,
) (*UtpStream, error) {
	if s.logger.Enabled(baseContext, LevelTrace) {
		s.logger.Trace("connecting with cid", "dst.peer.hash", cid.Peer.Hash(),
			"dst.send", cid.Send, "dst.recv", cid.Recv, "dst.hash", cid.Hash(), "dst.peer", cid.Peer)
	}
	// See the note in Connect.
	s.dispatchMu.Lock()
	_, exists := s.getConnStream(cid.Hash())
	if exists {
		s.dispatchMu.Unlock()
		return nil, fmt.Errorf("connection ID unavailable")
	}

	connected := make(chan error, 1)
	streamEvents := make(chan *streamEvent, connEventQueueLen)

	s.putConnStream(cid.Hash(), streamEvents)
	// The socket's context, not the dial's -- see the note in Connect.
	stream := newUtpStream(
		s.ctx,
		s.logger,
		cid,
		s.configForPeer(config, cid.Peer),
		nil,
		nil,
		s.sendEvent,
		streamEvents,
		connected,
		s.retransmitTimers,
	)
	s.attachInline(cid.Hash(), streamEvents, stream.conn)
	s.dispatchMu.Unlock()
	stream.linger = s.linger
	// Honour the caller's context, as Connect does. A bare receive here meant
	// that cancelling the context during connection setup never returned: the
	// connection's event loop exits on the same cancellation without
	// signalling this channel, so the caller waited forever.
	select {
	case err := <-connected:
		if err == nil {
			return stream, nil
		}
		s.logger.Error("failed to open connection", "cid.send", cid.Send, "cid.recv", cid.Recv, "cid.peer", cid.Peer.Hash(), "err", err)
		// Report the reason rather than assuming one. This used to return a
		// flat "connection timed out" whatever had happened, which was true
		// while running out of SYN attempts was the only way to fail; an
		// ICMP error is a second way, and "refused" and "timed out" are
		// exactly the distinction libutp draws with UTP_ECONNREFUSED against
		// UTP_ECONNRESET (utp_internal.cpp:3122). Wrapped, so errors.Is finds
		// ErrConnRefused and ErrTimedOut.
		return nil, fmt.Errorf("utp_socket: connect failed: %w", err)
	case <-ctx.Done():
		stream.abandonDial()
		return nil, ctx.Err()
	case <-s.ctx.Done():
		stream.abandonDial()
		return nil, s.ctx.Err()
	}
}

func (s *UtpSocket) awaitConnected(
	stream *UtpStream,
	accept *acceptRequest,
	connected chan error,
) {
	// Report against the stream's connection id, not the accept's. A cid-less
	// Accept has no cid of its own, and reading accept.cid here crashed the
	// moment anyone used that path.
	cid := stream.Cid()

	s.logger.Debug("waiting for answering the new connections", "dst.peer", cid.Peer.Hash(), "cid.Send", cid.Send, "cid.Recv", cid.Recv)
	err, ok := <-connected
	if err == nil && ok {
		if s.logger.Enabled(baseContext, LevelTrace) {
			s.logger.Trace("new connection created", "src", cid.Peer.Hash(), "src.cid.Send", cid.Send, "src.cid.Recv", cid.Recv)
		}
		accept.hand(&streamResult{stream: stream})
		return
	} else if err != nil {
		if s.logger.Enabled(baseContext, LevelTrace) {
			s.logger.Trace("connected failed", "peer", cid.Peer.Hash(), "cid.Send", cid.Send, "cid.Recv", cid.Recv, "err", err)
		}
		accept.hand(&streamResult{err: fmt.Errorf("utp_socket: connection failed: %w", err)})
		return
	}

	s.logger.Warn("connected failed", "peer", cid.Peer.Hash(), "cid.Send", cid.Send, "cid.Recv", cid.Recv)
	accept.hand(&streamResult{err: fmt.Errorf("connection aborted")})
}
