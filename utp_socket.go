package utp

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxUDPPayloadSize            = math.MaxUint16
	cidGenerationTryWarningCount = 10
	awaitingConnectionTimeout    = time.Second * 20

	// rstInfoTimeout is how long a sent RESET is remembered, so a peer that
	// keeps sending to a connection we do not have receives one RESET
	// rather than one per packet.
	rstInfoTimeout = 10 * time.Second

	// lingerAckTimeout is how long a closed connection's acknowledgement is
	// kept to answer a retransmitted FIN. It covers the first few
	// retransmissions, where a lost acknowledgement matters.
	lingerAckTimeout = 10 * time.Second

	// idQuarantine is how long a closed connection's ids are withheld from a
	// new connection to the same peer. See generateCid.
	idQuarantine = lingerAckTimeout
	// rstInfoLimit is the number of remembered RESETs beyond which unknown
	// packets are no longer answered at all.
	rstInfoLimit = 1000
)

// rstInfoKey identifies one unanswerable packet, so a repeat of it is not
// answered twice.
type rstInfoKey struct {
	peer   string
	connID uint16
	ackNr  uint16
}

var (
	// ErrAcceptTimedOut is returned by AcceptWithCID when no matching SYN
	// arrives within 20 seconds.
	ErrAcceptTimedOut = errors.New("utp: timed out waiting for incoming connection")
)

// Conn is the datagram transport a Socket runs over.
//
// WriteTo must behave like a UDP socket's: it may wait for the local send
// path, but never for the peer to read. The socket writes from the goroutine
// that reads, so a WriteTo that waited for the other side to read could
// deadlock two sockets. A transport that cannot keep up drops, as UDP does.
// WriteTo is called concurrently.
//
// WriteTo must not retain b after it returns: the socket reuses the buffer.
// net.PacketConn already works this way; a transport that queues datagrams
// must copy them.
//
// A Conn may also have the methods
//
//	HoldPeer(peer ConnectionPeer)
//	ReleasePeer(peer ConnectionPeer)
//
// The socket then calls HoldPeer when it starts a connection with peer, as
// it sends or receives a SYN, and ReleasePeer when it forgets that
// connection. A transport that keeps per-peer state, such as the original
// address to reply to, keeps it while held: from the first handshake packet
// to the last packet of the close. Each HoldPeer is matched by one
// ReleasePeer. They are called with the socket's connection table locked and
// must not call back into the socket.
type Conn interface {
	// ReadFrom reads one datagram into b and returns its length and sender.
	ReadFrom(b []byte) (int, ConnectionPeer, error)
	// WriteTo sends b as one datagram to dst.
	WriteTo(b []byte, dst ConnectionPeer) (int, error)
	// Close closes the transport, ending a ReadFrom in progress.
	Close() error
}

// peerHolder is a Conn that is told which peers the socket has connections
// with. See Conn.
type peerHolder interface {
	HoldPeer(peer ConnectionPeer)
	ReleasePeer(peer ConnectionPeer)
}

