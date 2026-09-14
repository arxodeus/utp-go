package utp_go

import (
	"net"
	"sync"
	"time"
)

// PathMTUProvider is implemented by a Conn that can say how large a datagram
// the local path towards a peer will carry.
//
// It is libutp's UTP_GET_UDP_MTU callback (utp.h:78), which `mtu_reset` uses
// to set the ceiling the path-MTU search starts from
// (utp_internal.cpp:1314-1322). This library had no equivalent: the ceiling
// was a fixed 1400 for every connection, which is safe on an ordinary path
// and too large on a tunnelled one -- and a ceiling above what the path
// carries is the stall recorded in KNOWN-LIMITATIONS.md.
//
// The Conn interface deliberately has only ReadFrom, WriteTo and Close, so
// that an emulated network and the libutp driver satisfy it as easily as a
// UDP socket. Asking an operating system about an interface has no place
// there. This is the optional extension instead: a Conn that can answer
// implements it, everything else is unaffected and keeps the configured
// ceiling.
//
// The value returned is the largest **uTP datagram** the path will carry --
// what fits in a UDP payload -- not the link MTU. That is the same quantity
// libutp's callback returns, and the same one mtuSearch works in.
//
// It is only ever used to *lower* the configured MaxPacketSize, never to
// raise it. A loopback interface reports 65536, and nothing here wants a
// 65-kilobyte datagram.
type PathMTUProvider interface {
	PathMTU(peer ConnectionPeer) (int, bool)
}

// pathMTUCacheTTL is how long a discovered interface MTU is reused.
//
// Discovery costs a route lookup and an interface enumeration, which is a
// netlink dump on Linux -- cheap once, wasteful once per connection for a
// client opening hundreds. An interface's MTU does change (a VPN coming up is
// exactly the case this exists for), so the answer is not cached
// indefinitely; a minute is short against the search's own 30-minute
// rediscovery interval and long against a burst of connection setups.
const pathMTUCacheTTL = time.Minute

// PathMTU reports the largest uTP datagram the local path towards peer will
// carry, discovered from the MTU of the interface that routes to it.
//
// The interface MTU is the first hop's, not the whole path's: a narrower link
// further along is still found the way it always was, by a probe that does
// not arrive, or now by an ICMP report. What this catches is the common case
// where the *local* interface is the narrow one -- a VPN or tunnel device,
// where WireGuard's default of 1420 and IPv6's floor of 1280 both sit below
// the 1400-byte datagram this library would otherwise have adopted.
//
// A kernel's own path-MTU cache would be better still: Linux will answer
// getsockopt(IP_MTU) with what it has learned from ICMP for that route. It is
// not used here because it is Linux-only, needs a connected socket, and would
// make golang.org/x/sys a direct dependency -- and the point of this fork is
// that it builds anywhere with CGO_ENABLED=0. Feeding ICMP in directly is the
// portable route to the same information; see ProcessICMPFragmentation.
func (c *UdpConn) PathMTU(peer ConnectionPeer) (int, bool) {
	addr, ok := udpAddrForPeer(peer)
	if !ok {
		return 0, false
	}
	return pathMTUFor(addr)
}

// udpAddrForPeer resolves a peer to a UDP address, by the same rules WriteTo
// uses: a known type directly, anything else through the address its Hash is
// by contract.
func udpAddrForPeer(peer ConnectionPeer) (*net.UDPAddr, bool) {
	switch p := peer.(type) {
	case nil:
		return nil, false
	case *ConnectionId:
		if p.Peer == nil {
			return nil, false
		}
		return udpAddrForPeer(p.Peer)
	case *UdpPeer:
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

var pathMTUCache = struct {
	sync.Mutex
	entries map[string]pathMTUEntry
}{entries: make(map[string]pathMTUEntry)}

type pathMTUEntry struct {
	mtu int
	ok  bool
	at  time.Time
}

// pathMTUFor does the discovery, cached.
func pathMTUFor(addr *net.UDPAddr) (int, bool) {
	if addr == nil || addr.IP == nil {
		return 0, false
	}
	key := addr.IP.String()

	pathMTUCache.Lock()
	if e, hit := pathMTUCache.entries[key]; hit && time.Since(e.at) < pathMTUCacheTTL {
		pathMTUCache.Unlock()
		return e.mtu, e.ok
	}
	pathMTUCache.Unlock()

	mtu, ok := discoverPathMTU(addr)

	pathMTUCache.Lock()
	pathMTUCache.entries[key] = pathMTUEntry{mtu: mtu, ok: ok, at: time.Now()}
	pathMTUCache.Unlock()
	return mtu, ok
}

// discoverPathMTU finds the interface that routes to addr and converts its
// MTU into a datagram size.
//
// The route lookup is a UDP "dial", which sends nothing: it asks the kernel
// which local address a datagram to addr would leave from, which is the only
// portable way to reach the routing table from Go. Split tunnelling is why
// this is per-peer rather than per-socket -- two peers on one socket can
// leave by different interfaces.
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

// datagramSizeForInterfaceMTU subtracts what the layers below uTP spend.
//
// Which family is in use is decided by the *local* address, not the peer's: a
// v4-mapped peer reached through a dual-stack socket still travels as IPv4
// and pays the IPv4 header.
func datagramSizeForInterfaceMTU(mtu int, local net.IP) (int, bool) {
	overhead := ipv4HeaderAndUDPOverhead
	if local.To4() == nil {
		overhead = ipv6HeaderAndUDPOverhead
	}
	size := mtu - int(overhead)
	if size < int(mtuAbsoluteFloor) {
		// An interface too small to carry IPv4's guaranteed reassembly size
		// tells us nothing worth acting on, and acting on it would drive the
		// search below its own floor.
		return 0, false
	}
	return size, true
}

// pathMTUCeiling is the datagram size a new connection to peer should start
// its search from: the configured ceiling, lowered to what the local path
// will carry when the Conn can say.
//
// Lowering only. libutp takes get_udp_mtu as the ceiling outright, which on
// loopback is 65488 and on a jumbo-frame link 8972; MaxPacketSize stays a
// hard cap here because a ceiling that high is a probe schedule this library
// has never been measured at, and raising it is a separate decision from
// fixing the case where 1400 is too big. Recorded in DEVIATIONS.md.
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
