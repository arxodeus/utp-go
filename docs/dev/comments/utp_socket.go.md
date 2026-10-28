# utp_socket.go

Every comment in `utp_socket.go` at e56c9aa, verbatim.

## Line 19 — const maxUdpPayloadSize, cidGenerationTryWarningCount, awaitingConnectionTime...

```
// rstInfoTimeout is how long a sent RESET is remembered, so a peer that
	// keeps sending packets for a connection we do not have receives one
	// RESET rather than one per packet. Matches libutp's RST_INFO_TIMEOUT
	// (utp_internal.cpp:71).
```

## Line 25 — const maxUdpPayloadSize, cidGenerationTryWarningCount, awaitingConnectionTime...

```
// lingerAckTimeout is how long a closed connection's acknowledgement is
	// kept so a retransmitted FIN can be answered with it. libutp's FIN
	// retransmissions back off from its 1000ms RTO floor, so this covers the
	// first few, which is where a lost acknowledgement costs something.
```

## Line 31 — const maxUdpPayloadSize, cidGenerationTryWarningCount, awaitingConnectionTime...

```
// idQuarantine is how long a closed connection's ids are kept from a new
	// connection to the same peer. See GenerateCid.
```

## Line 34 — const maxUdpPayloadSize, cidGenerationTryWarningCount, awaitingConnectionTime...

```
// rstInfoLimit is the number of remembered RESETs past which we stop
	// answering unknown packets at all. Matches libutp's RST_INFO_LIMIT
	// (utp_internal.cpp:72).
```

## Line 40 — type rstInfoKey

```
// rstInfoKey identifies one unanswerable packet, so a repeat of the same one
// is not answered twice. libutp keys on the same triple
// (utp_internal.cpp:2913-2917).
```

## Line 50 — var ErrAcceptTimedOut

```
// ErrAcceptTimedOut is returned by Accept/AcceptWithCid when no matching
	// SYN arrived within 20 seconds.
```

## Line 55 — type Conn

```
// Conn is the datagram transport a UtpSocket runs over.
//
// WriteTo must behave like a UDP socket's: it may wait for the local send
// path, but never for the peer to read. The socket writes from the goroutine
// that reads -- an acknowledgement leaves from the call stack that received
// the packet, as libutp's sendto callback does (utp_call_sendto,
// utp_internal.cpp:713) -- so a WriteTo that waited for the other side to
// read would let two sockets each wait on the other for ever. A transport
// that cannot keep up drops, as UDP does. WriteTo is called concurrently.
//
// WriteTo must not keep b once it returns: the socket encodes each packet into
// a buffer it reuses, as libutp's send callback is handed one it reuses
// (utp_call_sendto, utp_internal.cpp:713). net.PacketConn already works this
// way; a transport that queues a datagram must copy it.
//
// A Conn may also have the methods
//
//	HoldPeer(peer ConnectionPeer)
//	ReleasePeer(peer ConnectionPeer)
//
// and then the socket calls HoldPeer when it starts a connection with peer --
// a dial, as it sends its SYN, or a peer's SYN, as it is taken in -- and
// ReleasePeer when the socket forgets that connection, once it has ended. A
// transport that keeps something per peer, as utpnet keeps the address a
// datagram came from to write back to, keeps it while it is held: it is needed
// from the first packet of the handshake to the last of the close, which only
// the socket sees. Each HoldPeer is matched by one ReleasePeer, for a peer
// with as many connections as it is held. They are called with the socket's
// connection table locked, and must not call back into the socket.
```

## Line 90 — type peerHolder

```
// peerHolder is a Conn that is told which peers the socket has connections
// with. See Conn.
```

## Line 98 — type UtpSocket

```
// dispatchMu serialises the routing of incoming packets with everything
	// else that changes the routing tables: registering a new connection and
	// serving an Accept. The reader holds it for each datagram it routes. See
	// dispatch.
```

## Line 103 — type UtpSocket