// Socket is a uTP endpoint: one datagram transport carrying any number of
// connections. Create one with Bind or NewSocket.
type Socket struct {
	// dispatchMu serializes the routing of incoming packets with everything
	// that changes the routing tables: registering a new connection and
	// serving an Accept. The reader holds it for each datagram it routes.
	// See dispatch.
	dispatchMu sync.Mutex
	// keyScratch is where the reader builds the keys it routes by. Used only
	// under dispatchMu.
	keyScratch []byte
	// connsGen counts changes to conns and inlineConns, each made with
	// connsMutex held for writing. lastRoute, used only under dispatchMu, is
	// the route the last datagram took, good while connsGen has not moved.
	connsGen  atomic.Uint64
	lastRoute cachedRoute
	// writeMu keeps an ordinary write from being sent while an MTU probe has
	// don't-fragment set on the socket. See writeDatagram.
	writeMu sync.RWMutex
	// inlineConns maps a connection's event channel to the connection, so
	// the reader can hand it a packet directly. Guarded by connsMutex.
	inlineConns map[chan *streamEvent]*connection
	ctx         context.Context
	cancel      context.CancelFunc
	logger      *ulogger
	// clk is this socket's clock, handed to every connection it creates. Nil
	// means defaultClock.
	clk        clockSource
	connsMutex sync.RWMutex
	conns      map[string]chan *streamEvent
	// holder is socket, if it is a peerHolder, and held counts the
	// connections holding each peer. Guarded by connsMutex. See
	// registerLocked.
	holder                   peerHolder
	held                     map[string]ConnectionPeer
	accepts                  chan *acceptRequest
	acceptsWithCidCh         chan *acceptRequest
	awaiting                 *syncMap[*acceptRequest]
	awaitingExpirations      *timeWheel[any, *acceptRequest]
	incomingConns            *syncMap[*incomingPacket]
	incomingConnsExpirations *timeWheel[any, *incomingPacket]
	socket                   Conn
	// retransmitTimers is one timer wheel shared by every connection on this
	// socket. See retransmit_timer.go.
	retransmitTimers *retransmitTimers
	// pendingAccepts holds Accept calls that named no connection id and are
	// waiting for any incoming SYN. Guarded by dispatchMu.
	pendingAccepts []*acceptRequest
	// answered holds connections whose SYN was answered with no Accept
	// waiting, oldest first, until an Accept or AcceptWithCID takes one. Only
	// a socket with WithAcceptConfig does this. Guarded by dispatchMu.
	answered []*Stream
	// rstInfo remembers RESETs recently sent for connections we do not have.
	rstInfo            *syncMap[struct{}]
	rstInfoExpirations *timeWheel[any, rstInfoKey]
	// ownsSocket is true when this Socket created the underlying Conn
	// (Bind) and must close it. A Conn passed to NewSocket belongs to the
	// caller.
	ownsSocket bool
	// droppedFullConnQueue counts packets discarded because a connection's
	// event queue was full. Each is loss the peer must recover from.
	droppedFullConnQueue atomic.Uint64
	// lingerAcks holds, for a connection that has just closed after reaching
	// its peer's FIN, the acknowledgement to resend if that FIN arrives
	// again. See connection.lingerAck.
	lingerAcks        *syncMap[*packet]
	lingerExpirations *timeWheel[any, string]
	// recentlyClosed holds the keys of connections that ended within
	// idQuarantine. See generateCid.
	recentlyClosed            *syncMap[struct{}]
	recentlyClosedExpirations *timeWheel[any, string]
	// resetsSent counts RESETs sent in answer to packets for connections this
	// socket does not have.
	resetsSent atomic.Uint64
	// refusedByFirewall counts SYNs the firewall callback rejected.
	refusedByFirewall atomic.Uint64
	closeOnce         sync.Once
	// lingering holds the streams the application has closed whose
	// connections are still delivering. Close waits for them. See linger.
	lingering sync.Map // *Stream -> struct{}
	// firewall, when set, is consulted for every SYN for a connection this
	// socket does not have, before any state is created for it. Returning
	// true refuses the connection. Set at construction and read-only after.
	firewall func(ConnectionPeer) bool
	// acceptConfig is WithAcceptConfig's function, or nil.
	acceptConfig func() *ConnectionConfig
	// maxConns is the incoming-connection cap: a new incoming connection is
	// refused while the socket already holds more than this many. Zero or
	// less means no cap. See WithMaxConnections.
	maxConns int
	// refusedAtCapacity counts SYNs refused because the socket was at its
	// connection limit.
	refusedAtCapacity atomic.Uint64
	// refusedIDInUse counts SYNs refused because a connection we dialed
	// already receives on the id the new one would use. See
	// handleIncomingBuf.
	refusedIDInUse atomic.Uint64
}

// DefaultMaxConnections is the default limit on connections a socket holds
// before it refuses new incoming ones.
const DefaultMaxConnections = 3000

// WithMaxConnections limits the connections this socket holds before it
// refuses new incoming ones. A SYN is refused while the socket holds more
// than n, so up to n+1 are admitted.
//
// Every connection counts, dialed or accepted, established or closing, as
// does every SYN waiting for Accept. A refused SYN is dropped silently: a
// RESET would tell the peer something is listening.
//
// n == 0 means DefaultMaxConnections, the default without this option. n < 0
// means no limit.
func WithMaxConnections(n int) SocketOption {
	return func(s *Socket) {
		switch {
		case n == 0:
			s.maxConns = DefaultMaxConnections
		case n < 0:
			s.maxConns = 0
		default:
			s.maxConns = n
		}
	}
}

