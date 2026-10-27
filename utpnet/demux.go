package utpnet

import (
	"net"
	"sync"

	utp "github.com/arxodeus/utp"
)

// demuxConn is the utp.Conn the uTP socket reads from.
//
// It is not the UDP socket. The Socket owns the only reader of that, so it can
// decide per datagram whether uTP or the caller should get it; what the uTP
// machinery sees is this, a queue fed only with the datagrams that were uTP.
// Writes go straight out through the real socket, because there is nothing to
// decide on the way out.
type demuxConn struct {
	udp udpPacketConn

	incoming chan inbound

	// onWriteError is Options.OnWriteError, if set.
	onWriteError func(to *net.UDPAddr, err error)

	closeOnce sync.Once
	closed    chan struct{}

	// handed is the address of the datagram ReadFrom last returned, held
	// until the next ReadFrom. Used only by the uTP socket's reader, the one
	// caller of ReadFrom. See ReadFrom.
	handed *net.UDPAddr
}

type inbound struct {
	payload []byte
	pooled  *[]byte // see datagram.pooled
	from    *net.UDPAddr
	native  net.Addr // see datagram.native
}

// demuxDepth is how many uTP datagrams may be queued for the uTP socket
// before the read loop drops them.
//
// Deeper than the passthrough queue: the uTP socket drains this promptly and
// a drop here is real packet loss that a connection then has to recover from
// with a retransmission, where a dropped DHT datagram costs one query.
const demuxDepth = 4096

func newDemuxConn(udp udpPacketConn) *demuxConn {
	return &demuxConn{
		udp:      udp,
		incoming: make(chan inbound, demuxDepth),
		closed:   make(chan struct{}),
	}
}

// HoldPeer keeps the address peer's datagrams came from, which may not be a
// *net.UDPAddr, for as long as the uTP socket has a connection with it: from
// the first packet of the handshake, dialled or accepted, to the last of the
// close (utp.Conn). Only a Socket on a PacketConn of its own has such
// addresses (packetConn).
//
// It was kept from when the application had the connection -- the dial
// returned, or an Accept took it -- to the end of its stream. Before that, a
// dial's handshake and a connection waiting to be accepted could lose it to
// other addresses arriving, and their packets then went out in the UDP form,
// which such a PacketConn may refuse. Release review of 224c363, finding 3.
func (c *demuxConn) HoldPeer(peer utp.ConnectionPeer) {
	if pc, ok := c.udp.(*packetConn); ok {
		if addr, err := peerUDPAddr(peer); err == nil {
			pc.pin(addr)
		}
	}
}

// ReleasePeer lets go of what HoldPeer kept, once the uTP socket has
// forgotten the connection.
func (c *demuxConn) ReleasePeer(peer utp.ConnectionPeer) {
	if pc, ok := c.udp.(*packetConn); ok {
		if addr, err := peerUDPAddr(peer); err == nil {
			pc.unpin(addr)
		}
	}
}

// deliver hands a uTP datagram to the uTP socket. It never blocks: a full
// queue means the uTP socket is not keeping up, and stalling the single UDP
// reader would hold up every other connection on the port as well.
func (c *demuxConn) deliver(payload []byte, pooled *[]byte, from *net.UDPAddr, native net.Addr) {
	select {
	case c.incoming <- inbound{payload: payload, pooled: pooled, from: from, native: native}:
	case <-c.closed:
		releaseDatagram(pooled)
	default:
		releaseDatagram(pooled)
	}
}

// ReadFrom hands the uTP socket the next datagram, and on a PacketConn of the
// Socket's own, puts the address it came from back in the map and holds it
// there until the next ReadFrom.
//
// The datagram waited in the queue after the map last saw its address, and a
// busy port could make the map forget it in the meantime: a SYN's, before the
// connection it starts holds it (HoldPeer), and the SYN-ACK then went out in
// the UDP form, which such a PacketConn may refuse. The uTP socket handles a
// datagram, connection made and answer sent, before it reads the next, so
// what this holds the next ReadFrom can let go of; one address at most, and
// none for a datagram still queued or dropped. Review of 99ce302.
func (c *demuxConn) ReadFrom(b []byte) (int, utp.ConnectionPeer, error) {
	pc, _ := c.udp.(*packetConn)
	if c.handed != nil {
		pc.unpin(c.handed)
		c.handed = nil
	}
	select {
	case pkt := <-c.incoming:
		n := copy(b, pkt.payload)
		releaseDatagram(pkt.pooled)
		if pc != nil && pkt.native != nil {
			pc.restoreHeld(pkt.from, pkt.native)
			c.handed = pkt.from
		}
		return n, utp.NewUdpPeer(pkt.from), nil
	case <-c.closed:
		return 0, nil, ErrSocketClosed
	}
}

// Queued reports how many datagrams ReadFrom would return without waiting,
// so the uTP socket reads them all before it acknowledges (utp.QueuedReader).
func (c *demuxConn) Queued() int {
	return len(c.incoming)
}

func (c *demuxConn) WriteTo(b []byte, dst utp.ConnectionPeer) (int, error) {
	select {
	case <-c.closed:
		return 0, ErrSocketClosed
	default:
	}
	addr, err := peerUDPAddr(dst)
	if err != nil {
		return 0, err
	}
	n, err := c.udp.WriteToUDP(b, addr)
	if err != nil && c.onWriteError != nil {
		c.onWriteError(addr, err)
	}
	return n, err
}

// Close does nothing to the UDP socket. The Socket owns that and closes it
// once its read loop has stopped; closing it from under the reader here would
// race with that.
func (c *demuxConn) Close() error {
	c.close()
	return nil
}

func (c *demuxConn) close() {
	c.closeOnce.Do(func() { close(c.closed) })
}

// peerUDPAddr recovers the UDP address a uTP peer names.
func peerUDPAddr(dst utp.ConnectionPeer) (*net.UDPAddr, error) {
	switch peer := dst.(type) {
	case *utp.UdpPeer:
		return peer.Addr(), nil
	case *utp.ConnectionId:
		return peerUDPAddr(peer.Peer)
	case nil:
		return nil, ErrNilPeer
	default:
		// Anything else came from a caller that built its own peer type.
		// Its Hash is by contract the address, so parse that rather than
		// failing.
		return net.ResolveUDPAddr("udp", dst.Hash())
	}
}
