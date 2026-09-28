package utpnet

import (
	"errors"
	"net"
	"sync"
	"time"
)

// memNet is an in-memory UDP network: datagrams written to an address are
// delivered to the memUDP bound to it, and nothing else. It exists so that
// Socket can be run end to end with IPv6 addresses on a host whose kernel has
// no IPv6 -- the machine this was developed on answers every AF_INET6 socket
// with "address family not supported by protocol".
//
// What it does not stand in for is the kernel: no socket options, no
// dual-stack mapping, no path MTU. It exercises everything above the socket.
type memNet struct {
	mu    sync.Mutex
	conns map[string]*memUDP
}

func newMemNet() *memNet { return &memNet{conns: map[string]*memUDP{}} }

// listen binds addr, which must parse as a UDP address.
func (n *memNet) listen(addr string) (*memUDP, error) {
	a, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	c := &memUDP{
		net:     n,
		addr:    a,
		inbox:   make(chan memDatagram, 4096),
		closed:  make(chan struct{}),
		changed: make(chan struct{}, 1),
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, taken := n.conns[a.String()]; taken {
		return nil, errors.New("memNet: address in use")
	}
	n.conns[a.String()] = c
	return c, nil
}

type memDatagram struct {
	payload []byte
	from    *net.UDPAddr
}

// memUDP is one bound address on a memNet.
type memUDP struct {
	net   *memNet
	addr  *net.UDPAddr
	inbox chan memDatagram

	closeOnce sync.Once
	closed    chan struct{}

	mu       sync.Mutex
	deadline time.Time
	// changed wakes a blocked read when the deadline moves, which is how
	// Socket.Close unblocks its reader.
	changed chan struct{}
}

var _ udpPacketConn = (*memUDP)(nil)

type memTimeout struct{}

func (memTimeout) Error() string   { return "memUDP: i/o timeout" }
func (memTimeout) Timeout() bool   { return true }
func (memTimeout) Temporary() bool { return true }

func (c *memUDP) ReadFromUDP(b []byte) (int, *net.UDPAddr, error) {
	for {
		c.mu.Lock()
		deadline := c.deadline
		c.mu.Unlock()

		var expired <-chan time.Time
		if !deadline.IsZero() {
			wait := time.Until(deadline)
			if wait <= 0 {
				return 0, nil, memTimeout{}
			}
			timer := time.NewTimer(wait)
			defer timer.Stop()
			expired = timer.C
		}
		select {
		case d := <-c.inbox:
			return copy(b, d.payload), d.from, nil
		case <-c.closed:
			return 0, nil, net.ErrClosed
		case <-expired:
			return 0, nil, memTimeout{}
		case <-c.changed:
		}
	}
}

// WriteToUDP delivers to whatever is bound at addr, and like UDP says
// nothing if nothing is, or if its queue is full.
func (c *memUDP) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	c.net.mu.Lock()
	dst := c.net.conns[addr.String()]
	c.net.mu.Unlock()
	if dst != nil {
		payload := append([]byte(nil), b...)
		select {
		case dst.inbox <- memDatagram{payload: payload, from: c.addr}:
		default:
		}
	}
	return len(b), nil
}

func (c *memUDP) LocalAddr() net.Addr { return c.addr }

func (c *memUDP) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	select {
	case c.changed <- struct{}{}:
	default:
	}
	return nil
}

func (c *memUDP) SetReadBuffer(int) error  { return nil }
func (c *memUDP) SetWriteBuffer(int) error { return nil }

func (c *memUDP) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.net.mu.Lock()
		delete(c.net.conns, c.addr.String())
		c.net.mu.Unlock()
	})
	return nil
}
