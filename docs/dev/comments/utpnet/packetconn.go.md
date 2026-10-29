# utpnet/packetconn.go

Every comment in `utpnet/packetconn.go` at e56c9aa, verbatim.

## Line 13 — func NewSocketFromPacketConn

```
// NewSocketFromPacketConn makes a Socket on any net.PacketConn, not only a
// UDP socket: one wrapped for rate limiting, metrics or a gate a test holds,
// a socket shared with code that was handed it, or an in-memory network.
//
// The Socket takes ownership, and closing it closes pc. Unlike a Socket on a
// UDP socket it does not wait for its reader to return when it closes:
// nothing says a PacketConn it did not make honours a read deadline, and a
// Close that waited on one that does not would never return. The reader
// stops when pc's ReadFrom fails, and an error that is not a timeout, on a
// Socket not yet closed, closes it: Accept, ReadFrom and the dials then
// return ErrSocketClosed wrapping that error.
//
// Addresses need only name a host and port. A datagram from an address of
// some other type than *net.UDPAddr is handled under its UDP form, and
// replies go back to the address it came from; a connection dialled to an
// address of pc's own type takes DialAddr. A PacketConn that wants its own
// address type must be able to write to one it has itself returned.
```

## Line 40 — type packetConn

```
// packetConn adapts a net.PacketConn to what a Socket reads and writes.
```

## Line 44 — type packetConn

```
// native maps the UDP form of each address of another type to the
	// address itself, so what is sent to a peer goes to the address its
	// datagrams came from. Only addresses that are not *net.UDPAddr are
	// kept.
```

## Line 50 — type packetConn

```
// pins counts the live connections to each address in native. A pinned
	// address is never pruned: its connection writes to it, and its UDP form
	// is not something pc need accept. Review of 294cc2e, finding 4.
```

## Line 56 — type nativeAddr

```
// nativeAddr is an address of pc's own type and when a datagram last came
// from it.
```

## Line 63 — const nativeMax, nativeIdle

```
// The native map is bounded. Every address a datagram arrives from is
// remembered, before any protocol has looked at it, and nothing was ever
// forgotten: a PacketConn carrying a DHT grew it for as long as it ran.
// Release review of 9c44c4f (R10).
//
// An address forgotten while still in use is written to in its UDP form,
// which pc may not accept, so what goes first is what has been silent
// longest: nativeIdle is well past the 29 seconds within which a live uTP
// connection, keepalives included, sends something.
```

## Line 84 — func (*packetConn).readNative

```
// readNative is ReadFromUDP, and the address as pc gave it. A datagram waits
// in a queue before anything answers it, and the map can forget its address
// in the meantime; the address travels with it instead (Socket.run).
```

## Line 96 — func (*packetConn).readNative

```
// An address with no host and port cannot be answered or told
		// apart from another; the datagram is dropped, as one from an
		// address the kernel could not parse would be.
```

## Line 102 — func (*packetConn).restoreHeld

```
// restoreHeld remembers native as udp's address again, as when a datagram
// from it arrives, and keeps it until the matching unpin. See
// demuxConn.ReadFrom.
```

## Line 124 — func (*packetConn).udpForm

```
// udpForm is addr as a *net.UDPAddr, remembering addr to write back to.
```

## Line 129 — func (*packetConn).retain

```
// retain is udpForm, and keeps addr as pin does until the matching unpin: in
// one hold of the lock, so that nothing can forget it between the two.
```

## Line 175 — func (*packetConn).pruneLocked

```
// pruneLocked makes room in a full native map: what has been silent for
// nativeIdle goes, and if that is not enough, the longest silent until half
// is left.
```

## Line 199 — func (*packetConn).pin

```
// pin keeps udp's native address for as long as a connection to it lives;
// the matching unpin lets it be pruned again.
```

## Line 227 — func (*packetConn).nativeOf

```
// nativeOf is the address a UDP form was learned from, or the UDP form
// itself.
```

## Line 251 — func (*packetConn).SetReadBuffer

```
// SetReadBuffer and SetWriteBuffer reach the kernel when pc has a socket
// under it, and do nothing otherwise; the Socket only ever asks as a best
// effort.
```
