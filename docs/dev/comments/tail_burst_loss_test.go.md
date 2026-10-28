# tail_burst_loss_test.go

Every comment in `tail_burst_loss_test.go` at e56c9aa, verbatim.

## Line 9 — func tailBurstRecovery

```
// tailBurstRecovery runs one connection, ours sending, on a virtual clock
// against a scripted peer with a fixed round trip, and loses every packet of
// one burst but its last -- the shape a queue overflow leaves at the end of a
// transfer: too few packets after the holes for a selective ack to trigger
// fast retransmission. It returns the virtual time from the burst leaving to
// the peer holding all of it, and how many packets the burst lost.
```

## Line 60 — func tailBurstRecovery

```
// The peer: what it holds, its cumulative acknowledgement, and the one
	// packet past the burst it was given.
```

## Line 69 — func tailBurstRecovery

```
// A one-way delay of half the round trip: without a delay sample
		// LEDBAT leaves the window where it is, in libutp as here.
```

## Line 90 — func tailBurstRecovery

```
// Grow the window: every packet arrives, one round trip later.
```

## Line 110 — func tailBurstRecovery

```
// The burst is out, all lost but its last. From here every packet sent
	// arrives, and is acknowledged a round trip after it left; between
	// packets the clock moves in small steps, so a timer fires when it is due.
```

## Line 151 — func TestTailBurstLossRecoversNoSlowerThanLibutp

```
// A burst lost at the tail of what is in flight is recovered no slower than
// libutp would recover it. The bound here is libutp's rule worked from its
// source; TestTailBurstLossAgainstLibutp measures libutp itself in the same
// scenario: 1.75 s for 24 packets, against 1.3 s here, and 5.3 s here before
// the probe's acknowledgement started recovery.
//
// libutp has no loss probe. With too few packets past the holes for fast
// retransmission, it waits for its retransmission timeout -- at least a
// second (utp_internal.cpp:1380) -- which marks everything outstanding lost,
// and then resends one packet per acknowledgement, the fast-timeout retry
// (:2256-2282): the timeout plus about a round trip per hole.
//
// This library's loss probe resends the oldest packet first, after twice the
// smoothed round trip. Its acknowledgement retired that packet, which
// restarted the timeout, and re-armed the probe -- so the timeout never fired,
// and the burst came back one packet per probe timeout. In the 1000-transfer
// stress test, with round-trip estimates inflated by load, that was one packet
// every 4.3 seconds for 13 holes, and transfers took minutes
// (TestManyConcurrentTransfers past its budget about one run in four).
```
