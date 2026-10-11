package utpnet

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

// NewSocketFromPacketConn makes a Socket on any net.PacketConn, not only a
// UDP socket: one wrapped for rate limiting, metrics or a gate a test holds,
// a socket shared with code that was handed it, or an in-memory network.
//
// The Socket takes ownership, and closing it closes pc. Unlike a Socket on a
// UDP socket it does not wait for its reader to return when it closes:
// nothing says a PacketConn it did not make honours a read deadline, and a
// Close that waited on one that does not would never return. The reader
// stops when pc's ReadFrom fails.
//
// Addresses need only name a host and port. A datagram from an address of
// some other type than *net.UDPAddr is handled under its UDP form, and
// replies go back to the address it came from; a connection dialled to an
// address of pc's own type takes DialAddr. A PacketConn that wants its own
// address type must be able to write to one it has itself returned.
func NewSocketFromPacketConn(ctx context.Context, pc net.PacketConn, opts *Options) (*Socket, error) {
	if pc == nil {
		return nil, errors.New("utpnet: nil packet connection")
	}
	if udp, ok := pc.(*net.UDPConn); ok {
		return newSocket(ctx, udp, opts)
	}
	return newSocket(ctx, &packetConn{pc: pc}, opts)
}

// packetConn adapts a net.PacketConn to what a Socket reads and writes.
type packetConn struct {
	pc net.PacketConn

	// native maps the UDP form of each address of another type to the
	// address itself, so what is sent to a peer goes to the address its
	// datagrams came from. Only addresses that are not *net.UDPAddr are
	// kept.
	mu     sync.Mutex
	native map[string]net.Addr
}

var _ udpPacketConn = (*packetConn)(nil)

func (p *packetConn) ReadFromUDP(b []byte) (int, *net.UDPAddr, error) {
	for {
		n, addr, err := p.pc.ReadFrom(b)
		if err != nil {
			return n, nil, err
		}
		if udp, ok := p.udpForm(addr); ok {
			return n, udp, nil
		}
		// An address with no host and port cannot be answered or told
		// apart from another; the datagram is dropped, as one from an
		// address the kernel could not parse would be.
	}
}

func (p *packetConn) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	return p.pc.WriteTo(b, p.nativeOf(addr))
}

// udpForm is addr as a *net.UDPAddr, remembering addr to write back to.
func (p *packetConn) udpForm(addr net.Addr) (*net.UDPAddr, bool) {
	if udp, ok := addr.(*net.UDPAddr); ok {
		return udp, true
	}
	if addr == nil {
		return nil, false
	}
	host, portStr, err := net.SplitHostPort(addr.String())
	if err != nil {
		return nil, false
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, false
	}
	udp := &net.UDPAddr{Port: int(port)}
	if host != "" {
		ip, zone, _ := splitZone(host)
		if udp.IP = net.ParseIP(ip); udp.IP == nil {
			return nil, false
		}
		udp.Zone = zone
	}
	p.mu.Lock()
	if p.native == nil {
		p.native = make(map[string]net.Addr)
	}
	p.native[udp.String()] = addr
	p.mu.Unlock()
	return udp, true
}

// nativeOf is the address a UDP form was learned from, or the UDP form
// itself.
func (p *packetConn) nativeOf(udp *net.UDPAddr) net.Addr {
	p.mu.Lock()
	defer p.mu.Unlock()
	if a, ok := p.native[udp.String()]; ok {
		return a
	}
	return udp
}

func splitZone(host string) (ip, zone string, ok bool) {
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == '%' {
			return host[:i], host[i+1:], true
		}
	}
	return host, "", false
}

func (p *packetConn) LocalAddr() net.Addr               { return p.pc.LocalAddr() }
func (p *packetConn) SetReadDeadline(t time.Time) error { return p.pc.SetReadDeadline(t) }
func (p *packetConn) Close() error                      { return p.pc.Close() }

// SetReadBuffer and SetWriteBuffer reach the kernel when pc has a socket
// under it, and do nothing otherwise; the Socket only ever asks as a best
// effort.
func (p *packetConn) SetReadBuffer(bytes int) error {
	if s, ok := p.pc.(interface{ SetReadBuffer(int) error }); ok {
		return s.SetReadBuffer(bytes)
	}
	return nil
}

func (p *packetConn) SetWriteBuffer(bytes int) error {
	if s, ok := p.pc.(interface{ SetWriteBuffer(int) error }); ok {
		return s.SetWriteBuffer(bytes)
	}
	return nil
}
