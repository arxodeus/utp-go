# utpnet/native_addr_test.go

Every comment in `utpnet/native_addr_test.go` at e56c9aa, verbatim.

## Line 18 — func TestNativeAddressesAreBounded

```
// The map from UDP form back to a PacketConn's own address type is bounded,
// however many addresses datagrams arrive from, and keeps the most recent.
// It forgot nothing, so a PacketConn carrying a DHT grew it for as long as
// it ran. From the release review of 9c44c4f (R10).
```

## Line 40 — func TestNativeAddressOfALiveConnectionIsKept

```
// An address a live connection uses is kept however many others arrive. The
// longest silent went first whatever they were, so a quiet connection's peer
// could be pushed out by a burst of new addresses, and the connection's
// writes then went to the UDP form, which a PacketConn of another address
// type need not accept. Review of 294cc2e, finding 4.
```

## Line 71 — type otherPacketConn

```
// otherPacketConn is a UDP socket that names addresses as otherAddr and
// writes only to one.
```

## Line 95 — func TestAConnectionPinsItsPeersNativeAddress

```
// A connection over such a PacketConn pins its peer's address for its life,
// and lets it go when it ends.
```

## Line 147 — type firstLost

```
// firstLost is otherPacketConn losing the first datagram it is asked to send,
// and saying so.
```

## Line 164 — func churn

```
// churn has p learn more than it can remember of other addresses, as a busy
// port would.
```

## Line 190 — func TestADialKeepsItsPeersNativeAddressThroughTheHandshake

```
// A dial keeps its peer's native address through the handshake. It was kept
// from when the dial returned, so a SYN lost and resent after other addresses
// had crowded it out went out in the UDP form, which this PacketConn refuses,
// and the dial never completed. Release review of 224c363, finding 3.
```

## Line 223 — func TestAConnectionWaitingToBeAcceptedKeepsItsPeersNativeAddress

```
// A connection waiting to be accepted keeps its peer's native address. It was
// kept from when Accept took it, by when it could already have been crowded
// out, and the accepted connection's packets then went out in the UDP form.
```

## Line 259 — func TestAQueuedDatagramKeepsItsNativeAddress

```
// A uTP datagram keeps the address it came from while it waits to be handled.
// The map of such addresses could forget it while it was queued, before the
// connection it starts holds the address, and the answer then went out in
// the UDP form. Handed to the uTP socket, its address is remembered again and
// held until the socket reads the next one. Review of 99ce302.
```

## Line 274 — func TestAQueuedDatagramKeepsItsNativeAddress

```
// while it waits
```

## Line 285 — func TestAQueuedDatagramKeepsItsNativeAddress

```
// while the socket handles it
```

## Line 290 — func TestAQueuedDatagramKeepsItsNativeAddress

```
// The next read lets it go.
```

## Line 303 — func TestAPassthroughDatagramKeepsItsNativeAddress

```
// A datagram for ReadFrom keeps the address it came from in the PacketConn's
// own form, however busy the port is while it waits.
```
