package utp

func (s *UtpSocket) removeConnStream(key string) {
	s.connsMutex.Lock()
	defer s.connsMutex.Unlock()
	if s.logger.Enabled(baseContext, LevelTrace) {
		s.logger.Trace("remove conn stream", "key", key)
	}
	if ch, ok := s.conns[key]; ok {
		delete(s.inlineConns, ch)
		s.recentlyClosed.put(key, struct{}{})
		s.recentlyClosedExpirations.put(key, key, idQuarantine)
	}
	delete(s.conns, key)
	if peer, ok := s.held[key]; ok {
		delete(s.held, key)
		s.holder.ReleasePeer(peer)
	}
	s.connsGen.Add(1)
}

// registerLocked adds a connection to peer to the table under key. The first
// time key is added, it tells a transport that tracks peers (HoldPeer) that
// this one is in use. Called with connsMutex held for writing.
func (s *UtpSocket) registerLocked(key string, ch chan *streamEvent, peer ConnectionPeer) {
	if _, ok := s.conns[key]; !ok && s.holder != nil {
		if s.held == nil {
			s.held = make(map[string]ConnectionPeer)
		}
		s.held[key] = peer
		s.holder.HoldPeer(peer)
	}
	s.conns[key] = ch
	s.connsGen.Add(1)
}

// attachInline lets the reader hand packets on ch straight to c. Called
// with dispatchMu held, after c is set up.
func (s *UtpSocket) attachInline(key string, ch chan *streamEvent, c *connection) {
	s.connsMutex.Lock()
	defer s.connsMutex.Unlock()
	// A connection that has already gone, its entry removed, stays gone.
	if s.conns[key] == ch {
		s.inlineConns[ch] = c
		s.connsGen.Add(1)
		// From here the connection may rest; see dormant.go.
		c.attached.Store(true)
	}
}

// route finds the connection a packet is for: the first key derived by
// routeCandidates that the socket has, and its inline connection. Called by
// the reader with dispatchMu held. Keys are built in a reused buffer, so a
// lookup does not allocate.
func (s *UtpSocket) route(packetPtr *packet, peerKey string) (send, recv uint16, ch chan *streamEvent, c *connection) {
	// Consecutive datagrams usually belong to one flow and the tables
	// rarely change, so the last route is reused while the tables are
	// unchanged. A SYN always takes the full path.
	gen := s.connsGen.Load()
	r := &s.lastRoute
	isSyn := packetPtr.Header.PacketType == st_syn
	if !isSyn && r.ch != nil && r.gen == gen &&
		r.connID == packetPtr.Header.ConnectionId && r.peerKey == peerKey {
		return r.send, r.recv, r.ch, r.conn
	}
	send, recv, ch, c = s.routeLocked(packetPtr, peerKey)
	if !isSyn && ch != nil {
		*r = cachedRoute{gen: gen, connID: packetPtr.Header.ConnectionId, peerKey: peerKey,
			send: send, recv: recv, ch: ch, conn: c}
	}
	return send, recv, ch, c
}

// cachedRoute is the route one datagram took: its connection id and peer,
// what they led to, and the routing tables' generation at the time. See
// UtpSocket.route.
type cachedRoute struct {
	gen        uint64
	connID     uint16
	peerKey    string
	send, recv uint16
	ch         chan *streamEvent
	conn       *connection
}

// routeLocked is route's lookup proper, through every derivation in order.
func (s *UtpSocket) routeLocked(packetPtr *packet, peerKey string) (send, recv uint16, ch chan *streamEvent, c *connection) {
	s.connsMutex.RLock()
	defer s.connsMutex.RUnlock()
	var pairs [4][2]uint16
	for _, p := range routeCandidates(packetPtr, &pairs) {
		s.keyScratch = appendConnKey(s.keyScratch[:0], p[0], p[1], peerKey)
		if ch = s.conns[string(s.keyScratch)]; ch != nil {
			return p[0], p[1], ch, s.inlineConns[ch]
		}
	}
	return 0, 0, nil, nil
}

