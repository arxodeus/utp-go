# route_cache_test.go

Every comment in `route_cache_test.go` at e56c9aa, verbatim.

## Line 9 — func TestRouteCacheFollowsTheTables

```
// The reader remembers the last datagram's route. Every change to the routing
// tables must invalidate it: a connection registered, removed, or attached for
// inline handling. A stale route would hand a removed connection's packets to
// it, or keep sending a connection's packets through its channel after it was
// attached.
```

## Line 58 — func TestRouteCacheFollowsTheTables

```
// Registered over a route still remembered: a key reused before the
	// old entry's removal reached the tables.
```

## Line 75 — func TestReceiverRetiresTheSynTimer

```
// An initiator's SYN keeps its retransmission timer until an acknowledgement
// after the handshake retires it. On a connection that only receives, every
// such acknowledgement names the SYN and nothing new -- the case the
// acknowledgement fast path takes -- so the fast path must still retire it.
```
