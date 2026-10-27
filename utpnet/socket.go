// Package utpnet presents a uTP socket as the standard library's net types.
//
// The core package speaks in its own vocabulary -- ConnectionPeer, UtpStream,
// contexts on every call -- which is right for a library that has to work
// over an emulated network as easily as a real one. Everything that wants to
// *use* uTP wants net.Conn and net.PacketConn instead.
//
// This package is the translation. Its Socket satisfies:
//
//	net.PacketConn
//	Accept() (net.Conn, error)
//	Addr() net.Addr
//	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
//
// which is exactly the interface github.com/anacrolix/torrent requires of a
// uTP implementation (its unexported `utpSocket`, in utp.go). Go interfaces
// are structural, so satisfying it needs no import of that module and this
// package does not have one; integration/anacrolix holds a separate module
// that compiles this against the real interface, so the claim is checked
// rather than asserted.
//
// # Sharing a UDP port
//
// A BitTorrent client runs uTP and the DHT on one UDP port. So this Socket
// does not consume every datagram: it inspects each one, hands uTP packets to
// the uTP machinery, and leaves everything else to be returned by ReadFrom.
// That is what makes it a net.PacketConn rather than merely a listener, and
// it is why the type exists at all instead of a few helper functions.
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

// maxDatagram is the largest datagram the read loop will accept. A uTP packet
// never approaches this; the ceiling is for the non-uTP traffic sharing the
// port, which this package does not get to choose the size of.
const maxDatagram = 65536

// passthroughDepth is how many non-uTP datagrams may be buffered for ReadFrom
// before the read loop starts dropping them.
//
// Dropping is correct rather than regrettable: this is a datagram socket, the
// caller is expected to lose packets, and blocking here would stall uTP
// traffic behind a slow DHT reader on the same port.
const passthroughDepth = 256

// udpPacketConn is what a Socket uses of its UDP socket. *net.UDPConn is the
// only implementation outside tests; the interface exists so that tests can
// run a Socket over an in-memory network whose addresses are IPv6, on a host
// whose kernel has no IPv6 at all (utpnet's IPv6 tests, ipv6_test.go).
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
	// attempt that never waits (sendNow).
	raw *rawSender
	// passUDP is what the passthrough writer sends on: udp, kept apart so
	// that nothing else reads it.
	passUDP udpPacketConn
	sock    *utp.UtpSocket
	inner   *demuxConn
	logger  *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc

	// config is used for every stream accepted or dialled from now on; see
	// SetConnectionConfig.
	config atomic.Pointer[utp.ConnectionConfig]

	// generic is the adapter when the Socket was made on a net.PacketConn
	// that is not a UDP socket (NewSocketFromPacketConn), and nil otherwise.
	generic *packetConn

	passthrough chan datagram

	readDeadline  *deadline
	writeDeadline *deadline

	closeOnce sync.Once
	// closed ends what the application is waiting in -- Accept, ReadFrom,
	// the dials -- at the start of Close. appCtx ends with it, for Accept.
	closed    chan struct{}
	appCtx    context.Context
	appCancel context.CancelFunc
	// stopRead ends the reader, once the uTP socket has finished with it.
	stopRead chan struct{}
	// readErr is the read error that closed the socket, if one did.
	readErr atomic.Pointer[error]
	// passOut is what WriteTo hands its writer, started on the first.
	passOut        chan *passWrite
	passWriterOnce sync.Once
	readLoop       sync.WaitGroup
}

type datagram struct {
	payload []byte
	// pooled is payload's buffer if it came from datagramPool, to go back
	// once ReadFrom has copied it out.
	pooled *[]byte
	from   *net.UDPAddr
	// native is the address the datagram came from as a PacketConn of the
	// Socket's own gave it (packetConn), or nil.
	native net.Addr
}

