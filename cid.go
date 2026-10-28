package utp

import (
	"fmt"
	"net"
	"strconv"
)

// ConnectionPeer identifies the remote end of a connection. Hash must return
// a stable string that distinguishes peers.
type ConnectionPeer interface {
	Hash() string
}

// UdpPeer is a ConnectionPeer for a UDP address.
type UdpPeer struct {
	addr *net.UDPAddr
	// hash caches addr.String().
	hash string
}

func NewUdpPeer(addr *net.UDPAddr) *UdpPeer {
	return &UdpPeer{addr: addr, hash: addr.String()}
}

func (p *UdpPeer) Hash() string {
	if p.hash != "" {
		return p.hash
	}
	return p.addr.String()
}

// Addr returns the peer's UDP address.
func (p *UdpPeer) Addr() *net.UDPAddr {
	return p.addr
}

func (p *UdpPeer) String() string {
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

// ConnectionId identifies a connection by its send and receive ids and its
// peer.
type ConnectionId struct {
	Send uint16
	Recv uint16
	Peer ConnectionPeer
	hash string
}

func NewConnectionId(peer ConnectionPeer, recvId uint16, sendId uint16) *ConnectionId {
	connId := &ConnectionId{
		Peer: peer,
		Recv: recvId,
		Send: sendId,
	}
	connId.hash = genHash(connId)
	return connId
}

// genHash returns the connection's key: its ids and its peer's key, as text.
// The ids contain no colon, so distinct triples give distinct keys.
func genHash(connId *ConnectionId) string {
	peer := connId.Peer.Hash()
	return string(appendConnKey(make([]byte, 0, len(peer)+12), connId.Send, connId.Recv, peer))
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

// Hash returns the key the connection is tracked under. NewConnectionId
// caches it; for a ConnectionId built as a struct literal it is computed on
// each call.
func (id *ConnectionId) Hash() string {
	if id.hash == "" {
		return genHash(id)
	}
	return id.hash
}

func (id *ConnectionId) String() string {
	return fmt.Sprintf("send_id: %d recv_id:%d peer:%v hash: %s", id.Send, id.Recv, id.Peer, id.hash)
}
