# socket_routing.go

Every comment in `socket_routing.go` at e56c9aa, verbatim.

## Line 22 — func (*UtpSocket).registerLocked

```
// registerLocked enters a connection to peer in the table under key, and,
// the first time key is entered, tells a transport that keeps something per
// peer that this one is in use (Conn, HoldPeer). Called with connsMutex held
// for writing.
```

## Line 38 — func (*UtpSocket).attachInline

```
// attachInline lets the reader hand packets on ch straight to c. Called
// with dispatchMu held, after c is set up.
```

## Line 43 — func (*UtpSocket).attachInline

```
// A connection that has already gone, its entry removed, stays gone.
```

## Line 47 — func (*UtpSocket).attachInline

```
// From here the connection may rest: nothing the socket hands it
		// goes to a loop that is not there. See dormant.go.
```

## Line 53 — func (*UtpSocket).route

```
// route finds the connection a packet is for: the first of the derivations
// in cidTypes whose key the socket has, and its inline connection. Called by
// the reader, with dispatchMu held. The keys are built into a reused buffer
// and looked up under one hold of connsMutex; the conversion in the map index
// does not allocate.
```

## Line 59 — func (*UtpSocket).route

```
// The datagrams of one flow arrive one after another, and the routing
	// tables rarely change: the last route stands while nothing has been
	// added or removed. A SYN always takes the full path, which has its own
	// rule for it.
```

## Line 78 — type cachedRoute

```
// cachedRoute is the route one datagram took: its connection id and peer,
// what they led to, and the routing tables' generation at the time. See
// UtpSocket.route.
```

## Line 90 — func (*UtpSocket).routeLocked

```
// routeLocked is route's lookup proper, through every derivation in order.
```

## Line 104 — func routeCandidates

```
// routeCandidates is the connections, as (send, recv) id pairs, that a packet
// can be for, in the order they are tried. libutp's lookups, exactly:
//
//   - A SYN carries the sender's receive id, ours to send to, and is matched
//     only as a retransmission of one already taken: receive id one above it
//     (utp_internal.cpp:2957). Any other derivation would alias a SYN onto
//     whatever local connection holds that number.
//   - A RESET may carry either of our ids, so it is matched on the receive
//     id first and then on the send id (:2856-2859).
//   - Anything else carries our receive id and is matched on it alone
//     (:2884-2892). Receive ids are unique per peer (generateCid, and the
//     SYN check in handleIncomingBuf), so at most one connection matches:
//     ours if we dialled (send one above), the peer's if we accepted (send
//     one below).
//
// Trying a packet's id as our send id as well would let two connections to
// one peer with adjacent ids share a number, and a packet for one reach the
// other (TestPacketReachesTheConnectionItIsFor). History: HISTORY.md,
// "routeCandidates".
```

## Line 142 — func (*UtpSocket).idInUseLocked

```
// idInUseLocked reports whether a connection to the peer, live or ended in
// the last idQuarantine, sends or receives on id. Called with connsMutex
// held.
```

## Line 159 — func (*UtpSocket).recvIdTakenLocked

```
// recvIdTakenLocked reports whether a connection to the peer already receives
// on recv, whichever side opened it. Called with connsMutex held.
```

## Line 221 — func cidIds

```
// cidIds is cidFromPacket's send and receive ids, without the ConnectionId.
```

## Line 228 — func cidIds

```
// wrapping add
```

## Line 229 — func cidIds

```
// State, Data, Fin, reset
```

## Line 230 — func cidIds

```
// wrapping sub
```

## Line 235 — func cidIds

```
// wrapping add
```

## Line 240 — func cidIds

```
// wrapping sub
```
