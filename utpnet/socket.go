// Package utpnet presents a uTP socket through the standard library's net
// interfaces.
//
// A Socket satisfies
//
//	net.PacketConn
//	Accept() (net.Conn, error)
//	Addr() net.Addr
//	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
//
// and its connections are net.Conn values with CloseRead and CloseWrite, so
// it can be used wherever a BitTorrent client or other application expects a
// uTP socket in net terms.
//
// # Sharing a UDP port
//
// A BitTorrent client typically runs uTP and the DHT on one UDP port. A
// Socket therefore inspects each datagram: uTP packets go to the uTP
// connections, and everything else is returned by ReadFrom. WriteTo sends
// raw datagrams on the same port.
package utpnet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	utp "github.com/arxodeus/utp"
)

// ErrSocketClosed is returned by operations on a closed Socket.
var ErrSocketClosed = errors.New("utpnet: socket is closed")

// maxDatagram is the largest datagram the read loop accepts. uTP packets
// are far smaller; the limit is for other traffic sharing the port.
const maxDatagram = 65536

// passthroughDepth is how many non-uTP datagrams may be buffered for ReadFrom
// before the read loop drops them. Dropping is what a datagram socket does,
// and blocking would stall uTP traffic behind a slow reader on the same
// port.
const passthroughDepth = 256

// udpPacketConn is what a Socket uses of its UDP socket. *net.UDPConn is the
// only implementation outside tests; the interface lets tests run a Socket
// over an in-memory network, for example with IPv6 addresses on a host
// without IPv6.
type udpPacketConn interface {
	ReadFromUDP(b []byte) (int, *net.UDPAddr, error)
	WriteToUDP(b []byte, addr *net.UDPAddr) (int, error)
	LocalAddr() net.Addr
	SetReadDeadline(t time.Time) error
	SetReadBuffer(bytes int) error
	SetWriteBuffer(bytes int) error
	Close() error
}

var _ udpPacketConn = (*net.UDPConn)(nil)

// Socket is a uTP endpoint on a UDP port, presented as net types.
type Socket struct {
	udp udpPacketConn
	// raw is the UDP socket under udp, when it is one, for WriteTo's
	// non-blocking first attempt (sendNow).
	raw *rawSender
	// passUDP is what the passthrough writer sends on: udp, kept separate so
	// nothing else reads it.
	passUDP udpPacketConn
	sock    *utp.UtpSocket
	inner   *demuxConn
	logger  *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc

	// config is used for every stream accepted or dialed from now on; see
	// SetConnectionConfig.
	config atomic.Pointer[utp.ConnectionConfig]

	// generic is the adapter when the Socket was created on a net.PacketConn
	// that is not a UDP socket (NewSocketFromPacketConn), and nil otherwise.
	generic *packetConn

	passthrough chan datagram

	readDeadline  *deadline
	writeDeadline *deadline

	closeOnce sync.Once
	// closed ends Accept, ReadFrom and the dials at the start of Close.
	// appCtx ends with it, for Accept.
	closed    chan struct{}
	appCtx    context.Context
	appCancel context.CancelFunc
	// stopRead stops the reader once the uTP socket has finished with it.
	stopRead chan struct{}
	// readErr is the read error that closed the socket, if one did.
	readErr atomic.Pointer[error]
	// passOut is the channel WriteTo hands datagrams to its writer on,
	// started by the first WriteTo.
	passOut        chan *passWrite
	passWriterOnce sync.Once
	readLoop       sync.WaitGroup
}

type datagram struct {
	payload []byte
	// pooled is payload's buffer if it came from datagramPool, returned once
	// ReadFrom has copied it out.
	pooled *[]byte
	from   *net.UDPAddr
	// native is the original source address when the Socket's own
	// PacketConn gave one that is not a *net.UDPAddr (packetConn), or nil.
	native net.Addr
}

