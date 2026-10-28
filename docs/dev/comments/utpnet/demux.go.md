# utpnet/demux.go

Every comment in `utpnet/demux.go` at e56c9aa, verbatim.

## Line 10 — type demuxConn

```
// demuxConn is the utp.Conn the uTP socket reads from.
//
// It is not the UDP socket. The Socket owns the only reader of that, so it can
// decide per datagram whether uTP or the caller should get it; what the uTP
// machinery sees is this, a queue fed only with the datagrams that were uTP.
// Writes go straight out through the real socket, because there is nothing to
// decide on the way out.
```

## Line 22 — type demuxConn

```
// onWriteError is Options.OnWriteError, if set.
```

## Line 28 — type demuxConn

```
// handed is the address of the datagram ReadFrom last returned, held
	// until the next ReadFrom. Used only by the uTP socket's reader, the one
	// caller of ReadFrom. See ReadFrom.
```

## Line 36 — type inbound

```
// see datagram.pooled
```

## Line 38 — type inbound

```
// see datagram.native
```

## Line 41 — const demuxDepth

```
// demuxDepth is how many uTP datagrams may be queued for the uTP socket
// before the read loop drops them.
//
// Deeper than the passthrough queue: the uTP socket drains this promptly and
// a drop here is real packet loss that a connection then has to recover from
// with a retransmission, where a dropped DHT datagram costs one query.
```

## Line 57 — func (*demuxConn).HoldPeer

```
// HoldPeer keeps the address peer's datagrams came from, which may not be a
// *net.UDPAddr, for as long as the uTP socket has a connection with it: from
// the first packet of the handshake, dialled or accepted, to the last of the
// close (utp.Conn). Only a Socket on a PacketConn of its own has such
// addresses (packetConn).
//
// It was kept from when the application had the connection -- the dial
// returned, or an Accept took it -- to the end of its stream. Before that, a
// dial's handshake and a connection waiting to be accepted could lose it to
// other addresses arriving, and their packets then went out in the UDP form,
// which such a PacketConn may refuse. Release review of 224c363, finding 3.
```

## Line 76 — func (*demuxConn).ReleasePeer

```
// ReleasePeer lets go of what HoldPeer kept, once the uTP socket has
// forgotten the connection.
```

## Line 86 — func (*demuxConn).deliver

```
// deliver hands a uTP datagram to the uTP socket. It never blocks: a full
// queue means the uTP socket is not keeping up, and stalling the single UDP
// reader would hold up every other connection on the port as well.
```

## Line 99 — func (*demuxConn).ReadFrom

```
// ReadFrom hands the uTP socket the next datagram, and on a PacketConn of the
// Socket's own, puts the address it came from back in the map and holds it
// there until the next ReadFrom.
//
// The datagram waited in the queue after the map last saw its address, and a
// busy port could make the map forget it in the meantime: a SYN's, before the
// connection it starts holds it (HoldPeer), and the SYN-ACK then went out in
// the UDP form, which such a PacketConn may refuse. The uTP socket handles a
// datagram, connection made and answer sent, before it reads the next, so
// what this holds the next ReadFrom can let go of; one address at most, and
// none for a datagram still queued or dropped. Review of 99ce302.
```

## Line 130 — func (*demuxConn).Queued

```
// Queued reports how many datagrams ReadFrom would return without waiting,
// so the uTP socket reads them all before it acknowledges (utp.QueuedReader).
```

## Line 153 — func (*demuxConn).Close

```
// Close does nothing to the UDP socket. The Socket owns that and closes it
// once its read loop has stopped; closing it from under the reader here would
// race with that.
```

## Line 165 — func peerUDPAddr

```
// peerUDPAddr recovers the UDP address a uTP peer names.
```

## Line 175 — func peerUDPAddr

```
// Anything else came from a caller that built its own peer type.
		// Its Hash is by contract the address, so parse that rather than
		// failing.
```
