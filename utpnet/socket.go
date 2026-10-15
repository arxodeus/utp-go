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
	"net"
	"sync"
	"sync/atomic"
	"time"

	utp "github.com/arxodeus/utp"
	"github.com/ethereum/go-ethereum/log"
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
	udp    udpPacketConn
	sock   *utp.UtpSocket
	inner  *demuxConn
	logger log.Logger

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
	closed    chan struct{}
	readLoop  sync.WaitGroup
}

type datagram struct {
	payload []byte
	from    *net.UDPAddr
}

// Options configures a Socket.
type Options struct {
	// Logger receives the uTP socket's logging. A nil Logger discards it.
	Logger log.Logger
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
		logger = log.New("utp", "utpnet")
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
	}
	s.config.Store(config)
	s.generic, _ = conn.(*packetConn)
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
		case <-s.closed:
			return
		default:
		}

		n, from, err := s.udp.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-s.closed:
				return
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			// A read error on a socket nobody closed is terminal.
			s.logger.Debug("utpnet: UDP read failed", "err", err)
			return
		}

		payload := make([]byte, n)
		copy(payload, buf[:n])

		if isUtpPacket(payload) {
			s.inner.deliver(payload, from)
			continue
		}

		select {
		case s.passthrough <- datagram{payload: payload, from: from}:
		default:
			// The caller is not draining ReadFrom. Drop, as a UDP socket
			// does, rather than stalling uTP behind it.
		}
	}
}

// isUtpPacket reports whether a datagram is a uTP packet for this
// implementation.
//
// The test is the packet decoder's own header validation, which checks the
// length, the packet type, the version nibble and the first extension byte --
// the same four things libutp checks before it will look at a packet at all
// (utp_internal.cpp:2481, :2834). Reusing it means the sniff cannot drift
// away from what the socket will actually accept: a datagram this says is
// uTP is one the socket can parse, and one it rejects is one the socket
// would have dropped.
func isUtpPacket(b []byte) bool {
	if len(b) < utp.MINIMAL_HEADER_SIZE {
		return false
	}
	_, err := utp.DecodePacketHeader(b[:utp.MINIMAL_HEADER_SIZE])
	return err == nil
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
			return 0, nil, ErrSocketClosed
		case d := <-s.passthrough:
			stop()
			n := copy(p, d.payload)
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
func (s *Socket) WriteTo(p []byte, addr net.Addr) (int, error) {
	select {
	case <-s.closed:
		return 0, ErrSocketClosed
	default:
	}
	// expired rather than wait: this path only needs the answer, and wait
	// starts a goroutine to serve a channel WriteTo never reads.
	if s.writeDeadline.expired() {
		return 0, timeoutError{}
	}
	if s.generic != nil {
		// The caller's address goes to the PacketConn as it is: it is of
		// the PacketConn's own kind, or one it can write to.
		return s.generic.pc.WriteTo(p, addr)
	}
	udpAddr, err := toUDPAddr(addr)
	if err != nil {
		return 0, err
	}
	return s.udp.WriteToUDP(p, udpAddr)
}

// Accept waits for the next incoming uTP connection.
func (s *Socket) Accept() (net.Conn, error) {
	// A nil config: the connection takes s.config as it stands when its
	// SYN arrives (utp.WithAcceptConfig), not as it stood now.
	stream, err := s.sock.Accept(s.ctx, nil)
	if err != nil {
		select {
		case <-s.closed:
			return nil, ErrSocketClosed
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
		return nil, ErrSocketClosed
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
		return nil, ErrSocketClosed
	default:
	}
	var udpAddr *net.UDPAddr
	if s.generic != nil {
		var ok bool
		if udpAddr, ok = s.generic.udpForm(addr); !ok {
			return nil, fmt.Errorf("utpnet: %v has no host and port", addr)
		}
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

// SetWriteDeadline is accepted and has almost no effect: WriteTo hands the
// datagram to the kernel without blocking, so there is nothing to time out.
// A deadline already in the past is still honoured, because a caller that
// sets one expects writes to fail rather than succeed.
func (s *Socket) SetWriteDeadline(t time.Time) error {
	s.writeDeadline.set(t)
	return nil
}

// Close shuts down the socket, every connection on it, and the underlying UDP
// port. It is idempotent.
func (s *Socket) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		s.sock.Close()
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
	return nil
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
