package utp_go

import (
	"fmt"
	"net"
	"strconv"
)

// ConnectionPeer is an interface representing a remote peer.
type ConnectionPeer interface {
	Hash() string
}

// SocketAddr is a simple implementation of the ConnectionPeer interface using net.UDPAddr.
type UdpPeer struct {
	addr *net.UDPAddr
}

func NewUdpPeer(addr *net.UDPAddr) *UdpPeer {
	return &UdpPeer{addr: addr}
}

func (p *UdpPeer) Hash() string {
	return p.addr.String()
}

// Addr returns the UDP address this peer names. It exists so callers that
// need a net.Addr -- anything presenting a uTP stream as a net.Conn -- can
// get one back out without reparsing the hash.
func (p *UdpPeer) Addr() *net.UDPAddr {
	return p.addr
}

func (p *UdpPeer) String() string {
	return p.addr.String()
}

type TcpPeer struct {
	addr *net.TCPAddr
}

func (p *TcpPeer) Hash() string {
	return p.addr.String()
}

func (p *TcpPeer) String() string {
	return p.addr.String()
}

// ConnectionId represents a connection identifier with send and receive IDs and a peer.
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

// genHash is the connection's key: its two ids and its peer's key, as text.
// Two connections share a key exactly when all three match -- the ids come
// first and hold no colon, so the peer's key, whatever it contains, cannot
// make two different triples spell the same string.
//
// It was a SHA3-256 of that same text, cut to 20 bytes and hex-encoded, which
// bought nothing a map needs and cost most of the time a packet took: the
// socket's reader builds up to three candidate ids for every datagram it
// routes, and with the hash that was 27us of a 37us path from read to
// acknowledgement (10 Mb/s from libutp, TestAckTurnaround).
func genHash(connId *ConnectionId) string {
	peer := connId.Peer.Hash()
	b := make([]byte, 0, len(peer)+12)
	b = strconv.AppendUint(b, uint64(connId.Send), 10)
	b = append(b, ':')
	b = strconv.AppendUint(b, uint64(connId.Recv), 10)
	b = append(b, ':')
	b = append(b, peer...)
	return string(b)
}

// Hash returns the key this connection is tracked under.
//
// The value is cached by NewConnectionId. ConnectionId is exported with
// exported fields, so callers legitimately build one as a struct literal --
// and such a value has an empty cached hash. Returning that empty string made
// every literal-built ConnectionId hash to "" and therefore collide with
// every other one, silently breaking connection lookup. Fall back to
// computing it rather than handing back a key that is wrong.
//
// Prefer NewConnectionId: it caches, and this fallback does not.
func (id *ConnectionId) Hash() string {
	if id.hash == "" {
		return genHash(id)
	}
	return id.hash
}

func (id *ConnectionId) String() string {
	return fmt.Sprintf("send_id: %d recv_id:%d peer:%v hash: %s", id.Send, id.Recv, id.Peer, id.hash)
}