```
// keyScratch is where the reader builds the keys it routes by. Used only
	// under dispatchMu.
```

## Line 106 — type UtpSocket

```
// connsGen counts changes to conns and inlineConns, each made with
	// connsMutex held for writing. lastRoute, used only under dispatchMu, is
	// the route the last datagram took, good while connsGen has not moved.
```

## Line 111 — type UtpSocket

```
// writeMu keeps an ordinary write from landing while an MTU probe has the
	// don't-fragment bit set on the socket. See writeDatagram.
```

## Line 114 — type UtpSocket

```
// inlineConns maps a connection's event channel to the connection, so
	// the reader can hand it a packet directly. Guarded by connsMutex.
```

## Line 120 — type UtpSocket

```
// clk is this socket's clock, handed to every connection it creates.
	// Nil means the real one.
```

## Line 125 — type UtpSocket

```
// holder is socket, if it is a peerHolder, and held the peer each entry
	// in conns holds it for. Guarded by connsMutex. See registerLocked.
```

## Line 136 — type UtpSocket

```
// retransmitTimers is one timer wheel for every connection on this
	// socket. Per-connection wheels cost a ticker each; see
	// retransmit_timer.go.
```

## Line 140 — type UtpSocket

```
// pendingAccepts holds Accept calls that named no connection id and are
	// waiting for any incoming SYN. Guarded by dispatchMu.
```

## Line 143 — type UtpSocket

```
// answered holds connections whose SYN was answered with no Accept
	// waiting, oldest first, until an Accept or AcceptWithCid takes one. Only
	// a socket with WithAcceptConfig answers so. Guarded by dispatchMu.
```

## Line 147 — type UtpSocket

```
// rstInfo remembers RESETs recently sent for connections we do not have.
```

## Line 150 — type UtpSocket

```
// ownsSocket is true when this UtpSocket created the underlying Conn
	// (via Bind) and is therefore responsible for closing it. A Conn handed
	// in through WithSocket belongs to the caller.
```

## Line 154 — type UtpSocket

```
// droppedFullConnQueue counts packets discarded because a connection's
	// event queue was full. libutp never discards a datagram it has already
	// received and parsed -- utp_process_udp runs synchronously, with no queue
	// between the socket and the connection -- so any non-zero value here is
	// this library losing work the peer will have to retransmit.
	//
	// It exists to be asserted on rather than to be read in production. It
	// used to be reached routinely, because a connection whose reader stopped
	// blocked its own event loop and stopped draining this queue; that is
	// fixed, and TestSlowReaderDoesNotWedgeConnection asserts this stays zero
	// across a transfer whose reader stalls.
```

## Line 166 — type UtpSocket

```
// lingerAcks holds, for a connection that has just closed after reaching
	// its peer's FIN, the acknowledgement to re-send if that FIN arrives
	// again. See connection.lingerAck.
```

## Line 171 — type UtpSocket

```
// recentlyClosed holds the keys of connections that ended in the last
	// idQuarantine. See GenerateCid.
```

## Line 175 — type UtpSocket

```
// resetsSent counts RESETs answered to packets for connections this
	// socket does not have. A RESET tells the peer to give up, so a healthy
	// connection that draws one has been killed by us.
```

## Line 179 — type UtpSocket

```
// refusedByFirewall counts SYNs the firewall callback rejected. It exists
	// so a test can tell "refused" from "lost".
```

## Line 183 — type UtpSocket

```
// lingering holds the streams the application has closed whose
	// connections are still delivering. Close waits for them. See linger.
```

## Line 185 — type UtpSocket

```
// *UtpStream -> struct{}
```

## Line 186 — type UtpSocket

```
// firewall, when set, is asked about every SYN for a connection this
	// socket does not already have, before any state is created for it.
	// Reporting true refuses the connection. Set once at construction and
	// only read afterwards.
```

## Line 191 — type UtpSocket

```
// acceptConfig is WithAcceptConfig's function, or nil.
```

## Line 193 — type UtpSocket

