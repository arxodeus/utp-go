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
	maxUdpPayloadSize            = math.MaxUint16
	cidGenerationTryWarningCount = 10
	awaitingConnectionTimeout    = time.Second * 20

	// rstInfoTimeout is how long a sent RESET is remembered, so a peer that
	// keeps sending packets for a connection we do not have receives one
	// RESET rather than one per packet. Matches libutp's RST_INFO_TIMEOUT
	// (utp_internal.cpp:71).
	rstInfoTimeout = 10 * time.Second

	// lingerAckTimeout is how long a closed connection's acknowledgement is
	// kept so a retransmitted FIN can be answered with it. libutp's FIN
	// retransmissions back off from its 1000ms RTO floor, so this covers the
	// first few, which is where a lost acknowledgement costs something.
	lingerAckTimeout = 10 * time.Second

	// idQuarantine is how long a closed connection's ids are kept from a new
	// connection to the same peer. See GenerateCid.
	idQuarantine = lingerAckTimeout
	// rstInfoLimit is the number of remembered RESETs past which we stop
	// answering unknown packets at all. Matches libutp's RST_INFO_LIMIT
	// (utp_internal.cpp:72).
	rstInfoLimit = 1000
)

// rstInfoKey identifies one unanswerable packet, so a repeat of the same one
// is not answered twice. libutp keys on the same triple
// (utp_internal.cpp:2913-2917).
type rstInfoKey struct {
	peer   string
	connID uint16
	ackNr  uint16
}

var (
	// ErrAcceptTimedOut is returned by Accept/AcceptWithCid when no matching
	// SYN arrived within 20 seconds.
	ErrAcceptTimedOut = errors.New("utp_socket: timed out waiting for incoming connection")
)

// Conn is the datagram transport a UtpSocket runs over.
//
// WriteTo must behave like a UDP socket's: it may wait for the local send
// path, but never for the peer to read. The socket writes from the goroutine
// that reads -- an acknowledgement leaves from the call stack that received
// the packet, as libutp's sendto callback does (utp_call_sendto,
// utp_internal.cpp:713) -- so a WriteTo that waited for the other side to
// read would let two sockets each wait on the other for ever. A transport
// that cannot keep up drops, as UDP does. WriteTo is called concurrently.
//
// WriteTo must not keep b once it returns: the socket encodes each packet into
// a buffer it reuses, as libutp's send callback is handed one it reuses
// (utp_call_sendto, utp_internal.cpp:713). net.PacketConn already works this
// way; a transport that queues a datagram must copy it.
//
// A Conn may also have the methods
//
//	HoldPeer(peer ConnectionPeer)
//	ReleasePeer(peer ConnectionPeer)
//
// and then the socket calls HoldPeer when it starts a connection with peer --
// a dial, as it sends its SYN, or a peer's SYN, as it is taken in -- and
// ReleasePeer when the socket forgets that connection, once it has ended. A
// transport that keeps something per peer, as utpnet keeps the address a
// datagram came from to write back to, keeps it while it is held: it is needed
// from the first packet of the handshake to the last of the close, which only
// the socket sees. Each HoldPeer is matched by one ReleasePeer, for a peer
// with as many connections as it is held. They are called with the socket's
// connection table locked, and must not call back into the socket.
type Conn interface {
	ReadFrom(b []byte) (int, ConnectionPeer, error)
	WriteTo(b []byte, dst ConnectionPeer) (int, error)
	Close() error
}

// peerHolder is a Conn that is told which peers the socket has connections
// with. See Conn.
type peerHolder interface {
	HoldPeer(peer ConnectionPeer)
	ReleasePeer(peer ConnectionPeer)
}

