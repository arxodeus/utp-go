package utp_go

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p/netutil"
)

const (
	MAX_UDP_PAYLOAD_SIZE         = math.MaxUint16
	CidGenerationTryWarningCount = 10
	AWAITING_CONNECTION_TIMEOUT  = time.Second * 20
)

var (
	ErrConnect = errors.New("utp_socket: connect error")
	// ErrAcceptTimedOut is returned by Accept/AcceptWithCid when no matching
	// SYN arrived within AWAITING_CONNECTION_TIMEOUT.
	ErrAcceptTimedOut = errors.New("utp_socket: timed out waiting for incoming connection")
)

type PeerInfo interface {
	Hash() [32]byte
}

type Conn interface {
	ReadFrom(b []byte) (int, ConnectionPeer, error)
	WriteTo(b []byte, dst ConnectionPeer) (int, error)
	Close() error
}

type StreamResult struct {
	stream *UtpStream
	err    error
}

type Accept struct {
	ctx    context.Context
	stream chan *StreamResult
	config *ConnectionConfig
	cid    *ConnectionId
	hasCid bool
}

type UdpConn struct {
	base *net.UDPConn
}

func (c *UdpConn) ReadFrom(b []byte) (int, ConnectionPeer, error) {
	n, addr, err := c.base.ReadFrom(b)
	if err != nil {
		return 0, nil, err
	}
	udpAddr := addr.(*net.UDPAddr)
	return n, &UdpPeer{addr: udpAddr}, nil
}
func (c *UdpConn) WriteTo(b []byte, dst ConnectionPeer) (int, error) {
	if dst == nil {
		return 0, fmt.Errorf("utp_socket: can't write to nil connection")
	}
	switch baseDst := dst.(type) {
	case *ConnectionId:
		return c.WriteTo(b, baseDst.Peer)
	case *UdpPeer:
		return c.base.WriteToUDP(b, baseDst.addr)
	}
	return 0, nil
}

func (c *UdpConn) Close() error {
	return c.base.Close()
}

type IncomingPacketRaw struct {
	peer    ConnectionPeer
	payload []byte
}

type IncomingPacket struct {
	pkt *packet
	cid *ConnectionId
}

type UtpSocket struct {
	ctx                      context.Context
	cancel                   context.CancelFunc
	logger                   log.Logger
	connsMutex               sync.RWMutex
	conns                    map[string]chan *streamEvent
	accepts                  chan *Accept
	acceptsWithCidCh         chan *Accept
	socketEvents             chan *socketEvent
	awaiting                 *syncMap[*Accept]
	awaitingExpirations      *timeWheel[*Accept]
	incomingConns            *syncMap[*IncomingPacket]
	incomingConnsExpirations *timeWheel[*IncomingPacket]
	socket                   Conn
	// retransmitTimers is one timer wheel for every connection on this
	// socket. Per-connection wheels cost a ticker each; see
	// retransmit_timer.go.
	retransmitTimers *retransmitTimers
	// ownsSocket is true when this UtpSocket created the underlying Conn
	// (via Bind) and is therefore responsible for closing it. A Conn handed
	// in through WithSocket belongs to the caller.
	ownsSocket  bool
	closeOnce   sync.Once
	readNextCh  chan struct{}
	incomingBuf chan *IncomingPacketRaw
}

// DefaultSocketBufferSize is the size requested for the underlying UDP
// socket's send and receive buffers.
//
// A single readLoop goroutine drains the socket, so anything the kernel drops
// before it gets there is invisible packet loss that uTP then has to recover
// from with retransmits. At the OS default (~208 KiB on Linux) a few hundred
// concurrent connections overflow the receive queue and handshakes start
// failing outright. The kernel silently clamps this to net.core.rmem_max, so
// requesting more than the system allows is harmless.
const DefaultSocketBufferSize = 4 * 1024 * 1024

