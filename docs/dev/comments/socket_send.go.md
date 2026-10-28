# socket_send.go

Every comment in `socket_send.go` at e56c9aa, verbatim.

## Line 9 — const connEventQueueLen

```
// connEventQueueLen is the capacity of a connection's event queue. Packets go
// through it only until the connection's event loop has finished setting up --
// after that the socket's reader hands them over inline (receiveInline) -- and
// a peer has nothing to send before then but a repeated SYN: its data waits
// for the SYN-ACK, which that setup sends. Otherwise it carries the
// application's close and half-close and ICMP reports, a few each. A packet
// that finds it full is dropped and counted (PacketsDroppedFullConnQueue).
//
// It was 1000, and a channel's buffer is allocated whole: 8 KB per
// connection, about a quarter of what an idle connection held once its
// receive buffer stopped being allocated up front.
```

## Line 22 — var encodeBufs

```
// encodeBufs holds the buffers outgoing packets are encoded into. A packet is
// written the moment it is encoded and the Conn may not keep it, so a buffer
// is free again once WriteTo returns. Encoding into a fresh slice was one
// allocation per packet sent, about a twelfth of all the allocations of a LAN
// transfer.
```

## Line 32 — func (*UtpSocket).sendEvent

```
// sendEvent carries out what a connection or the socket wants done on the
// wire, on the caller's goroutine: a packet written, or a finished
// connection's entry removed.
//
// This was a write loop on its own goroutine, fed by socketEvents, which put
// a hand-off between every decision to send and the send itself -- on an
// acknowledgement's path, the last of three. libutp's send callback writes
// from inside the call that decided to send (utp_call_sendto,
// utp_internal.cpp:713). Conn.WriteTo is called concurrently from here, as
// net.PacketConn allows.
```

## Line 92 — func (*UtpSocket).writeDatagram

```
// writeDatagram sends one datagram, with fragmentation disabled if it is an
// MTU probe and the Conn can do that.
//
// Falling back to an ordinary write is deliberate, and is what makes this
// safe to add. A Conn with no don't-fragment support probes exactly as it did
// before -- the search still runs, it just cannot tell a dropped probe from a
// fragmented one on IPv4. Failing the send instead would turn a missing
// socket option into a stalled connection.
//
// Datagrams are written from several goroutines -- the reader, each
// connection's event loop -- so a Conn that sets the bit as a socket option
// around one send has that send to itself: writeMu is held exclusively for it
// and shared by every other write. A Conn that cannot set the bit takes no
// lock at all.
```
