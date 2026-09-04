# What to send back to zen-eth/utp-go

Almost everything in this fork so far is a straight bug fix with no
BitTorrent-specific slant, and it would be poor form to keep it here silently.
Upstream is built for the Portal Network, where uTP runs over discv5 rather
than raw UDP, but none of these defects are specific to either transport.

Suggested as **five separate pull requests**, smallest and least arguable
first, so none of them is held up by the others.

## PR 1 — the transfer-path hangs

Highest value, no API change, no judgement calls.

| Fix | File |
| --- | --- |
| Acceptor answers a retransmitted SYN by re-sending the SYN-ACK. Nothing else retransmits it, so one lost SYN-ACK stranded the connection until the initiator gave up. | `conn.go` |
| Deliver the end-of-stream marker on every close path. The `if !c.eof()` guard was inverted in effect — `eof()` is true by definition once state is `ConnClosed` — so readers on any connection that closed by idle timeout, RESET or local close blocked in `ReadToEOF` forever. | `conn.go` |
| `UtpSocket.Close()` made idempotent; `timeWheel.stop()` likewise. The second call panicked closing a closed channel. | `utp_socket.go`, `delay_set.go` |
| `UtpSocket.Close()` closes the socket it opened. It previously leaked the fd and left `readLoop` parked in `ReadFrom`, which a cancelled context does not interrupt. | `utp_socket.go` |
| Expired accepts report `ErrAcceptTimedOut` instead of being dropped silently; both accept paths propagate `StreamResult.err`, which they previously discarded. | `utp_socket.go` |
| `ConnectionId.Hash()` computes the hash when the cached one is empty. The type is exported with exported fields, so a struct literal — which upstream's own tests use — hashed to `""` and collided with every other literal-built id. | `cid.go` |
| Time wheel dispatches expiries with its lock released. The callback hands a packet to the connection event loop, and that loop calls `put`/`remove`, which need the same lock. | `delay_set.go` |
| Socket shutdown fan-out snapshots the channel list instead of sending under `connsMutex`. | `utp_socket.go` |
| Accepts served before incoming packets in the socket event loop. The non-blocking pre-drain of `incomingBuf` starved accepts completely under load. | `utp_socket.go` |

Plus the regression tests in `integrated/regression_test.go`, which name the
defect each one covers.

## PR 2 — the timestamp difference

This one deserves its own PR and its own discussion, because it changes what
the congestion controller sees rather than just fixing a hang.

`onPacket` computed the peer timestamp difference from the wrong header field,
subtracting a `uint32` wire value from a full-width `int64` epoch clock. Every
packet upstream sends currently advertises a constant `1,000,000` µs delay, so
every peer's LEDBAT controller is fed a constant and performs no delay-based
control at all. The acceptor's SYN path has the same `int64`/`uint32` mixing.

Fixed as a `uint32` wrapping subtraction of the packet's timestamp from the
current time, matching libutp's `reply_micro`, via a named
`wrappingSubUint32` helper with wrap-boundary tests. `DurationBetween` is also
corrected — it was off by one on the wrapping branch and returned a
nanosecond-typed `Duration` holding a microsecond count.

Worth flagging to upstream directly rather than only as a PR: **anyone who has
benchmarked upstream's congestion control has measured a controller with a
constant input.** Evidence and before/after loopback numbers are in
[KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md).

## PR 3 — the retransmission timer

The largest single performance defect found so far, and entirely
transport-agnostic: it costs Portal Network users exactly what it costs
BitTorrent users.

Each connection owned a timer wheel built with `interval = InitialTimeout/4`
-- one second -- while the RTO is 500 ms. A one-second tick cannot represent
500 ms: the timer landed on the next tick, uniformly 0-1000 ms away, so
packets were declared lost before their ack could arrive. On a link dropping
nothing, about 6% of packets were retransmitted.

The change makes the wheel socket-wide at 25 ms resolution, rounds delays up
so a timer never fires early, adds a rounds counter so long delays are
scheduled instead of clamped (which also un-caps RTO backoff), and makes
removal O(1). Sharing is what makes the finer resolution affordable: a 25 ms
wheel per connection would mean 40,000 timer wake-ups per second at a thousand
connections.

Measured: 6.18% to 0.00% spurious retransmits, +53% goodput on an emulated
8 Mbps / 40 ms path, +36% on the loopback large-transfer test, and no
measurable cost at a thousand concurrent connections.

Depends on nothing else in this fork, but the numbers come from the harness in
PR 5, so send that first or quote the figures.

## PR 4 — the test suite

Independent of the library. On a clean checkout upstream's suite cannot pass:

- `TestUdpTransfer` never called the library — it blasted raw datagrams
  between two `net.UDPConn`s, and its send loop never advanced `offset`, so it
  could only ever end at the timeout. Rewritten to transfer over uTP and
  verify the payload.
- `TestSocketReportsTwoConns` hung forever, on the `ConnectionId` defect above.
- `TestCloseErrorsIfAllPacketsDropped` gave `Close()` exactly the idle timeout
  it waits on.
- `TestManyConcurrentTransfers` and `TestManyTimeHugeData` both registered
  `/debug/fgprof` on `http.DefaultServeMux`, so `go test ./integrated/`
  panicked on a duplicate registration.
- `native/cgo` needs a C library that is not vendored, so `go build ./...`
  failed for the whole module. Now behind the `utp_cgo_harness` build tag.

Also in this PR: profiling scaffolding removed from tests, `-race`-aware time
budgets, and `UTP_TEST_TRANSFERS` to run the concurrency test where 1000
simultaneous transfers do not fit in memory.

## PR 5 — the network harness

`netem/`, the in-process emulated network, plus the `Controller.Stats()` and
`ConnectionConfig.Metrics` hooks it reads. Upstream has no way to measure
congestion behaviour at all today, and both defects above were invisible
without it.

This is the largest and least urgent of the five. It is also the one upstream
may reasonably want to shape differently, since it adds a package and two
interface methods.

## Not for upstream

- `DefaultSocketBufferSize` and the `Bind` buffer sizing — defensible, but it
  is a policy choice about a socket upstream may not consider its own. Offer
  it, do not assume it. Recorded in [DEVIATIONS.md](DEVIATIONS.md).
- `REFERENCE.md` and `scripts/check-libutp-reference.sh` — useful to upstream,
  but they exist to serve a libutp-conformance goal upstream has not signed up
  for. Mention rather than push.

## Order of operations

PR 1 and PR 4 are close to unarguable and should go first.

PR 2 and PR 3 both need a conversation, for the same reason: any tuning or
measurement upstream has done was taken against a controller with a constant
delay signal and a timer that fired at random within a one-second window.
Both will move once these land, and that is the point, but it should not be a
surprise.

PR 5 is optional for upstream and the most invasive. Offer it last.