func Bind(ctx context.Context, network string, addr *net.UDPAddr, logger log.Logger) (*UtpSocket, error) {
	conn, err := net.ListenUDP(network, addr)
	if err != nil {
		return nil, err
	}
	// Best effort: a smaller buffer costs throughput but is not fatal.
	if err := conn.SetReadBuffer(DefaultSocketBufferSize); err != nil && logger != nil {
		logger.Debug("could not enlarge UDP read buffer", "err", err)
	}
	if err := conn.SetWriteBuffer(DefaultSocketBufferSize); err != nil && logger != nil {
		logger.Debug("could not enlarge UDP write buffer", "err", err)
	}
	sock := WithSocket(ctx, &UdpConn{conn}, logger)
	sock.ownsSocket = true
	return sock, nil
}

func WithSocket(ctx context.Context, socket Conn, logger log.Logger) *UtpSocket {
	ctx, cancel := context.WithCancel(ctx)
	if logger == nil {
		logger = log.New("utp", "socket")
	}

	awaitingMap := newSyncMap[*Accept]()
	handleAwaitExpirations := func(key any, accReq *Accept) {
		awaitingMap.remove(key)
		// Tell the caller. Dropping the request silently left AcceptWithCid
		// blocked on accReq.stream forever whenever the caller's own context
		// had no deadline.
		select {
		case accReq.stream <- &StreamResult{err: ErrAcceptTimedOut}:
		default:
		}
	}
	awaitExpirations := newTimeWheel[*Accept](2*time.Second, 20, handleAwaitExpirations)

	incomingConns := newSyncMap[*IncomingPacket]()
	handleIncomingExpirations := func(key any, incomingPacket *IncomingPacket) {
		incomingConns.remove(key.(string))
	}
	incomingExpirations := newTimeWheel[*IncomingPacket](2*time.Second, 20, handleIncomingExpirations)

	utp := &UtpSocket{
		ctx:                      ctx,
		cancel:                   cancel,
		retransmitTimers:         newRetransmitTimers(defaultRetransmitTickInterval, defaultRetransmitSlots),
		logger:                   logger,
		conns:                    make(map[string]chan *streamEvent),
		accepts:                  make(chan *Accept, 1000),
		acceptsWithCidCh:         make(chan *Accept, 1000),
		socketEvents:             make(chan *socketEvent, 1000000),
		awaiting:                 awaitingMap,
		awaitingExpirations:      awaitExpirations,
		incomingConns:            incomingConns,
		incomingConnsExpirations: incomingExpirations,
		socket:                   socket,
		readNextCh:               make(chan struct{}, 1000000),
		incomingBuf:              make(chan *IncomingPacketRaw, 1000000),
	}

	go utp.readLoop()
	go utp.writeLoop()
	go utp.eventLoop()

	return utp
}

func (s *UtpSocket) readLoop() {
	buf := make([]byte, math.MaxUint16)

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			n, from, err := s.socket.ReadFrom(buf)
			if s.logger.Enabled(BASE_CONTEXT, log.LevelDebug) {
				s.logger.Debug("read data from base socket", "n", n, "from", from)
			}
			if netutil.IsTemporaryError(err) {
				// Ignore temporary read errors.
				s.logger.Error("Temporary UDP read error", "err", err)
				continue
			} else if err != nil {
				// Shut down the loop for permanent errors.
				if !errors.Is(err, io.EOF) {
					s.logger.Error("UDP read error", "err", err)
				}
				return
			}
			dstBuf := make([]byte, n)
			copy(dstBuf, buf[:n])
			s.incomingBuf <- &IncomingPacketRaw{peer: from, payload: dstBuf}
		}

	}
}

