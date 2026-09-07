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
	"time"

	"github.com/ethereum/go-ethereum/log"
	utp "github.com/zen-eth/utp-go"
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

// Socket is a uTP endpoint on a UDP port, presented as net types.
type Socket struct {
	udp    *net.UDPConn
	sock   *utp.UtpSocket
	inner  *demuxConn
	logger log.Logger

	ctx    context.Context
	cancel context.CancelFunc

	// config is used for every accepted and dialled stream.
	config *utp.ConnectionConfig

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
		config:        config,
		passthrough:   make(chan datagram, passthroughDepth),
		readDeadline:  newDeadline(),
		writeDeadline: newDeadline(),
		closed:        make(chan struct{}),
	}
	s.inner = newDemuxConn(conn)
	s.sock = utp.WithSocket(ctx, s.inner, logger)

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
		timeout, expired := s.readDeadline.wait()
		if expired {
			return 0, nil, timeoutError{}
		}
		select {
		case <-s.closed:
			return 0, nil, ErrSocketClosed
		case d := <-s.passthrough:
			n := copy(p, d.payload)
			return n, d.from, nil
		case <-timeout:
			// The deadline may have been moved rather than reached; the loop
			// re-checks it.
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
	if _, expired := s.writeDeadline.wait(); expired {
		return 0, timeoutError{}
	}
	udpAddr, err := toUDPAddr(addr)
	if err != nil {
		return 0, err
	}
	return s.udp.WriteToUDP(p, udpAddr)
}

// Accept waits for the next incoming uTP connection.
func (s *Socket) Accept() (net.Conn, error) {
	stream, err := s.sock.Accept(s.ctx, s.config)
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
	stream, err := s.sock.Connect(ctx, utp.NewUdpPeer(udpAddr), s.config)
	if err != nil {
		return nil, err
	}
	return newConn(s, stream), nil
}

// Dial is DialContext with a background context.
func (s *Socket) Dial(network, addr string) (net.Conn, error) {
	return s.DialContext(context.Background(), network, addr)
}

// Addr returns the local address, satisfying net.Listener.
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
