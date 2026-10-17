package utp

import (
	"errors"
	"fmt"
	"net"
	"sync/atomic"
)

type udpConn struct {
	base *net.UDPConn
	// udpConnReader is what batched reads keep between reads, which differs
	// by platform (batch_read_unix.go, batch_read_other.go). Only the
	// socket's read loop reads, so one is enough.
	udpConnReader
	// dfUnsupported records that this platform has no per-packet
	// don't-fragment option, so the MTU search stops asking. See
	// WriteToDontFragment.
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
	// A peer type this package did not create. Its Hash is by contract the
	// address, which is what utpnet's peerUDPAddr already relies on, so parse
	// that rather than dropping the packet.
	//
	// This used to `return 0, nil`: every packet to such a peer vanished, and
	// the connection above saw a successful send and timed out with nothing to
	// explain it. Silence is the wrong answer whether or not the address
	// parses -- if it does not, that is an error the caller needs.
	addr, err := net.ResolveUDPAddr("udp", dst.Hash())
	if err != nil {
		return 0, fmt.Errorf("utp_socket: peer %T hashes to %q, which is not a UDP address: %w",
			dst, dst.Hash(), err)
	}
	return c.base.WriteToUDP(b, addr)
}

// WriteToDontFragment sends one datagram with fragmentation disabled,
// implementing DontFragmentWriter for a real UDP socket.
//
// The bit is a socket option on every platform that has one at all -- there is
// no per-datagram control message for it -- so it is set, one datagram is
// sent, and it is cleared again. That is safe here because the socket holds
// writeMu exclusively for such a send and shared for every other one, so no
// other write can land between the two. See UtpSocket.writeDatagram.
//
// It is cleared even when the send fails. Leaving it set would make every
// subsequent datagram on this socket unfragmentable, which is the one thing
// libutp is explicit about not wanting: ordinary data has to stay
// fragmentable, "now we need it to fragment just to get it through"
// (utp_internal.cpp:898-905).
//
// A platform with no such option answers ErrDontFragmentUnsupported, and
// nothing is sent. The caller then sends it normally.
func (c *udpConn) WriteToDontFragment(b []byte, dst ConnectionPeer) (int, error) {
	if c.dfUnsupported.Load() {
		return 0, ErrDontFragmentUnsupported
	}
	if err := setDontFragment(c.base, true); err != nil {
		// Remember it, so a socket on a platform without the option is not
		// asked once per probe for the life of the process.
		c.dfUnsupported.Store(true)
		return 0, ErrDontFragmentUnsupported
	}
	n, err := c.WriteTo(b, dst)
	if clearErr := setDontFragment(c.base, false); clearErr != nil && err == nil {
		// The datagram went out, but the socket is now in a state where every
		// later one would too. Report it rather than carrying on quietly.
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

// QueuedReader is a Conn that can say how many datagrams ReadFrom would return
// without waiting. The socket reads those before it acknowledges anything, so
// a run of datagrams that were already waiting draws one acknowledgement, not
// one each: libutp's embedder reads until the socket would block and only then
// calls utp_issue_deferred_acks (utp.h:512-517). A Conn that implements
// neither this nor batched reads has every datagram treated as a batch of its
// own, which holds nothing back.
//
// Queued may count low, never high: a datagram it counts must be one ReadFrom
// returns at once.
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