func (s *UtpSocket) writeLoop() {
	for event := range s.socketEvents {
		if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			s.logger.Trace("a socket event should be sent to target", "socketEvents.len", len(s.socketEvents), "event.type", event.Type, "event.cid", event.ConnectionId)
		}
		switch event.Type {
		case outgoing:
			encoded := event.Packet.Encode()
			if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				s.logger.Trace("Send a packet out",
					"s.socketEvents.len", len(s.socketEvents),
					"target.cid", event.ConnectionId,
					"packet.type", event.Packet.Header.PacketType.String(),
					"packet.seqNum", event.Packet.Header.SeqNum,
					"packet.ackNum", event.Packet.Header.AckNum,
					"packet.body.len", len(event.Packet.Body),
					"encoded.len", len(encoded))
			}
			var peer ConnectionPeer
			if cid, ok := event.ConnectionId.(*ConnectionId); ok {
				peer = cid.Peer
			} else {
				peer = event.ConnectionId
			}
			if _, err := s.socket.WriteTo(encoded, peer); err != nil {
				var eackEncodeLen int
				if event.Packet.Eack != nil {
					eackEncodeLen = event.Packet.Eack.EncodedLen()
				}
				if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
					s.logger.Trace("Failed to send uTP packet",
						"error", err,
						"cid", event.Packet.Header.ConnectionId,
						"type", event.Packet.Header.PacketType,
						"encoded.eack.len", eackEncodeLen,
						"encoded.len", len(encoded))
				}
			}

		case socketShutdown:
			if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				s.logger.Trace("uTP conn shutdown", "cid.Hash", event.ConnectionId.Hash())
			}
			s.removeConnStream(event.ConnectionId.Hash())
		}
	}
}

func (s *UtpSocket) eventLoop() {
	for {
		// Serve pending accept requests before incoming packets.
		//
		// This priority used to be the other way round: the loop drained
		// incomingBuf first and only looked at the accept channels when no
		// packet was queued. Under load incomingBuf is never empty, so accepts
		// starved completely and expired after AWAITING_CONNECTION_TIMEOUT --
		// with hundreds of concurrent connections, AcceptWithCid failed on
		// connections whose SYN had already arrived and was sitting in the
		// incoming-conns map.
		//
		// Accepts are safe to prefer: they are driven by the application, at
		// most one per connection, and handling one is a map lookup. Packets
		// are the high-volume side and cannot be starved by them.
		select {
		case acceptWithCid := <-s.acceptsWithCidCh:
			s.handleNewAcceptWithCidEvent(acceptWithCid)
			continue
		case accept := <-s.accepts:
			s.handleNewAcceptEvent(accept)
			continue
		default:
		}
		select {
		case acceptWithCid := <-s.acceptsWithCidCh:
			s.handleNewAcceptWithCidEvent(acceptWithCid)
		case accept := <-s.accepts:
			s.handleNewAcceptEvent(accept)
		case incomingRaw := <-s.incomingBuf:
			if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				s.logger.Trace("will handle a packet from remote", "s.incomingBuf.len", len(s.incomingBuf))
			}
			s.handleIncomingBuf(incomingRaw)
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *UtpSocket) handleIncomingBuf(incomingRaw *IncomingPacketRaw) {
	// Handle incoming packets
	packetPtr, err := DecodePacket(incomingRaw.payload)
	if err != nil {
		s.logger.Warn("Unable to decode uTP packet", "peer", incomingRaw.peer, "err", err)
		return
	}

	if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		s.logger.Trace("receive incoming packet",
			"s.incomingBuf.len", len(s.incomingBuf),
			"src.peer", incomingRaw.peer,
			"packet.type", packetPtr.Header.PacketType.String(),
			"packet.cid", packetPtr.Header.ConnectionId,
			"packet.seqNum", packetPtr.Header.SeqNum,
			"packet.ackNum", packetPtr.Header.AckNum,
			"packet.Data.len", len(packetPtr.Body))
	}

	// used by syn_pkt init
	cids := make([]*ConnectionId, 3)
	for i, cidType := range cidTypes {
		cid := CidFromPacket(packetPtr, incomingRaw.peer, cidType)
		cids[i] = cid
		// Look for existing connection
		if connStream := s.getConnStreamWithCids(cid, cidType); connStream != nil {
			select {
			case connStream <- &streamEvent{
				Type:   streamIncoming,
				Packet: packetPtr,
			}:
			default:
				if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
					s.logger.Warn("connection stream channel is full, dropping packet",
						"connStream.len", len(connStream), "cid.send", cid.Send, "cid.recv", cid.Recv, "cid.peer", cid.Peer.Hash())
				}
				return
			}
			if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				s.logger.Trace("recieve a packet for a exist conn stream", "connStream.len", len(connStream))
			}
			return
		}
	}

	if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		s.logger.Trace("received uTP packet for non-existing conn",
			"cid", packetPtr.Header.ConnectionId,
			"packetType", packetPtr.Header.PacketType,
			"seq", packetPtr.Header.SeqNum,
			"ack", packetPtr.Header.AckNum,
			"peerInitCID", cids[0],
			"weInitCid", cids[1],
			"accCID", cids[2])
	}
	if packetPtr.Header.PacketType != st_syn {
		if packetPtr.Header.PacketType != st_reset {
			randSeqNum := RandomUint16()
			resetPacket := NewPacketBuilder(st_reset, packetPtr.Header.ConnectionId, uint32(time.Now().UnixMicro()), 100_000, randSeqNum).Build()

			s.socketEvents <- newOutgoingSocketEvent(resetPacket, incomingRaw.peer)
		}
		return
	}

	cid := cids[2]
	cidHash := cid.Hash()
	s.logger.Debug("receive a syn packet from a new conn stream",
		"src.peer", incomingRaw.peer,
		"cid.Send", cid.Send,
		"cid.Recv", cid.Recv,
		"cid.hash", cidHash)

	if accept, exist := s.getAwaiting(cidHash); exist {
		if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			s.logger.Trace("found a accept request from awaiting map...",
				"accept.cid.Send", accept.cid.Send, "accept.cid.Recv", accept.cid.Recv)
		}
		s.removeAwaiting(cidHash)
		connected := make(chan error, 1)
		newConnStream := make(chan *streamEvent, 1000)
		s.putConnStream(cidHash, newConnStream)
		stream := NewUtpStream(s.ctx, s.logger, cid, accept.config, packetPtr, s.socketEvents, newConnStream, connected, s.retransmitTimers)
		go s.awaitConnected(stream, accept, connected)
	} else {
		s.logger.Debug("put a new syn packet to incomingConns...")
		s.putIncomingConn(cidHash, &IncomingPacket{pkt: packetPtr, cid: cid})
	}
}

