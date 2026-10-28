package utp

import (
	"fmt"
	"net"
	"strconv"
)

// ConnectionPeer identifies the remote end of a connection. Hash must return
// a stable string that distinguishes peers.
type ConnectionPeer interface {
	// Hash returns the peer's key, conventionally its address as text.
	Hash() string
}

// UDPPeer is a ConnectionPeer for a UDP address.
type UDPPeer struct {
	addr *net.UDPAddr
	// hash caches addr.String().
	hash string
}

// NewUDPPeer returns a peer for addr.
func NewUDPPeer(addr *net.UDPAddr) *UDPPeer {
	return &UDPPeer{addr: addr, hash: addr.String()}
}

// Hash returns the peer's address as text.
func (p *UDPPeer) Hash() string {
	if p.hash != "" {
		return p.hash
	}
	return p.addr.String()
}

// Addr returns the peer's UDP address.
func (p *UDPPeer) Addr() *net.UDPAddr {
	return p.addr
}

// String returns the peer's address.
func (p *UDPPeer) String() string {
	return p.addr.String()
}

type tcpPeer struct {
	addr *net.TCPAddr
}

func (p *tcpPeer) Hash() string {
	return p.addr.String()
}

func (p *tcpPeer) String() string {
	return p.addr.String()
}

// ConnectionID identifies a connection by its send and receive ids and its
// peer.
type ConnectionID struct {
	// Send is the id this end sends with: the peer's receive id.
	Send uint16
	// Recv is the id this end receives on.
	Recv uint16
	// Peer is the remote end.
	Peer ConnectionPeer
	hash string
}

// NewConnectionID returns the connection id for peer with the given receive
// and send ids.
func NewConnectionID(peer ConnectionPeer, recvID uint16, sendID uint16) *ConnectionID {
	connID := &ConnectionID{
		Peer: peer,
		Recv: recvID,
		Send: sendID,
	}
	connID.hash = genHash(connID)
	return connID
}

// genHash returns the connection's key: its ids and its peer's key, as text.
// The ids contain no colon, so distinct triples give distinct keys.
func genHash(connID *ConnectionID) string {
	peer := connID.Peer.Hash()
	return string(appendConnKey(make([]byte, 0, len(peer)+12), connID.Send, connID.Recv, peer))
}

// appendConnKey appends the key genHash returns for these ids and peer key,
// so a lookup can build it in a reused buffer.
func appendConnKey(b []byte, send, recv uint16, peer string) []byte {
	b = strconv.AppendUint(b, uint64(send), 10)
	b = append(b, ':')
	b = strconv.AppendUint(b, uint64(recv), 10)
	b = append(b, ':')
	return append(b, peer...)
}

// Hash returns the key the connection is tracked under. NewConnectionID
// caches it; for a ConnectionID built as a struct literal it is computed on
// each call.
func (id *ConnectionID) Hash() string {
	if id.hash == "" {
		return genHash(id)
	}
	return id.hash
}

// String returns a description of the id for logging.
func (id *ConnectionID) String() string {
	return fmt.Sprintf("send_id: %d recv_id:%d peer:%v hash: %s", id.Send, id.Recv, id.Peer, id.hash)
}
