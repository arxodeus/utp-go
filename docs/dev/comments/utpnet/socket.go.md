# utpnet/socket.go

Every comment in `utpnet/socket.go` at e56c9aa, verbatim.

## Line 1 — package utpnet

```
// Package utpnet presents a uTP socket as the standard library's net types.
//
// The core package speaks in its own vocabulary -- ConnectionPeer, Stream,
// contexts on every call -- which is right for a library that has to work
// over an emulated network as easily as a real one. Everything that wants to
// *use* uTP wants net.Conn and net.PacketConn instead.
//
// This package is the translation. Its Socket satisfies:
//
//	net.PacketConn
//	Accept() (net.Conn, error)
//	Addr() net.Addr
//	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
//
// which is exactly the interface github.com/anacrolix/torrent requires of a
// uTP implementation (its unexported `utpSocket`, in utp.go). Go interfaces
// are structural, so satisfying it needs no import of that module and this
// package does not have one; integration/anacrolix holds a separate module
// that compiles this against the real interface, so the claim is checked
// rather than asserted.
//
// # Sharing a UDP port
//
// A BitTorrent client runs uTP and the DHT on one UDP port. So this Socket
// does not consume every datagram: it inspects each one, hands uTP packets to
// the uTP machinery, and leaves everything else to be returned by ReadFrom.
// That is what makes it a net.PacketConn rather than merely a listener, and
// it is why the type exists at all instead of a few helper functions.
```

## Line 44 — var ErrSocketClosed

```
// ErrSocketClosed is returned by operations on a closed Socket.
```

## Line 47 — const maxDatagram

```
// maxDatagram is the largest datagram the read loop will accept. A uTP packet
// never approaches this; the ceiling is for the non-uTP traffic sharing the
// port, which this package does not get to choose the size of.
```

## Line 52 — const passthroughDepth

```
// passthroughDepth is how many non-uTP datagrams may be buffered for ReadFrom
// before the read loop starts dropping them.
//
// Dropping is correct rather than regrettable: this is a datagram socket, the
// caller is expected to lose packets, and blocking here would stall uTP
// traffic behind a slow DHT reader on the same port.
```

## Line 60 — type udpPacketConn

```
// udpPacketConn is what a Socket uses of its UDP socket. *net.UDPConn is the
// only implementation outside tests; the interface exists so that tests can
// run a Socket over an in-memory network whose addresses are IPv6, on a host
// whose kernel has no IPv6 at all (utpnet's IPv6 tests, ipv6_test.go).
```

## Line 76 — type Socket

```
// Socket is a uTP endpoint on a UDP port, presented as net types.
```

## Line 79 — type Socket

```
// raw is the UDP socket under udp, when it is one, for WriteTo's
	// attempt that never waits (sendNow).
```

## Line 82 — type Socket

```
// passUDP is what the passthrough writer sends on: udp, kept apart so
	// that nothing else reads it.
```

## Line 92 — type Socket

```
// config is used for every stream accepted or dialled from now on; see
	// SetConnectionConfig.
```

## Line 96 — type Socket

```
// generic is the adapter when the Socket was made on a net.PacketConn
	// that is not a UDP socket (NewSocketFromPacketConn), and nil otherwise.
```

## Line 106 — type Socket

```
// closed ends what the application is waiting in -- Accept, ReadFrom,
	// the dials -- at the start of Close. appCtx ends with it, for Accept.
```

## Line 111 — type Socket

```
// stopRead ends the reader, once the uTP socket has finished with it.
```

## Line 113 — type Socket

```
// readErr is the read error that closed the socket, if one did.
```

## Line 115 — type Socket

```
// passOut is what WriteTo hands its writer, started on the first.
```

## Line 123 — type datagram

```
// pooled is payload's buffer if it came from datagramPool, to go back
	// once ReadFrom has copied it out.
```

## Line 127 — type datagram

```
// native is the address the datagram came from as a PacketConn of the
	// Socket's own gave it (packetConn), or nil.
```

## Line 132 — type Options

```
// Options configures a Socket.
```

## Line 134 — type Options

```
// Logger receives the uTP socket's logging. A nil Logger discards it.
```

## Line 136 — type Options

```
// ConnectionConfig is applied to every stream this socket accepts or
	// dials. A nil value uses utp.NewConnectionConfig().
	//
	// This is where a BitTorrent client selects LEDBAT++ -- see
	// BENCHMARKS.md for why it should.
```