// routeCandidates returns the (send, recv) id pairs of the connections a
// packet can be for, in the order they are tried:
//
//   - A SYN carries the sender's receive id, which is our send id, and
//     matches only a retransmission of a SYN already accepted: a connection
//     receiving on one above it. Any other match would alias a new SYN onto
//     an unrelated local connection.
//   - A RESET may carry either of our ids, so it is matched on the receive
//     id first and then on the send id.
//   - Any other packet carries our receive id and is matched on it alone.
//     Receive ids are unique per peer, so at most one connection matches:
//     ours if we dialed (send id one above), the peer's if we accepted (send
//     id one below).
func routeCandidates(p *packet, pairs *[4][2]uint16) [][2]uint16 {
	id := p.Header.ConnectionId
	switch p.Header.PacketType {
	case st_syn:
		pairs[0] = [2]uint16{id, id + 1}
		return pairs[:1]
	case st_reset:
		pairs[0] = [2]uint16{id + 1, id}
		pairs[1] = [2]uint16{id - 1, id}
		pairs[2] = [2]uint16{id, id - 1}
		pairs[3] = [2]uint16{id, id + 1}
		return pairs[:4]
	default:
		pairs[0] = [2]uint16{id + 1, id}
		pairs[1] = [2]uint16{id - 1, id}
		return pairs[:2]
	}
}

// idInUseLocked reports whether a connection to the peer, live or ended in
// the last idQuarantine, sends or receives on id. Called with connsMutex
// held.
func (s *UtpSocket) idInUseLocked(id uint16, peerKey string) bool {
	var buf [96]byte
	for _, pair := range [4][2]uint16{{id + 1, id}, {id - 1, id}, {id, id + 1}, {id, id - 1}} {
		key := appendConnKey(buf[:0], pair[0], pair[1], peerKey)
		if _, ok := s.conns[string(key)]; ok {
			return true
		}
		if _, ok := s.recentlyClosed.get(string(key)); ok {
			return true
		}
	}
	return false
}

// recvIdTakenLocked reports whether a connection to the peer already receives
// on recv, whichever side opened it. Called with connsMutex held.
func (s *UtpSocket) recvIdTakenLocked(recv uint16, peerKey string) bool {
	var buf [96]byte
	for _, send := range [2]uint16{recv + 1, recv - 1} {
		key := appendConnKey(buf[:0], send, recv, peerKey)
		if _, ok := s.conns[string(key)]; ok {
			return true
		}
	}
	return false
}

func (s *UtpSocket) getConnStream(key string) (chan *streamEvent, bool) {
	s.connsMutex.RLock()
	defer s.connsMutex.RUnlock()
	ch, ok := s.conns[key]
	return ch, ok
}

func (s *UtpSocket) putConnStream(cid *ConnectionId, streamCh chan *streamEvent) {
	s.connsMutex.Lock()
	defer s.connsMutex.Unlock()
	key := cid.Hash()
	if s.logger.Enabled(baseContext, LevelTrace) {
		s.logger.Trace("put conn stream", "key", key)
	}
	s.registerLocked(key, streamCh, cid.Peer)
}

type idType int

const (
	idTypeRecvId idType = iota
	idTypeSendIdWeInitiated
	idTypeSendIdPeerInitiated
)

var cidTypes []idType = []idType{idTypeSendIdPeerInitiated, idTypeSendIdWeInitiated, idTypeRecvId}

func (i idType) String() string {
	switch i {
	case idTypeRecvId:
		return "idTypeRecvId"
	case idTypeSendIdWeInitiated:
		return "idTypeSendIdWeInitiated"
	case idTypeSendIdPeerInitiated:
		return "idTypeSendIdPeerInitiated"
	default:
		return "Unknown"
	}
}

func cidFromPacket(
	packet *packet,
	src ConnectionPeer,
	idType idType,
) *ConnectionId {
	send, recv := cidIds(packet, idType)
	return NewConnectionId(src, recv, send)
}

// cidIds is cidFromPacket's send and receive ids, without the ConnectionId.
func cidIds(packet *packet, idType idType) (send, recv uint16) {
	switch idType {
	case idTypeRecvId:
		switch packet.Header.PacketType {
		case st_syn:
			send = packet.Header.ConnectionId
			recv = packet.Header.ConnectionId + 1 // wrapping add
		case st_state, st_data, st_fin, st_reset: // State, Data, Fin, reset
			send = packet.Header.ConnectionId - 1 // wrapping sub
			recv = packet.Header.ConnectionId
		}

	case idTypeSendIdWeInitiated:
		send = packet.Header.ConnectionId + 1 // wrapping add
		recv = packet.Header.ConnectionId

	case idTypeSendIdPeerInitiated:
		send = packet.Header.ConnectionId
		recv = packet.Header.ConnectionId - 1 // wrapping sub
	}
	return send, recv
}