// ConnectionsRefusedAtCapacity reports how many incoming connections were
// refused because the socket was at its connection cap. See
// WithMaxConnections.
func (s *Socket) ConnectionsRefusedAtCapacity() uint64 {
	return s.refusedAtCapacity.Load()
}

// ConnectionsRefusedIDInUse reports how many incoming connections were
// refused because a connection this socket dialed to the same peer already
// receives on the id the new one would use.
func (s *Socket) ConnectionsRefusedIDInUse() uint64 {
	return s.refusedIDInUse.Load()
}

// connectionCount returns the number the connection limit is checked
// against. See WithMaxConnections.
func (s *Socket) connectionCount() int {
	return s.NumConnections() + s.incomingConns.len()
}

// SocketOption configures a Socket at construction.
type SocketOption func(*Socket)

// WithFirewall refuses incoming connections the callback rejects.
//
// The callback is consulted for a SYN for a connection this socket does not
// already have, before any state exists for it. Returning true drops the
// packet without an answer, so a refused peer costs no connection state and
// learns nothing; a typical use is an IP blocklist.
//
// The callback runs on the socket's receive path. It must not block or call
// back into the socket.
func WithFirewall(refuse func(peer ConnectionPeer) bool) SocketOption {
	return func(s *Socket) { s.firewall = refuse }
}

// WithAcceptConfig gives the socket its own configuration for incoming
// connections, read as each one is created. With it, a SYN is answered as it
// arrives, whether or not an Accept is waiting, and the connection is held
// until an Accept or AcceptWithCID takes it.
//
// The function's configuration is used for every connection answered before
// an Accept asks, and for one accepted with a nil config. An Accept given a
// config of its own still applies it to a connection whose SYN arrives while
// it waits; one that takes an already answered connection gets it as it was
// created.
//
// Without this option the configuration comes from each Accept, so a SYN
// that arrives before its Accept waits, unanswered, until an Accept claims
// it or 20 seconds pass.
//
// Answered connections count against WithMaxConnections and are held until
// the application accepts them.
func WithAcceptConfig(config func() *ConnectionConfig) SocketOption {
	return func(s *Socket) { s.acceptConfig = config }
}

// configForAccept returns the configuration an accepted connection gets.
func (s *Socket) configForAccept(accept *acceptRequest, peer ConnectionPeer) *ConnectionConfig {
	config := accept.config
	if config == nil && s.acceptConfig != nil {
		config = s.acceptConfig()
	}
	if config == nil {
		config = NewConnectionConfig()
	}
	return s.configForPeer(config, peer)
}

// clock returns this socket's clock, never nil.
func (s *Socket) clock() clockSource {
	if s.clk != nil {
		return s.clk
	}
	return defaultClock
}

// withClock runs this socket's retransmission wheel on clk and makes it the
// default for every connection the socket creates. The wheel is shared by
// every connection on the socket, so the clock is chosen per socket. It is
// used by tests; see clockSource.
func withClock(clk clockSource) SocketOption {
	return func(s *Socket) {
		if clk == nil {
			return
		}
		s.clk = clk
		s.retransmitTimers.stop()
		s.retransmitTimers = newRetransmitTimersWithClock(
			defaultRetransmitTickInterval, defaultRetransmitSlots, clk)
	}
}

// DefaultSocketBufferSize is the size requested for the underlying UDP
// socket's send and receive buffers. A single goroutine drains the socket,
// and datagrams the kernel drops before it does are loss uTP must recover
// from; the operating system's default is too small for a few hundred
// concurrent connections. The kernel clamps the request to its configured
// maximum.
const DefaultSocketBufferSize = 4 * 1024 * 1024

// Bind opens a UDP socket on addr and runs a uTP socket on it, which owns it
// and closes it on Close. logger receives the socket's logging; nil discards
// it. LevelTrace is its per-packet level.
func Bind(ctx context.Context, network string, addr *net.UDPAddr, logger *slog.Logger, opts ...SocketOption) (*Socket, error) {
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
	sock := NewSocket(ctx, &udpConn{base: conn}, logger, opts...)
	sock.ownsSocket = true
	return sock, nil
}

