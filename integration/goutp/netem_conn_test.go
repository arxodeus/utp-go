package goutp

import (
	"fmt"
	"net"
	"time"

	"github.com/zen-eth/utp-go/netem"
)

// netemAddr is a netem peer as a net.Addr.
type netemAddr struct{ peer *netem.Peer }

func (a netemAddr) Network() string { return "netem" }
func (a netemAddr) String() string  { return a.peer.Name() }

// netemPacketConn runs purego over a netem endpoint: purego takes a
// net.PacketConn and uses ReadFrom, WriteTo, LocalAddr and Close of it.
type netemPacketConn struct {
	ep    *netem.Endpoint
	peers map[string]*netem.Peer // by name, for resolving dial addresses
}

func newNetemPacketConn(ep *netem.Endpoint, peers ...*netem.Endpoint) *netemPacketConn {
	c := &netemPacketConn{ep: ep, peers: map[string]*netem.Peer{}}
	for _, p := range peers {
		c.peers[p.Name()] = p.Addr()
	}
	return c
}

func (c *netemPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, from, err := c.ep.ReadFrom(b)
	if err != nil {
		return 0, nil, err
	}
	return n, netemAddr{from.(*netem.Peer)}, nil
}

func (c *netemPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	a, ok := addr.(netemAddr)
	if !ok {
		return 0, fmt.Errorf("not a netem address: %v", addr)
	}
	return c.ep.WriteTo(b, a.peer)
}

func (c *netemPacketConn) LocalAddr() net.Addr              { return netemAddr{c.ep.Addr()} }
func (c *netemPacketConn) Close() error                     { return c.ep.Close() }
func (c *netemPacketConn) SetDeadline(time.Time) error      { return nil }
func (c *netemPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (c *netemPacketConn) SetWriteDeadline(time.Time) error { return nil }

// resolve is purego's address resolver for this network: a dial address is
// an endpoint's name.
func (c *netemPacketConn) resolve(_, addr string) (net.Addr, error) {
	p, ok := c.peers[addr]
	if !ok {
		return nil, fmt.Errorf("no netem endpoint %q", addr)
	}
	return netemAddr{p}, nil
}