```
// maxConns is the incoming-connection cap: a new incoming connection is
	// refused while the socket already holds more than this many. Zero or
	// less means no cap. See WithMaxConnections.
```

## Line 197 — type UtpSocket

```
// refusedAtCapacity counts SYNs refused because the socket was at its
	// connection cap. It exists so a test can tell "refused" from "lost".
```

## Line 200 — type UtpSocket

```
// refusedIdInUse counts SYNs refused because a connection we dialled
	// already receives on the id the new one would. See handleIncomingBuf.
```

## Line 205 — const DefaultMaxConnections

```
// DefaultMaxConnections is libutp's cap on the sockets one context may hold
// before it refuses a new incoming connection (utp_internal.cpp:2967-2974).
```

## Line 209 — func WithMaxConnections

```
// WithMaxConnections caps the connections this socket holds before it refuses
// a new incoming one.
//
// libutp refuses a SYN when its context already holds more than 3000 sockets:
//
//	if (ctx->utp_sockets->GetCount() > 3000) {
//	    ...
//	    return 1;
//	}
//	                                        (utp_internal.cpp:2967-2974)
//
// This is the same check in the same place -- after the duplicate-connection
// lookup and before the firewall -- with the figure configurable, because 3000
// is a policy choice rather than a protocol rule. The comparison is libutp's
// too: a SYN is refused while the socket holds *more than* n, so it admits
// one past n, exactly as libutp admits a 3001st socket.
//
// What is counted is libutp's count. Every connection this socket has, dialled
// or accepted, established or still closing, plus every SYN parked waiting for
// Accept -- the state libutp holds as a socket in CS_SYN_RECV.
//
// A refusal is silent, as libutp's `return 1` is. A RESET would tell the peer
// something is listening, and at capacity there is nothing to offer it.
//
// n == 0 means DefaultMaxConnections, which is also what a socket gets without
// this option. n < 0 means no cap.
```

## Line 248 — func (*UtpSocket).ConnectionsRefusedAtCapacity

```
// ConnectionsRefusedAtCapacity reports how many incoming connections were
// refused because the socket was at its connection cap. See
// WithMaxConnections.
```

## Line 255 — func (*UtpSocket).ConnectionsRefusedIdInUse

```
// ConnectionsRefusedIdInUse reports how many incoming connections were
// refused because a connection this socket dialled to the same peer already
// receives on the id the new one would have. libutp refuses these too.
```

## Line 262 — func (*UtpSocket).connectionCount

```
// connectionCount is what the incoming-connection cap is measured against:
// libutp's `utp_sockets->GetCount()`. See WithMaxConnections.
```

## Line 268 — type SocketOption

```
// SocketOption configures a UtpSocket at construction.
```

## Line 271 — func WithFirewall

```
// WithFirewall refuses incoming connections the callback rejects.
//
// It is asked about a SYN for a connection this socket does not already have,
// before any state exists for it, and reporting true drops the packet without
// answering. That is libutp's UTP_ON_FIREWALL, in the same position and with
// the same meaning -- "true means yes, block connection"
// (utp_internal.cpp:2975-2982), reached after the duplicate-connection check
// and before the socket is created, and refusing by returning rather than by
// sending a RESET.
//
// A BitTorrent client uses it for an IP blocklist: the point of doing it here
// rather than after Accept is that a refused peer costs no connection state
// and gets no reply to confirm anything is listening.
//
// The callback runs on the socket's receive path. It must not block, and must
// not call back into the socket.
```

## Line 291 — func WithAcceptConfig

