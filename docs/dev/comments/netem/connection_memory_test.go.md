# netem/connection_memory_test.go

Every comment in `netem/connection_memory_test.go` at e56c9aa, verbatim.

## Line 12 — func TestIdleConnectionMemory

```
// TestIdleConnectionMemory bounds what an open connection holds once it has
// carried a little data and gone quiet -- the state a BitTorrent client's
// peer connections spend most of their time in, and, with no idle timeout by
// default, what a vanished peer the application never closes costs.
//
// It was 1.08 MB per connection end: the receive buffer was allocated at its
// full capacity, 1 MB by default, when the connection was set up, and its
// event queue and the socket's held 1,000 and 1,000,000 slots apiece,
// allocated whole. Then about 23 KB, and 13.4 KB since the timer and write
// queues shrank, measured over 400 connection ends; libutp
// holds about 10 KB per connection, plus 67 KB per context (measured with
// mallinfo2 around 500 of its driver's connections that had exchanged 1 KB
// each way). The bound is loose on purpose: heap figures move with whatever
// else the runtime holds, and the defect it guards against is fifty times it.
//
// It is measured twice, over two sets of connections: within the keep-alive
// interval, at libutp's 29 seconds, and once connections given a short one
// have been quiet past it, when each has released its receive buffer's
// storage. The heap figure includes this test's own emulated network and
// bookkeeping, about 1.5 KB an end by a heap profile. The goroutine stacks
// are reported beside it, since libutp has no thread per connection and an
// honest comparison counts them; an idle connection holds none now, and
// this fails if it holds a goroutine (dormant.go).
```

## Line 38 — func TestIdleConnectionMemory

```
// The first set's connections end in the background once their sockets
	// close; measured from before they have, the second baseline holds
	// their teardown and the second figure reads low.
```

## Line 50 — func TestIdleConnectionMemory

```
// About 3 KB less in five separate runs, 2.8-3.3 KB, and 0.9 KB with the
	// release disabled. Not asserted: in one process the figures move with
	// what earlier runs left in pools and free lists, by more than that.
	// TestQuietConnectionReleasesReceiveStorage asserts the release itself.
```

## Line 56 — func idleConnectionMemory

```
// idleConnectionMemory sets up 200 connections that exchange 1 KB each, waits,
// and reports the heap each end holds. A keepAlive of zero is the default.
```

## Line 141 — func idleConnectionMemory

```
// An idle connection gives its goroutine back (dormant.go): libutp has no
	// thread per connection, and the parked goroutine's stack was 8 KB an
	// end. A few may still be on their way out when this looks.
```

## Line 152 — func stackInUse

```
// stackInUse is the process's goroutine stack memory, after a collection has
// shrunk what it can. Goroutines that have exited leave their stacks cached,
// counted here, so only the first measurement in a process is clean; the
// figure is reported, not asserted.
```

## Line 163 — const idleMemoryKeepAlive

```
// idleMemoryKeepAlive stands in for the 29-second keep-alive interval, after
// which a quiet connection releases its receive buffer's storage.
```
