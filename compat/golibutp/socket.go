package utp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/log"
	"github.com/anacrolix/missinggo/inproc"

	"github.com/arxodeus/utp/utpnet"
)

var (
	// mu guards every Socket's and Conn's mutable fields, as go-libutp's
	// package lock does. Nothing that can call back into this package --
	// the engine's firewall and write-error hooks -- is called with it held.
	mu sync.Mutex
	// sockets is every open Socket, for WriteStatus.
	sockets = map[*Socket]struct{}{}

	errSocketClosed = errors.New("Socket closed")
)

// A FirewallCallback returns true if an incoming connection request from the
// address should be refused. A refused SYN gets no answer, which is better
// than accepting and closing: the peer learns nothing.
type FirewallCallback func(net.Addr) bool

// NewSocketOpt configures a Socket at NewSocket.
type NewSocketOpt func(s *Socket)

// WithLogger sets the Socket's logger, in place of Logger.
func WithLogger(l log.Logger) NewSocketOpt {
	return func(s *Socket) {
		s.logger = l
	}
}

// Socket is a uTP endpoint on a packet connection. It is a net.PacketConn
// for the datagrams that are not uTP, which is how a BitTorrent client runs
// its DHT on the same port, and a net.Listener for incoming connections.
type Socket struct {
	pc     net.PacketConn
	us     *utpnet.Socket
	logger log.Logger

	// minLogLevel is the least slog level passed to logger; see logHandler.
	minLogLevel atomic.Int64

	// Guarded by mu.
	closed  bool
	conns   map[*Conn]struct{}
	options contextOptions
	// dialing is the connection attempts under way, by the address they
	// are for, each with the means to fail it.
	dialing map[string]map[*dialAttempt]struct{}

	asyncFirewallCallback FirewallCallback
	syncFirewallCallback  FirewallCallback
}

type dialAttempt struct {
	fail context.CancelCauseFunc
}

var (
	_ net.PacketConn = (*Socket)(nil)
	_ net.Listener   = (*Socket)(nil)
)

func listenPacket(network, addr string) (net.PacketConn, error) {
	if network == "inproc" {
		return inproc.ListenPacket(network, addr)
	}
	return net.ListenPacket(network, addr)
}

func resolveAddr(network, addr string) (net.Addr, error) {
	switch network {
	case "inproc":
		return inproc.ResolveAddr(network, addr)
	default:
		return net.ResolveUDPAddr(network, addr)
	}
}

// NewSocket listens on network ("udp", "udp4", "udp6", or "inproc" for
// missinggo's in-process network) at addr.
func NewSocket(network, addr string, opts ...NewSocketOpt) (*Socket, error) {
	pc, err := listenPacket(network, addr)
	if err != nil {
		return nil, err
	}
	return NewSocketFromPacketConn(pc, opts...)
}

