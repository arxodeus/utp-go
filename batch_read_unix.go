//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package utp_go

import (
	"net"
	"sync"
	"syscall"
)

// maxReadBatch bounds one batch. libutp's embedders read until the socket
// would block, with no bound (the bridge in native/libutp does exactly that);
// this one only keeps a pathological burst from holding acknowledgements back
// indefinitely. A 4 MiB socket buffer holds about 3000 full-size datagrams.
const maxReadBatch = 4096

// readBatch returns every datagram the kernel already has queued, waiting only
// for the first one: libutp's embedder loop of recvfrom until EWOULDBLOCK,
// after which it calls utp_issue_deferred_acks once (utp.h:512-517;
// native/libutp/bridge.cpp). See UtpSocket.readLoop. drain does the reading:
// recvmmsg on Linux (batch_drain_linux.go), recvfrom elsewhere
// (batch_drain_recvfrom.go).
func (c *UdpConn) readBatch() ([]datagram, error) {
	rc, err := c.base.SyscallConn()
	if err != nil {
		return nil, err
	}
	// The socket dispatches every datagram of a batch before it reads the
	// next, so the list and the bytes are both free again by then.
	c.arena = c.arena[:0]
	out := c.batch[:0]
	err = c.drain(rc, &out)
	c.batch = out
	return out, err
}

// lend copies a datagram into the batch's arena and returns it there.
//
// Each datagram was copied into a slice of its own, which the decoded packet
// then kept as its body: an allocation for every datagram received, for an
// acknowledgement as much as for data. A packet decoded from a borrowed
// buffer copies its body only if it is kept (packet.own).
func (c *UdpConn) lend(b []byte) []byte {
	start := len(c.arena)
	c.arena = append(c.arena, b...)
	return c.arena[start:len(c.arena):len(c.arena)]
}

// peerFor returns the peer a datagram came from, reusing the one made for
// the last datagram from the same address. Making one per datagram converted
// the address and formatted it as text -- the key every packet is routed by
// -- for every packet: the largest single cost of routing one, measured on a
// reader just woken. Only the read loop calls this, so the cache needs no
// lock.
func (c *UdpConn) peerFor(sa syscall.Sockaddr) *UdpPeer {
	var k peerKey
	switch a := sa.(type) {
	case *syscall.SockaddrInet4:
		copy(k.ip[:], a.Addr[:])
		k.port = a.Port
	case *syscall.SockaddrInet6:
		k.ip, k.port, k.zone, k.v6 = a.Addr, a.Port, a.ZoneId, true
	default:
		return nil
	}
	return c.peerForKey(k)
}

// peerForKey is peerFor from the address's key.
func (c *UdpConn) peerForKey(k peerKey) *UdpPeer {
	if p, ok := c.peers[k]; ok {
		return p
	}
	if c.peers == nil || len(c.peers) >= maxCachedPeers {
		c.peers = make(map[peerKey]*UdpPeer)
	}
	p := NewUdpPeer(k.udpAddr())
	c.peers[k] = p
	return p
}

// udpAddr is the address net.UDPConn.ReadFrom would have returned for the
// datagram this key was made from, so a peer read in a batch hashes exactly as
// one read singly does.
func (k peerKey) udpAddr() *net.UDPAddr {
	if k.v6 {
		ip := make(net.IP, net.IPv6len)
		copy(ip, k.ip[:])
		return &net.UDPAddr{IP: ip, Port: k.port, Zone: zoneName(k.zone)}
	}
	ip := make(net.IP, net.IPv4len)
	copy(ip, k.ip[:net.IPv4len])
	return &net.UDPAddr{IP: ip, Port: k.port}
}

var zoneNames sync.Map // uint32 -> string

// zoneName is the interface name for an IPv6 scope id, as net formats it: the
// name when the interface exists, the number otherwise.
func zoneName(id uint32) string {
	if id == 0 {
		return ""
	}
	if v, ok := zoneNames.Load(id); ok {
		return v.(string)
	}
	name := uitoa(id)
	if ifi, err := net.InterfaceByIndex(int(id)); err == nil {
		name = ifi.Name
	}
	zoneNames.Store(id, name)
	return name
}

func uitoa(v uint32) string {
	if v == 0 {
		return "0"
	}
	var b [10]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
