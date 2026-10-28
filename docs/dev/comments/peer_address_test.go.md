# peer_address_test.go

Every comment in `peer_address_test.go` at e56c9aa, verbatim.

## Line 9 — func TestUdpPeerHashIdentifiesTheAddressNotItsEncoding

```
// Connections are keyed by the peer's address rendered as a string
// (UdpPeer.Hash, cid.go), so two renderings of the same address must produce
// the same key and two different addresses must not.
//
// The case that matters is a dual-stack socket. A caller resolving
// "127.0.0.1:9000" may get a 4-byte IP, while a packet arriving on a socket
// bound to "::" may be reported with the same address in its 16-byte
// v4-mapped form. If those hashed differently, the connection the dialler
// created and the connection the inbound packet looks up would be two
// different entries and nothing would ever match.
//
// net.IP.String renders a v4-mapped address as dotted quad, so they agree.
// That is load-bearing rather than incidental, which is why it is pinned here.
```

## Line 30 — func TestUdpPeerHashIdentifiesTheAddressNotItsEncoding

```
// And the distinctions that must survive.
```

## Line 45 — func TestUdpPeerHashIdentifiesTheAddressNotItsEncoding

```
// "2001:db8::1" and "2001:db8:0:0:0:0:0:1" are the same address in
		// two spellings, and must land on the same key.
```

## Line 61 — func TestConnectionIdHashDistinguishesIPv6Peers

```
// A v6 peer has to key a connection id the same way a v4 one does: the id
// hashes send, recv and the peer together (cid.go genHash), and a peer whose
// hash is empty or unstable would collapse every connection to one peer into
// a single entry.
```

## Line 84 — func TestUdpConnWritesToForeignPeerTypes

```
// A peer type this package did not create must still receive packets.
//
// udpConn.WriteTo switches on *UdpPeer and *ConnectionId and used to end in
// `return 0, nil` for anything else: the packet was dropped and the caller was
// told it had been sent. A connection built on such a peer would then time out
// with nothing anywhere to say why. Neither half of that is acceptable -- a
// send that did not happen is not a success -- so the address is parsed from
// the peer's Hash, which is the contract the rest of this repository already
// relies on (utpnet's peerUDPAddr), and a Hash that is not an address is an
// error rather than silence.
```

## Line 129 — func TestUdpConnWritesToForeignPeerTypes

```
// And a peer whose hash is not an address is reported, not swallowed.
```

## Line 135 — type addressOnlyPeer

```
// addressOnlyPeer implements ConnectionPeer without being any of the types
// udpConn.WriteTo knows about.
```