func (s *UtpSocket) handleNewAcceptWithCidEvent(acceptWithCid *Accept) {
	s.logger.Debug("get a accept event with cid",
		"accept.cid.Send", acceptWithCid.cid.Send,
		"accept.cid.Recv",
		acceptWithCid.cid.Recv, "accept.cid.Peer", acceptWithCid.cid.Peer,
		"hash", acceptWithCid.cid.Hash())
	incomingConnsKey := acceptWithCid.cid.Hash()

	if incomingConn, exists := s.removeIncomingConn(incomingConnsKey); exists {
		if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			s.logger.Trace("conn has already accepted", "key", incomingConnsKey)
		}
		s.selectAcceptHelper(acceptWithCid.ctx, acceptWithCid.cid, incomingConn.pkt, acceptWithCid, s.socketEvents)
	} else {
		if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			s.logger.Trace("wait for the syn pkt arrive", "key", incomingConnsKey)
		}
		s.putAwaiting(incomingConnsKey, acceptWithCid)
	}
}

func (s *UtpSocket) handleNewAcceptEvent(accept *Accept) {
	incomingAccept := s.nextIncomingConn()
	if incomingAccept != nil {
		s.selectAcceptHelper(accept.ctx, incomingAccept.cid, incomingAccept.pkt, accept, s.socketEvents)
	} else {
		accept.stream <- &StreamResult{
			err: errors.New("no incoming conn"),
		}
	}
}

func (s *UtpSocket) getAwaiting(key string) (*Accept, bool) {
	accept, ok := s.awaiting.get(key)
	return accept, ok
}

func (s *UtpSocket) putAwaiting(key string, acceptWithCid *Accept) {
	s.awaiting.put(key, acceptWithCid)
	s.awaitingExpirations.put(key, acceptWithCid, AWAITING_CONNECTION_TIMEOUT)
}