// Options configures a Socket.
type Options struct {
	// Logger receives the uTP socket's logging. A nil Logger discards it.
	Logger *slog.Logger
	// ConnectionConfig is applied to every stream this socket accepts or
	// dials. A nil value uses utp.NewConnectionConfig().
	ConnectionConfig *utp.ConnectionConfig
	// Firewall, when set, is consulted for every incoming connection before
	// any state is created for it. Returning true refuses it: the packet is
	// dropped without an answer, so a refused peer learns nothing. A typical
	// use is an IP blocklist.
	//
	// It runs on the socket's receive path: it must not block or call back
	// into the socket.
	Firewall func(net.Addr) bool
	// OnWriteError, when set, is called for every datagram the uTP
	// connections failed to send, with its destination. The connections
	// carry on regardless, since a lost datagram is retransmitted; this lets
	// a caller give up sooner on an error it knows is permanent, or count
	// them.
	//
	// It is called on the sending goroutine: it must not block or call back
	// into the socket.
	OnWriteError func(to net.Addr, err error)
	// MaxConnections limits the connections this socket holds before it
	// silently refuses new incoming ones. Zero means
	// utp.DefaultMaxConnections; a negative value means no limit. See
	// utp.WithMaxConnections.
	MaxConnections int
}

// Listen binds a UDP port and returns a uTP socket on it.
//
// network is "udp", "udp4" or "udp6"; addr is a host:port as accepted by
// net.ResolveUDPAddr, and an empty port lets the kernel choose one.
func Listen(ctx context.Context, network, addr string, opts *Options) (*Socket, error) {
	if opts == nil {
		opts = &Options{}
	}
	udpAddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return nil, fmt.Errorf("utpnet: resolving %q: %w", addr, err)
	}
	conn, err := net.ListenUDP(network, udpAddr)
	if err != nil {
		return nil, err
	}
	return NewSocket(ctx, conn, opts)
}

// NewSocket returns a Socket on an already bound UDP connection. The Socket
// takes ownership, and closing it closes conn. Use it to set socket options
// or bind with a particular control function.
func NewSocket(ctx context.Context, conn *net.UDPConn, opts *Options) (*Socket, error) {
	if conn == nil {
		return nil, errors.New("utpnet: nil UDP connection")
	}
	return newSocket(ctx, conn, opts)
}

