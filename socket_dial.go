package utp

import (
	"context"
	"errors"
	"fmt"
)

// cidRandomDraws is how many random receive ids generateCid tries before it
// looks at every one in turn.
const cidRandomDraws = 64

// errNoFreeConnectionID is returned when every pair of ids a new connection
// to the peer could take is in use, or was used within idQuarantine.
var errNoFreeConnectionID = errors.New("utp: no free connection id for this peer")

// generateCid picks ids for a new connection to peer and, if eventCh is not
// nil, registers it under them.
//
// It tries random ids first and then every receive id in turn from a random
// start, so it terminates even when the peer holds every id. A connection
// takes two ids, so about 28,000 connections to one peer exhaust them.
func (s *Socket) generateCid(peer ConnectionPeer, isInitiator bool, eventCh chan *streamEvent) (*ConnectionID, error) {
	cid := &ConnectionID{
		Peer: peer,
	}

	var start uint16
	for attempt := 0; attempt < cidRandomDraws+1<<16; attempt++ {
		var recv uint16
		switch {
		case attempt < cidRandomDraws:
			recv = randomUint16()
		case attempt == cidRandomDraws:
			start = randomUint16()
			recv = start
		default:
			recv = start + uint16(attempt-cidRandomDraws) // wrapping
		}
		var send uint16
		// The send id is derived from the receive id.
		if isInitiator {
			send = recv + 1
		} else {
			send = recv - 1
		}

		cid.Send = send
		cid.Recv = recv
		cid.hash = genHash(cid)

		// An id pair is free when:
		//   - no connection to this peer receives on the receive id, since
		//     packets are routed on it alone (routeCandidates);
		//   - no connection to this peer that ended within idQuarantine
		//     used it, so packets still in flight for that connection cannot
		//     reach the new one;
		//   - neither id is used in either direction by another connection
		//     to the peer, since a RESET is matched on either id. With each
		//     id used by at most one connection, no packet can be for two.
		s.connsMutex.Lock()
		peerKey := peer.Hash()
		if !s.idInUseLocked(recv, peerKey) && !s.idInUseLocked(send, peerKey) {
			if eventCh != nil {
				s.registerLocked(cid.Hash(), eventCh, peer)
			}
			s.connsMutex.Unlock()
			return cid, nil
		}
		s.connsMutex.Unlock()
	}
	return nil, errNoFreeConnectionID
}

// Connect dials peer with newly chosen connection ids and returns the stream
// once the handshake completes. ctx bounds the dial only; the connection
// lives until it is closed or the socket closes. A nil config means the
// defaults.
func (s *Socket) Connect(ctx context.Context, peer ConnectionPeer, config *ConnectionConfig) (*Stream, error) {
	// Create channels for connection status and events.
	connectedCh := make(chan error, 1)
	streamEvents := make(chan *streamEvent, connEventQueueLen)

	// Register under the reader's lock, so the reader never finds the
	// connection's queue without the connection. A packet that arrives
	// before the event loop is set up waits on the connection's lock; see
	// newUtpStream.
	s.dispatchMu.Lock()
	// Generate connection ids.
	cid, err := s.generateCid(peer, true, streamEvents)
	if err != nil {
		s.dispatchMu.Unlock()
		return nil, err
	}
	s.logger.Info("connecting", "dst.peer.hash", peer.Hash(), "dst.send", cid.Send, "dst.recv", cid.Recv, "dst.hash", cid.Hash(), "dst.peer", peer)
	// Create the stream.
	s.putConnStream(cid, streamEvents)
	// The connection lives on the socket's context, not the dial's:
	// canceling ctx after the dial returns does not affect the connection,
	// as with net.Dialer. ctx still bounds the wait below.
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

	// Wait for the result.
	select {
	case err, ok := <-connectedCh:
		if ok && err == nil {
			return stream, nil
		} else if err != nil {
			return nil, err
		}
		// The connection ended without reporting an outcome.
		return nil, errors.New("utp: connection closed before it was established")
	case <-ctx.Done():
		// The caller may still abandon the attempt, although the stream does
		// not share its context.
		stream.abandonDial()
		return nil, ctx.Err()
	case <-s.ctx.Done():
		stream.abandonDial()
		return nil, s.ctx.Err()
	}
}

// ConnectWithCID is Connect with the connection ids given by cid.
func (s *Socket) ConnectWithCID(
	ctx context.Context,
	cid *ConnectionID,
	config *ConnectionConfig,
) (*Stream, error) {
	if s.logger.Enabled(baseContext, LevelTrace) {
		s.logger.Trace("connecting with cid", "dst.peer.hash", cid.Peer.Hash(),
			"dst.send", cid.Send, "dst.recv", cid.Recv, "dst.hash", cid.Hash(), "dst.peer", cid.Peer)
	}
	// See the note in Connect.
	s.dispatchMu.Lock()
	_, exists := s.getConnStream(cid.Hash())
	if exists {
		s.dispatchMu.Unlock()
		return nil, errors.New("utp: connection ID unavailable")
	}

	connected := make(chan error, 1)
	streamEvents := make(chan *streamEvent, connEventQueueLen)

	s.putConnStream(cid, streamEvents)
	// The socket's context, not the dial's; see Connect.
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
	// Honor the caller's context, as Connect does.
	select {
	case err := <-connected:
		if err == nil {
			return stream, nil
		}
		s.logger.Error("failed to open connection", "cid.send", cid.Send, "cid.recv", cid.Recv, "cid.peer", cid.Peer.Hash(), "err", err)
		// Wrap the reason, so errors.Is finds ErrConnRefused or ErrTimedOut.
		return nil, fmt.Errorf("utp_socket: connect failed: %w", err)
	case <-ctx.Done():
		stream.abandonDial()
		return nil, ctx.Err()
	case <-s.ctx.Done():
		stream.abandonDial()
		return nil, s.ctx.Err()
	}
}

func (s *Socket) awaitConnected(
	stream *Stream,
	accept *acceptRequest,
	connected chan error,
) {
	// Report against the stream's connection id: a cid-less Accept has
	// none of its own.
	cid := stream.CID()

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
	accept.hand(&streamResult{err: errors.New("utp: connection aborted")})
}
