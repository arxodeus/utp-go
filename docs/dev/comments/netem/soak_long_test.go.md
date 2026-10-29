# netem/soak_long_test.go

Every comment in `netem/soak_long_test.go` at e56c9aa, verbatim.

## Line 24 — func soakDuration

```
// soakDuration is how long TestSoakLongRunning runs: UTP_SOAK_DURATION, or
// fifteen seconds, enough to exercise everything it checks.
```

## Line 37 — type streamReader

```
// streamReader adapts a stream to io.Reader for io.ReadFull.
```

## Line 45 — func soakServe

```
// soakServe answers every message on a connection -- a 4-byte length, then
// that many bytes -- with the message's SHA-256, until the peer ends its
// side.
```

## Line 70 — func soakExchange

```
// soakExchange sends one message of n bytes and checks the digest it gets
// back.
```

## Line 89 — func soakSize

```
// soakSize draws a message size: mostly small, as a peer's requests and
// control messages are, sometimes a piece-sized block.
```

## Line 124 — func TestSoakLongRunning

```
// TestSoakLongRunning runs the traffic a BitTorrent client lives with, for
// as long as it is told to, over a link that loses and reorders packets, and
// checks that nothing accumulates.
//
// Sixteen workers open a connection, exchange one message of random size and
// close it, over and over; four connections stay open throughout, sending a
// message and then going quiet for up to twenty seconds. Every message is
// answered with its SHA-256, so every byte is checked. The live heap,
// goroutines and tracked connections are sampled as it runs. At the end, with
// everything closed, there must be no connections left on either socket and
// no more goroutines than at the start, and the heap over the last half of
// the run must not exceed the first half's by more than the noise between
// samples allows. A leak of a few bytes per connection, which the seconds-long
// soaks cannot see, shows here within the hour: at the rate this churns, a
// hundred bytes per connection is tens of megabytes an hour.
//
// It runs for fifteen seconds by default. For a real soak:
//
//	UTP_SOAK_DURATION=4h go test ./netem/ -run TestSoakLongRunning -count=1 -timeout 5h -v
```

## Line 148 — func TestSoakLongRunning

```
// One sample a minute in a real soak, more often in a short one.
```

## Line 168 — func TestSoakLongRunning

```
// The link's per-packet queue samples are kept for its life, and
		// would be the soak's largest heap growth.
```

## Line 182 — func TestSoakLongRunning

```
// serverMetrics is each server connection's latest sample, for
		// reporting one that never ends.
```

## Line 192 — func TestSoakLongRunning

```
// The server accepts and serves until the run is over.
```

## Line 200 — func TestSoakLongRunning

```
// Only while it is being served: an entry for every
				// connection ever made would be the soak's own leak.
```

## Line 303 — func TestSoakLongRunning

```
// A server connection whose client has gone must still end: the
	// client's socket answers its keep-alive, every 29 seconds, with a RESET.
```

## Line 331 — func TestSoakLongRunning

```
// Every connection gone from both sockets; the closing side lingers a
	// moment for its FIN to be acknowledged.
```

## Line 367 — func TestSoakLongRunning

```
// Clean three-minute runs put the two medians within 1.6 MB of each
		// other, at 13-16 MB. What this resolves is growth of 4 MB plus a
		// quarter of the first half's median between the halves: over two
		// hours, about 880,000 connections, a leak of some 25 bytes each.
		// A run of seconds resolves nothing that small, and is not meant to.
```