func newSocket(ctx context.Context, conn udpPacketConn, opts *Options) (*Socket, error) {
	if opts == nil {
		opts = &Options{}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	config := opts.ConnectionConfig
	if config == nil {
		config = utp.NewConnectionConfig()
	}

	// Best effort: a smaller buffer costs throughput but is not fatal.
	_ = conn.SetReadBuffer(utp.DefaultSocketBufferSize)
	_ = conn.SetWriteBuffer(utp.DefaultSocketBufferSize)

	ctx, cancel := context.WithCancel(ctx)
	s := &Socket{
		udp:           conn,
		logger:        logger,
		ctx:           ctx,
		cancel:        cancel,
		passthrough:   make(chan datagram, passthroughDepth),
		readDeadline:  newDeadline(),
		writeDeadline: newDeadline(),
		closed:        make(chan struct{}),
		stopRead:      make(chan struct{}),
		passOut:       make(chan *passWrite),
	}
	s.appCtx, s.appCancel = context.WithCancel(ctx)
	s.config.Store(config)
	s.generic, _ = conn.(*packetConn)
	s.passUDP = conn
	if raw, ok := conn.(*net.UDPConn); ok {
		s.raw = newRawSender(raw)
	}
	s.inner = newDemuxConn(conn)
	if f := opts.OnWriteError; f != nil {
		s.inner.onWriteError = func(to *net.UDPAddr, err error) { f(s.nativeAddr(to), err) }
	}
	var sockOpts []utp.SocketOption
	if opts.Firewall != nil {
		refuse := opts.Firewall
		sockOpts = append(sockOpts, utp.WithFirewall(func(peer utp.ConnectionPeer) bool {
			// The callback takes a net.Addr. Peers this package creates carry the
			// address; anything else is refused rather than let through
			// unchecked, since a firewall that fails open is worse than none.
			addr, err := peerUDPAddr(peer)
			if err != nil {
				logger.Warn("refusing an incoming connection: its peer has no usable address",
					"peer", peer, "err", err)
				return true
			}
			return refuse(addr)
		}))
	}
	sockOpts = append(sockOpts, utp.WithMaxConnections(opts.MaxConnections))
	sockOpts = append(sockOpts, utp.WithAcceptConfig(s.config.Load))
	s.sock = utp.WithSocket(ctx, s.inner, logger, sockOpts...)

	s.readLoop.Add(1)
	go s.run()
	return s, nil
}

// run is the single reader of the UDP socket. Every datagram goes either to
// the uTP connections or to the passthrough queue that ReadFrom drains.
func (s *Socket) run() {
	defer s.readLoop.Done()
	buf := make([]byte, maxDatagram)
	for {
		select {
		case <-s.stopRead:
			return
		default:
		}

		// On the Socket's own PacketConn, the original source address travels
		// with the datagram: the datagram waits in a queue before it is
		// answered, and the address map could drop the address meanwhile.
		var n int
		var from *net.UDPAddr
		var native net.Addr
		var err error
		if g, ok := s.udp.(*packetConn); ok {
			n, from, native, err = g.readNative(buf)
		} else {
			n, from, err = s.udp.ReadFromUDP(buf)
		}
		if err != nil {
			select {
			case <-s.stopRead:
				return
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			// A read error on a socket nobody closed is terminal, and the
			// socket closes with it, so Accept, ReadFrom and the connections
			// fail promptly. From another goroutine, because Close waits for
			// this one.
			s.logger.Debug("utpnet: UDP read failed; closing the socket", "err", err)
			go s.shutdown(err)
			return
		}

		payload, pooled := copyDatagram(buf[:n])

		if isUtpPacket(payload) {
			s.inner.deliver(payload, pooled, from, native)
			continue
		}

		select {
		case s.passthrough <- datagram{payload: payload, pooled: pooled, from: from, native: native}:
		default:
			// The caller is not draining ReadFrom. Drop, as a UDP socket does,
			// rather than stall uTP behind it.
			releaseDatagram(pooled)
		}
	}
}

// pooledDatagram is the size of datagramPool's buffers: room for any uTP
// packet and most other traffic sharing the port. A larger datagram gets a
// buffer of its own.
const pooledDatagram = 2048

// datagramPool holds the buffers each datagram is copied into between the
// socket's reader and its consumer. Both consumers copy it out at once, so a
// buffer is free again as soon as it has been taken.
var datagramPool = sync.Pool{New: func() any {
	b := make([]byte, pooledDatagram)
	return &b
}}

// copyDatagram copies b out of the read buffer, into a pooled buffer if it
// fits one, and returns the pooled buffer to release, or nil.
func copyDatagram(b []byte) (payload []byte, pooled *[]byte) {
	if len(b) > pooledDatagram {
		return append([]byte(nil), b...), nil
	}
	pooled = datagramPool.Get().(*[]byte)
	payload = (*pooled)[:len(b)]
	copy(payload, b)
	return payload, pooled
}

// releaseDatagram returns a pooled buffer once its datagram has been copied
// out. It must not be used after.
func releaseDatagram(pooled *[]byte) {
	if pooled != nil {
		datagramPool.Put(pooled)
	}
}

// isUtpPacket reports whether a datagram is a uTP packet, using the packet
// decoder's own header validation (utp.IsPacket), so the classification
// matches what the uTP socket accepts.
func isUtpPacket(b []byte) bool {
	return utp.IsPacket(b)
}

// ReadFrom returns the next datagram that was not a uTP packet, such as a
// BitTorrent client's DHT traffic on a shared port.
func (s *Socket) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		timeout, expired, stop := s.readDeadline.wait()
		if expired {
			stop()
			return 0, nil, timeoutError{}
		}
		select {
		case <-s.closed:
			stop()
			return 0, nil, s.closedErr()
		case d := <-s.passthrough:
			stop()
			n := copy(p, d.payload)
			releaseDatagram(d.pooled)
			if d.native != nil {
				return n, d.native, nil
			}
			return n, s.nativeAddr(d.from), nil
		case <-timeout:
			// The deadline may have moved rather than passed; the loop re-checks
			// it. Stop this waiter: the next pass starts its own.
			stop()
		}
	}
}