func (s *UtpSocket) removeAwaiting(key string) {
	s.awaiting.remove(key)
	s.awaitingExpirations.remove(key)
}

func (s *UtpSocket) putIncomingConn(key string, incomingConn *IncomingPacket) {
	s.incomingConns.put(key, incomingConn)
	s.incomingConnsExpirations.put(key, incomingConn, AWAITING_CONNECTION_TIMEOUT)
}

func (s *UtpSocket) removeIncomingConn(key string) (*IncomingPacket, bool) {
	incomingConn, exists := s.incomingConns.get(key)
	if exists {
		s.incomingConns.remove(key)
		s.incomingConnsExpirations.remove(key)
	}
	return incomingConn, exists
}

func (s *UtpSocket) nextIncomingConn() *IncomingPacket {
	var cidHash string
	var incomingAccept *IncomingPacket
	s.incomingConns.Range(func(key any, accept *IncomingPacket) bool {
		cidHash = key.(string)
		incomingAccept = accept
		return false
	})
	if incomingAccept != nil {
		s.removeIncomingConn(cidHash)
	}
	return incomingAccept
}

func (s *UtpSocket) NumConnections() int {
	s.connsMutex.Lock()
	defer s.connsMutex.Unlock()
	return len(s.conns)
}

// Close shuts the socket down. It is idempotent: `defer sock.Close()`
// alongside an explicit Close is ordinary Go, and the second call used to
// panic closing an already-closed channel.
func (s *UtpSocket) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		s.sendShutdownEventToConns()
		s.awaitingExpirations.stop()
		s.incomingConnsExpirations.stop()
		s.retransmitTimers.stop()
		// Close the underlying socket when we opened it. Without this the UDP
		// port stayed bound for the life of the process and readLoop stayed
		// parked in ReadFrom, which cancelling the context does not interrupt
		// -- an fd and goroutine leak, and the reason two runs of the same
		// test in one process failed with "address already in use".
		if s.ownsSocket {
			if err := s.socket.Close(); err != nil {
				s.logger.Debug("error closing underlying socket", "err", err)
			}
		}
	})
}

func (s *UtpSocket) Cid(peer ConnectionPeer, isInitiator bool) *ConnectionId {
	return s.GenerateCid(peer, isInitiator, nil)
}

func (s *UtpSocket) GenerateCid(peer ConnectionPeer, isInitiator bool, eventCh chan *streamEvent) *ConnectionId {
	cid := &ConnectionId{
		Peer: peer,
	}

	generationAttemptCount := 0
	for {
		// Generate random recv ID
		recv := RandomUint16()
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

		if _, exists := s.getConnStream(cid.Hash()); !exists {
			if eventCh != nil {
				s.putConnStream(cid.Hash(), eventCh)
			}
			break
		}
		generationAttemptCount++
	}
	return cid
}