## Line 142 — type Options

```
// Firewall, when set, is asked about every incoming connection before any
	// state is created for it. Returning true refuses it: the packet is
	// dropped without an answer, so a refused peer learns nothing.
	//
	// This is libutp's UTP_ON_FIREWALL. A BitTorrent client uses it for an IP
	// blocklist, which is why torrent's own uTP constructor takes one.
	//
	// It runs on the socket's receive path: it must not block, and must not
	// call back into the socket.
```

## Line 152 — type Options

```
// OnWriteError, when set, hears of every datagram the uTP machinery
	// failed to send, with the address it was for. The machinery carries on
	// regardless -- a lost datagram is retransmitted like any other -- so
	// this is for a caller that wants to give up sooner on an error it
	// knows will not clear, or to count them. go-libutp's sendto callback
	// draws the same line.
	//
	// It is called on the sending goroutine: it must not block, and must not
	// call back into the socket.
```

## Line 162 — type Options

```
// MaxConnections caps the connections this socket holds before it
	// refuses a new incoming one, silently, as libutp does past 3000
	// (utp_internal.cpp:2967-2974). Zero means utp.DefaultMaxConnections,
	// libutp's 3000; a negative value means no cap. See
	// utp.WithMaxConnections for exactly what is counted.
```

## Line 170 — func Listen

```
// Listen binds a UDP port and returns a uTP socket on it.
//
// network is "udp", "udp4" or "udp6"; addr is a host:port as net.ResolveUDPAddr
// accepts, and an empty port asks the kernel to choose one.
```

## Line 189 — func NewSocket

```
// NewSocket wraps an already-bound UDP connection.
//
// The Socket takes ownership: closing it closes conn. This is the entry point
// for a caller that needs to set socket options, bind with a particular
// control function, or hand over a socket it obtained some other way.
```

## Line 214 — func newSocket

```
// Best effort, as in utp.Bind: a smaller buffer costs throughput but is
	// not fatal.
```

## Line 247 — func newSocket

```
// The caller was given a net.Addr signature, because that is what
			// it already has a blocklist keyed by. Peers this package creates
			// carry the address; anything else is refused rather than let
			// through unchecked, since a firewall that fails open is worse
			// than no firewall.
```

## Line 270 — func (*Socket).run

```
// run is the single reader of the UDP socket. Every datagram goes to exactly
// one of two places: the uTP machinery, or the passthrough queue that
// ReadFrom drains.
```

## Line 283 — func (*Socket).run

```
// On a PacketConn of the Socket's own, the address the datagram
		// came from is kept as it was given, to go with the datagram: it
		// waits in a queue before anything answers it, and the map of such
		// addresses could forget it meanwhile (review of 99ce302).
```

## Line 305 — func (*Socket).run

```
// A read error on a socket nobody closed is terminal, and the
			// socket closes with it. It used to stop reading and nothing
			// else: Accept and ReadFrom waited for ever, and every
			// connection went on retransmitting into a socket that could no
			// longer hear the answer. From another goroutine, because Close
			// waits for this one. Release review of 9c44c4f (R9).
```

## Line 326 — func (*Socket).run

```
// The caller is not draining ReadFrom. Drop, as a UDP socket
			// does, rather than stalling uTP behind it.
```

## Line 333 — const pooledDatagram

```
// pooledDatagram is the size of datagramPool's buffers: room for any uTP
// packet, which libutp keeps under 1402 bytes and this library under its
// own ceiling, and for most of what shares a port with it. A larger datagram
// gets a buffer of its own.
```

## Line 339 — var datagramPool

```
// datagramPool holds the buffers each datagram is copied into between the
// socket's reader and whoever takes it. Both consumers copy it out at once
// -- the uTP socket into its own read buffer, ReadFrom into its caller's --
// so a buffer is free again as soon as it has been taken, and copying every
// datagram into a new one was most of what a go-libutp transfer allocated:
// 39% of the bytes in compat/golibutp's 100 MB benchmark.
```

## Line 350 — func copyDatagram

```
// copyDatagram copies b out of the read buffer, into a pooled buffer if it
// fits one, and returns the pooled buffer to release, or nil.
```

## Line 362 — func releaseDatagram

```
// releaseDatagram returns a pooled buffer once its datagram has been copied
// out. It must not be used after.
```

## Line 370 — func isUtpPacket

