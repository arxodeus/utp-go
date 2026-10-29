# teardown_race_test.go

Every comment in `teardown_race_test.go` at e56c9aa, verbatim.

## Line 14 — func TestTeardownRace

```
// Concurrent teardown, every way a connection can end, all at once.
//
// This exists for one recorded observation: a data race seen once, years of
// runs ago in this repository's terms, in an early -race pass of
// TestManyConcurrentTransfers, "under conditions where many connections were
// hitting the 60s idle timeout". The report was never captured and it has not
// reproduced since, so there is nothing to fix -- only a hypothesis to press
// on until it either produces a race or stops being worth suspecting.
//
// What that observation implies, and what this concentrates:
//
//   - many connections ending at once, not one at a time
//   - the *idle timeout* path specifically, which no other test drives in
//     bulk: there is no idle timeout by default, and the ones tests set are
//     long, so reaching it in bulk takes the short timeout set here
//   - teardown overlapping teardown -- a close racing an idle expiry racing a
//     cancelled context racing the socket going away underneath all of them
//
// Each cycle runs a batch of connections whose ends are deliberately mixed, so
// the socket's connection table, the shared retransmission wheel and the
// per-connection event loops are all being torn down from several directions
// at the same moment.
//
// Longer runs, which is the point of it:
//
//	UTP_SOAK_CYCLES=200 go test -race -run TestTeardownRace -timeout 60m
```

## Line 46 — func TestTeardownRace

```
// Per cycle. Enough that teardowns genuinely overlap rather than queue.
	//
	// Overridable because a race is a timing accident: running the same shape
	// repeatedly explores one interleaving landscape, and varying the
	// concurrency between runs explores several.
```

## Line 64 — func TestTeardownRace

```
// A fresh socket pair per cycle, so socket teardown is exercised too:
		// the sockets are closed below with connections still in flight.
```

## Line 81 — func TestTeardownRace

```
// The condition the original observation named. At the default of
			// 60s no test reaches this path in bulk.
```

## Line 104 — func TestTeardownRace

```
// Each connection gets its own context so mode 3 can cancel one
			// mid-flight without disturbing the others.
```

## Line 117 — func TestTeardownRace

```
// Abandoned: accepted, never read, then closed. This is
					// the path that used to deadlock Close against its own
					// event loop.
```

## Line 144 — func TestTeardownRace

```
// Silent peer: write nothing and never close, so the
					// connection ends on its idle timeout rather than a FIN.
```

## Line 150 — func TestTeardownRace

```
// Cancelled mid-transfer.
```

## Line 167 — func TestTeardownRace

```
// Close the sockets while connections are still winding down. This is
		// the overlap the observation pointed at: the socket's table and the
		// shared timer wheel going away underneath live event loops.
```

## Line 185 — func TestTeardownRace

```
// Every mode must actually have been exercised. A soak test that silently
	// stopped reaching the idle-timeout path would look like evidence and be
	// worth nothing.
```