// WriteTo sends a raw datagram to addr on the port the uTP connections use,
// for example a DHT reply.
//
// The write deadline applies while the write waits, and moving it moves the
// wait. The datagram is handed to a single writer goroutine (see
// passthroughWriter), so the wait can end without the write; a deadline set
// on the port itself would end the uTP connections' writes too. A datagram
// whose deadline passes before the writer reaches it is not sent; one the
// writer is already sending may still be. p is copied, so the caller may
// reuse it on return.
//
// On Unix a datagram is first offered to the kernel once without waiting
// (sendNow); only one the kernel cannot take at once goes to the writer, so
// a blocked write can always be ended by the deadline. While no deadline is
// set, handing over uses no timer or goroutine.
func (s *Socket) WriteTo(p []byte, addr net.Addr) (int, error) {
	select {
	case <-s.closed:
		return 0, s.closedErr()
	default:
	}
	// expired rather than wait: only the answer is needed, and wait starts
	// a goroutine.
	if s.writeDeadline.expired() {
		return 0, timeoutError{}
	}
	var udpAddr *net.UDPAddr
	if s.generic == nil {
		var err error
		if udpAddr, err = toUDPAddr(addr); err != nil {
			return 0, err
		}
		// What the kernel takes at once needs no hand-off and no wait.
		if n, sent, err := s.raw.sendNow(p, udpAddr); sent {
			return n, err
		}
	}
	w := passWritePool.Get().(*passWrite)
	w.buf, w.addr, w.udp = append(w.buf[:0], p...), addr, udpAddr
	s.passWriterOnce.Do(func() { go s.passthroughWriter() })

	queued := false
	for {
		// No timer or goroutine while there is no deadline: the changed
		// channel signals when one is set.
		at, changed := s.writeDeadline.snapshot()
		var timer *time.Timer
		var timeout <-chan time.Time
		if !at.IsZero() {
			d := time.Until(at)
			if d <= 0 {
				// Withdraw it if the writer has not reached it. If it has, it may
				// still be sent.
				w.cancel()
				return 0, timeoutError{}
			}
			timer = time.NewTimer(d)
			timeout = timer.C
		}
		if !queued {
			select {
			case s.passOut <- w:
				queued = true
			case <-s.closed:
				stopTimer(timer)
				passWritePool.Put(w.reset())
				return 0, s.closedErr()
			case <-changed:
			case <-timeout:
			}
		}
		if queued {
			select {
			case r := <-w.done:
				stopTimer(timer)
				// The writer is done with it, so it can be reused.
				passWritePool.Put(w.reset())
				return r.n, r.err
			case <-s.closed:
				stopTimer(timer)
				w.cancel()
				return 0, s.closedErr()
			case <-changed:
			case <-timeout:
			}
		}
		// The deadline passed or moved: the loop re-reads it.
		stopTimer(timer)
	}
}

func stopTimer(t *time.Timer) {
	if t != nil {
		t.Stop()
	}
}

// passWritePool holds WriteTo's requests, each with its result channel and a
// buffer. A request returns to the pool only once its result has been taken,
// since one given up on may still be with the writer.
var passWritePool = sync.Pool{New: func() any { return &passWrite{done: make(chan passResult, 1)} }}

func (w *passWrite) reset() *passWrite {
	w.addr, w.udp, w.started, w.dropped = nil, nil, false, false
	return w
}

// passWrite is one WriteTo waiting for the writer.
type passWrite struct {
	buf  []byte
	addr net.Addr
	udp  *net.UDPAddr
	done chan passResult

	// done has room for the result, so the writer never waits on a caller
	// that has gone.
	mu      sync.Mutex
	started bool
	dropped bool
}

type passResult struct {
	n   int
	err error
}

// cancel withdraws a write the writer has not started.
func (w *passWrite) cancel() {
	w.mu.Lock()
	w.dropped = !w.started
	w.mu.Unlock()
}

// passthroughWriter sends what WriteTo queues, one datagram at a time, until
// the socket closes. A blocked write holds up only the writes behind it.
func (s *Socket) passthroughWriter() {
	for {
		select {
		case w := <-s.passOut:
			w.mu.Lock()
			if w.dropped {
				w.mu.Unlock()
				continue
			}
			w.started = true
			w.mu.Unlock()
			var r passResult
			if s.generic != nil {
				// The caller's address goes to the PacketConn unchanged: it is of the
				// PacketConn's own type, or one it can write to.
				r.n, r.err = s.generic.pc.WriteTo(w.buf, w.addr)
			} else {
				r.n, r.err = s.passUDP.WriteToUDP(w.buf, w.udp)
			}
			w.done <- r
		case <-s.stopRead:
			return
		}
	}
}

