package utp

import (
	"net"
	"sync"
	"time"
)

// PathMTUProvider is an optional interface for a Conn that can report the
// largest datagram the local path towards a peer carries. The connection
// starts its path-MTU search from that value.
//
// The value is the largest uTP datagram, the UDP payload, not the link MTU.
// It only lowers the configured MaxPacketSize, never raises it.
type PathMTUProvider interface {
	PathMTU(peer ConnectionPeer) (int, bool)
}

// pathMTUCacheTTL is how long a discovered interface MTU is reused.
// Discovery costs a route lookup and an interface enumeration, so it is not
// repeated for every connection; an interface's MTU can change, as when a
// VPN comes up, so it is not cached indefinitely.
const pathMTUCacheTTL = time.Minute

// PathMTU reports the largest uTP datagram the local path towards peer will
// carry, from the MTU of the interface that routes to it.
//
// This is the first hop's MTU, not the whole path's. A narrower link further
// along is found by the MTU search or an ICMP report. It covers the common
// case where the local interface is the narrow one, such as a VPN or tunnel.
func (c *udpConn) PathMTU(peer ConnectionPeer) (int, bool) {
	addr, ok := udpAddrForPeer(peer)
	if !ok {
		return 0, false
	}
	return pathMTUFor(addr)
}

// udpAddrForPeer resolves a peer to a UDP address by the rules WriteTo uses:
// a known type directly, anything else by parsing its Hash.
func udpAddrForPeer(peer ConnectionPeer) (*net.UDPAddr, bool) {
	switch p := peer.(type) {
	case nil:
		return nil, false
	case *ConnectionID:
		if p.Peer == nil {
			return nil, false
		}
		return udpAddrForPeer(p.Peer)
	case *UDPPeer:
		if p.addr == nil {
			return nil, false
		}
		return p.addr, true
	}
	addr, err := net.ResolveUDPAddr("udp", peer.Hash())
	if err != nil {
		return nil, false
	}
	return addr, true
}

// pathMTUCacheMax bounds the cache, which is process-wide and keyed by
// destination.
const pathMTUCacheMax = 4096

var pathMTUCache = struct {
	sync.Mutex
	entries map[string]pathMTUEntry
}{entries: make(map[string]pathMTUEntry)}

type pathMTUEntry struct {
	mtu int
	ok  bool
	at  time.Time
}

// pathMTUFor returns the discovered datagram size, using the cache.
func pathMTUFor(addr *net.UDPAddr) (int, bool) {
	if addr == nil || addr.IP == nil {
		return 0, false
	}
	// Include the zone: link-local addresses on two interfaces are two
	// paths.
	key := addr.IP.String()
	if addr.Zone != "" {
		key += "%" + addr.Zone
	}

	pathMTUCache.Lock()
	if e, hit := pathMTUCache.entries[key]; hit && time.Since(e.at) < pathMTUCacheTTL {
		pathMTUCache.Unlock()
		return e.mtu, e.ok
	}
	pathMTUCache.Unlock()

	mtu, ok := discoverPathMTU(addr)

	now := time.Now()
	pathMTUCache.Lock()
	if len(pathMTUCache.entries) >= pathMTUCacheMax {
		prunePathMTUCache(now)
	}
	pathMTUCache.entries[key] = pathMTUEntry{mtu: mtu, ok: ok, at: now}
	pathMTUCache.Unlock()
	return mtu, ok
}

// prunePathMTUCache makes room in a full cache: it removes expired entries
// and, if there were none, half of the rest. Called with the cache locked.
func prunePathMTUCache(now time.Time) {
	for k, e := range pathMTUCache.entries {
		if now.Sub(e.at) >= pathMTUCacheTTL {
			delete(pathMTUCache.entries, k)
		}
	}
	if len(pathMTUCache.entries) < pathMTUCacheMax {
		return
	}
	for k := range pathMTUCache.entries {
		if len(pathMTUCache.entries) < pathMTUCacheMax/2 {
			break
		}
		delete(pathMTUCache.entries, k)
	}
}

// discoverPathMTU finds the interface that routes to addr and converts its
// MTU into a datagram size.
//
// The route lookup is a UDP dial, which sends nothing; it is the portable way
// to ask which local address a datagram to addr would leave from. It is per
// peer because, with split tunneling, peers on one socket can be reached
// through different interfaces.
func discoverPathMTU(addr *net.UDPAddr) (int, bool) {
	probe, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return 0, false
	}
	local, ok := probe.LocalAddr().(*net.UDPAddr)
	probe.Close()
	if !ok || local.IP == nil || local.IP.IsUnspecified() {
		return 0, false
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return 0, false
	}
	for _, iface := range ifaces {
		if iface.MTU <= 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok || !ipNet.IP.Equal(local.IP) {
				continue
			}
			return datagramSizeForInterfaceMTU(iface.MTU, local.IP)
		}
	}
	return 0, false
}

// datagramSizeForInterfaceMTU subtracts the IP and UDP headers. The family
// is decided by the local address, since a v4-mapped peer on a dual-stack
// socket still travels as IPv4.
func datagramSizeForInterfaceMTU(mtu int, local net.IP) (int, bool) {
	overhead := ipv4HeaderAndUDPOverhead
	if local.To4() == nil {
		overhead = ipv6HeaderAndUDPOverhead
	}
	size := mtu - int(overhead)
	if size < int(mtuAbsoluteFloor) {
		// An interface smaller than IPv4's guaranteed reassembly size is
		// ignored, as acting on it would take the search below its floor.
		return 0, false
	}
	return size, true
}

// ipv6SafeDatagram is the largest uTP datagram every IPv6 path carries: the
// 1280-byte minimum IPv6 link MTU less 40 bytes of IPv6 header and 8 of UDP.
const ipv6SafeDatagram = 1280 - ipv6HeaderAndUDPOverhead

// ipv6Ceiling caps the ceiling for an IPv6 peer at ipv6SafeDatagram. IPv6
// routers do not fragment, and where ICMPv6 is filtered an oversized packet
// is silently dropped, so starting from the safe size avoids a stall.
//
// An IPv4-mapped address travels as IPv4 and is left alone, as is a peer
// with no address.
func ipv6Ceiling(peer ConnectionPeer, configured uint16) uint16 {
	addressed, ok := peer.(interface{ Addr() *net.UDPAddr })
	if !ok || configured <= ipv6SafeDatagram {
		return configured
	}
	addr := addressed.Addr()
	if addr == nil || addr.IP.To4() != nil || addr.IP.To16() == nil {
		return configured
	}
	return ipv6SafeDatagram
}

// pathMTUCeiling returns the datagram size a new connection to peer starts
// its search from: the configured ceiling, lowered to what the local path
// will carry when the Conn can report it.
func pathMTUCeiling(socket Conn, peer ConnectionPeer, configured uint16) uint16 {
	provider, ok := socket.(PathMTUProvider)
	if !ok || peer == nil {
		return configured
	}
	discovered, ok := provider.PathMTU(peer)
	if !ok || discovered <= 0 || discovered >= int(configured) {
		return configured
	}
	return uint16(discovered)
}