// NewSocket runs a uTP socket on socket, which remains the caller's. logger
// receives the socket's logging; nil discards it. LevelTrace is its
// per-packet level.
func NewSocket(ctx context.Context, socket Conn, logger *slog.Logger, opts ...SocketOption) *Socket {
	ctx, cancel := context.WithCancel(ctx)
	ulog := newLogger(logger)

	awaitingMap := newSyncMap[*acceptRequest]()
	handleAwaitExpirations := func(key any, accReq *acceptRequest) {
		awaitingMap.remove(key)
		// Report it to the caller, so AcceptWithCID does not wait forever.
		select {
		case accReq.stream <- &streamResult{err: ErrAcceptTimedOut}:
		default:
		}
	}
	awaitExpirations := newTimeWheel[any, *acceptRequest](2*time.Second, 20, handleAwaitExpirations)

	incomingConns := newSyncMap[*incomingPacket]()
	handleIncomingExpirations := func(key any, incomingPacket *incomingPacket) {
		incomingConns.remove(key.(string))
	}
	incomingExpirations := newTimeWheel[any, *incomingPacket](2*time.Second, 20, handleIncomingExpirations)

	rstInfo := newSyncMap[struct{}]()
	rstExpirations := newTimeWheel[any, rstInfoKey](time.Second, 16, func(key any, _ rstInfoKey) {
		rstInfo.remove(key)
	})

	lingerAcks := newSyncMap[*packet]()
	lingerExpirations := newTimeWheel[any, string](time.Second, 16, func(key any, _ string) {
		lingerAcks.remove(key)
	})

	recentlyClosed := newSyncMap[struct{}]()
	recentlyClosedExpirations := newTimeWheel[any, string](time.Second, 16, func(key any, _ string) {
		recentlyClosed.remove(key)
	})

	utp := &Socket{
		recentlyClosed:            recentlyClosed,
		recentlyClosedExpirations: recentlyClosedExpirations,
		ctx:                       ctx,
		cancel:                    cancel,
		lingerAcks:                lingerAcks,
		lingerExpirations:         lingerExpirations,
		retransmitTimers:          newRetransmitTimers(defaultRetransmitTickInterval, defaultRetransmitSlots),
		rstInfo:                   rstInfo,
		rstInfoExpirations:        rstExpirations,
		logger:                    ulog,
		conns:                     make(map[string]chan *streamEvent),
		inlineConns:               make(map[chan *streamEvent]*connection),
		accepts:                   make(chan *acceptRequest, 1000),
		acceptsWithCidCh:          make(chan *acceptRequest, 1000),
		awaiting:                  awaitingMap,
		awaitingExpirations:       awaitExpirations,
		incomingConns:             incomingConns,
		incomingConnsExpirations:  incomingExpirations,
		socket:                    socket,
		maxConns:                  DefaultMaxConnections,
	}
	if h, ok := socket.(peerHolder); ok {
		utp.holder = h
	}

	// Applied before the loops start, so nothing observes a partly built
	// socket.
	for _, opt := range opts {
		opt(utp)
	}

	go utp.readLoop()
	go utp.eventLoop()

	return utp
}

// PacketsDroppedFullConnQueue reports how many received packets this socket
// discarded because the destination connection's event queue was full. It
// should be zero.
func (s *Socket) PacketsDroppedFullConnQueue() uint64 {
	return s.droppedFullConnQueue.Load()
}

// ConnectionsRefusedByFirewall reports how many incoming connections the
// firewall callback rejected.
func (s *Socket) ConnectionsRefusedByFirewall() uint64 {
	return s.refusedByFirewall.Load()
}

// PacketsResetSent reports how many RESETs this socket sent in answer to
// packets for connections it does not have.
func (s *Socket) PacketsResetSent() uint64 { return s.resetsSent.Load() }

// LocalAddr returns the address the underlying socket is bound to, or nil if
// it cannot report one. After binding to port 0 it reports the chosen port.
func (s *Socket) LocalAddr() net.Addr {
	type localAddresser interface{ LocalAddr() net.Addr }
	if la, ok := s.socket.(localAddresser); ok {
		return la.LocalAddr()
	}
	return nil
}

// NumConnections returns how many connections the socket currently tracks.
func (s *Socket) NumConnections() int {
	s.connsMutex.Lock()
	defer s.connsMutex.Unlock()
	return len(s.conns)
}

