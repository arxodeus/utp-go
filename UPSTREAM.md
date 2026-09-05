# What to send back to zen-eth/utp-go

Almost everything in this fork so far is a straight bug fix with no
BitTorrent-specific slant, and it would be poor form to keep it here silently.
Upstream is built for the Portal Network, where uTP runs over discv5 rather
than raw UDP, but none of these defects are specific to either transport.

Suggested as **eleven separate pull requests**, smallest and least arguable
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
PR 11, so send that first or quote the figures.

## PR 4 — RESET rate limiting

A socket answered every packet addressed to a connection it did not have,
unconditionally: a 300-connection run emitted over 3000 RESETs. That is an
amplification vector as well as an incompatibility -- a peer that keeps
sending to a torn-down connection draws one RESET per packet.

libutp keys on `(connection id, address, seq nr)`, stays quiet for repeats
within `RST_INFO_TIMEOUT`, and stops answering entirely past
`RST_INFO_LIMIT` stored entries. This implements the same policy, citing
`utp_internal.cpp` line numbers throughout.

Small, self-contained, and matches the reference implementation exactly, so
it should be among the easier ones to land.

## PR 5 — the give-up rule

An established connection retransmitted forever. Nothing limited consecutive
timeouts, so the only thing that could end a connection whose peer had stopped
acking was the idle timer -- which any inbound packet resets. libutp kills the
connection at four consecutive timeouts, resetting the count on any ack
(`utp_internal.cpp:1191` and `:1398`).

Worth reviewing carefully rather than taking on trust: libutp has a single
connection-wide RTO, while this implementation arms one timer per outstanding
packet, so the counter has to sit inside the existing timeout-amplification
guard. Counting raw callbacks kills healthy connections under load.

Also changes the default `MaxConnAttempts` from 6 to 3, which is what libutp's
two-timeout limit in `CS_SYN_SENT` works out to.

## PR 6 — zero-length ST_DATA, SYN backoff, and a context leak

Three small conformance fixes, each independently checkable against
`utp_internal.cpp`:

- **A zero-length `ST_DATA` was rejected by the decoder**, so it never reached
  the connection, was never acked, and the peer retransmitted it forever.
  libutp accepts and acks one (`utp_internal.cpp:2342-2355`).
- **SYN retransmission backed off by `InitialTimeout * 1.5^attempts`**, which
  grows from the initial value rather than the current one. libutp doubles the
  current timeout each time, starting at 3000 ms (`:1179`, `:1203`, `:2762`).
- **`ConnectWithCid` ignored its own context**: a bare channel receive with no
  context arm, so cancelling during setup blocked the caller forever.
  `Connect` already had the arm.

## PR 7 — the RTT estimator and RTO

Builds on PR 2, which fixed the unit errors that made the estimate track
nothing. This makes the arithmetic match libutp exactly
(`utp_internal.cpp:1362-1380`):

- the first RTT sample is adopted outright (`rtt = ertt`, `rtt_var = ertt/2`)
  rather than eased up from zero over about twenty samples;
- the RTO floor is 1000 ms, not 500 ms;
- initial `rtt_var` is 800 ms, matching `utp_internal.cpp:2610`.

On an emulated 2% loss path this moved recovery entirely onto duplicate-ack
fast retransmit -- zero RTO-driven timeouts, against one before -- which is
the healthier path.

## PR 8 — Accept, and Close

Two defects on paths nothing in the repository exercised, both found by
pointing a real libutp peer at this implementation:

- **`Accept` could not accept.** The cid-less path -- the only one usable
  against a peer that picks its own connection id -- polled once for an
  already-arrived SYN and failed with "no incoming conn" if none had come yet,
  so the ordinary server pattern could never work. Behind that,
  `awaitConnected` dereferenced the accept's connection id, which is nil on
  that path, and panicked once the first fix let it get that far.
- **`Close` took up to the idle timeout.** It set its shutdown flag and waited
  on the event loop, but the loop blocks in a select that the flag is not part
  of, so it only noticed when an unrelated packet or timer arrived. Measured
  at 29 seconds; now 0. It sends the `streamShutdown` event the socket already
  uses. For a client that opens and closes connections constantly this is the
  more consequential of the two.

## PR 9 — the selective-ack bit order

A one-line wire-format fix, and the most consequential single change in this
fork.

The selective-ack bitmask was built least-significant-bit-last: the first
entry went to bit 7 of each byte where BEP 29 and libutp put it at bit 0
(`utp_internal.cpp:806-818`). The decoder reversed it the same way, so the
implementation agreed with itself and nothing Go-to-Go could detect it --
but every selective ack exchanged with a real peer was misread in both
directions.

Only shows up under loss, and only against another implementation. Worth
sending on its own so it is not lost in a larger diff.

## PR 10 — the test suite

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
- `native/cgo` needed a C library that was not vendored, so `go build ./...`
  failed for the whole module. Removed rather than fixed: despite its name it
  wrapped the Rust `ethereum/utp` FFI, not libutp, and could never have tested
  interoperability with the C library. See PR 10.

Also in this PR: profiling scaffolding removed from tests, `-race`-aware time
budgets, and `UTP_TEST_TRANSFERS` to run the concurrency test where 1000
simultaneous transfers do not fit in memory.

## PR 11 — the harnesses: emulated network, libutp interop, conformance corpus

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

PR 1, PR 4, PR 6, PR 8, PR 9 and PR 10 are close to unarguable and should go first.

PR 2 and PR 3 both need a conversation, for the same reason: any tuning or
measurement upstream has done was taken against a controller with a constant
delay signal and a timer that fired at random within a one-second window.
Both will move once these land, and that is the point, but it should not be a
surprise.

PR 5 needs review of the per-packet-timer subtlety above. PR 11 is optional for upstream and the most invasive; offer it last.
