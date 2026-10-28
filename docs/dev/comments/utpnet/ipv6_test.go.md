# utpnet/ipv6_test.go

Every comment in `utpnet/ipv6_test.go` at e56c9aa, verbatim.

## Line 15 — func requireIPv6Loopback

```
// requireIPv6Loopback skips the test when the host has no IPv6 stack.
//
// It is a skip rather than a failure because address family availability is a
// property of the machine, not of this library: a container started without
// IPv6 answers "address family not supported by protocol" to the listen below,
// and nothing in this package can change that. The skip message says so, so a
// run that never exercised IPv6 cannot be mistaken for one that did.
```

## Line 31 — type ipv6Transport

```
// ipv6Transport is a way to get two Sockets on IPv6 addresses.
```

## Line 37 — func ipv6Transports

```
// ipv6Transports runs each IPv6 test twice: over the kernel's ::1, which
// skips on a host without IPv6, and over memNet, which always runs.
//
// The kernel run is the real thing and the only one that can catch a socket
// option or a dual-stack surprise. The memNet runs cover everything above
// the socket -- dialling by a v6 address string, keying connections by the
// peer's address, reporting addresses back -- on any host, including this
// one, whose kernel was built without IPv6. Before they existed every IPv6
// test here skipped on every run and IPv6 was rated "None" in
// COMPATIBILITY.md. The zoned link-local pair is the case most likely to
// break a key built from an address string.
```

## Line 102 — func TestDialAcceptTransferIPv6

```
// A transfer over IPv6, end to end.
//
// Nothing in the protocol is address-family dependent -- a uTP header carries
// no addresses, and the path-MTU ceiling is a UDP payload size (1400), which
// clears a 1280-byte IPv6 minimum link MTU with its 40-byte header. What can
// break is the plumbing around it: connections are keyed by the peer's address
// as a string, so the dialler's rendering of an address and the kernel's
// rendering of the same address's packets have to agree, and that is a
// different code path for a v6 address than for a v4 one.
```

## Line 169 — func TestConnAddressesIPv6

```
// The addresses handed back over IPv6 have to be the real ones, in the
// bracketed form the rest of Go uses, or a client cannot tell peers apart.
```

## Line 209 — func TestIPv6AddressPlumbing

```
// The address plumbing itself, which needs no IPv6 stack to exercise.
//
// These are the two conversions a v6 address passes through in this package,
// and both are string-shaped, which is where v6 addresses go wrong: an
// unbracketed "::1:8080" is ambiguous and a bracketed one is not.
```

## Line 215 — func TestIPv6AddressPlumbing

```
// anacrolix/torrent names its networks "utp", "utp4" and "utp6"; a "6"
	// must not be lost, or a v6 dial resolves as v4 and fails.
```

## Line 226 — func TestIPv6AddressPlumbing

```
// peerUDPAddr's fallback re-parses a peer's Hash, which is by contract
	// the address. For a v6 peer that hash is bracketed, and has to survive
	// the round trip unchanged.
```

## Line 246 — type hashOnlyPeer

```
// hashOnlyPeer is a caller-supplied peer type: not *utp.UdpPeer, so
// peerUDPAddr takes its parsing fallback.
```

## Line 254 — func TestIPv6PeersDifferingOnlyInAddressAreDistinct

```
// Two IPv6 peers that differ only in their address -- same port, or for
// link-local peers the same address and port on different interfaces -- are
// different connections, even when they happen to pick the same connection
// ids.
//
// A connection is keyed by its peer's address as a string together with its
// connection ids. The ids are random, so two peers usually differ there
// anyway, and a key that lost the v6 address or a link-local zone would go
// unnoticed -- the first version of this test, with random ids, passed with
// the address or the zone deliberately dropped from the key. Peers share ids
// by chance one pair in 65536, which a busy seeder meets; this gives both
// the same ids so that only the address separates them, and holds both
// connections open, since a collision only shows while the first occupies
// the key. In memory only: a kernel cannot give one host two addresses on
// ::1, and zones need real interfaces.
```

## Line 298 — func TestIPv6PeersDifferingOnlyInAddressAreDistinct

```
// Each client sends its own address as its payload, so the
			// server can tell which stream reached which connection. Both
			// connections are held open until both have been read: a
			// collision on the key only shows while the first connection
			// still occupies it. With the connections closed as soon as they
			// finished, a key that lost the v6 address still passed -- the
			// second client's SYN was ignored as a duplicate, retransmitted,
			// and accepted three seconds later once the first had gone.
```

## Line 340 — func TestIPv6PeersDifferingOnlyInAddressAreDistinct

```
// The same connection ids from both clients, and a deadline
				// shorter than a SYN retransmission (3 s). A server keying by a
				// collapsed address does not cross the two streams: it ignores
				// the second client's first SYN as belonging to the first
				// connection, and lets it in on the retransmission. Measured
				// with the v6 address dropped from the key: the second dial
				// took 3.03 s and held up the first connection's data with it,
				// where with a correct key both take milliseconds. A dial that
				// needs a retransmission is the failure.
```

## Line 383 — func TestIPv6TransferOverTheMinimumMTUPath

```
// A transfer to an IPv6 peer over a path at IPv6's minimum link MTU, 1280,
// whose routers' "packet too big" messages never arrive.
//
// Every IPv6 path carries 1280, and tunnels often carry little more, so a
// sender that tries anything larger and learns nothing when it vanishes can
// stall on any of them. libutp never tries: its default path MTU for an IPv6
// peer is 1232 (utp_utils.cpp:230-235), and so now is this library's
// (ipv6Ceiling). Measured before it, over the emulated network: this library
// stalled on such a path in five runs of five.
```

## Line 394 — func TestIPv6TransferOverTheMinimumMTUPath

```
// the UDP payload a 1280-byte IPv6 link carries
```