type UtpSocket struct {
	// dispatchMu serialises the routing of incoming packets with everything
	// else that changes the routing tables: registering a new connection and
	// serving an Accept. The reader holds it for each datagram it routes. See
	// dispatch.
	dispatchMu sync.Mutex
	// keyScratch is where the reader builds the keys it routes by. Used only
	// under dispatchMu.
	keyScratch []byte
	// connsGen counts changes to conns and inlineConns, each made with
	// connsMutex held for writing. lastRoute, used only under dispatchMu, is
	// the route the last datagram took, good while connsGen has not moved.
	connsGen  atomic.Uint64
	lastRoute cachedRoute
	// writeMu keeps an ordinary write from landing while an MTU probe has the
	// don't-fragment bit set on the socket. See writeDatagram.
	writeMu sync.RWMutex
	// inlineConns maps a connection's event channel to the connection, so
	// the reader can hand it a packet directly. Guarded by connsMutex.
	inlineConns map[chan *streamEvent]*connection
	ctx         context.Context
	cancel      context.CancelFunc
	logger      *ulogger
	// clk is this socket's clock, handed to every connection it creates.
	// Nil means the real one.
	clk        Clock
	connsMutex sync.RWMutex
	conns      map[string]chan *streamEvent
	// holder is socket, if it is a peerHolder, and held the peer each entry
	// in conns holds it for. Guarded by connsMutex. See registerLocked.
	holder                   peerHolder
	held                     map[string]ConnectionPeer
	accepts                  chan *acceptRequest
	acceptsWithCidCh         chan *acceptRequest
	awaiting                 *syncMap[*acceptRequest]
	awaitingExpirations      *timeWheel[any, *acceptRequest]
	incomingConns            *syncMap[*incomingPacket]
	incomingConnsExpirations *timeWheel[any, *incomingPacket]
	socket                   Conn
	// retransmitTimers is one timer wheel for every connection on this
	// socket. Per-connection wheels cost a ticker each; see
	// retransmit_timer.go.
	retransmitTimers *retransmitTimers
	// pendingAccepts holds Accept calls that named no connection id and are
	// waiting for any incoming SYN. Guarded by dispatchMu.
	pendingAccepts []*acceptRequest
	// answered holds connections whose SYN was answered with no Accept
	// waiting, oldest first, until an Accept or AcceptWithCid takes one. Only
	// a socket with WithAcceptConfig answers so. Guarded by dispatchMu.
	answered []*UtpStream
	// rstInfo remembers RESETs recently sent for connections we do not have.
	rstInfo            *syncMap[struct{}]
	rstInfoExpirations *timeWheel[any, rstInfoKey]
	// ownsSocket is true when this UtpSocket created the underlying Conn
	// (via Bind) and is therefore responsible for closing it. A Conn handed
	// in through WithSocket belongs to the caller.
	ownsSocket bool
	// droppedFullConnQueue counts packets discarded because a connection's
	// event queue was full. libutp never discards a datagram it has already
	// received and parsed -- utp_process_udp runs synchronously, with no queue
	// between the socket and the connection -- so any non-zero value here is
	// this library losing work the peer will have to retransmit.
	//
	// It exists to be asserted on rather than to be read in production. It
	// used to be reached routinely, because a connection whose reader stopped
	// blocked its own event loop and stopped draining this queue; that is
	// fixed, and TestSlowReaderDoesNotWedgeConnection asserts this stays zero
	// across a transfer whose reader stalls.
	droppedFullConnQueue atomic.Uint64
	// lingerAcks holds, for a connection that has just closed after reaching
	// its peer's FIN, the acknowledgement to re-send if that FIN arrives
	// again. See connection.lingerAck.
	lingerAcks        *syncMap[*packet]
	lingerExpirations *timeWheel[any, string]
	// recentlyClosed holds the keys of connections that ended in the last
	// idQuarantine. See GenerateCid.
	recentlyClosed            *syncMap[struct{}]
	recentlyClosedExpirations *timeWheel[any, string]
	// resetsSent counts RESETs answered to packets for connections this
	// socket does not have. A RESET tells the peer to give up, so a healthy
	// connection that draws one has been killed by us.
	resetsSent atomic.Uint64
	// refusedByFirewall counts SYNs the firewall callback rejected. It exists
	// so a test can tell "refused" from "lost".
	refusedByFirewall atomic.Uint64
	closeOnce         sync.Once
	// lingering holds the streams the application has closed whose
	// connections are still delivering. Close waits for them. See linger.
	lingering sync.Map // *UtpStream -> struct{}
	// firewall, when set, is asked about every SYN for a connection this
	// socket does not already have, before any state is created for it.
	// Reporting true refuses the connection. Set once at construction and
	// only read afterwards.
	firewall func(ConnectionPeer) bool
	// acceptConfig is WithAcceptConfig's function, or nil.
	acceptConfig func() *ConnectionConfig
	// maxConns is the incoming-connection cap: a new incoming connection is
	// refused while the socket already holds more than this many. Zero or
	// less means no cap. See WithMaxConnections.
	maxConns int
	// refusedAtCapacity counts SYNs refused because the socket was at its
	// connection cap. It exists so a test can tell "refused" from "lost".
	refusedAtCapacity atomic.Uint64
	// refusedIdInUse counts SYNs refused because a connection we dialled
	// already receives on the id the new one would. See handleIncomingBuf.
	refusedIdInUse atomic.Uint64
}

