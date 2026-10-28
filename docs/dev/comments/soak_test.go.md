# soak_test.go

Every comment in `soak_test.go` at e56c9aa, verbatim.

## Line 16

```
// Soak tests: the defects that only appear after something has been running
// for a while.
//
// Everything else in this repository measures a transfer or a packet. These
// measure what is left behind afterwards -- goroutines, map entries, heap --
// because a library that leaks a goroutine per connection works perfectly in
// every test and dies in production after a day.
//
// Longer runs:
//
//	UTP_SOAK_CYCLES=2000 go test -run TestSoak -timeout 30m
```

## Line 37 — func settleGoroutines

```
// settleGoroutines waits for the goroutine count to stop falling, so a
// measurement is not taken while teardown is still in flight.
```

## Line 60 — func TestSoakConnectionChurn

```
// A connection opened and closed leaves nothing behind. Repeatedly.
//
// This is the shape of leak that matters most for the target use: a
// BitTorrent client opens and drops peer connections continuously for days.
```

## Line 93 — func TestSoakConnectionChurn

```
// A few cycles first, so one-off allocations (buffers, timer wheels) are
	// not counted as growth.
```

## Line 167 — func TestSoakConnectionChurn

```
// Split by socket, because which side leaks says what the leak is: the
	// side that closes second is a teardown that never fires, the side that
	// closes first is one that fires and does not clean up. Chasing a
	// half-close regression here, the split was the difference between a guess
	// and a diagnosis.
```

## Line 179 — func TestSoakConnectionChurn

```
// A goroutine per connection would show as `cycles` extra goroutines.
	// The allowance is for the runtime's own workers and the sockets' timer
	// wheels, not for anything that scales with the number of connections.
```

## Line 193 — func TestSoakAbandonedConnections

```
// The same for connections that are abandoned rather than closed: the peer
// vanishes and nothing is ever read or written again. A client dealing with
// real peers gets these constantly.
```

## Line 213 — func TestSoakAbandonedConnections

```
// Every one of these is a connection attempt to a port with nothing on
	// it. Each should give up and clean up on its own.
```

## Line 236 — func TestSoakUnknownConnectionFlood

```
// A socket answering packets for connections it does not have must not
// accumulate state for them. This is the amplification surface: a peer that
// sends to torn-down connections should cost the socket a bounded amount.
```

## Line 278 — func TestSoakUnknownConnectionFlood

```
// The RESET rate limiting is what should hold this down: libutp remembers
	// what it has already answered and stops entirely past a limit
	// (utp_internal.cpp:2907-2945). Without it a peer draws one RESET per
	// packet, which is both an amplification vector and unbounded state.
```