// Accept waits for the next incoming uTP connection.
func (s *Socket) Accept() (net.Conn, error) {
	// A nil config: the connection takes s.config as it stands when its
	// SYN arrives (utp.WithAcceptConfig).
	stream, err := s.sock.Accept(s.appCtx, nil)
	if err != nil {
		select {
		case <-s.closed:
			return nil, s.closedErr()
		default:
		}
		return nil, err
	}
	return newConn(s, stream), nil
}

// DialContext opens a uTP connection to addr, a host:port. network is a UDP
// network name; "utp", "utp4" and "utp6" are accepted as aliases.
func (s *Socket) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	select {
	case <-s.closed:
		return nil, s.closedErr()
	default:
	}
	if network == "" {
		network = "udp"
	}
	udpAddr, err := net.ResolveUDPAddr(udpNetwork(network), addr)
	if err != nil {
		return nil, fmt.Errorf("utpnet: resolving %q: %w", addr, err)
	}
	return s.DialAddr(ctx, udpAddr)
}

// DialAddr opens a uTP connection to an address already resolved. It takes
// any address with a host and port. On a Socket created with
// NewSocketFromPacketConn, use it to dial an address of the PacketConn's own
// type, which DialContext cannot resolve.
func (s *Socket) DialAddr(ctx context.Context, addr net.Addr) (net.Conn, error) {
	select {
	case <-s.closed:
		return nil, s.closedErr()
	default:
	}
	var udpAddr *net.UDPAddr
	if s.generic != nil {
		var ok bool
		// Hold it from now, not from the uTP socket's first SYN: the address
		// is recorded now, and other traffic could evict it before the
		// socket holds it (demuxConn.HoldPeer).
		if udpAddr, ok = s.generic.retain(addr); !ok {
			return nil, fmt.Errorf("utpnet: %v has no host and port", addr)
		}
		defer s.generic.unpin(udpAddr)
	} else {
		var err error
		if udpAddr, err = toUDPAddr(addr); err != nil {
			return nil, err
		}
	}
	stream, err := s.sock.Connect(ctx, utp.NewUdpPeer(udpAddr), s.config.Load())
	if err != nil {
		return nil, err
	}
	return newConn(s, stream), nil
}

// AcceptBacklog reports how many incoming connections are waiting for an
// Accept to take them.
func (s *Socket) AcceptBacklog() int {
	return s.sock.AcceptBacklog()
}

// SetConnectionConfig replaces the configuration given to streams accepted
// or dialed from now on; open streams keep theirs. A nil config means
// utp.NewConnectionConfig(). The config is not copied and must not be
// modified afterwards.
func (s *Socket) SetConnectionConfig(config *utp.ConnectionConfig) {
	if config == nil {
		config = utp.NewConnectionConfig()
	}
	s.config.Store(config)
}

// ConnectionConfig returns the configuration given to streams accepted or
// dialed now.
func (s *Socket) ConnectionConfig() *utp.ConnectionConfig {
	return s.config.Load()
}

// nativeAddr returns a UDP address as the caller knows it: on a Socket
// created on its own PacketConn, the original address it came from.
func (s *Socket) nativeAddr(udp *net.UDPAddr) net.Addr {
	if s.generic != nil && udp != nil {
		return s.generic.nativeOf(udp)
	}
	return udp
}

// Dial calls DialContext with a background context.
func (s *Socket) Dial(network, addr string) (net.Conn, error) {
	return s.DialContext(context.Background(), network, addr)
}

// ProcessICMPFragmentation reports an ICMP "fragmentation needed" message
// (IPv4 type 3 code 4, or ICMPv6 "packet too big") for a datagram this socket
// sent, so the path-MTU search can use the router's figure.
//
// quoted is the uTP datagram the ICMP message quoted, the original UDP
// payload without the ICMP packet around it, and addr is where it was sent.
// nextHopMTU is the router's figure, which may be zero. It reports whether
// quoted matched a live connection.
//
// This package does not collect ICMP itself. On Linux the usual source is
// IP_RECVERR on the UDP socket the Socket was built from.
func (s *Socket) ProcessICMPFragmentation(quoted []byte, addr *net.UDPAddr, nextHopMTU uint16) bool {
	if addr == nil {
		return false
	}
	return s.sock.ProcessICMPFragmentation(quoted, utp.NewUdpPeer(addr), nextHopMTU)
}