```
// isUtpPacket reports whether a datagram is a uTP packet for this
// implementation, by the packet decoder's own header validation (utp.IsPacket):
// the sniff cannot drift away from what the socket will actually accept.
```

## Line 377 — func (*Socket).ReadFrom

```
// ReadFrom returns the next datagram that was not a uTP packet.
//
// This is what makes a Socket usable on a port shared with another protocol:
// a BitTorrent client's DHT traffic arrives here while its uTP traffic is
// handled by Accept and DialContext.
```

## Line 402 — func (*Socket).ReadFrom

```
// The deadline may have been moved rather than reached; the loop
			// re-checks it. stop() here too: the next pass starts its own
			// waiter, and this one's goroutine has already served its purpose.
```

## Line 410 — func (*Socket).WriteTo

```
// WriteTo sends a datagram, unchanged, to addr.
//
// It is the outbound half of sharing the port: a caller's DHT replies go out
// through the same socket the uTP connections use.
//
// The write deadline applies for as long as the write waits, and moving it
// moves the wait, as net.PacketConn's contract says. The datagram is handed
// to one writer goroutine (see passthroughWriter), so the wait can end
// without the write: the port is shared with the uTP connections, and a
// deadline set on the port itself would end their writes too. A datagram
// whose deadline passes before the writer reaches it is not sent; one the
// writer is already sending may still be, as one already given to the kernel
// may. p is copied, so the caller has it back on return either way.
//
// On a UDP socket a datagram is first offered to the kernel once, without
// waiting (sendNow, on Unix): the common case, at the cost of a direct
// write. Only one the kernel cannot take at once goes to the writer, so a
// blocked write is always one the deadline can end, including a deadline
// set after it began. It used to write directly whenever no deadline was
// set, and a deadline set during such a write waited for the kernel. Review
// of 294cc2e, finding 2. Elsewhere every datagram is handed over: the
// request is pooled, and there is no timer or goroutine while no deadline
// is set (BenchmarkWriteTo).
```

## Line 439 — func (*Socket).WriteTo

```
// expired rather than wait for the common case: it needs only the
	// answer, and wait starts a goroutine.
```

## Line 450 — func (*Socket).WriteTo

```
// What the kernel takes at once needs no hand-over and no wait.
```

## Line 461 — func (*Socket).WriteTo

```
// No timer and no goroutine while there is no deadline: the changed
		// channel alone says when one is set.
```

## Line 469 — func (*Socket).WriteTo

```
// Withdrawn if the writer has not reached it. If it has, it
				// may yet be sent, as a datagram given to the kernel may be.
```

## Line 493 — func (*Socket).WriteTo

```
// The writer is done with it, so it can be used again.
```

## Line 504 — func (*Socket).WriteTo

```
// The deadline passed or moved: the loop re-reads it.
```

## Line 515 — var passWritePool

```
// passWritePool holds WriteTo's requests, each with its result channel and a
// buffer grown to the datagrams it has carried. One goes back only once its
// result has been taken: a request given up on may still be in the writer's
// hands.
```

## Line 526 — type passWrite

```
// passWrite is one WriteTo waiting for the writer.
```

## Line 533 — type passWrite

```
// done has room for the result, so the writer never waits on a caller
	// that has gone.
```

## Line 545 — func (*passWrite).cancel

```
// cancel withdraws a write the writer has not started.
```

## Line 552 — func (*Socket).passthroughWriter

```
// passthroughWriter sends what WriteTo queues, one datagram at a time, until
// the socket closes. One goroutine, so a blocked write holds up the writes
// behind it and nothing more.
```

## Line 568 — func (*Socket).passthroughWriter

```
// The caller's address goes to the PacketConn as it is: it
				// is of the PacketConn's own kind, or one it can write to.
```

## Line 581 — func (*Socket).Accept

```
// Accept waits for the next incoming uTP connection.
```

## Line 583 — func (*Socket).Accept

```
// A nil config: the connection takes s.config as it stands when its
	// SYN arrives (utp.WithAcceptConfig), not as it stood now.
```

## Line 597 — func (*Socket).DialContext

```
// DialContext opens a uTP connection to addr.
//
// network is accepted for interface compatibility and must name a UDP
// network; addr is a host:port.
```

## Line 617 — func (*Socket).DialAddr

```
// DialAddr opens a uTP connection to an address already resolved. It takes
// any address with a host and port; on a Socket made on a PacketConn of its
// own (NewSocketFromPacketConn), that is how to dial an address of the
// PacketConn's own type, which DialContext's resolver does not know.
```

