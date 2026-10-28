package utp

import (
	"errors"
	"fmt"
	"net"
	"sync/atomic"
)

type udpConn struct {
	base *net.UDPConn
	// udpConnReader is the platform-specific state of batched reads. Only
	// the socket's read loop reads, so one is enough.
	udpConnReader
	// dfUnsupported records that the platform has no don't-fragment
	// option, so it is not tried again. See WriteToDontFragment.
	dfUnsupported atomic.Bool
}

func (c *udpConn) ReadFrom(b []byte) (int, ConnectionPeer, error) {
	n, addr, err := c.base.ReadFrom(b)
	if err != nil {
		return 0, nil, err
	}
	udpAddr := addr.(*net.UDPAddr)
	return n, &UdpPeer{addr: udpAddr}, nil
}
func (c *udpConn) WriteTo(b []byte, dst ConnectionPeer) (int, error) {
	if dst == nil {
		return 0, fmt.Errorf("utp_socket: can't write to nil connection")
	}
	switch baseDst := dst.(type) {
	case *ConnectionId:
		return c.WriteTo(b, baseDst.Peer)
	case *UdpPeer:
		return c.base.WriteToUDP(b, baseDst.addr)
	}
	// A peer type this package did not create. Its Hash is the address by
	// contract, so parse it; a peer whose address does not parse is an
	// error rather than a silently dropped packet.
	addr, err := net.ResolveUDPAddr("udp", dst.Hash())
	if err != nil {
		return 0, fmt.Errorf("utp_socket: peer %T hashes to %q, which is not a UDP address: %w",
			dst, dst.Hash(), err)
	}
	return c.base.WriteToUDP(b, addr)
}

// WriteToDontFragment sends one datagram with fragmentation disabled,
// implementing DontFragmentWriter.
//
// Don't-fragment is a socket option, so it is set, one datagram is sent, and
// it is cleared again. The socket holds writeMu exclusively for such a send,
// so no other write can fall in between. The option is cleared even if the
// send fails, so that ordinary data stays fragmentable.
//
// On a platform without the option it returns ErrDontFragmentUnsupported and
// sends nothing.
func (c *udpConn) WriteToDontFragment(b []byte, dst ConnectionPeer) (int, error) {
	if c.dfUnsupported.Load() {
		return 0, ErrDontFragmentUnsupported
	}
	if err := setDontFragment(c.base, true); err != nil {
		// Remember it, so the option is not tried on every probe.
		c.dfUnsupported.Store(true)
		return 0, ErrDontFragmentUnsupported
	}
	n, err := c.WriteTo(b, dst)
	if clearErr := setDontFragment(c.base, false); clearErr != nil && err == nil {
		// The datagram was sent, but later ones would be unfragmentable too.
		// Report it.
		return n, fmt.Errorf("utp_socket: sent an MTU probe but could not clear "+
			"the don't-fragment bit, so later packets would not be fragmentable: %w", clearErr)
	}
	return n, err
}

func (c *udpConn) Close() error {
	return c.base.Close()
}

// LocalAddr returns the address this connection is bound to.
func (c *udpConn) LocalAddr() net.Addr {
	return c.base.LocalAddr()
}

type incomingPacketRaw struct {
	peer    ConnectionPeer
	payload []byte
	// borrowed says payload is the reader's buffer, which it reuses once the
	// datagram has been dispatched: the decoded packet copies its body out.
	borrowed bool
}

// datagram is one datagram from a batched read.
type datagram struct {
	payload []byte
	peer    ConnectionPeer
	// borrowed says payload is reused by the next batch. See
	// incomingPacketRaw.borrowed.
	borrowed bool
}

// batchReader is a Conn that can hand over everything already queued in one
// call. udpConn on Unix is one; anything else reads a datagram at a time.
type batchReader interface {
	readBatch() ([]datagram, error)
}

// QueuedReader is an optional interface for a Conn that can report how many
// datagrams ReadFrom would return without blocking. The socket reads those
// before acknowledging, so a run of datagrams already waiting draws one
// acknowledgement rather than one each. A Conn that implements neither this
// nor batched reads has each datagram treated as its own batch.
//
// Queued may undercount but must not overcount: every datagram it counts
// must be returned by ReadFrom without blocking.
type QueuedReader interface {
	Queued() int
}

// maxQueuedRun bounds how many datagrams a QueuedReader's run may hold an
// acknowledgement for, as maxReadBatch does a batched read.
const maxQueuedRun = 4096

var errBatchReadUnsupported = errors.New("utp: batched reads are not supported on this platform")

type incomingPacket struct {
	pkt *packet
	cid *ConnectionId
}