// Close shuts the socket down. It is safe to call more than once.
//
// It first waits for streams already closed to finish delivering what was
// written to them, while each one's peer keeps answering (awaitLingering);
// everything else ends at once.
func (s *Socket) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.awaitLingering()
		s.cancel()
		s.sendShutdownEventToConns()
		s.awaitingExpirations.stop()
		s.incomingConnsExpirations.stop()
		s.retransmitTimers.stop()
		s.rstInfoExpirations.stop()
		s.lingerExpirations.stop()
		s.recentlyClosedExpirations.stop()
		// Close the underlying socket if we opened it, which releases the
		// port and stops readLoop.
		if s.ownsSocket {
			err = s.socket.Close()
		}
	})
	return err
}

// linger keeps a closed stream's connection running until it has
// delivered what was queued and ended, and lets Close wait for it.
// Stream.Close returns at once and the connection keeps sending in the
// background; closing the socket waits for it, so that writing, closing the
// stream, closing the socket and exiting delivers every byte.
func (s *Socket) linger(stream *Stream) {
	s.lingering.Store(stream, struct{}{})
	go func() {
		stream.connHandle.Wait()
		s.lingering.Delete(stream)
		stream.streamCancel()
	}()
}

// awaitLingering waits for every closed stream's connection to finish
// delivering, while its peer is still answering: a connection whose peer has
// been silent for closeStallTimeout is waited for no longer, and the socket's
// cancellation then ends it. See Stream.waitForFlush.
func (s *Socket) awaitLingering() {
	var wg sync.WaitGroup
	s.lingering.Range(func(key, _ any) bool {
		stream := key.(*Stream)
		wg.Add(1)
		go func() {
			defer wg.Done()
			stream.waitForFlush()
		}()
		return true
	})
	wg.Wait()
}

// wakeConns starts the event loop of every resting connection on the socket.
// See dormant.go.
func (s *Socket) wakeConns() {
	s.connsMutex.RLock()
	conns := make([]*connection, 0, len(s.inlineConns))
	for _, c := range s.inlineConns {
		conns = append(conns, c)
	}
	s.connsMutex.RUnlock()
	for _, c := range conns {
		c.wake()
	}
}

func (s *Socket) sendShutdownEventToConns() {
	// Snapshot the channels rather than send under the lock: a blocking send
	// to one stuck connection would hold connsMutex and stall packet
	// delivery for every other connection.
	s.connsMutex.RLock()
	channels := make([]chan *streamEvent, 0, len(s.conns))
	conns := make([]*connection, 0, len(s.conns))
	for _, ch := range s.conns {
		channels = append(channels, ch)
		conns = append(conns, s.inlineConns[ch])
	}
	s.connsMutex.RUnlock()

	for i, ch := range channels {
		select {
		case ch <- &streamEvent{Type: streamShutdown}:
		default:
			// The connection's queue is full; it is already ending with the
			// socket's context.
		}
		if conns[i] != nil {
			conns[i].wake()
		}
	}
}

// configForPeer returns the connection config for a peer, with the path-MTU
// ceiling lowered when the socket's Conn can report what the local path
// carries. The caller's config is copied, never modified. See
// pathMTUCeiling.
func (s *Socket) configForPeer(config *ConnectionConfig, peer ConnectionPeer) *ConnectionConfig {
	// Nil means the defaults, as for an accept (configForAccept).
	if config == nil {
		config = NewConnectionConfig()
	}
	// A socket-wide clock is applied here, unless the connection's config
	// sets its own.
	if s.clk != nil && config.clock == nil {
		adjusted := *config
		adjusted.clock = s.clk
		config = &adjusted
	}
	if s.socket == nil {
		return config
	}
	ceiling := pathMTUCeiling(s.socket, peer, ipv6Ceiling(peer, config.MaxPacketSize))
	if ceiling == config.MaxPacketSize {
		return config
	}
	s.logger.Debug("lowering the path-MTU ceiling to what the path is known to carry",
		"configured", config.MaxPacketSize, "discovered", ceiling, "peer", peer.Hash())
	adjusted := *config
	adjusted.MaxPacketSize = ceiling
	return &adjusted
}
