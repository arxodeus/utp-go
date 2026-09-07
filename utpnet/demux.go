package utpnet

import (
	"net"
	"sync"

	utp "github.com/zen-eth/utp-go"
)

// demuxConn is the utp.Conn the uTP socket reads from.
//
// It is not the UDP socket. The Socket owns the only reader of that, so it can
// decide per datagram whether uTP or the caller should get it; what the uTP
// machinery sees is this, a queue fed only with the datagrams that were uTP.
// Writes go straight out through the real socket, because there is nothing to
// decide on the way out.
type demuxConn struct {
	udp *net.UDPConn

	incoming chan inbound

	closeOnce sync.Once
	closed    chan struct{}
}

type inbound struct {
	payload []byte
	from    *net.UDPAddr
}

// demuxDepth is how many uTP datagrams may be queued for the uTP socket
// before the read loop drops them.
//
// Deeper than the passthrough queue: the uTP socket drains this promptly and
// a drop here is real packet loss that a connection then has to recover from
// with a retransmission, where a dropped DHT datagram costs one query.
const demuxDepth = 4096

func newDemuxConn(udp *net.UDPConn) *demuxConn {
	return &demuxConn{
		udp:      udp,
		incoming: make(chan inbound, demuxDepth),
		closed:   make(chan struct{}),
	}
}

// deliver hands a uTP datagram to the uTP socket. It never blocks: a full
// queue means the uTP socket is not keeping up, and stalling the single UDP
// reader would hold up every other connection on the port as well.
func (c *demuxConn) deliver(payload []byte, from *net.UDPAddr) {
	select {
	case c.incoming <- inbound{payload: payload, from: from}:
	case <-c.closed:
	default:
	}
}

func (c *demuxConn) ReadFrom(b []byte) (int, utp.ConnectionPeer, error) {
	select {
	case pkt := <-c.incoming:
		n := copy(b, pkt.payload)
		return n, utp.NewUdpPeer(pkt.from), nil
	case <-c.closed:
		return 0, nil, ErrSocketClosed
	}
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
	return c.udp.WriteToUDP(b, addr)
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