## Line 630 — func (*Socket).DialAddr

```
// Kept from here, not from the uTP socket's first SYN: the address
		// is remembered now, and churn before the socket holds it
		// (demuxConn.HoldPeer) could forget it again.
```

## Line 650 — func (*Socket).AcceptBacklog

```
// AcceptBacklog reports how many incoming connections are waiting for an
// Accept to take them.
```

## Line 656 — func (*Socket).SetConnectionConfig

```
// SetConnectionConfig replaces the configuration given to streams accepted
// or dialled from now on; streams already open keep theirs. A nil config is
// utp.NewConnectionConfig(). The config is not copied, and must not be
// changed once handed over.
```

## Line 667 — func (*Socket).ConnectionConfig

```
// ConnectionConfig is the configuration streams accepted or dialled now get.
```

## Line 672 — func (*Socket).nativeAddr

```
// nativeAddr is a UDP address as the caller knows it: on a Socket made on a
// PacketConn of its own, the address of that PacketConn's type it came from.
```

## Line 681 — func (*Socket).Dial

```
// Dial is DialContext with a background context.
```

## Line 686 — func (*Socket).ProcessICMPFragmentation

```
// Addr returns the local address, satisfying net.Listener.
// ProcessICMPFragmentation reports an ICMP "fragmentation needed" message
// (IPv4 type 3 code 4, or ICMPv6 "packet too big") for a datagram this socket
// sent, so the path-MTU search can take the router's word for it.
//
// quoted is the uTP datagram the ICMP message quoted -- the original UDP
// payload, not the ICMP packet -- and addr is where that datagram was sent.
// nextHopMTU is the router's figure, which may be zero when it did not supply
// one. It reports whether the quoted bytes matched a live connection.
//
// Nothing in this package collects ICMP: on Linux the usual source is
// IP_RECVERR on the UDP socket this Socket was built from, and reading it is
// the embedder's job. libutp draws the same line.
```

## Line 706 — func (*Socket).ProcessICMPError

```
// ProcessICMPError reports an ICMP message that should tear a connection
// down: destination unreachable other than fragmentation-needed, time
// exceeded, parameter problem. Which messages are fatal is the caller's
// judgement, as it is in libutp.
//
// quoted and addr mean what they mean for ProcessICMPFragmentation, and the
// return value likewise.
```

## Line 722 — func (*Socket).LocalAddr

```
// LocalAddr returns the local address, satisfying net.PacketConn.
```

## Line 725 — func (*Socket).SetDeadline

```
// SetDeadline sets both the read and write deadlines for the packet-oriented
// side of this socket. It does not affect established uTP connections, which
// carry their own deadlines.
```

## Line 734 — func (*Socket).SetReadDeadline

```
// SetReadDeadline bounds how long ReadFrom will wait.
```

## Line 740 — func (*Socket).SetWriteDeadline

```
// SetWriteDeadline sets the deadline for WriteTo, including a WriteTo already
// waiting. It does not apply to the uTP connections' own writes, which share
// the port. See WriteTo.
```

## Line 748 — func (*Socket).Close

```
// Close shuts down the socket, every connection on it, and the underlying UDP
// port. It is idempotent.
//
// Connections already closed are given the time to deliver what they hold,
// as utp.Socket.Close gives them, and the port goes on being read while
// they do: their acknowledgements arrive on it. The reader used to stop
// first, so a Write followed by Close and Socket.Close lost whatever was
// still behind the congestion window. Release review of 9c44c4f (R2).
```

## Line 761 — func (*Socket).shutdown

```
// shutdown is Close, and the close a terminal read error causes.
```

## Line 767 — func (*Socket).shutdown

```
// What the application waits in ends now.
```

## Line 771 — func (*Socket).shutdown

```
// Nothing can be delivered without a receive path: end the
			// connections rather than wait for acknowledgements.
```

## Line 781 — func (*Socket).shutdown

```
// See NewSocketFromPacketConn: closing it is what stops the
			// reader, and waiting first could wait for ever.
```

## Line 791 — func (*Socket).closedErr

```
// closedErr is what an operation on a closed socket returns: ErrSocketClosed,
// wrapping the read error that closed it if one did.
```

## Line 805 — func udpNetwork

```
// anacrolix/torrent names its networks "utp", "utp4" and "utp6".
```

## Line 831 — type timeoutError

```
// timeoutError is what net expects a deadline to produce.
```