// Options configures a Socket.
type Options struct {
	// Logger receives the uTP socket's logging. A nil Logger discards it.
	Logger *slog.Logger
	// ConnectionConfig is applied to every stream this socket accepts or
	// dials. A nil value uses utp.NewConnectionConfig().
	//
	// This is where a BitTorrent client selects LEDBAT++ -- see
	// BENCHMARKS.md for why it should.
	ConnectionConfig *utp.ConnectionConfig
	// Firewall, when set, is asked about every incoming connection before any
	// state is created for it. Returning true refuses it: the packet is
	// dropped without an answer, so a refused peer learns nothing.
	//
	// This is libutp's UTP_ON_FIREWALL. A BitTorrent client uses it for an IP
	// blocklist, which is why torrent's own uTP constructor takes one.
	//
	// It runs on the socket's receive path: it must not block, and must not
	// call back into the socket.
	Firewall func(net.Addr) bool
	// OnWriteError, when set, hears of every datagram the uTP machinery
	// failed to send, with the address it was for. The machinery carries on
	// regardless -- a lost datagram is retransmitted like any other -- so
	// this is for a caller that wants to give up sooner on an error it
	// knows will not clear, or to count them. go-libutp's sendto callback
	// draws the same line.
	//
	// It is called on the sending goroutine: it must not block, and must not
	// call back into the socket.
	OnWriteError func(to net.Addr, err error)
	// MaxConnections caps the connections this socket holds before it
	// refuses a new incoming one, silently, as libutp does past 3000
	// (utp_internal.cpp:2967-2974). Zero means utp.DefaultMaxConnections,
	// libutp's 3000; a negative value means no cap. See
	// utp.WithMaxConnections for exactly what is counted.
	MaxConnections int
}

// Listen binds a UDP port and returns a uTP socket on it.
//
// network is "udp", "udp4" or "udp6"; addr is a host:port as net.ResolveUDPAddr
// accepts, and an empty port asks the kernel to choose one.
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

// NewSocket wraps an already-bound UDP connection.
//
// The Socket takes ownership: closing it closes conn. This is the entry point
// for a caller that needs to set socket options, bind with a particular
// control function, or hand over a socket it obtained some other way.
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

	// Best effort, as in utp.Bind: a smaller buffer costs throughput but is
	// not fatal.
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
			// The caller was given a net.Addr signature, because that is what
			// it already has a blocklist keyed by. Peers this package creates
			// carry the address; anything else is refused rather than let
			// through unchecked, since a firewall that fails open is worse
			// than no firewall.
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

// run is the single reader of the UDP socket. Every datagram goes to exactly
// one of two places: the uTP machinery, or the passthrough queue that
// ReadFrom drains.
func (s *Socket) run() {
	defer s.readLoop.Done()
	buf := make([]byte, maxDatagram)
	for {
		select {
		case <-s.stopRead:
			return
		default:
		}

		// On a PacketConn of the Socket's own, the address the datagram
		// came from is kept as it was given, to go with the datagram: it
		// waits in a queue before anything answers it, and the map of such
		// addresses could forget it meanwhile (review of 99ce302).
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
			// socket closes with it. It used to stop reading and nothing
			// else: Accept and ReadFrom waited for ever, and every
			// connection went on retransmitting into a socket that could no
			// longer hear the answer. From another goroutine, because Close
			// waits for this one. Release review of 9c44c4f (R9).
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
			// The caller is not draining ReadFrom. Drop, as a UDP socket
			// does, rather than stalling uTP behind it.
			releaseDatagram(pooled)
		}
	}
}

// pooledDatagram is the size of datagramPool's buffers: room for any uTP
// packet, which libutp keeps under 1402 bytes and this library under its
// own ceiling, and for most of what shares a port with it. A larger datagram
// gets a buffer of its own.
const pooledDatagram = 2048

// datagramPool holds the buffers each datagram is copied into between the
// socket's reader and whoever takes it. Both consumers copy it out at once
// -- the uTP socket into its own read buffer, ReadFrom into its caller's --
// so a buffer is free again as soon as it has been taken, and copying every
// datagram into a new one was most of what a go-libutp transfer allocated:
// 39% of the bytes in compat/golibutp's 100 MB benchmark.
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

// isUtpPacket reports whether a datagram is a uTP packet for this
// implementation, by the packet decoder's own header validation (utp.IsPacket):
// the sniff cannot drift away from what the socket will actually accept.
func isUtpPacket(b []byte) bool {
	return utp.IsPacket(b)
}