// NewSocketFromPacketConn makes a Socket on a packet connection the caller
// already has. The Socket takes ownership of it.
func NewSocketFromPacketConn(pc net.PacketConn, opts ...NewSocketOpt) (*Socket, error) {
	s := &Socket{
		pc:      pc,
		logger:  Logger,
		conns:   make(map[*Conn]struct{}),
		options: defaultContextOptions(),
		dialing: make(map[string]map[*dialAttempt]struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.minLogLevel.Store(int64(logLevelFor(s.options)))
	us, err := utpnet.NewSocketFromPacketConn(context.Background(), pc, &utpnet.Options{
		Logger:           newGethLogger(s),
		ConnectionConfig: s.options.connectionConfig(),
		Firewall:         s.firewall,
		OnWriteError:     s.onWriteError,
	})
	if err != nil {
		pc.Close()
		return nil, err
	}
	s.us = us
	mu.Lock()
	sockets[s] = struct{}{}
	mu.Unlock()
	return s, nil
}

// firewall is the engine's firewall hook: the synchronous callback if there
// is one, as go-libutp prefers it, and otherwise the asynchronous one. Both
// are asked only about a connection request, and neither with mu held.
func (s *Socket) firewall(addr net.Addr) bool {
	mu.Lock()
	f := s.syncFirewallCallback
	if f == nil {
		f = s.asyncFirewallCallback
	}
	mu.Unlock()
	return f != nil && f(addr)
}

// onWriteError is go-libutp's handling of a failed send (its sendtoCallback):
// a connection's OnError handler hears of it; with none, an error that says
// the address can never be reached fails the connection, and anything else
// is left to retransmission.
func (s *Socket) onWriteError(to net.Addr, err error) {
	key := to.String()
	fatal := strings.Contains(err.Error(), "can't assign requested address") ||
		strings.Contains(err.Error(), "invalid argument")
	var (
		handlers []func(error)
		failing  []*Conn
		dials    []*dialAttempt
	)
	mu.Lock()
	for c := range s.conns {
		if c.remoteKey != key {
			continue
		}
		if c.userOnError != nil {
			handlers = append(handlers, c.userOnError)
		} else if fatal {
			failing = append(failing, c)
		}
	}
	if fatal {
		for d := range s.dialing[key] {
			dials = append(dials, d)
		}
	}
	mu.Unlock()
	for _, f := range handlers {
		go f(err)
	}
	for _, c := range failing {
		// Not here: this runs on whatever goroutine sent the datagram,
		// which may be the connection's own, and closing it waits on that.
		go c.fail(err)
	}
	for _, d := range dials {
		d.fail(err)
	}
	if len(handlers)+len(failing)+len(dials) == 0 {
		s.logger.Levelf(log.Debug, "error sending packet: %v", err)
	}
}

// Close closes the socket and every connection on it, at once.
func (s *Socket) Close() error {
	mu.Lock()
	if s.closed {
		mu.Unlock()
		return nil
	}
	s.closed = true
	delete(sockets, s)
	for c := range s.conns {
		c.us = nil
	}
	clear(s.conns)
	mu.Unlock()
	return s.us.Close()
}

// Addr is the local address, as net.Listener has it.
func (s *Socket) Addr() net.Addr {
	return s.pc.LocalAddr()
}

// LocalAddr is the local address, as net.PacketConn has it.
func (s *Socket) LocalAddr() net.Addr {
	return s.pc.LocalAddr()
}

// Accept waits for the next incoming connection.
//
// A dial completes whether or not anything is waiting here: the engine
// answers a SYN as it arrives and holds the connection until an Accept takes
// it, as libutp hands it to go-libutp's backlog. go-libutp closes any beyond
// five waiting; these are held, counted against the socket's connection cap
// (libutp's 3000), until taken.
func (s *Socket) Accept() (net.Conn, error) {
	nc, err := s.us.Accept()
	if err != nil {
		mu.Lock()
		closed := s.closed
		mu.Unlock()
		if closed {
			return nil, errors.New("closed")
		}
		return nil, err
	}
	return s.newConn(nc.(*utpnet.Conn))
}

// Dial connects to addr and waits as long as libutp does for an answer.
func (s *Socket) Dial(addr string) (net.Conn, error) {
	return s.DialTimeout(addr, 0)
}

// DialTimeout connects to addr, waiting at most timeout; zero is no limit
// beyond libutp's own.
func (s *Socket) DialTimeout(addr string, timeout time.Duration) (net.Conn, error) {
	ctx := context.Background()
	if timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return s.DialContext(ctx, "", addr)
}

// DialContext connects to addr. An empty network is the Socket's own.
func (s *Socket) DialContext(ctx context.Context, network, addr string) (_ net.Conn, err error) {
	if network == "" {
		network = s.pc.LocalAddr().Network()
	}
	ua, err := resolveAddr(network, addr)
	if err != nil {
		return nil, fmt.Errorf("error resolving address: %v", err)
	}
	dialCtx, fail := context.WithCancelCause(ctx)
	defer fail(nil)
	attempt := &dialAttempt{fail: fail}
	key := ua.String()
	mu.Lock()
	if s.closed {
		mu.Unlock()
		return nil, errSocketClosed
	}
	if s.dialing[key] == nil {
		s.dialing[key] = make(map[*dialAttempt]struct{})
	}
	s.dialing[key][attempt] = struct{}{}
	mu.Unlock()
	defer func() {
		mu.Lock()
		delete(s.dialing[key], attempt)
		if len(s.dialing[key]) == 0 {
			delete(s.dialing, key)
		}
		mu.Unlock()
	}()

	nc, err := s.us.DialAddr(dialCtx, ua)
	if err != nil {
		switch cause := context.Cause(dialCtx); {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case cause != nil && !errors.Is(cause, context.Canceled):
			return nil, cause // a send that can never succeed
		}
		mu.Lock()
		closed := s.closed
		mu.Unlock()
		if closed {
			return nil, errSocketClosed
		}
		return nil, err
	}
	return s.newConn(nc.(*utpnet.Conn))
}

// ReadFrom returns the next datagram that was not uTP.
func (s *Socket) ReadFrom(b []byte) (n int, addr net.Addr, err error) {
	return s.us.ReadFrom(b)
}

// WriteTo sends a datagram as it is.
func (s *Socket) WriteTo(b []byte, addr net.Addr) (int, error) {
	return s.us.WriteTo(b, addr)
}

// SetDeadline sets ReadFrom's and WriteTo's deadlines. go-libutp panics
// here; connections carry their own deadlines either way.
func (s *Socket) SetDeadline(t time.Time) error {
	return s.us.SetDeadline(t)
}

// SetReadDeadline bounds how long ReadFrom waits.
func (s *Socket) SetReadDeadline(t time.Time) error {
	return s.us.SetReadDeadline(t)
}

// SetWriteDeadline bounds WriteTo.
func (s *Socket) SetWriteDeadline(t time.Time) error {
	return s.us.SetWriteDeadline(t)
}

// ReadBufferLen is the RecvBuffer option.
func (s *Socket) ReadBufferLen() int {
	mu.Lock()
	defer mu.Unlock()
	return s.options.get(RecvBuffer)
}

// WriteBufferLen is the SendBuffer option: libutp's opt_sndbuf, the
// ceiling on each connection's congestion window.
func (s *Socket) WriteBufferLen() int {
	mu.Lock()
	defer mu.Unlock()
	return s.options.get(SendBuffer)
}

// SetWriteBufferLen sets the SendBuffer option for connections made from now
// on, and panics on a value libutp refuses, as go-libutp does.
func (s *Socket) SetWriteBufferLen(len int) {
	if i := s.SetOption(SendBuffer, len); i != 0 {
		panic(i)
	}
}

// SetOption sets a libutp context option, which connections made from then
// on take: utp_context_set_option. It returns 0, or -1 for an option or
// value libutp does not take.
func (s *Socket) SetOption(opt Option, val int) int {
	mu.Lock()
	ret := s.options.set(opt, val)
	opts := s.options
	mu.Unlock()
	if ret == 0 {
		s.minLogLevel.Store(int64(logLevelFor(opts)))
		s.us.SetConnectionConfig(opts.connectionConfig())
	}
	return ret
}

// SetFirewallCallback sets a firewall callback. go-libutp calls this one for
// every datagram, without its lock, before knowing whether the datagram is a
// connection request; here it is asked only about connection requests,
// which is all go-libutp uses its answer for. A synchronous callback, if
// set, is preferred to it.
func (s *Socket) SetFirewallCallback(f FirewallCallback) {
	mu.Lock()
	s.asyncFirewallCallback = f
	mu.Unlock()
}

// SetSyncFirewallCallback sets the firewall callback, asked about each
// incoming connection request before anything is created for it. Unlike
// go-libutp's, it is not called with this package's lock held, so it may
// take locks that code using this package also holds. It must not block.
func (s *Socket) SetSyncFirewallCallback(f FirewallCallback) {
	mu.Lock()
	s.syncFirewallCallback = f
	mu.Unlock()
}

// WriteStatus writes a summary of every open Socket.
func WriteStatus(w io.Writer) {
	mu.Lock()
	type status struct {
		s     *Socket
		conns int
	}
	var all []status
	for s := range sockets {
		all = append(all, status{s, len(s.conns)})
	}
	mu.Unlock()
	for _, st := range all {
		fmt.Fprintf(w, "listening at %s\n", st.s.Addr())
		fmt.Fprintf(w, "has %d conns\n", st.conns)
		fmt.Fprintf(w, "backlog: %d\n", st.s.us.AcceptBacklog())
		fmt.Fprintf(w, "\n")
	}
}