```
// WithAcceptConfig gives the socket a configuration of its own for incoming
// connections, read as each one is made, and with it libutp's way of
// accepting: a SYN is answered as it arrives, whether or not an Accept is
// waiting, and the connection is held until an Accept or AcceptWithCid takes
// it. libutp creates the socket and sends the SYN-ACK in utp_process_udp
// (utp_internal.cpp:2984-2990) with the context's options as they stand
// (:2616-2621), and hands the connection to its embedder.
//
// The function's configuration is used for every connection the socket
// answers before an Accept asks, and for one accepted with a nil config. An
// Accept or AcceptWithCid given a config of its own still applies it to a
// connection whose SYN arrives while it waits; one that takes a connection
// already answered gets that connection as it was made.
//
// Without it, the configuration is each Accept's, so a SYN that arrives
// before its Accept is parked, unanswered, until an Accept claims it or
// 20 seconds pass: the connection cannot be made before
// the call that says how. DEVIATIONS.md, "A SYN is answered when an Accept
// takes it, unless the socket has a configuration of its own".
//
// The answered connections count against WithMaxConnections, as libutp's
// do against its cap, and are held, like libutp's, for as long as the
// application leaves them.
```

## Line 318 — func (*UtpSocket).configForAccept

```
// configForAccept is the configuration an accepted connection gets.
```

## Line 330 — func (*UtpSocket).clock

```
// clock is this socket's clock, never nil.
```

## Line 338 — func WithClock

```
// WithClock runs this socket's retransmission wheel on the given clock, and
// makes it the default for every connection the socket creates.
//
// The wheel is shared by every connection on a socket, so the clock has to be
// chosen here rather than per connection: two connections on one socket
// cannot disagree about when a tick happened.
//
// Nothing outside a test should need this. See Clock.
```

## Line 358 — const DefaultSocketBufferSize

```
// DefaultSocketBufferSize is the size requested for the underlying UDP
// socket's send and receive buffers.
//
// A single readLoop goroutine drains the socket, so anything the kernel drops
// before it gets there is invisible packet loss that uTP then has to recover
// from with retransmits. At the OS default (~208 KiB on Linux) a few hundred
// concurrent connections overflow the receive queue and handshakes start
// failing outright. The kernel silently clamps this to net.core.rmem_max, so
// requesting more than the system allows is harmless.
```

## Line 369 — func Bind

```
// Bind opens a UDP socket on addr and runs a uTP socket on it, which owns it
// and closes it with itself. logger receives the socket's logging; nil
// discards it. LevelTrace is its per-packet level.
```

## Line 377 — func Bind

```
// Best effort: a smaller buffer costs throughput but is not fatal.
```

## Line 389 — func WithSocket

```
// WithSocket runs a uTP socket on socket, which stays the caller's. logger
// receives the socket's logging; nil discards it. LevelTrace is its
// per-packet level.
```

## Line 399 — func WithSocket

```
// Tell the caller. Dropping the request silently left AcceptWithCid
		// blocked on accReq.stream forever whenever the caller's own context
		// had no deadline.
```

## Line 456 — func WithSocket

```
// Applied before the loops start, so nothing can observe a half-built
	// socket.
```

## Line 468 — func (*UtpSocket).PacketsDroppedFullConnQueue

```
// LocalAddr returns the address the underlying socket is bound to, or nil if
// it cannot report one.
//
// A caller that binds to port 0 -- which is the usual thing to do -- has no
// other way to learn which port the kernel chose.
// PacketsDroppedFullConnQueue reports how many received, decoded packets this
// socket discarded because the destination connection's event queue was full.
//
// It should be zero. libutp has no equivalent: it processes each datagram
// synchronously, so there is no queue between receiving and processing and
// nothing to overflow. A non-zero value means this library threw away work the
// peer must now retransmit, and is a bug rather than a tuning signal.
```

## Line 484 — func (*UtpSocket).ConnectionsRefusedByFirewall

```
// PacketsResetSent reports how many RESETs this socket answered to packets for
// connections it does not have.
// ConnectionsRefusedByFirewall reports how many incoming connections the
// firewall callback rejected.
```

## Line 508 — func (*UtpSocket).Close

```
// Close shuts the socket down. It is idempotent: `defer sock.Close()`
// alongside an explicit Close is ordinary Go, and the second call used to
// panic closing an already-closed channel.
//
// It first waits for the streams already closed to finish delivering what was
// written to them, for as long as each one's peer is still answering
// (awaitLingering); everything else is ended at once.
```