// ReadFrom returns the next datagram that was not a uTP packet.
//
// This is what makes a Socket usable on a port shared with another protocol:
// a BitTorrent client's DHT traffic arrives here while its uTP traffic is
// handled by Accept and DialContext.
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
			// The deadline may have been moved rather than reached; the loop
			// re-checks it. stop() here too: the next pass starts its own
			// waiter, and this one's goroutine has already served its purpose.
			stop()
		}
	}
}

// WriteTo sends a datagram, unchanged, to addr.
//
// It is the outbound half of sharing the port: a caller's DHT replies go out
// through the same socket the uTP connections use.
//
// The write deadline applies for as long as the write waits, and moving it
// moves the wait, as net.PacketConn's contract says. The datagram is handed
// to one writer goroutine (see passthroughWriter), so the wait can end
// without the write: the port is shared with the uTP connections, and a
// deadline set on the port itself would end their writes too. A datagram
// whose deadline passes before the writer reaches it is not sent; one the
// writer is already sending may still be, as one already given to the kernel
// may. p is copied, so the caller has it back on return either way.
//
// On a UDP socket a datagram is first offered to the kernel once, without
// waiting (sendNow, on Unix): the common case, at the cost of a direct
// write. Only one the kernel cannot take at once goes to the writer, so a
// blocked write is always one the deadline can end, including a deadline
// set after it began. It used to write directly whenever no deadline was
// set, and a deadline set during such a write waited for the kernel. Review
// of 294cc2e, finding 2. Elsewhere every datagram is handed over: the
// request is pooled, and there is no timer or goroutine while no deadline
// is set (BenchmarkWriteTo).
func (s *Socket) WriteTo(p []byte, addr net.Addr) (int, error) {
	select {
	case <-s.closed:
		return 0, s.closedErr()
	default:
	}
	// expired rather than wait for the common case: it needs only the
	// answer, and wait starts a goroutine.
	if s.writeDeadline.expired() {
		return 0, timeoutError{}
	}
	var udpAddr *net.UDPAddr
	if s.generic == nil {
		var err error
		if udpAddr, err = toUDPAddr(addr); err != nil {
			return 0, err
		}
		// What the kernel takes at once needs no hand-over and no wait.
		if n, sent, err := s.raw.sendNow(p, udpAddr); sent {
			return n, err
		}
	}
	w := passWritePool.Get().(*passWrite)
	w.buf, w.addr, w.udp = append(w.buf[:0], p...), addr, udpAddr
	s.passWriterOnce.Do(func() { go s.passthroughWriter() })

	queued := false
	for {
		// No timer and no goroutine while there is no deadline: the changed
		// channel alone says when one is set.
		at, changed := s.writeDeadline.snapshot()
		var timer *time.Timer
		var timeout <-chan time.Time
		if !at.IsZero() {
			d := time.Until(at)
			if d <= 0 {
				// Withdrawn if the writer has not reached it. If it has, it
				// may yet be sent, as a datagram given to the kernel may be.
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
				// The writer is done with it, so it can be used again.
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
// buffer grown to the datagrams it has carried. One goes back only once its
// result has been taken: a request given up on may still be in the writer's
// hands.
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
// the socket closes. One goroutine, so a blocked write holds up the writes
// behind it and nothing more.
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
				// The caller's address goes to the PacketConn as it is: it
				// is of the PacketConn's own kind, or one it can write to.
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
	// SYN arrives (utp.WithAcceptConfig), not as it stood now.
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

// DialContext opens a uTP connection to addr.
//
// network is accepted for interface compatibility and must name a UDP
// network; addr is a host:port.
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
// any address with a host and port; on a Socket made on a PacketConn of its
// own (NewSocketFromPacketConn), that is how to dial an address of the
// PacketConn's own type, which DialContext's resolver does not know.
func (s *Socket) DialAddr(ctx context.Context, addr net.Addr) (net.Conn, error) {
	select {
	case <-s.closed:
		return nil, s.closedErr()
	default:
	}
	var udpAddr *net.UDPAddr
	if s.generic != nil {
		var ok bool
		// Kept from here, not from the uTP socket's first SYN: the address
		// is remembered now, and churn before the socket holds it
		// (demuxConn.HoldPeer) could forget it again.
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
// or dialled from now on; streams already open keep theirs. A nil config is
// utp.NewConnectionConfig(). The config is not copied, and must not be
// changed once handed over.
func (s *Socket) SetConnectionConfig(config *utp.ConnectionConfig) {
	if config == nil {
		config = utp.NewConnectionConfig()
	}
	s.config.Store(config)
}

// ConnectionConfig is the configuration streams accepted or dialled now get.
func (s *Socket) ConnectionConfig() *utp.ConnectionConfig {
	return s.config.Load()
}

// nativeAddr is a UDP address as the caller knows it: on a Socket made on a
// PacketConn of its own, the address of that PacketConn's type it came from.
func (s *Socket) nativeAddr(udp *net.UDPAddr) net.Addr {
	if s.generic != nil && udp != nil {
		return s.generic.nativeOf(udp)
	}
	return udp
}

// Dial is DialContext with a background context.
func (s *Socket) Dial(network, addr string) (net.Conn, error) {
	return s.DialContext(context.Background(), network, addr)
}

// Addr returns the local address, satisfying net.Listener.
// ProcessICMPFragmentation reports an ICMP "fragmentation needed" message
// (IPv4 type 3 code 4, or ICMPv6 "packet too big") for a datagram this socket
// sent, so the path-MTU search can take the router's word for it.
//
// quoted is the uTP datagram the ICMP message quoted -- the original UDP
// payload, not the ICMP packet -- and addr is where that datagram was sent.
// nextHopMTU is the router's figure, which may be zero when it did not supply
// one. It reports whether the quoted bytes matched a live connection.
//
// Nothing in this package collects ICMP: on Linux the usual source is
// IP_RECVERR on the UDP socket this Socket was built from, and reading it is
// the embedder's job. libutp draws the same line.
func (s *Socket) ProcessICMPFragmentation(quoted []byte, addr *net.UDPAddr, nextHopMTU uint16) bool {
	if addr == nil {
		return false
	}
	return s.sock.ProcessICMPFragmentation(quoted, utp.NewUdpPeer(addr), nextHopMTU)
}

// ProcessICMPError reports an ICMP message that should tear a connection
// down: destination unreachable other than fragmentation-needed, time
// exceeded, parameter problem. Which messages are fatal is the caller's
// judgement, as it is in libutp.
//
// quoted and addr mean what they mean for ProcessICMPFragmentation, and the
// return value likewise.
func (s *Socket) ProcessICMPError(quoted []byte, addr *net.UDPAddr) bool {
	if addr == nil {
		return false
	}
	return s.sock.ProcessICMPError(quoted, utp.NewUdpPeer(addr))
}

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

// SetReadDeadline bounds how long ReadFrom will wait.
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
// port. It is idempotent.
//
// Connections already closed are given the time to deliver what they hold,
// as utp.UtpSocket.Close gives them, and the port goes on being read while
// they do: their acknowledgements arrive on it. The reader used to stop
// first, so a Write followed by Close and Socket.Close lost whatever was
// still behind the congestion window. Release review of 9c44c4f (R2).
func (s *Socket) Close() error {
	s.shutdown(nil)
	return nil
}

// shutdown is Close, and the close a terminal read error causes.
func (s *Socket) shutdown(readErr error) {
	s.closeOnce.Do(func() {
		if readErr != nil {
			s.readErr.Store(&readErr)
		}
		// What the application waits in ends now.
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
			// See NewSocketFromPacketConn: closing it is what stops the
			// reader, and waiting first could wait for ever.
			_ = s.udp.Close()
			return
		}
		s.readLoop.Wait()
		_ = s.udp.Close()
	})
}

// closedErr is what an operation on a closed socket returns: ErrSocketClosed,
// wrapping the read error that closed it if one did.
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
		// anacrolix/torrent names its networks "utp", "utp4" and "utp6".
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

// timeoutError is what net expects a deadline to produce.
type timeoutError struct{}

func (timeoutError) Error() string   { return "utpnet: i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
