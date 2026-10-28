//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package utp

import (
	"net"
	"sync"
)

// maxReadBatch bounds one batch, so a pathological burst cannot hold
// acknowledgements back indefinitely. A 4 MiB socket buffer holds about 3000
// full-size datagrams.
const maxReadBatch = 4096

// udpConnReader is the state udpConn's batched reads keep between reads.
type udpConnReader struct {
	// peers caches the peer for each source address the read loop has seen.
	// See peerForKey.
	peers map[peerKey]*UDPPeer
	// arena holds the datagrams of the last batch read, and batch the list of
	// them: both are reused by the next. See lend.
	arena []byte
	batch []datagram
	// drainState is what this platform's drain reads into.
	drainState
}

// peerKey is a source address as the kernel reports it, comparable so it can
// key the reader's peer cache (udpConn.peerForKey).
type peerKey struct {
	ip   [16]byte
	port int
	zone uint32
	v6   bool
}

// maxCachedPeers bounds the peer cache. Past it the cache starts again, which
// costs one address formatting per peer that sends again.
const maxCachedPeers = 4096

// readBatch returns every datagram the kernel has queued, waiting only for
// the first. The socket then flushes deferred acknowledgements once per
// batch; see Socket.readLoop. drain does the reading: recvmmsg on Linux,
// recvfrom elsewhere.
func (c *udpConn) readBatch() ([]datagram, error) {
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

// lend copies a datagram into the batch's arena and returns the copy. The
// decoded packet borrows it, and copies its body only if it keeps the packet
// (packet.own).
func (c *udpConn) lend(b []byte) []byte {
	start := len(c.arena)
	c.arena = append(c.arena, b...)
	return c.arena[start:len(c.arena):len(c.arena)]
}

// peerForKey returns the peer a datagram came from, reusing the one made for
// the previous datagram from the same address, which saves formatting the
// address for every packet. Only the read loop calls it, so the cache needs
// no lock.
func (c *udpConn) peerForKey(k peerKey) *UDPPeer {
	if p, ok := c.peers[k]; ok {
		return p
	}
	if c.peers == nil || len(c.peers) >= maxCachedPeers {
		c.peers = make(map[peerKey]*UDPPeer)
	}
	p := NewUDPPeer(k.udpAddr())
	c.peers[k] = p
	return p
}

// udpAddr returns the address net.UDPConn.ReadFrom would have returned for
// this key's datagram, so a peer read in a batch hashes the same as one read
// singly.
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
