package utpnet

import (
	"net"
	"sync"

	utp "github.com/arxodeus/utp"
)

// demuxConn is the utp.Conn the uTP socket reads from: a queue of the
// datagrams the Socket's single UDP reader classified as uTP. Writes go
// directly to the underlying socket.
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
// before the read loop drops them. It is deeper than the passthrough queue
// because a drop here is packet loss a connection must recover from.
const demuxDepth = 4096

func newDemuxConn(udp udpPacketConn) *demuxConn {
	return &demuxConn{
		udp:      udp,
		incoming: make(chan inbound, demuxDepth),
		closed:   make(chan struct{}),
	}
}

// HoldPeer keeps the original address of peer, which may not be a
// *net.UDPAddr, for as long as the uTP socket has a connection with it: from
// the first handshake packet, dialed or accepted, to the end of the close.
// Only a Socket on its own PacketConn has such addresses (packetConn).
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

// ReadFrom returns the next datagram to the uTP socket. On a Socket's own
// PacketConn it also re-registers the datagram's original address and holds
// it until the next ReadFrom, so a reply such as a SYN-ACK goes to that
// address even if the map dropped it while the datagram was queued. The uTP
// socket finishes handling one datagram before reading the next, so at most
// one address is held this way.
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
		return n, utp.NewUDPPeer(pkt.from), nil
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
	case *utp.UDPPeer:
		return peer.Addr(), nil
	case *utp.ConnectionID:
		return peerUDPAddr(peer.Peer)
	case nil:
		return nil, ErrNilPeer
	default:
		// A caller-defined peer type. Its Hash is the address by contract,
		// so parse it.
		return net.ResolveUDPAddr("udp", dst.Hash())
	}
}
