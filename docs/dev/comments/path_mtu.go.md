# path_mtu.go

Every comment in `path_mtu.go` at e56c9aa, verbatim.

## Line 9 — type PathMTUProvider

```
// PathMTUProvider is implemented by a Conn that can say how large a datagram
// the local path towards a peer will carry.
//
// It is libutp's UTP_GET_UDP_MTU callback (utp.h:78), which `mtu_reset` uses
// to set the ceiling the path-MTU search starts from
// (utp_internal.cpp:1314-1322). A ceiling above what the path carries is the
// stall recorded in KNOWN-LIMITATIONS.md.
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
```

## Line 35 — const pathMTUCacheTTL

```
// pathMTUCacheTTL is how long a discovered interface MTU is reused.
//
// Discovery costs a route lookup and an interface enumeration, which is a
// netlink dump on Linux -- cheap once, wasteful once per connection for a
// client opening hundreds. An interface's MTU does change (a VPN coming up is
// exactly the case this exists for), so the answer is not cached
// indefinitely; a minute is short against the search's own 30-minute
// rediscovery interval and long against a burst of connection setups.
```

## Line 45 — func (*udpConn).PathMTU

```
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
```

## Line 69 — func udpAddrForPeer

```
// udpAddrForPeer resolves a peer to a UDP address, by the same rules WriteTo
// uses: a known type directly, anything else through the address its Hash is
// by contract.
```

## Line 94 — const pathMTUCacheMax

```
// pathMTUCacheMax bounds the cache. It is process-wide and keyed by
// destination, so a process talking to ever more addresses -- a DHT node --
// grew it for as long as it ran: expired entries were never removed, only
// not reused. Release review of 9c44c4f (R10).
```

## Line 111 — func pathMTUFor

```
// pathMTUFor does the discovery, cached.
```

## Line 116 — func pathMTUFor

```
// The zone too: link-local addresses on two interfaces are two paths.
```

## Line 141 — func prunePathMTUCache

```
// prunePathMTUCache makes room in a full cache: the expired entries go, and
// if none had, half of the rest, which are only rediscovered if used again.
// Called with the cache locked.
```

## Line 161 — func discoverPathMTU

```
// discoverPathMTU finds the interface that routes to addr and converts its
// MTU into a datagram size.
//
// The route lookup is a UDP "dial", which sends nothing: it asks the kernel
// which local address a datagram to addr would leave from, which is the only
// portable way to reach the routing table from Go. Split tunnelling is why
// this is per-peer rather than per-socket -- two peers on one socket can
// leave by different interfaces.
```

## Line 203 — func datagramSizeForInterfaceMTU

```
// datagramSizeForInterfaceMTU subtracts what the layers below uTP spend.
//
// Which family is in use is decided by the *local* address, not the peer's: a
// v4-mapped peer reached through a dual-stack socket still travels as IPv4
// and pays the IPv4 header.
```

## Line 215 — func datagramSizeForInterfaceMTU

```
// An interface too small to carry IPv4's guaranteed reassembly size
		// tells us nothing worth acting on, and acting on it would drive the
		// search below its own floor.
```

## Line 223 — const ipv6SafeDatagram

```
// ipv6SafeDatagram is the largest uTP datagram every IPv6 path carries: the
// 1280-byte IPv6 minimum link MTU less 40 bytes of IPv6 header and 8 of UDP.
// libutp's UDP_TEREDO_MTU, the path MTU its default callback reports for any
// IPv6 peer (utp_utils.cpp:222, :230, :232-235).
```

## Line 229 — func ipv6Ceiling

```
// ipv6Ceiling caps the ceiling for a peer whose address is IPv6 at
// ipv6SafeDatagram, as libutp does.
//
// libutp's comment calls it conservative -- "Since we don't know the local
// address of the interface, be conservative and assume all IPv6 connections
// are Teredo" -- and it is, but it is also what keeps it from stalling. IPv6
// routers do not fragment, and with ICMPv6 filtered an oversized packet
// simply vanishes: the "path MTU below the size already adopted" stall in
// KNOWN-LIMITATIONS.md. The measurements: HISTORY.md, "ipv6Ceiling".
//
// An IPv4-mapped address travels as IPv4 and is left alone, as is any peer
// that does not carry an address.
```

## Line 253 — func pathMTUCeiling

```
// pathMTUCeiling is the datagram size a new connection to peer should start
// its search from: the configured ceiling, lowered to what the local path
// will carry when the Conn can say.
//
// Lowering only. libutp takes get_udp_mtu as the ceiling outright, which on
// loopback is 65488 and on a jumbo-frame link 8972; MaxPacketSize stays a
// hard cap here because a ceiling that high is a probe schedule this library
// has never been measured at. Recorded in DEVIATIONS.md.
```
