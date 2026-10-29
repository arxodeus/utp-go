# cid_routing_test.go

Every comment in `cid_routing_test.go` at e56c9aa, verbatim.

## Line 10 — func awaitSyn

```
// awaitSyn waits for our socket to emit a SYN on conn and returns it.
```

## Line 26 — func TestPacketReachesTheConnectionItIsFor

```
// A packet is delivered to the connection whose receive id it carries, and
// only to that one.
//
// A uTP packet carries the receiving end's id: each side sends with its send
// id, which is the other's receive id. The socket looked a packet up three
// ways in turn, the first of them as if the id were our *send* id. Two
// connections to the same peer whose ids are adjacent -- one receiving on R
// and sending on R+1, the next receiving on R+1 -- then share a number, and
// a packet for the second, carrying R+1, matched the first. Here that is the
// second connection's SYN-ACK: it went into the first, as an acknowledgement
// outside its window, and the second never connected. Found by the long soak,
// which drew such a pair once every few thousand connections; libutp looks a
// packet up by its receive id alone (utp_internal.cpp:2884-2892).
```

## Line 59 — func TestPacketReachesTheConnectionItIsFor

```
// The SYN carries our receive id; the answer comes back on it.
```

## Line 83

```
// Receive ids are unique per peer, whichever side opened the connection, so
// that a packet's id names one connection. Two rules keep them so, both
// libutp's.
```

## Line 87 — func TestSynForATakenReceiveIdIsRefused

```
// A SYN whose connection would receive on an id a connection we dialled
// already receives on is refused, without an answer: "rejected incoming
// connection, connection already exists" (utp_internal.cpp:2957-2965).
```

## Line 102 — func TestSynForATakenReceiveIdIsRefused

```
// The peer dials us with id recv-1: the connection would receive on recv.
```

## Line 121 — func TestDialledConnectionAvoidsAReceiveIdInUse

```
// A connection we dial does not take a receive id an accepted one already
// receives on. libutp draws until the (address, receive id) key is free
// (utp_internal.cpp:2533-2538); the check here was for the exact pair alone.
```

## Line 132 — func TestDialledConnectionAvoidsAReceiveIdInUse

```
// An accepted connection receiving on taken: its SYN carried taken-1.
```

## Line 143 — func TestDialledConnectionAvoidsAReceiveIdInUse

```
// The first draw is the taken id, the second a free one.
```

## Line 167 — func TestClosedConnectionIdsAreNotReusedAtOnce

```
// A connection that has just ended does not lend its ids to the next one to
// the same peer. Whatever was still in flight for it -- here, the RESET the
// peer sent -- would reach the new connection; in the long soak a RESET for
// the old connection refused the new one while it dialled.
```

## Line 190 — func TestClosedConnectionIdsAreNotReusedAtOnce

```
// The peer resets it, and it is gone.
```

## Line 223 — func TestDialledConnectionDoesNotSendOnAnIdInUse

```
// A dialled connection does not send on an id another connection to the
// peer receives on. A RESET is matched on the receive id first, as libutp
// matches it, so the first connection would take the second's RESETs: in the
// long soak, one dialling was refused by a RESET meant for one that sent on
// its receive id.
```

## Line 236 — func TestDialledConnectionDoesNotSendOnAnIdInUse

```
// A connection receiving on recv, sending on recv+1.
```

## Line 240 — func TestDialledConnectionDoesNotSendOnAnIdInUse

```
// The first draw would receive on recv-1 and send on recv.
```
