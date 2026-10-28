package utpnet

import (
	"context"
	"errors"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"
)

// NewSocketFromPacketConn returns a Socket on any net.PacketConn, not only a
// UDP socket: for example one wrapped for rate limiting or metrics, one
// shared with other code, or an in-memory network.
//
// The Socket takes ownership of pc, and closing the Socket closes it. Unlike
// a Socket on a UDP socket, Close does not wait for the reader to return,
// since pc may not honor read deadlines. The reader stops when pc's ReadFrom
// fails. A non-timeout error on an open Socket closes it, and Accept,
// ReadFrom and the dial methods then return ErrSocketClosed wrapping that
// error.
//
// Addresses must name a host and port. A datagram from an address that is
// not a *net.UDPAddr is handled under its UDP form, and replies are sent to
// the original address; DialAddr dials an address of pc's own type. A
// PacketConn that uses its own address type must accept, in WriteTo, any
// address it has returned from ReadFrom.
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

	// native maps the UDP form of each address of another type back to the
	// original, so replies go to the address datagrams came from. Only
	// addresses that are not *net.UDPAddr are stored.
	mu     sync.Mutex
	native map[string]nativeAddr
	// pins counts the live connections to each address in native. A pinned
	// address is never pruned.
	pins map[string]int
}

// nativeAddr is an address of pc's own type and when a datagram last came
// from it.
type nativeAddr struct {
	addr net.Addr
	seen time.Time
}

// The native map is bounded, since every source address is recorded
// before any protocol has looked at it. When it is full, the addresses
// silent longest go first: nativeIdle is well beyond the 29 seconds within
// which a live uTP connection sends something.
const (
	nativeMax  = 8192
	nativeIdle = 5 * time.Minute
)

var _ udpPacketConn = (*packetConn)(nil)

func (p *packetConn) ReadFromUDP(b []byte) (int, *net.UDPAddr, error) {
	n, udp, _, err := p.readNative(b)
	return n, udp, err
}

// readNative is ReadFromUDP that also returns the address as pc gave it.
// The address travels with the datagram, since the map may drop it while the
// datagram is queued (Socket.run).
func (p *packetConn) readNative(b []byte) (int, *net.UDPAddr, net.Addr, error) {
	for {
		n, addr, err := p.pc.ReadFrom(b)
		if err != nil {
			return n, nil, nil, err
		}
		if udp, ok := p.udpForm(addr); ok {
			return n, udp, addr, nil
		}
		// An address without a host and port cannot be answered, so the
		// datagram is dropped.
	}
}

// restoreHeld remembers native as udp's address again, as when a datagram
// from it arrives, and keeps it until the matching unpin. See
// demuxConn.ReadFrom.
func (p *packetConn) restoreHeld(udp *net.UDPAddr, native net.Addr) {
	now := time.Now()
	key := udp.String()
	p.mu.Lock()
	if p.native == nil {
		p.native = make(map[string]nativeAddr)
	}
	if _, known := p.native[key]; !known && len(p.native) >= nativeMax {
		p.pruneLocked(now)
	}
	p.native[key] = nativeAddr{addr: native, seen: now}
	p.pinLocked(key)
	p.mu.Unlock()
}

func (p *packetConn) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	return p.pc.WriteTo(b, p.nativeOf(addr))
}

// udpForm is addr as a *net.UDPAddr, remembering addr to write back to.
func (p *packetConn) udpForm(addr net.Addr) (*net.UDPAddr, bool) {
	return p.remember(addr, false)
}

// retain is udpForm that also pins addr until the matching unpin, under
// one hold of the lock so it cannot be pruned in between.
func (p *packetConn) retain(addr net.Addr) (*net.UDPAddr, bool) {
	return p.remember(addr, true)
}

func (p *packetConn) remember(addr net.Addr, pin bool) (*net.UDPAddr, bool) {
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
	now := time.Now()
	p.mu.Lock()
	if p.native == nil {
		p.native = make(map[string]nativeAddr)
	}
	key := udp.String()
	if _, known := p.native[key]; !known && len(p.native) >= nativeMax {
		p.pruneLocked(now)
	}
	p.native[key] = nativeAddr{addr: addr, seen: now}
	if pin {
		p.pinLocked(key)
	}
	p.mu.Unlock()
	return udp, true
}

// pruneLocked makes room in a full native map: it removes addresses silent
// for nativeIdle and, if that is not enough, the longest silent until half
// remain.
func (p *packetConn) pruneLocked(now time.Time) {
	for k, e := range p.native {
		if now.Sub(e.seen) >= nativeIdle && p.pins[k] == 0 {
			delete(p.native, k)
		}
	}
	if len(p.native) < nativeMax {
		return
	}
	keys := make([]string, 0, len(p.native))
	for k := range p.native {
		if p.pins[k] == 0 {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return p.native[keys[i]].seen.Before(p.native[keys[j]].seen) })
	for _, k := range keys[:max(len(keys)-nativeMax/2, 0)] {
		delete(p.native, k)
	}
}

// pin keeps udp's native address for as long as a connection to it lives;
// the matching unpin lets it be pruned again.
func (p *packetConn) pin(udp *net.UDPAddr) {
	p.mu.Lock()
	p.pinLocked(udp.String())
	p.mu.Unlock()
}

func (p *packetConn) pinLocked(key string) {
	if p.pins == nil {
		p.pins = make(map[string]int)
	}
	p.pins[key]++
}

func (p *packetConn) unpin(udp *net.UDPAddr) {
	key := udp.String()
	p.mu.Lock()
	if n, ok := p.pins[key]; ok {
		if n <= 1 {
			delete(p.pins, key)
		} else {
			p.pins[key] = n - 1
		}
	}
	p.mu.Unlock()
}

// nativeOf returns the address a UDP form was learned from, or the UDP form
// itself.
func (p *packetConn) nativeOf(udp *net.UDPAddr) net.Addr {
	p.mu.Lock()
	defer p.mu.Unlock()
	if a, ok := p.native[udp.String()]; ok {
		return a.addr
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
// under it, and do nothing otherwise. The Socket calls them on a best-effort
// basis.
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