## Line 526 — func (*UtpSocket).Close

```
// Close the underlying socket when we opened it. Without this the UDP
		// port stayed bound for the life of the process and readLoop stayed
		// parked in ReadFrom, which cancelling the context does not interrupt
		// -- an fd and goroutine leak, and the reason two runs of the same
		// test in one process failed with "address already in use".
```

## Line 539 — func (*UtpSocket).linger

```
// linger keeps a closed stream's connection going until it has delivered
// what was queued and ended, and lets Close wait for it.
//
// UtpStream.Close returns at once, as libutp's utp_close does
// (utp_internal.cpp:3358-3380): the FIN is queued and the connection goes on
// sending in the background. libutp leaves the rest to its embedder, which
// must keep the context alive and pumping until its sockets have finished;
// destroying the context throws away whatever they still held. Here closing
// the socket is that moment, and it waits instead (awaitLingering), so that
// writing, closing the stream, closing the socket and exiting delivers every
// byte.
```

## Line 559 — func (*UtpSocket).awaitLingering

```
// awaitLingering waits for every closed stream's connection to finish
// delivering, while its peer is still answering: a connection whose peer has
// been silent for closeStallTimeout is waited for no longer, and the socket's
// cancellation then ends it. See UtpStream.waitForFlush.
```

## Line 577 — func (*UtpSocket).wakeConns

```
// wakeConns starts the event loop of every resting connection on the socket.
// See dormant.go.
```

## Line 592 — func (*UtpSocket).sendShutdownEventToConns

```
// Snapshot the channels rather than sending under the lock: a blocking
	// send to one wedged connection would otherwise hold connsMutex and stall
	// packet delivery for every other connection on this socket.
```

## Line 608 — func (*UtpSocket).sendShutdownEventToConns

```
// The connection's queue is full; it is already being torn down
			// by the cancelled socket context.
```

## Line 617 — func (*UtpSocket).configForPeer

```
// getConnStreamForQuotedId finds the connection that sent the packet an ICMP
// message quoted.
//
// The quoted packet is one of ours, so its connection id is our *send* id --
// except for a SYN, which carries the sender's own receive id. libutp covers
// both with three lookups against a map keyed on the receive id alone
// (utp_internal.cpp:3056-3058):
//
//	Lookup(UTPSocketKey(addr, id))
//	Lookup(UTPSocketKey(addr, id + 1)) && conn_id_send == id
//	Lookup(UTPSocketKey(addr, id - 1)) && conn_id_send == id
//
// The first catches a quoted SYN -- the ICMP case that matters most, since an
// unreachable host answers the SYN and nothing else. The second and third
// catch an established connection's data, from the acceptor and the initiator
// side respectively.
//
// This socket's map is keyed on the pair, so libutp's first lookup, which
// leaves the send id free, becomes two candidates: an initiator's send id is
// its receive id plus one and an acceptor's is minus one, so those are the
// only two connections libutp's map could have held under that key. The order
// is otherwise libutp's, which is what decides it when a peer holds several
// connections whose ids happen to be adjacent.
// configForPeer returns the connection config to use for a peer, with the
// path-MTU ceiling lowered when this socket's Conn can say what the local
// path carries.
//
// The caller's config is never modified: a ConnectionConfig belongs to
// whoever built it and may be reused across connections, so this copies
// before changing anything. See pathMTUCeiling.
```

## Line 648 — func (*UtpSocket).configForPeer

```
// Nil is the defaults, for a dial as for an accept (configForAccept). It
	// was passed on as it was, and a Connect or ConnectWithCid given nil
	// panicked making the connection.
```

## Line 654 — func (*UtpSocket).configForPeer

```
// A socket-wide clock reaches its connections here, so a caller that
	// configured one does not have to remember to set it on every config it
	// hands to Connect or Accept. An explicit per-connection Clock still
	// wins: this only fills a gap.
```