// ProcessICMPError reports an ICMP message that should end a connection:
// destination unreachable other than fragmentation needed, time exceeded, or
// parameter problem. Which messages are fatal is for the caller to decide.
//
// quoted, addr and the result are as for ProcessICMPFragmentation.
func (s *Socket) ProcessICMPError(quoted []byte, addr *net.UDPAddr) bool {
	if addr == nil {
		return false
	}
	return s.sock.ProcessICMPError(quoted, utp.NewUdpPeer(addr))
}

// Addr returns the local address, implementing net.Listener.
func (s *Socket) Addr() net.Addr { return s.udp.LocalAddr() }

// LocalAddr returns the local address, satisfying net.PacketConn.
func (s *Socket) LocalAddr() net.Addr { return s.udp.LocalAddr() }

// SetDeadline sets both the read and write deadlines for the packet-oriented
// side of this socket. It does not affect established uTP connections, which
// carry their own deadlines.
func (s *Socket) SetDeadline(t time.Time) error {
	s.readDeadline.set(t)
	s.writeDeadline.set(t)
	return nil
}

// SetReadDeadline sets how long ReadFrom will wait.
func (s *Socket) SetReadDeadline(t time.Time) error {
	s.readDeadline.set(t)
	return nil
}

// SetWriteDeadline sets the deadline for WriteTo, including a WriteTo already
// waiting. It does not apply to the uTP connections' own writes, which share
// the port. See WriteTo.
func (s *Socket) SetWriteDeadline(t time.Time) error {
	s.writeDeadline.set(t)
	return nil
}

// Close shuts down the socket, every connection on it, and the underlying UDP
// port. It is safe to call more than once.
//
// Connections already closed are given time to deliver what they hold, as
// utp.UtpSocket.Close gives them, and the port keeps being read meanwhile,
// since their acknowledgements arrive on it.
func (s *Socket) Close() error {
	s.shutdown(nil)
	return nil
}

// shutdown implements Close, and the close a terminal read error causes.
func (s *Socket) shutdown(readErr error) {
	s.closeOnce.Do(func() {
		if readErr != nil {
			s.readErr.Store(&readErr)
		}
		// Calls the application is waiting in end now.
		close(s.closed)
		s.appCancel()
		if readErr != nil {
			// Nothing can be delivered without a receive path: end the
			// connections rather than wait for acknowledgements.
			s.cancel()
		}
		s.sock.Close()
		close(s.stopRead)
		s.cancel()
		s.inner.close()
		_ = s.udp.SetReadDeadline(time.Now())
		if s.generic != nil {
			// See NewSocketFromPacketConn: closing pc is what stops the reader,
			// and waiting first could wait forever.
			_ = s.udp.Close()
			return
		}
		s.readLoop.Wait()
		_ = s.udp.Close()
	})
}

// closedErr returns the error for an operation on a closed socket:
// ErrSocketClosed, wrapping the read error that closed it, if any.
func (s *Socket) closedErr() error {
	if err := s.readErr.Load(); err != nil {
		return fmt.Errorf("%w: %w", ErrSocketClosed, *err)
	}
	return ErrSocketClosed
}

func udpNetwork(network string) string {
	switch network {
	case "udp", "udp4", "udp6":
		return network
	default:
		// Accept "utp", "utp4" and "utp6" as aliases of the UDP networks.
		switch {
		case len(network) > 0 && network[len(network)-1] == '4':
			return "udp4"
		case len(network) > 0 && network[len(network)-1] == '6':
			return "udp6"
		}
		return "udp"
	}
}

func toUDPAddr(addr net.Addr) (*net.UDPAddr, error) {
	switch a := addr.(type) {
	case *net.UDPAddr:
		return a, nil
	case nil:
		return nil, errors.New("utpnet: nil destination address")
	default:
		resolved, err := net.ResolveUDPAddr(udpNetwork(addr.Network()), addr.String())
		if err != nil {
			return nil, fmt.Errorf("utpnet: %q is not a UDP address: %w", addr, err)
		}
		return resolved, nil
	}
}

// timeoutError is the net.Error a deadline produces.
type timeoutError struct{}

func (timeoutError) Error() string   { return "utpnet: i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