// DefaultMaxConnections is libutp's cap on the sockets one context may hold
// before it refuses a new incoming connection (utp_internal.cpp:2967-2974).
const DefaultMaxConnections = 3000

// WithMaxConnections caps the connections this socket holds before it refuses
// a new incoming one.
//
// libutp refuses a SYN when its context already holds more than 3000 sockets:
//
//	if (ctx->utp_sockets->GetCount() > 3000) {
//	    ...
//	    return 1;
//	}
//	                                        (utp_internal.cpp:2967-2974)
//
// This is the same check in the same place -- after the duplicate-connection
// lookup and before the firewall -- with the figure configurable, because 3000
// is a policy choice rather than a protocol rule. The comparison is libutp's
// too: a SYN is refused while the socket holds *more than* n, so it admits
// one past n, exactly as libutp admits a 3001st socket.
//
// What is counted is libutp's count. Every connection this socket has, dialled
// or accepted, established or still closing, plus every SYN parked waiting for
// Accept -- the state libutp holds as a socket in CS_SYN_RECV.
//
// A refusal is silent, as libutp's `return 1` is. A RESET would tell the peer
// something is listening, and at capacity there is nothing to offer it.
//
// n == 0 means DefaultMaxConnections, which is also what a socket gets without
// this option. n < 0 means no cap.
func WithMaxConnections(n int) SocketOption {
	return func(s *UtpSocket) {
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
func (s *UtpSocket) ConnectionsRefusedAtCapacity() uint64 {
	return s.refusedAtCapacity.Load()
}

// ConnectionsRefusedIdInUse reports how many incoming connections were
// refused because a connection this socket dialled to the same peer already
// receives on the id the new one would have. libutp refuses these too.
func (s *UtpSocket) ConnectionsRefusedIdInUse() uint64 {
	return s.refusedIdInUse.Load()
}

// connectionCount is what the incoming-connection cap is measured against:
// libutp's `utp_sockets->GetCount()`. See WithMaxConnections.
func (s *UtpSocket) connectionCount() int {
	return s.NumConnections() + s.incomingConns.len()
}

// SocketOption configures a UtpSocket at construction.
type SocketOption func(*UtpSocket)

// WithFirewall refuses incoming connections the callback rejects.
//
// It is asked about a SYN for a connection this socket does not already have,
// before any state exists for it, and reporting true drops the packet without
// answering. That is libutp's UTP_ON_FIREWALL, in the same position and with
// the same meaning -- "true means yes, block connection"
// (utp_internal.cpp:2975-2982), reached after the duplicate-connection check
// and before the socket is created, and refusing by returning rather than by
// sending a RESET.
//
// A BitTorrent client uses it for an IP blocklist: the point of doing it here
// rather than after Accept is that a refused peer costs no connection state
// and gets no reply to confirm anything is listening.
//
// The callback runs on the socket's receive path. It must not block, and must
// not call back into the socket.
func WithFirewall(refuse func(peer ConnectionPeer) bool) SocketOption {
	return func(s *UtpSocket) { s.firewall = refuse }
}

// WithAcceptConfig gives the socket a configuration of its own for incoming
// connections, read as each one is made, and with it libutp's way of
// accepting: a SYN is answered as it arrives, whether or not an Accept is
// waiting, and the connection is held until an Accept or AcceptWithCid takes
// it. libutp creates the socket and sends the SYN-ACK in utp_process_udp
// (utp_internal.cpp:2984-2990) with the context's options as they stand
// (:2616-2621), and hands the connection to its embedder.
//
// The function's configuration is used for every connection the socket
// answers before an Accept asks, and for one accepted with a nil config. An
// Accept or AcceptWithCid given a config of its own still applies it to a
// connection whose SYN arrives while it waits; one that takes a connection
// already answered gets that connection as it was made.
//
// Without it, the configuration is each Accept's, so a SYN that arrives
// before its Accept is parked, unanswered, until an Accept claims it or
// 20 seconds pass: the connection cannot be made before
// the call that says how. DEVIATIONS.md, "A SYN is answered when an Accept
// takes it, unless the socket has a configuration of its own".
//
// The answered connections count against WithMaxConnections, as libutp's
// do against its cap, and are held, like libutp's, for as long as the
// application leaves them.
func WithAcceptConfig(config func() *ConnectionConfig) SocketOption {
	return func(s *UtpSocket) { s.acceptConfig = config }
}

// configForAccept is the configuration an accepted connection gets.
func (s *UtpSocket) configForAccept(accept *acceptRequest, peer ConnectionPeer) *ConnectionConfig {
	config := accept.config
	if config == nil && s.acceptConfig != nil {
		config = s.acceptConfig()
	}
	if config == nil {
		config = NewConnectionConfig()
	}
	return s.configForPeer(config, peer)
}

// clock is this socket's clock, never nil.
func (s *UtpSocket) clock() Clock {
	if s.clk != nil {
		return s.clk
	}
	return RealClock
}

// WithClock runs this socket's retransmission wheel on the given clock, and
// makes it the default for every connection the socket creates.
//
// The wheel is shared by every connection on a socket, so the clock has to be
// chosen here rather than per connection: two connections on one socket
// cannot disagree about when a tick happened.
//
// Nothing outside a test should need this. See Clock.
func WithClock(clk Clock) SocketOption {
	return func(s *UtpSocket) {
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
// socket's send and receive buffers.
//
// A single readLoop goroutine drains the socket, so anything the kernel drops
// before it gets there is invisible packet loss that uTP then has to recover
// from with retransmits. At the OS default (~208 KiB on Linux) a few hundred
// concurrent connections overflow the receive queue and handshakes start
// failing outright. The kernel silently clamps this to net.core.rmem_max, so
// requesting more than the system allows is harmless.
const DefaultSocketBufferSize = 4 * 1024 * 1024

// Bind opens a UDP socket on addr and runs a uTP socket on it, which owns it
// and closes it with itself. logger receives the socket's logging; nil
// discards it. LevelTrace is its per-packet level.
func Bind(ctx context.Context, network string, addr *net.UDPAddr, logger *slog.Logger, opts ...SocketOption) (*UtpSocket, error) {
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
	sock := WithSocket(ctx, &udpConn{base: conn}, logger, opts...)
	sock.ownsSocket = true
	return sock, nil
}

// WithSocket runs a uTP socket on socket, which stays the caller's. logger
// receives the socket's logging; nil discards it. LevelTrace is its
// per-packet level.
func WithSocket(ctx context.Context, socket Conn, logger *slog.Logger, opts ...SocketOption) *UtpSocket {
	ctx, cancel := context.WithCancel(ctx)
	ulog := newLogger(logger)

	awaitingMap := newSyncMap[*acceptRequest]()
	handleAwaitExpirations := func(key any, accReq *acceptRequest) {
		awaitingMap.remove(key)
		// Tell the caller. Dropping the request silently left AcceptWithCid
		// blocked on accReq.stream forever whenever the caller's own context
		// had no deadline.
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

	utp := &UtpSocket{
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

	// Applied before the loops start, so nothing can observe a half-built
	// socket.
	for _, opt := range opts {
		opt(utp)
	}

	go utp.readLoop()
	go utp.eventLoop()

	return utp
}

// LocalAddr returns the address the underlying socket is bound to, or nil if
// it cannot report one.
//
// A caller that binds to port 0 -- which is the usual thing to do -- has no
// other way to learn which port the kernel chose.
// PacketsDroppedFullConnQueue reports how many received, decoded packets this
// socket discarded because the destination connection's event queue was full.
//
// It should be zero. libutp has no equivalent: it processes each datagram
// synchronously, so there is no queue between receiving and processing and
// nothing to overflow. A non-zero value means this library threw away work the
// peer must now retransmit, and is a bug rather than a tuning signal.
func (s *UtpSocket) PacketsDroppedFullConnQueue() uint64 {
	return s.droppedFullConnQueue.Load()
}

// PacketsResetSent reports how many RESETs this socket answered to packets for
// connections it does not have.
// ConnectionsRefusedByFirewall reports how many incoming connections the
// firewall callback rejected.
func (s *UtpSocket) ConnectionsRefusedByFirewall() uint64 {
	return s.refusedByFirewall.Load()
}

func (s *UtpSocket) PacketsResetSent() uint64 { return s.resetsSent.Load() }

func (s *UtpSocket) LocalAddr() net.Addr {
	type localAddresser interface{ LocalAddr() net.Addr }
	if la, ok := s.socket.(localAddresser); ok {
		return la.LocalAddr()
	}
	return nil
}

func (s *UtpSocket) NumConnections() int {
	s.connsMutex.Lock()
	defer s.connsMutex.Unlock()
	return len(s.conns)
}

// Close shuts the socket down. It is idempotent: `defer sock.Close()`
// alongside an explicit Close is ordinary Go, and the second call used to
// panic closing an already-closed channel.
//
// It first waits for the streams already closed to finish delivering what was
// written to them, for as long as each one's peer is still answering
// (awaitLingering); everything else is ended at once.
func (s *UtpSocket) Close() {
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

// linger keeps a closed stream's connection going until it has delivered
// what was queued and ended, and lets Close wait for it.
//
// UtpStream.Close returns at once, as libutp's utp_close does
// (utp_internal.cpp:3358-3380): the FIN is queued and the connection goes on
// sending in the background. libutp leaves the rest to its embedder, which
// must keep the context alive and pumping until its sockets have finished;
// destroying the context throws away whatever they still held. Here closing
// the socket is that moment, and it waits instead (awaitLingering), so that
// writing, closing the stream, closing the socket and exiting delivers every
// byte.
func (s *UtpSocket) linger(stream *UtpStream) {
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
// cancellation then ends it. See UtpStream.waitForFlush.
func (s *UtpSocket) awaitLingering() {
	var wg sync.WaitGroup
	s.lingering.Range(func(key, _ any) bool {
		stream := key.(*UtpStream)
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
func (s *UtpSocket) wakeConns() {
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

func (s *UtpSocket) sendShutdownEventToConns() {
	// Snapshot the channels rather than sending under the lock: a blocking
	// send to one wedged connection would otherwise hold connsMutex and stall
	// packet delivery for every other connection on this socket.
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
			// The connection's queue is full; it is already being torn down
			// by the cancelled socket context.
		}
		if conns[i] != nil {
			conns[i].wake()
		}
	}
}

// getConnStreamForQuotedId finds the connection that sent the packet an ICMP
// message quoted.
//
// The quoted packet is one of ours, so its connection id is our *send* id --
// except for a SYN, which carries the sender's own receive id. libutp covers
// both with three lookups against a map keyed on the receive id alone
// (utp_internal.cpp:3056-3058):
//
//	Lookup(UTPSocketKey(addr, id))
//	Lookup(UTPSocketKey(addr, id + 1)) && conn_id_send == id
//	Lookup(UTPSocketKey(addr, id - 1)) && conn_id_send == id
//
// The first catches a quoted SYN -- the ICMP case that matters most, since an
// unreachable host answers the SYN and nothing else. The second and third
// catch an established connection's data, from the acceptor and the initiator
// side respectively.
//
// This socket's map is keyed on the pair, so libutp's first lookup, which
// leaves the send id free, becomes two candidates: an initiator's send id is
// its receive id plus one and an acceptor's is minus one, so those are the
// only two connections libutp's map could have held under that key. The order
// is otherwise libutp's, which is what decides it when a peer holds several
// connections whose ids happen to be adjacent.
// configForPeer returns the connection config to use for a peer, with the
// path-MTU ceiling lowered when this socket's Conn can say what the local
// path carries.
//
// The caller's config is never modified: a ConnectionConfig belongs to
// whoever built it and may be reused across connections, so this copies
// before changing anything. See pathMTUCeiling.
func (s *UtpSocket) configForPeer(config *ConnectionConfig, peer ConnectionPeer) *ConnectionConfig {
	// Nil is the defaults, for a dial as for an accept (configForAccept). It
	// was passed on as it was, and a Connect or ConnectWithCid given nil
	// panicked making the connection.
	if config == nil {
		config = NewConnectionConfig()
	}
	// A socket-wide clock reaches its connections here, so a caller that
	// configured one does not have to remember to set it on every config it
	// hands to Connect or Accept. An explicit per-connection Clock still
	// wins: this only fills a gap.
	if s.clk != nil && config.Clock == nil {
		adjusted := *config
		adjusted.Clock = s.clk
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