func (s *UtpSocket) Accept(ctx context.Context, config *ConnectionConfig) (*UtpStream, error) {
	accept := &Accept{
		ctx:    ctx,
		stream: make(chan *StreamResult, 1),
		config: config,
		hasCid: false,
	}

	// Send accept request through channel
	select {
	case s.accepts <- accept:
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}

	// Wait for stream or timeout
	select {
	case streamRes := <-accept.stream:
		if streamRes == nil {
			return nil, fmt.Errorf("stream creation failed")
		}
		if streamRes.err != nil {
			return nil, streamRes.err
		}
		return streamRes.stream, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *UtpSocket) AcceptWithCid(ctx context.Context, cid *ConnectionId, config *ConnectionConfig) (*UtpStream, error) {
	accept := &Accept{
		ctx:    ctx,
		stream: make(chan *StreamResult, 1),
		config: config,
		hasCid: true,
		cid:    cid,
	}

	// Send accept request through channel
	select {
	case s.acceptsWithCidCh <- accept:
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}

	// Wait for stream or timeout
	select {
	case streamRes := <-accept.stream:
		if streamRes == nil {
			return nil, fmt.Errorf("stream creation failed")
		}
		if streamRes.err != nil {
			return nil, streamRes.err
		}
		return streamRes.stream, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *UtpSocket) Connect(ctx context.Context, peer ConnectionPeer, config *ConnectionConfig) (*UtpStream, error) {
	// Create channels for connection status and events
	connectedCh := make(chan error, 1)
	streamEvents := make(chan *streamEvent, 1000)

	// Generate connection ID
	cid := s.GenerateCid(peer, true, streamEvents)
	s.logger.Info("connecting", "dst.peer.hash", peer.Hash(), "dst.send", cid.Send, "dst.recv", cid.Recv, "dst.hash", cid.Hash(), "dst.peer", peer)
	// Create new UTP stream
	s.putConnStream(cid.Hash(), streamEvents)
	stream := NewUtpStream(
		ctx,
		s.logger,
		cid,
		config,
		nil,
		s.socketEvents,
		streamEvents,
		connectedCh,
		s.retransmitTimers,
	)

	// Wait for connection result
	select {
	case err, ok := <-connectedCh:
		if ok && err == nil {
			return stream, nil
		} else if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("connection timed out")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *UtpSocket) ConnectWithCid(
	ctx context.Context,
	cid *ConnectionId,
	config *ConnectionConfig,
) (*UtpStream, error) {
	if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		s.logger.Trace("connecting with cid", "dst.peer.hash", cid.Peer.Hash(),
			"dst.send", cid.Send, "dst.recv", cid.Recv, "dst.hash", cid.Hash(), "dst.peer", cid.Peer)
	}
	_, exists := s.getConnStream(cid.Hash())
	if exists {
		return nil, fmt.Errorf("connection ID unavailable")
	}

	connected := make(chan error, 1)
	streamEvents := make(chan *streamEvent, 1000)

	s.putConnStream(cid.Hash(), streamEvents)
	stream := NewUtpStream(
		ctx,
		s.logger,
		cid,
		config,
		nil,
		s.socketEvents,
		streamEvents,
		connected,
		s.retransmitTimers,
	)
	err := <-connected
	if err == nil {
		return stream, nil
	} else {
		s.logger.Error("failed to open connection", "cid.send", cid.Send, "cid.recv", cid.Recv, "cid.peer", cid.Peer.Hash())
		return nil, fmt.Errorf("connection timed out")
	}
}

func (s *UtpSocket) awaitConnected(
	stream *UtpStream,
	accept *Accept,
	connected chan error,
) {
	s.logger.Debug("waiting for answering the new connections", "dst.peer", accept.cid.Peer.Hash(), "cid.Send", accept.cid.Send, "cid.Recv", accept.cid.Recv)
	err, ok := <-connected
	if err == nil && ok {
		if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			s.logger.Trace("new connection created", "src", accept.cid.Peer.Hash(), "src.cid.Send", accept.cid.Send, "src.cid.Recv", accept.cid.Recv)
		}
		accept.stream <- &StreamResult{stream: stream}
		return
	} else if err != nil {
		if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			s.logger.Trace("connected failed", "peer", accept.cid.Peer.Hash(), "cid.Send", accept.cid.Send, "cid.Recv", accept.cid.Recv, "err", err)
		}
		accept.stream <- &StreamResult{err: fmt.Errorf("connection failed")}
		return
	}

	s.logger.Warn("connected failed", "peer", accept.cid.Peer.Hash(), "cid.Send", accept.cid.Send, "cid.Recv", accept.cid.Recv)
	accept.stream <- &StreamResult{err: fmt.Errorf("connection aborted")}
}

func (s *UtpSocket) selectAcceptHelper(
	streamCtx context.Context,
	cid *ConnectionId,
	syn *packet,
	accept *Accept,
	socketEvents chan *socketEvent,
) {
	if _, exists := s.getConnStream(cid.Hash()); exists {
		accept.stream <- &StreamResult{
			stream: nil,
			err:    fmt.Errorf("connection ID unavailable"),
		}
		return
	}

	connected := make(chan error, 1)
	streamEvents := make(chan *streamEvent, 1000)

	if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		s.logger.Trace("put a conn stream at selectAcceptHelper", "cid.Peer", cid.Peer, "cid", cid)
	}
	s.putConnStream(cid.Hash(), streamEvents)

	stream := NewUtpStream(
		streamCtx,
		s.logger,
		cid,
		accept.config,
		syn,
		socketEvents,
		streamEvents,
		connected,
		s.retransmitTimers,
	)

	go s.awaitConnected(stream, accept, connected)
}

func (s *UtpSocket) removeConnStream(key string) {
	s.connsMutex.Lock()
	defer s.connsMutex.Unlock()
	if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		s.logger.Trace("remove conn stream", "key", key)
	}
	delete(s.conns, key)
}

func (s *UtpSocket) getConnStream(key string) (chan *streamEvent, bool) {
	s.connsMutex.RLock()
	defer s.connsMutex.RUnlock()
	ch, ok := s.conns[key]
	return ch, ok
}

func (s *UtpSocket) putConnStream(key string, streamCh chan *streamEvent) {
	s.connsMutex.Lock()
	defer s.connsMutex.Unlock()
	if s.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		s.logger.Trace("put conn stream", "key", key)
	}
	s.conns[key] = streamCh
}

func (s *UtpSocket) sendShutdownEventToConns() {
	// Snapshot the channels rather than sending under the lock: a blocking
	// send to one wedged connection would otherwise hold connsMutex and stall
	// packet delivery for every other connection on this socket.
	s.connsMutex.RLock()
	channels := make([]chan *streamEvent, 0, len(s.conns))
	for _, ch := range s.conns {
		channels = append(channels, ch)
	}
	s.connsMutex.RUnlock()

	for _, ch := range channels {
		select {
		case ch <- &streamEvent{Type: streamShutdown}:
		default:
			// The connection's queue is full; it is already being torn down
			// by the cancelled socket context.
		}
	}
}

func (s *UtpSocket) getConnStreamWithCids(cid *ConnectionId, idType IdType) chan *streamEvent {
	s.connsMutex.RLock()
	defer s.connsMutex.RUnlock()
	if ch, exist := s.conns[cid.Hash()]; exist {
		if s.logger.Enabled(BASE_CONTEXT, log.LevelDebug) {
			s.logger.Debug("get conn stream", "accInitCidKey", cid.Hash(), "accCid.Send", cid.Send, "accCid.Recv", cid.Recv)
		}
		return ch
	}

	return nil
}

type IdType int

const (
	IdTypeRecvId IdType = iota
	IdTypeSendIdWeInitiated
	IdTypeSendIdPeerInitiated
)

var cidTypes []IdType = []IdType{IdTypeSendIdPeerInitiated, IdTypeSendIdWeInitiated, IdTypeRecvId}

func (i IdType) String() string {
	switch i {
	case IdTypeRecvId:
		return "IdTypeRecvId"
	case IdTypeSendIdWeInitiated:
		return "IdTypeSendIdWeInitiated"
	case IdTypeSendIdPeerInitiated:
		return "IdTypeSendIdPeerInitiated"
	default:
		return "Unknown"
	}
}

func CidFromPacket(
	packet *packet,
	src ConnectionPeer,
	idType IdType,
) *ConnectionId {
	var send, recv uint16

	switch idType {
	case IdTypeRecvId:
		switch packet.Header.PacketType {
		case st_syn:
			send = packet.Header.ConnectionId
			recv = packet.Header.ConnectionId + 1 // wrapping add
		case st_state, st_data, st_fin, st_reset: // State, Data, Fin, reset
			send = packet.Header.ConnectionId - 1 // wrapping sub
			recv = packet.Header.ConnectionId
		}

	case IdTypeSendIdWeInitiated:
		send = packet.Header.ConnectionId + 1 // wrapping add
		recv = packet.Header.ConnectionId

	case IdTypeSendIdPeerInitiated:
		send = packet.Header.ConnectionId
		recv = packet.Header.ConnectionId - 1 // wrapping sub
	}

	return NewConnectionId(src, recv, send)
}
