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

## PR 9b — header validation, and the RESET for an unknown connection

Small and independent of PR 9, though they touch the same file.

- The version nibble was never checked. A packet claiming any version was
  parsed as version 1. libutp drops anything that is not 1
  (`utp_internal.cpp:2481`, `:2834`).
- The first extension byte was never checked. Any value was accepted and
  treated as "no extension" unless it was 1. libutp requires `ext < 3`
  (`utp_internal.cpp:2481`).
- The RESET sent for a packet addressed to a connection the socket does not
  have carried `ack_nr = 0` and advertised a 100000-byte window. libutp's
  `send_rst` sets `ack_nr` to the rejected packet's sequence number and
  `windowsize` to zero (`utp_internal.cpp:846-863`).

All three are the "more permissive or less informative than the reference"
direction. None of them can be triggered by a well-behaved peer, which is why
nothing caught them; all three were found by comparing against libutp on
malformed input.

## PR 9c — the selective-ack window

The bitfield had no width limit: it grew until every pending packet was
covered, `MAX_SELECTIVE_ACK_COUNT = 32 * 63`, so up to a 252-byte extension.
libutp scans a fixed 30-entry window and writes exactly four bytes
(`utp_internal.cpp:797`, `:805-818`).

A peer that leaves a gap open and then delivers a packet far past it made this
fork answer every subsequent packet with a 254-byte extension where libutp
answers with six bytes -- on the ack path, exactly when the path is already in
trouble.

Measured on a deterministic 2%-loss emulated link: goodput 1.82 -> 1.84 Mbps,
packets sent 451 -> 436, fast retransmits 56 either way. No recovery lost.

Also in this PR: the selective ack is suppressed once the peer's FIN has been
reached, as libutp does (`utp_internal.cpp:786-788`).

## PR 9d — loss recovery

The largest throughput defect found so far, and four separate causes on one
path: what happens after a selective ack reveals a hole.

- No `fast_resend_seq_nr`. A packet declared lost was fast retransmitted on
  every subsequent ack until its own ack arrived, and the window was halved
  again each time. libutp advances the counter past each resent packet
  (`utp_internal.cpp:1603`) and refuses to resend below it (`:1537`, `:1560`).
  Measured: 52 fast retransmissions for 7 distinct lost packets, one packet
  sent 16 extra times.
- No cap on retransmissions per ack. libutp resends at most four
  (`utp_internal.cpp:1605-1606`).
- The congestion window was halved once per lost packet, with no rate limit.
  libutp halves at most once per 100 ms (`MAX_WINDOW_DECAY`,
  `utp_internal.cpp:51`, `:602-605`) and once per ack, not once per packet
  (`:1609-1610`).
- A selective ack whose `ack_nr` equalled the connection's initial sequence
  number was discarded whole. That is exactly the case where the first data
  packet was lost, so that loss was never detected and recovery waited for the
  RTO.

On a deterministic 2%-loss emulated link: goodput 1.88 -> 2.78 Mbps, elapsed
1.118 -> 0.754 s, retransmit rate 12.84% -> 2.03% against a link that drops
2%, time spent with the window pinned at its floor 11.2% -> 1.6%. The
loss-free path is unchanged.

This one needs review rather than a rubber stamp: it changes when the window
decays, and any tuning upstream has done was taken against the old behaviour.

## PR 12 — the congestion controller, against libutp's apply_ccontrol

Seven defects, and the largest throughput change in the fork: +42% to +80%
goodput depending on the link, with queueing delay flat or lower and Jain
fairness unchanged. Full tables in BENCHMARKS.md.

- No slow start at all. libutp starts every connection in it
  (`utp_internal.cpp:2620-2621`, `:1691-1702`), re-enters on a timeout
  (`:1227`) and leaves on any window decay (`:616-617`).
- The window factor divided by bytes **in flight** rather than by the
  congestion window (`utp_internal.cpp:1668`), and had no min/max pair. With
  one packet outstanding the factor reached 1 and a single ack claimed a whole
  RTT of increase.
- `MAX_CWND_INCREASE_BYTES_PER_RTT` was the packet size, 1024, against
  libutp's 3000 (`utp_internal.cpp:43`).
- The queueing delay was never clamped to the RTT (`utp_internal.cpp:1621`),
  so a peer reporting a wild timestamp collapsed the window in one ack.
- No `last_maxed_out_window`: a window the application never filled grew
  anyway (`utp_internal.cpp:1681-1686`).
- No ceiling on the window (`utp_internal.cpp:1710`). The `WindowSize` config
  field existed and was never read.
- A retransmission timeout collapsed the window to its floor even when nothing
  was in flight, where libutp decays to two thirds because an idle connection
  has not actually lost anything (`utp_internal.cpp:1216-1228`).

This is the PR that most needs a conversation rather than a review. Any tuning
upstream has done was taken against a controller with these defects, and the
numbers will move.

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



## PR 13 — LEDBAT++

New file, opt-in behind `ConnectionConfig.CongestionAlgorithm`, default
unchanged. Implements draft-irtf-iccrg-ledbat-plus-plus-01: modified slow
start (§4.1), a gain that scales with the base RTT (§4.2), multiplicative
decrease (§4.3), periodic slowdowns (§4.4) and a 60 ms target (§4.5).

Worth sending because of what measuring it revealed about the existing
controller: on a sustained transfer over a 40 ms path, classic LEDBAT leaves
33 ms of standing queue and reports causing 500µs, because its base-delay
estimate has drifted up to include its own queue. LEDBAT++ leaves 1.55 ms.

Comes with the standing-queue metric that shows it (see "Not for upstream"
below on the harness), the latecomer experiment, and two documented readings
of ambiguous parts of the draft.

The result upstream should see first is the deference one. Over a shared
bottleneck against a loss-based competitor, classic LEDBAT takes 60% of the
link and leaves 25ms of queue; LEDBAT++ takes 44% and leaves 2.5ms. libutp's
LEDBAT is not less-than-best-effort, and every throughput number in this fork
that favours it favours it for that reason.

## PR 14 — the send buffer retained the caller's slice

One line, and the most damaging defect found in the fork.

`sendBuffer.Write` stored the caller's `[]byte` by reference, and
`UtpStream.Write` returns before those bytes are transmitted. Any caller that
reuses its buffer between writes -- which `io.Writer`'s contract explicitly
permits, and `io.Copy` and `bufio.Writer` do -- had its queued data
overwritten.

Only reachable in full-duplex use, which is why the whole suite passed: every
test in the repository is unidirectional. Reproduces in a sixteen-line echo
loop.

## PR 15 — UtpSocket.Connect, and two API gaps

- `Connect` reported every successful connection as "connection timed out".
  The initiator signalled success by closing `connectedCh`, and `Connect`
  checked the receive's `ok` flag. `ConnectWithCid` used the bare receive form
  and so worked -- and every test uses `ConnectWithCid`.
- `UtpStream.Read`, an incremental read. `ReadToEOF` only returns when the
  whole transfer is done, so nothing could treat a uTP stream as an
  `io.Reader`.
- `UtpSocket.LocalAddr`, so a socket bound to port 0 can report its port.

## PR 16 — the net adapter

`utpnet`, presenting a uTP socket as `net.PacketConn` and its streams as
`net.Conn`, including passing through datagrams that are not uTP so the port
can be shared with a DHT. Satisfies the interface `anacrolix/torrent`
requires; `integration/anacrolix` is a separate module that checks that
against the real interface by reflection rather than against a copy of it.

Optional for upstream, but it is what makes the library usable by anything
that expects standard library types, and it is what found PRs 14 and 15.

## PR 17 — a leaked connection table entry, and an encoder inconsistency

Both found by tests of a kind this repository did not have.

- The socket was told to forget a connection only from the branch of the
  event loop where the state machine reached `ConnClosed`. A connection torn
  down by a cancelled context left its table entry forever: 40 attempts to a
  dead port left 40 tracked connections. Goroutines were unaffected, which is
  why goroutine-counting caught nothing.
- `packet.Encode` took the extension byte from the header rather than from
  what it was writing, so a decoded packet carrying an extension this library
  discards re-encoded into a packet this library rejects.

Comes with the fuzz targets and soak tests that found them, plus the two
minimised regression inputs.

## PR 18 — two hardening rules from libutp, found by differential fuzzing

Both are checks libutp performs and this fork did not, and both are in the
direction that matters: we answered packets the reference drops.

- **No reorder-window bound.** libutp drops any packet more than 1024
  sequence numbers past the next expected one, and re-acks (without
  processing) one up to 1024 behind (`utp_internal.cpp:54`, `:1890`). We
  buffered anything a peer named, anywhere in the 16-bit space, and answered
  it: unbounded pending state driven by a remote party, plus one emitted
  packet per junk packet.
- **No ack_nr validation.** libutp drops any packet acking outside a short
  window ending at the last sequence number it sent, and says why in the
  source: "a spoofed address or a malicious attempt to attach the uTP
  implementation" (`utp_internal.cpp:1794-1807`). Order matters -- a packet
  rejected here must not first draw a re-ack from the reorder check.

Comes with `FuzzDifferentialResponder`, which found them: the M2 conformance
harness and the M8 fuzzer wired together, so libutp decides whether the answer
to a generated packet sequence was right. Neither half could find these alone.

## PR 19 — a SYN could be delivered into an established connection

The socket derives three candidate connection ids for every incoming packet
and delivers to whichever matches. Two of the three treat the packet's id as a
*send* id, which is right for an established connection and wrong for a SYN --
a SYN carries the sender's own receive id, so those derivations alias it onto
whatever local connection holds that number. A SYN whose id equalled an
outgoing connection's receive id was delivered into that connection, where
onSyn answered it with a RESET.

libutp has exactly one lookup per case and rejects a SYN that matches an
existing socket outright (`utp_internal.cpp:2957-2965`). The spurious RESET is
the symptom; what matters is that an off-path attacker who guessed a
connection id could otherwise inject a SYN into an established connection.

Found by the differential fuzzer's initiator role, which models a malicious
server answering our SYN -- the direction a client is exposed to on every dial.

## PR 20 — the retransmission schedule, measured against libutp

Two defects in the retransmission timers, both found by measuring libutp's
schedule on its virtual clock and comparing ours against it.

- **The backoff doubled every other timeout.** The guard reconciling this
  fork's per-packet timers with libutp's single connection-wide RTO compared
  the time since the last timeout against the current RTO, which after a
  doubling is never satisfied by the next expiry. Two retransmissions per RTO
  value instead of one. The connection now keeps its own deadline, as libutp
  does (`rto_timeout`, utp_internal.cpp:494, :994-998, :1147-1148, :1204,
  :1388-1389).
- **The timer wheel could fire a full interval early.** An item due in n ticks
  was placed n-1 slots ahead, which is only right when the wheel has just
  started. Armed mid-cycle -- which is every timer in a live connection -- a
  20ms timer fired at 5.4ms. Early is the wrong direction: it resends a packet
  the peer was still going to acknowledge.

Comes with conformance_timing_test.go, which measures libutp's schedule on
every run rather than hard-coding it, and asserts both that the multiples
match and that our base timeouts equal libutp's -- which together mean the
absolute schedules coincide. Also a wheel test that arms mid-cycle; the
existing one armed at the single moment when the off-by-one is harmless, and
passed throughout.

## PR 21 — the send path: a zero-window deadlock and a missing keep-alive

Two things libutp does that this fork did not, both found by auditing the send
path against `utp_internal.cpp`.

- **No zero-window probe.** A peer advertising a zero receive window stops
  this sender, and nothing restarts it. The peer's window update is a single
  packet on an unreliable path; lost, it leaves the sender with nothing
  outstanding -- so no retransmission timer -- and no reason to transmit. The
  connection stops dead with data queued until the idle timeout. libutp arms a
  timer on a zero-window ack and forces one packet through when it expires
  (`utp_internal.cpp:2149-2151`, `:1142-1145`). Implemented, with libutp's
  15-second default.
- **No keep-alive.** libutp sends one after 29 seconds of silence
  (`utp_internal.cpp:74`, `:1271-1274`), acking one behind so the peer answers
  without consuming a sequence number. 29 seconds sits under the 30-second UDP
  mapping timeout common in NATs. This fork sent nothing, and against another
  copy of itself neither side ever spoke first.

Both intervals are configurable, defaulting to libutp's, on the same grounds
as the retransmission timeouts this library already exposes: so they can be
tested in less than half a minute.

Not included, deliberately: raising the packet size from 1024 towards libutp's
~1400. Measured at +2.4% on a long transfer and +17% on a reordering profile,
within noise elsewhere -- but a 1420-byte datagram is dropped on any path below
a 1500-byte MTU, and nothing here detects that. That belongs with MTU
discovery, not with a larger constant.

## PR 22 — path-MTU discovery, and the packet size it makes safe

libutp sizes packets from the interface MTU less the header, discovered by
probing, landing around 1400 bytes. This fork used a fixed 1024, and that was
the right call without discovery: a datagram above the path's MTU is
fragmented, or on IPv6 silently dropped, and a connection that picks too large
a size and cannot detect it does not run slowly, it stops.

`mtu.go` implements libutp's search -- a binary search between a 576-byte floor
and a ceiling, each probe an ordinary data packet at the midpoint, the floor
rising when one is acknowledged and the ceiling dropping when one is lost, with
the whole thing redone every 30 minutes (`mtu_search_update`,
utp_internal.cpp:1289-1312; `mtu_reset`, :1314-1322; the probe decision in
send_packet, :890-925; acknowledged, :1969-1974; the two failure paths, :1152-1167
and :1927-1940).

The ceiling then rose from 1024 to 1400, which is safe precisely because the
ceiling is no longer what gets sent: the search starts at the midpoint, 988
bytes -- *below* the old 1024 -- and grows only once a probe of that size has
been acknowledged. An untested path gets a smaller packet than before. A path
that drops every probe settles on 576. That argument is asserted as a test,
not left as prose.

Measured against the fixed 1024: +2.1% long transfer, +11% reordering, +2.6%
two flows, flat elsewhere.

Not included, and stated in KNOWN-LIMITATIONS.md: probes do not carry the
don't-fragment bit, because this library writes through an abstract Conn with
nowhere to put it. On IPv6 an oversized probe is dropped and the search learns
correctly; on IPv4 it is fragmented and acknowledged, so the search settles on
a size that works but costs fragmentation. And the ceiling is a fixed 1400
rather than the interface MTU, so a jumbo-frame path is not found.

Since done: the don't-fragment bit is PR 48, and the interface MTU as a ceiling
is PR 40. The 1400 cap remains, as a recorded deviation.

## PR 23 — deferred acks

Upstream sends one STATE packet for every ST_DATA packet received. libutp
sends one per batch: `schedule_ack()` sets a flag
(`utp_internal.cpp:2377`) and the embedder flushes it once after draining a
batch of datagrams (`utp.h:512-517`, `utp_internal.cpp:3796-3808`).

`connection.ackPending` and `flushAck()` do the same, driven from the end of
the event-loop pass rather than from an embedder call, with the priority drain
extended to pull up to 64 further queued events into the same pass.

The count cannot match libutp's exactly, and the reason is worth stating
because it is structural: libutp's embedder hands it a whole batch of datagrams
before the flush, where our packets cross three goroutines, so a pass covers
however many happen to have arrived. The saving is therefore load-dependent.
Measured on a 512 KB transfer over the emulated network, acks per data packet:
upstream 1.000 at every rate; this 0.82 at 20 Mbps, 0.37 at 100 Mbps, 0.17 at
1 Gbps. Two tests pin it — one against real libutp for the structural bound,
one under load for the ratio.

A FIN is excluded and acked immediately — libutp acks it directly at
`:2369-2370`, and deferring it emits nothing at all, because the connection is
torn down in the same pass and `statePacket()` then returns nil.

No throughput claim attaches to this. Re-running the benchmark suite put every
profile inside its own run-to-run range; the change removes return-path
packets, and none of those profiles is ack-limited. It is worth upstreaming
because it matches the reference and costs nothing, not because it is faster.

## PR 24 — libutp over the emulated network, and two harness defects

Only relevant to upstream if it takes PR 11 (the harnesses). It puts real
libutp on one end of an emulated link, so congestion-control changes can be
measured against the reference instead of against the previous commit.

Two defects had to be fixed first, and both are worth knowing about
independently, because both produced plausible tables rather than obvious
failures:

- The driver's virtual clock started at zero while this library stamps packets
  with `time.Now().UnixMicro()`. uTP peers exchange timestamps and subtract, so
  libutp read the epoch offset as its queueing delay and its LEDBAT controller
  collapsed the window. On a 100ms path that meant two packets per second; on
  short paths it merely looked slow.
- Neither `driver.cpp` nor `bridge.cpp` registered `UTP_GET_UDP_MTU`, so
  `utp_call_get_udp_mtu` returned 0, `mtu_ceiling` was 0 against a floor of 576,
  and libutp's MTU search settled on 288-byte packets and could never converge
  (the `mtu_ceiling - mtu_floor <= 16` test underflows). Fixing it moved the
  interop numbers from ~430/~374 Mbps to ~600/~430.

The comparison itself says our classic LEDBAT is more aggressive than libutp's
— 34% faster on broadband, 41% under 1% loss, with our sender against libutp's
receiver as the control. That is a finding about this fork, not a win, and it
belongs in any conversation about upstreaming PR 12.

## PR 25 — the event loop blocked on the application

`processReads` handed bytes to the reader with a blocking channel send, so an
application that stopped reading stopped its connection's event loop: no acks,
no window updates, no retransmissions. libutp cannot reach that state --
`utp_call_on_read` returns immediately and backpressure is the advertised
receive window, fed by `utp_call_get_read_buffer_size`. It also deadlocked
`Close()` against a consumer that never read.

The drain now stops when the read queue is full, leaving the rest in `RecvBuf`,
whose `Available()` is what we advertise -- so the window closes instead of the
loop stopping.

Three further changes are load-bearing, and each one is a silent data loss or a
deadlock if it is left out:

- End of stream is announced only once the receive buffer is drained. `eof()`
  means the bytes *arrived*, not that the reader has them; without this the
  transfer truncates (measured: 409308 of 524288 bytes against libutp).
- The end-of-stream marker is best-effort and the event loop closes `c.reads`
  on exit, so a reader that never received the marker still learns, with the
  reason carried in `terminalErr`.
- The teardown drain waits for room, escaping on the connection context or on
  the consumer having closed the stream. Without that second escape the
  deadlock returns.

Take it with the tests. `netem.TestEventLoopRunsWhileReaderIsStalled` measures
the event loop directly through the metrics callback, which runs on the loop's
own goroutine, and fails 5 of 5 against the unfixed code with identical numbers.
`netem.TestCloseWithoutReadingDoesNotHang` fails 3 of 3.

## PR 26 — an early timer callback resent a packet and reset its backoff

The retransmission wheel counts ticks, and never fires an item early in ticks.
Wall-clock time is a different matter: under scheduling pressure its goroutine
is descheduled past a tick and processes the pending one immediately on resume,
so eight ticks can elapse in slightly under eight intervals. Measured: a 200ms
timer delivered at 198.6ms.

`onTimeout` already compared the clock against `rtoDeadline` before backing
off, as libutp does (`current_ms - rto_timeout >= 0`, utp_internal.cpp:1147-1148).
The resend below it was not guarded, so an early callback sent the packet again
and re-armed at the undoubled RTO, and the schedule ran 1, 2, 4, 8, 16 times
the floor instead of 1, 3, 7, 15.

An early callback with one packet outstanding now waits out the remainder. The
restriction matters: with several packets outstanding they share an expiry and
the siblings arriving after the real timeout must all be resent, because libutp
marks every outstanding packet need_resend on an RTO (:1230-1237). Skipping them
was tried once and stopped the 5%-loss benchmark completing.

Reproduced at roughly 1 in 15 runs under load before, 0 in 60 after, with the
benchmark suite unchanged at seven repeats.

## PR 27 — a completed transfer could end as a connection reset for the peer

The highest-value thing in this fork for anyone talking to real libutp peers.

libutp finishes sending and closes, so it sends a FIN. We acknowledge it, hand
the application its end of stream and tear the connection down. If that
acknowledgement is lost, libutp retransmits the FIN -- or a data packet whose
acknowledgement was also lost -- the socket no longer has the connection, and
it answers with a RESET. libutp reports UTP_ECONNRESET on a transfer whose
every byte had already been read.

Measured on a path with 3% loss, 2% reordering and jitter, libutp sending:
4 of 20 seeds ended this way before, 0 of 30 after.

The connection hands the socket the acknowledgement it sent alongside its FIN
on its way out; the socket keeps it for ten seconds and re-sends it instead of
a RESET. Checked only after every connection lookup has missed, so a live
connection always wins, and repeated only for a packet at or below the stored
acknowledgement number. A packet past it is data the peer wrote after we said
we were finished: libutp takes it into CS_GOT_FIN and says nothing, and we
cannot take it but we can also say nothing, so the socket drops it in silence.
TestConformanceDataAfterReachedFin pins both halves and caught this fix twice
-- once too broad, once too narrow.

That patch was not a half-close and did not pretend to be; the half-close
itself is PR 34 below, and with it the payload reaches the application as
libutp's does. This one stops a finished connection lying to its peer about how
it ended, which is worth having on its own.

Found only because libutp was run over a path that damages packets. The
loopback interop gate cannot find this class of defect at all, which is worth
knowing independently of this patch.

## PR 28 — a connection died when its dial context was cancelled

The smallest diff here and, for anyone embedding this library behind a
`net.Conn`, the one that decides whether it works at all.

`UtpSocket.Connect` and `ConnectWithCid` passed the caller's context to
`NewUtpStream`, which made it the connection's lifetime. `net.Dialer`
documents the opposite -- "Canceling ctx does not affect the connection after
it is established" -- and the standard Go shape relies on it:

```go
ctx, cancel := context.WithTimeout(ctx, dialTimeout)
defer cancel()
conn, err := dialer.DialContext(ctx, network, addr)
```

The `defer cancel()` fires as the dialling function returns, with the
connection still in the caller's hands. Here that killed it.

Every test in the repository dialled with a context it kept alive for the
whole transfer, so nothing saw it. anacrolix/torrent does not: its outgoing
connections cancel their dial context on the way out of
`establishOutgoingConn`. What that produced was a BitTorrent connection that
completed its handshake and then stopped mid-stream -- the peer read 796 bytes
of a 1007-byte exchange, saw EOF, and the torrent never moved a piece.

The fix is to give the stream the socket's context and leave the caller's
context governing only the wait for the connection to come up, plus an
explicit teardown on the paths where the caller gives up, since cancelling no
longer does it implicitly. The accept path had the same shape for the
`Accept` call's context and is changed with it.

`utpnet.TestDialContextCancelDoesNotCloseTheConnection` pins it and fails
against the old code with "writing after the dial context was cancelled: not
connected".

Found by running a real torrent between two `anacrolix/torrent` clients over
this library -- which is also worth sending, as
`integration/anacrolix/torrent_transfer_test.go`.

## PR 29 — Close held the caller for up to a minute, and Write kept its buffer

Both found by running the Go standard library's own `net.Conn` conformance
suite (`golang.org/x/net/nettest`) against `utpnet.Conn`, under `-race`, with
kernel TCP as a control in the same process. TCP finished the eleven subtests
in 0.43 s; this library took 139.91 s.

**Close waited out the retransmission ladder.** `UtpStream.Close` waited for
the connection's event loop to exit. A loop with unacknowledged data does not
exit until its ladder runs out -- 1+2+4+8+16 seconds -- and behind that the
60-second idle timeout. Measured: 31 s in one shape, 60.001 s in another, with
the caller held throughout. libutp's `utp_close` never blocks at all
(`utp_internal.cpp:3232-3247`).

Capping the wait is the wrong fix and the interesting part of the patch.
`Write` returns once the data is in the send buffer rather than once it is
acknowledged, so a write followed by a close has a tail still to flush, and on
a slow link that tail outlasts any timeout worth having. The wait is therefore
bounded by the peer going silent, not by a clock: two seconds without a packet,
which is twice libutp's RTO floor, so a lost FIN and its first retransmission
do not read as a stall. Giving up ends the *waiting* only -- the connection
goes on retransmitting and ends on its own, which is what libutp's does.

`netem.TestCloseFlushesASlowTail` and
`netem.TestCloseDoesNotWaitForASilentPeer` hold the two ends, and each fails
against the design the other argues for. Both had to be staged on the emulated
network: on loopback the tail flushes in milliseconds and a closed peer sends a
FIN, so neither case exists.

**Write retained the caller's buffer.** `UtpStream.Write` queued the caller's
slice and returned on `ctx.Done()` when the deadline expired, with the entry
still in the connection's pending list. The caller may reuse that buffer as
soon as Write returns; the connection would copy whatever it had become, and
put those bytes on the wire, on a write the caller had been told had failed.
It now copies what it queues, as libutp does
(`utp_writev`, `utp_internal.cpp:1057-1066`).

Worth sending together because the test module that found them is the third
thing in the patch: `integration/nettest`, a separate Go module so nothing a
caller depends on grows.

## PR 30 — the shared port leaked a goroutine per datagram

`utpnet.Socket` is a `net.PacketConn` so a BitTorrent client can run its DHT on
the same UDP port as its uTP connections. Both `ReadFrom` and `WriteTo` asked
the deadline for a timer channel, and the deadline started a goroutine to serve
it; with no deadline set -- the normal case, and the only case for a DHT --
that goroutine parked until the deadline was next changed, which for such a
caller is never. `WriteTo` discarded the channel outright and leaked one per
call regardless.

Measured: 500 `WriteTo` and 500 `ReadFrom` calls left exactly 1000 extra
goroutines (40 before, 1040 after). After the fix, 20 and 20.

`deadline.wait` now returns a stop function that callers invoke on every path
out, and `WriteTo` uses a new `deadline.expired`, which starts nothing because
the answer is all it needed.

Comes with the `net.PacketConn` contract tests that were missing with it --
past deadlines, a deadline moved under a blocked read, Close unblocking a
waiting read -- and a matching goroutine check on the connection path next
door, which uses `deadline.context` instead and was already clean.

## PR 31 — a quiet connection punished itself

On a link with no loss, a connection that finished a transfer and then wrote
200 bytes every 200 ms took retransmission timeouts: 2 timeouts, 12
retransmissions of data the peer already had, and the congestion window taken
from 55513 to 24831 bytes over five seconds. That is the shape a BitTorrent
peer connection spends most of its life in.

Retransmission timers are armed one per packet and cancelled on the ack, but
cancelling cannot stop a timer that has already fired -- it is out of the wheel
and on its way to the event loop. `onTimeout` then ran for an acknowledged
packet, and the early-timer guard re-armed it before returning, so every dead
timer went back into the wheel. The armed set grew by one per write and never
shrank, and a dozen of them landing past the deadline together were taken for a
real timeout.

libutp cannot reach this state: it keeps one deadline rather than a timer per
packet, and retransmits out of its outgoing buffer
(`utp_internal.cpp:1230-1244`), which no longer holds an acknowledged packet.
The fix asks the same question explicitly -- `sentPackets.Outstanding(seq)` --
and returns without acting. After: 0 timeouts, 0 retransmissions, window held.

Worth sending because it costs nothing in throughput and is invisible to any
bulk benchmark: every congestion profile in the suite is a continuous transfer,
so none of them ever goes quiet long enough for a timer to outlive its own ack.
`netem.TestQuietConnectionDoesNotTimeOut` comes with it.

## PR 32 — an idle connection kept a window it had not measured in seconds

libutp decays the congestion window of a connection sitting idle: it keeps one
retransmission deadline per socket, never clears it when the window empties
(set at `utp_internal.cpp:997`, reset per acked packet at `:1389`, re-armed on
each expiry at `:1204`), and when it passes with nothing in flight the window
falls by a third (`:1216-1222`). `retransmit_count` only increments when
something is outstanding (`:1240`), so it repeats without killing the
connection.

This library armed retransmission timers per packet, so an idle connection had
none and its event loop was entirely quiescent -- its window stayed exactly
where the last transfer left it, and the next write put a stale window onto a
path nothing had probed for seconds.

Measured against real libutp over the same emulated link, 8 seconds idle after
a 512 KB transfer -- its window read through its first flight, since
`max_window` is private and `utp_socket_stats` does not report it:

| | no idle | after 8s idle |
| --- | --- | --- |
| libutp | 55176 B | 15972 B (29%) |
| ours, before | 55375 B | 55375 B (100%) |
| ours, after | 55447 B | 16428 B (30%) |

29% is (2/3)³, three decays, which is where a doubling RTO from a 1000 ms
floor puts them in eight seconds.

The patch adds one wake-up: `scheduleIdleRto` arms a timer for the deadline
whenever nothing is outstanding, and `onIdleRto` applies the decay through the
same `sentPackets.OnTimeout` the in-flight path uses, which asks
`HasUnackedPackets` and so takes libutp's `cur_window_packets == 0` branch by
construction.

`netem.TestLibutpIdleWindowDecay` comes with it, comparing the two as
decays-by-a-third rather than as ratios so that a differing expiry count is not
mistaken for a differing rule.

## PR 33 — the congestion controller, measured rather than cited

Not a fix: a set of measurements. Every mechanism in this library's congestion
controller had been matched by hand against `apply_ccontrol` and none had been
measured individually; only the aggregate goodput had been compared against the
reference. These tests close that, and two of them found real divergences,
which are PR 31 and PR 32.

| Mechanism | Test | Result |
| --- | --- | --- |
| Application-limited guard (`:1681-1686`) | `netem.TestApplicationLimitedWindowDoesNotGrow` | 5s of 1 KB writes leave the window unchanged to the byte |
| Window decay rate limit (`:602-615`) | `netem.TestWindowDecayIsRateLimited` | 25ms of 50% loss costs one halving, 52%; unlimited it reaches the floor at 4% |
| Timeout, idle branch (`:1216-1222`) | `netem.TestLibutpIdleWindowDecay` | libutp keeps 29% of its window across 8s idle, ours 30% |
| Timeout, in-flight branch (`:1223-1228`) | `netem.TestTimeoutWithDataInFlightCollapsesWindow` | window to one packet with slow start on; 43560 against 44233 bytes delivered in recovery |
| Delay clamp to RTT (`:1615-1621`) | `TestDelayClampedToRTT` | 20 acknowledgements claiming 30s of delay leave the window intact; unclamped they take it to the floor |

`ControllerStats` and `ConnectionMetrics` gain `SlowStart` and
`AppLimitedSince`, without which two of these cannot be measured at all: a
window growing while the application is idle is correct in slow start and a
defect after it, and nothing outside the controller could tell which.

The tests are worth as much as their failure modes, which are documented with
them in KNOWN-LIMITATIONS.md. Four of the five passed against a *disabled*
mechanism on the first attempt -- because the stimulus was too small to move
the window, because the phase boundary was taken where `Write` returned rather
than where the data left, because slow start is ungated and the test could not
see which phase it was in, or because the measurement window opened at an
instant when nothing was flowing yet. Every one of them asserts its own
preconditions now, and every one was checked by removing the mechanism and
confirming it fails.

## PR 34 — the half-close

The one capability libutp had that this library did not.

libutp holds a socket in `CS_GOT_FIN` when the peer closes its sending side and
keeps delivering to its application, and offers `utp_shutdown(s, SHUT_WR)`
(`utp.h:176`) for the other direction. This library moved to `ConnClosed` as
soon as the remote FIN was reached, so a peer closing its sending side took the
whole connection with it, and whatever it was still sending back was lost.

`UtpStream.CloseWrite`, plus `utpnet.Conn.CloseWrite` so that a caller holding
a `net.Conn` finds it by type assertion — the name follows
`net.TCPConn.CloseWrite` rather than `shutdown(how)`, because that is what Go
callers reach for.

Measured against the reference:
`netem.TestCloseWriteDeliversWhatThePeerSendsAfterIt` half-closes, real libutp
sees end of stream and sends 4096 bytes back, and all of it is read. The same
exchange with `Close` still discards the reply, correctly, and
`netem.TestPeerMayWriteAfterOurFin` pins that half.

Three things it turns on, each of which was wrong first and is written up in
KNOWN-LIMITATIONS.md:

1. `CloseWrite` needs its own wake-up event. The loop sets `stream.shutdown`
   from `streamShutdown`, so sharing it promoted every half-close to a full
   close.
2. A FIN being acknowledged must not end a half-closed connection. libutp
   destroys on that only when `close_requested` is set (`:2178-2182`), and
   `utp_shutdown` does not set it where `utp_close` does.
3. The side that closes second must still finish. It cannot wait for its FIN to
   be acknowledged, because the first closer is already gone — libutp gets a
   RESET there and treats it as a clean destroy (`:2865-2868`), a route this
   library cannot use since its socket stays silent for a half-closing peer.
   Without this the soak test measured 29 connections still tracked after 60
   closed cycles.

A side effect worth mentioning: the closing check moved out of `onPacket` into
the event loop, because the second closer's FIN goes to a peer that will never
answer. Teardown no longer waits for a packet to notice, and the soak test went
from 131s to 0.93s.

## PR 35 — the firewall callback, which was accepted and ignored

libutp asks its embedder about every SYN for a connection it does not already
have, after the duplicate check and before it creates anything: "true means
yes, block connection" (`utp_internal.cpp:2975-2982`), and a refusal is a bare
`return 1`.

This library had no hook, and `integration/anacrolix`'s `NewUtpSocket` took
torrent's `FirewallCallback` and discarded it — a caller that passed a
blocklist got no blocking and no sign that it was not happening.

`utp.WithFirewall` is a construction option on the socket;
`utpnet.Options.Firewall` reaches it in `net.Addr` terms, which is what a
blocklist is keyed by; the adapter passes torrent's callback through.

Two properties beyond the obvious one, both asserted: a refusal creates no
connection state, and a refusal is silent rather than a RESET, because
answering confirms to a refused peer that something is listening. The
translation from `ConnectionPeer` to `net.Addr` fails closed.

## PR 36 — the ICMP path

libutp has two entry points an embedder feeds from the error queue of its UDP
socket, and this library had neither.

`utp_process_icmp_fragmentation` (`utp_internal.cpp:3079-3106`) takes the
router's next-hop MTU and lowers the path-MTU ceiling to it, so the search does
not have to rediscover the same limit by losing a probe.
`utp_process_icmp_error` (`:3117-3150`) tears a connection down, reporting
`UTP_ECONNREFUSED` if only the SYN had gone out and `UTP_ECONNRESET`
otherwise. Both find their connection by parsing the uTP header the ICMP
message quoted (`parse_icmp_payload`, `:3019-3067`).

`UtpSocket.ProcessICMPFragmentation` and `UtpSocket.ProcessICMPError` are those
two, with `utpnet.Socket` pass-throughs in `net.Addr` terms. Collecting ICMP is
left to the embedder, as it is in libutp.

Three things are worth a reviewer's attention.

**The lookup.** libutp's map is keyed on the receive id alone, so it needs
three probes; the connections here are keyed on the pair, so the first of those
probes -- the one that catches a quoted **SYN**, which carries a receive id
rather than a send id -- becomes two candidates. Order is otherwise libutp's.

**A unit conversion, which is a deviation.** libutp compares `next_hop_mtu`
against `mtu_ceiling` directly, but the first is a link MTU and the second a
UDP payload capacity, so libutp's ceiling ends up 28 bytes too large on IPv4
exactly when the router's figure is the binding one. The conversion and its
reasoning are in [DEVIATIONS.md](DEVIATIONS.md).

**What it is worth, measured.** `netem.Config.OnMTUDrop` lets an emulated
router report what it refused, so the mechanism can be tested end to end.
Over a link that carries 1100 bytes, the search settles at 1191 bytes with the
router silent and 1094 with it reporting. It does not rescue a transfer that
has already stalled, and the test says so rather than asserting a result it
does not produce: uTP numbers packets, so one already built cannot be re-cut
smaller.

`ConnectWithCid` also stopped reporting every failure as "connection timed
out". That was true while running out of SYN attempts was the only way to
fail; refused and timed out are now different answers, and the error is
wrapped so `errors.Is` finds either.

## PR 37 — an unmatched acknowledgement killed the connection

`sentPackets.onAck` returned `ErrInvalidAckNum` for an acknowledgement number
outside the range it tracked, and `processAck` turned that into a reset.
libutp discards the count and carries on: `if (acks > cur_window_packets) acks
= 0;` (`utp_internal.cpp:1907`), commented "this happens when we receive an
old ack nr".

libutp's own pre-filter for acknowledgements of packets never sent is already
implemented here (`invalidAckNum`, `:1794-1807`), so what reached the reset was
a *stale* acknowledgement: behind the tracked range, still inside the
three-packet tolerance below the last sequence number sent. A delayed or
duplicated `ST_STATE` early in a connection is exactly that, and a forged one
is a one-packet way to end an established transfer.

CONFORMANCE.md had recorded this as a divergence the packet-injection corpus
cannot see, because both implementations answer such a packet with silence.
The tests therefore assert connection state, and one of them first checks that
the pre-filter does not reject its own packet, so it cannot pass by never
reaching the branch.

One deliberate narrowing: the selective-ack extension on such a packet is
dropped with it, where libutp still applies it. Reasoning in DEVIATIONS.md.

## PR 38 — the read-side half-close, and the vectored write

An audit of libutp's public surface against this fork found exactly two
missing features.

**`utp_shutdown(s, SHUT_RD)`.** `UtpStream.CloseRead`, with
`utpnet.Conn.CloseRead` for type assertion. The subtlety is that libutp's
`read_shutdown` suppresses *delivery* while `conn->ack_nr++` stays outside the
guard (`utp_internal.cpp:2344-2355`, `:2392-2395`): incoming data is
acknowledged and dropped, so the peer is never throttled and can finish its
own close. Implemented by writing a zero-length payload into the receive
buffer, which records the sequence number, copies nothing, and consumes no
window. The test pushes 4MB through a 1MB buffer; the naive
buffer-and-stop-delivering variant stalls at 31s.

Data already buffered when `CloseRead` is called is discarded. libutp has no
receive buffer of its own so the question does not arise there; holding it
would keep the window closed by that much, which is the one thing the feature
exists to avoid.

**`utp_writev`.** `UtpStream.WriteV`, and `utpnet.Conn.WriteBuffers` in
`net.Buffers` terms. Not a capability, a copy: ~136KB allocated per 16.5KB
vectored write against ~155KB for join-then-`Write`, measured back to back.
Two deviations, both recorded: it blocks (following `Write`'s contract, not
libutp's partial-write one), and there is no `UTP_IOV_MAX`, since that cap is
the size of a `static utp_iovec[1024]` rather than anything the protocol says
— and silently truncating a caller's buffers to honour an array bound we do
not have would be a defect, not fidelity.

## PR 39 — a queueing-delay metric that subtracted two different directions

`ConnectionMetrics.QueueingDelay` computed `PeerTsDiff - BaseDelay`. Those are
measurements of opposite paths: `BaseDelay` is the base of the series the peer
reports about our outbound packets (libutp's `our_hist`,
`utp_internal.cpp:2016-2021`), `PeerTsDiff` our own measurement of the inbound
one (`their_delay`, `:2000-2001`). The difference between two directions is not
a queue in either; on an asymmetric path it is the asymmetry with the real
queue buried in it.

`ControllerStats` now carries `CurrentDelay`, the newest sample of the same
series `BaseDelay` is the minimum of, and the metric is their difference. Both
halves are tested: a 10ms/100ms path with no bottleneck (1.9ms corrected
against 90.7ms uncorrected, both computed from the same samples so the
comparison is not a claim about a previous run), and a real bottleneck
cross-checked against the emulated link's own measurement (78.9ms against
84.7ms).

The congestion controller was never affected — it compares a sample against the
base of the same series, in hand at the point it acts. The doc comment claiming
this was "the number the M5 gates are judged on" was also wrong; those gates
read the link's `MeanQueueDelay`.

## PR 40 — the MTU ceiling, taken from the interface when the Conn can say

libutp sets its path-MTU ceiling from `UTP_GET_UDP_MTU` before the first
packet goes out (`mtu_reset`, `utp_internal.cpp:1314-1322`). This fork used a
fixed 1400 for every connection, which is safe on an ordinary path and too
large on a tunnelled one — and a ceiling above what the path carries is the
stall in KNOWN-LIMITATIONS.md, which neither implementation can recover from
once the size is adopted.

`utp.PathMTUProvider` is an **optional** interface — `PathMTU(ConnectionPeer)
(int, bool)` — implemented by `UdpConn` and by netem's `Endpoint`. The `Conn`
interface is untouched, so the libutp driver and any other implementation are
unaffected and keep the configured ceiling. `UdpConn` finds it by asking the
routing table which local address a datagram to that peer would leave from (a
UDP "dial", which sends nothing) and reading the MTU of the interface that
owns it; per peer rather than per socket, because split tunnelling sends two
peers on one socket out of two interfaces, with a one-minute cache so a client
opening hundreds of connections does not pay a netlink dump for each.

One deviation, recorded: the answer is combined with `MaxPacketSize` as a
minimum rather than replacing it, because loopback reports a 65508-byte
datagram and a jumbo link 8972, and adopting either would put the library on a
window regime nothing has measured it at — the minimum window is two packets.

Worth it, measured over the 1100-byte link that stalls both implementations:
1,048,576 bytes in 0.69s with the sender told what its interface carries,
against 2,800 bytes and a 60-second timeout without. That is the same link as
the skipped `TestMtuSearchCannotRecoverFromAPathLimitBelowItsChoice`; the
stall is prevented rather than recovered from, and only where the narrow link
is the local one.

## PR 41 — clock drift, made producible and then measured

Not a library change beyond one config field, but it answers a question the
ledger had been carrying as an unknown.

LEDBAT's signal is a one-way delay, which is a difference between two clocks.
If they run at different rates the difference contains an error that grows
without bound, and the controller reads it as a queue. libutp says so in
`DelayHist.add_sample` (`utp_internal.cpp:293-298`) and corrects for it twice:
a delay base rotated every minute across thirteen minutes of history, and a
shift of its own base whenever the peer's base drops (`:2002-2014`). This fork
has the first in a different form — a sliding-window minimum — and not the
second.

Nothing could produce skew to find out what that costs: both endpoints of the
emulated network read the same process clock. `netem.DriftingClock` wraps a
`Conn` and rewrites the timestamps its packets carry, which is what a clock
with a rate error does. `ConnectionConfig.DelayWindow` exposes the window that
was a hard-coded two minutes, because no drift experiment can run in a
reasonable time against that.

Three findings.

**The rule.** Phantom queueing delay settles at window x drift rate: 0.99x the
prediction at 5000, 10000 and 20000ppm, on an application-limited flow whose
link queued 329µs throughout. A bulk flow cannot measure this — LEDBAT fills
the bottleneck queue to target by design, so the "uncongested" control sees
tens of milliseconds of perfectly real queue. That was measured the wrong way
first.

**libutp is more exposed than we are, not less** — 780 seconds of base history
against our 120 — which is much of why it needs the correction.

**The cost is fairness, not throughput.** A single flow with the link to
itself lost about 1% at 139ms of phantom queue; the controller backs off but
keeps the pipe full. Two flows through one bottleneck, one drifted: 35% of the
link against 65%. That run is the test; the 1% one is not kept, because
asserting on a 1% difference over a saturated link is asserting on noise.

## PR 42 — the clock-skew correction

libutp watches the delay in the *peer's* direction and shifts its own base
delay whenever the peer's base falls, on the grounds that two clocks drifting
apart move both directions' bases together
(`their_hist`, `utp_internal.cpp:2002-2014`). This fork had the raw value and
no history, so no correction.

Measured before and after, on an application-limited flow with a deliberately
skewed clock (`netem.DriftingClock`): the phantom queueing delay falls from
99% of window x drift rate to 0.3-0.5% of it, and a drifted flow's share of a
shared 20Mb/s bottleneck goes from 35.0% to 51.8%.

Three subtleties, each of which was wrong in a first version and is worth a
reviewer's attention.

**A drift model must skew both directions.** Rewriting only outgoing
timestamps produces the phantom queue faithfully and removes the only evidence
the correction keys on. The first implementation, tested against that model,
did nothing — correctly.

**The evidence must stay in wrapping arithmetic.** A clock drifting slow makes
the inbound delay shrink until it passes zero and the `uint32` subtraction
wraps; this fork capped that at one second as an unusable reading, so the
peer-direction base stopped moving exactly when the drift grew large enough to
matter. libutp keeps `their_delay` as a raw wrapping `uint32` and compares
with `wrapping_compare_less` throughout. `peerDelayHist` mirrors that,
including the thirteen rotating buckets, and the controller is fed the
uncapped value.

**The correction must age out.** Shifts accumulate at the full drift rate
while the error they cancel is a constant, so an unbounded correction
overshoots by however many windows the connection has been open and leaves the
flow delay-blind — measured at 4ms of perceived queue against a neighbour's
40ms, and 56.6% of the shared link, winning by ignoring congestion. libutp
bounds it without appearing to, by rotating one history bucket onto a fresh
unshifted sample every minute. Samples here are stored net of the correction
standing when they were pushed, which is the same thing expressed
continuously.

`ConnectionStats`/`ConnectionMetrics` gained `ClockSkewCorrection`, because a
correction that cannot be observed cannot be tested — and the first two
subtleties above were both found by reading it.

## PR 43 — an injectable wall clock, and the three divergences it exposed

`ConnectionConfig.NowMicros` is where a connection reads the wall clock, in
the uint32 microseconds uTP puts on the wire. It defaults to the real one;
every timestamp stamped and every one-way delay derived goes through it.

The point is not configurability. `Timestamp` and `TimestampDiff` had been
excluded from the conformance corpus's field-by-field comparison since it was
written, because libutp's driver reads a virtual clock and this library read
the real one, so they could never agree whatever either implementation did.
Pinning our clock to the driver's removes the exclusion — and the first run
found three divergences, every one of them in
`timestamp_difference_microseconds`, which is the peer's entire delay signal.

1. **The SYN-ACK echoed a measured delay; libutp echoes zero.** Its SYN path
   returns at `if (syn) { return 0; }` before `reply_micro` is ever set
   (`utp_internal.cpp:2002`), and zero is a defined "no sample yet" signal.
2. **Packets libutp rejects still changed what we echoed.** libutp sets
   `reply_micro` after the ack-number rule (`:1794-1807`) and the
   reorder-window check (`:1886-1899`); we set it first thing, so a packet the
   reference discards silently still moved the peer's delay signal.
3. **A cap put a value on the wire no libutp would send.** Anything above
   `MaxIdleTimeout` was pinned to one second — protection against an older
   defect that had outlived it. Measured as ours `1000000` against libutp's
   `3487502864`.

The matching receive-side rule was also missing and is now in: a `reply_micro`
of exactly `INT_MAX` means "no measurement", not 35 minutes (`:2017`).

Each fix was confirmed non-vacuous by restoring the old behaviour
individually and watching its own failure return.

**What this does not do** is make ack *latency* comparable. `NowMicros`
governs what is stamped, not scheduling; that needs the retransmit wheel and
the event loop on a virtual clock too, which is a much larger change and is
recorded as still open.

## PR 44 — virtual timers

`Clock` and `IdleBarrier`. Every deadline the connection compares, its four
event-loop timers and the socket's retransmission wheel come from an
injectable clock; `RealClock` is the default and the production path is
unchanged.

The point is what it makes measurable. Everything in the corpus compared what
a packet *contained*; this is the first comparison of *when* one was sent.
Both sides are read from the packets' own timestamp fields, which each
implementation re-stamps on every transmission, so neither number is inferred
from wall time.

Three things had to be right, and each was wrong first — they are worth a
reviewer's attention because each failure mode is silent.

1. **The loop must report when it parks.** Marking busy in each case body of
   the event loop's select means ten places to get right. The select was
   restructured to receive without acting — record which case fired, mark
   busy once, dispatch from a switch — so the invariant is structural.
2. **A parked state can be stale.** A channel send returns before the receiver
   runs. Acting on "everything is parked" right after a delivery dropped
   seventy-nine of eighty wheel ticks in a two-second advance, and nothing
   retransmitted at all.
3. **Parked is not idle while something is in flight.** The wheel delivers a
   timeout and parks before the connection wakes; time then moved before the
   re-arm, giving 9.05s, 9.075s, 9.125s or 9.15s for the same virtual
   deadline. Handoffs are counted, noted by the sender and taken by the
   receiver, and deliberately not folded into MarkBusy — a participant wakes
   for reasons unrelated to any handoff.

With all three: bit-identical instants across five runs and five `-race` runs.

**What it found.** Our retransmission deadlines drift later with each backoff
— 3.025s and 9.05s against libutp's 3.0s and 9.0s — because the wheel rounds
up to a whole tick and each backoff re-arms relative to the last firing, while
libutp compares against an absolute `rto_timeout`. Bounded, one-directional,
in the safe direction, and recorded rather than fixed.

**What it does not cover.** The socket's inbound event loop is not a
participant, so the timing of a reply to an injected packet is not yet
measurable. The outbound path is.

## PR 45 — the inbound path on the virtual clock

The outbound half landed with the virtual timers; this is the other half. The
socket's read and event loops are barrier participants, so the whole chain --
read loop, socket event loop, connection event loop, write loop -- is
synchronised, with every hop between them counted as a handoff. A packet can
now be injected and the reply to it timed.

Two things were needed beyond registering the loops, and both are the kind of
thing that fails silently.

**The handoff accounting has to be symmetric by construction.** A connection's
event channel has several writers: the socket's event loop, the ICMP entry
points, and the application's own CloseWrite, CloseRead and Close. Only the
first is a clock participant, so a global count would let an
application-driven close consume the note belonging to an inbound packet and
declare the system quiet while that packet was still queued. Both halves are
keyed on the event type instead -- only `streamIncoming` is noted, only
`streamIncoming` is taken -- so the pairing cannot drift.

**A test's own injection is not a participant.** Injecting and then asking
whether everything is parked answers about the moment *before* the injection.
`AwaitReactionTo` captures the wake count first.

**What it found: nothing wrong, and that is the result.** Both implementations
answer at the instant the packet arrived. Each defers acknowledgements and
flushes them on an external trigger -- libutp when its embedder calls
`utp_issue_deferred_acks`, this library at the end of the event-loop pass that
received the packet -- so the same instant is correct for both, and a non-zero
delay on either side would have meant the acknowledgement had slipped a pass.
It was assumed before and is checked now.

Confirmed non-vacuous: without the final inbound handoff note the test fails
intermittently, reporting no reply because quiescence is declared before the
acknowledgement has been emitted.

## PR 46 — two timing assumptions in the differential fuzz harness

Not a library change, but it belongs in the same review: both differential
fuzz targets primed their run with a fixed `time.Sleep(20ms)` and settled each
step with a quiet window that began before our socket had necessarily read the
injected packet.

Under load neither held. The initiator's SYN could arrive after priming had
cleared the transcript, and then read as this implementation answering a
packet libutp ignored; and a settle window could elapse with the packet still
queued, so our reply landed in the next step's transcript or was coalesced
into the next acknowledgement and never appeared at all.

Both now wait for the thing itself: the SYN, the accept request's
registration, and the injected packet actually being read.

## PR 47 — the initiator role in the corpus, and two ways a case can assert nothing

Every hand-written conformance case drove both implementations as the side
*accepting* a connection. The initiator role — we send the SYN, the peer
answers — was covered only by the differential fuzzer, which generates its
inputs rather than naming them and discards the handshake before it starts
comparing. That is the direction a BitTorrent client is exposed to on every
dial, and the role in which this library, not its peer, picks the sequence
numbers and connection ids.

`conformance_initiator_corpus_test.go` adds nine cases in that role, on the
virtual clock, comparing the SYN itself as step zero.

Writing them turned up two ways a case in this corpus could pass without
asserting anything, both of which were live:

**Mutual silence compared equal.** `compareStep` compared emission counts, and
0 == 0, so a step that expected a reply and got none from either side passed
on the match. A step that means to observe silence declares `wantNoEmission`;
every other step must now emit something. The guard caught two existing cases
on its first run. `TestConformanceZeroWindow` advertised a zero window and
stopped there, never writing, so the window never bit — and libutp was
refusing the write for an unrelated reason anyway, its responder sitting in
`CS_SYN_RECV` until an `ST_DATA` arrives (`utp_internal.cpp:2158`), where
`utp_writev` returns 0 for any state but `CS_CONNECTED`. Both zero-window
cases now write through the stall and out the other side, each with a control
that proves the write path works when the window is open.

`TestMalformedUnknownExtensionType` expected an unknown-but-well-formed
extension to be skipped per BEP 29, and had been getting silence from both
sides since it was written. Both implementations drop the packet. libutp's
extension loop would skip it — there is no default case — but `UTP_Version`
(`utp_internal.cpp:2480`) rejects any packet whose *first* extension byte is 3
or more before it reaches a socket. The gate reads only that first byte, so
the same unknown type reached through a chain is skipped as the specification
says, and both implementations accept that packet. Both halves are now cases,
each with a control, and the shared departure is in `DEVIATIONS.md`.

**A differential comparison cannot see which path it is on.** Only that both
sides are on the same one. The initiator corpus was first written with its
data one sequence number past what the dialling side expects — the SYN-ACK is
a bare `ST_STATE` and consumes none — so every "in order" case exercised the
out-of-order path, and passed, because libutp reordered identically.
Deliberately breaking that constant failed only two of the nine cases.

`step.wantAck` is the fix: an expected ack number, a claim about this
implementation alone rather than about the two agreeing. With it the same
deliberate break fails four of nine on the initiator side, and eight cases
rather than four on the responder side.

Not a library change apart from one fix it exposed: a connection's event loop
never unregistered from the idle barrier when it exited, so a case whose peer
answered the SYN with a `RESET` hung waiting for quiescence that could not
arrive.

## PR 48 — the don't-fragment bit on MTU probes

libutp sends an MTU probe with fragmentation disabled and everything else
fragmentable, by passing `UTP_UDP_DONTFRAG` to its embedder's sendto callback
(`utp_internal.cpp:928`). This fork recorded that as blocked on
`golang.org/x/net/ipv4`'s per-write control messages. Both halves of that were
wrong: `x/net/ipv4` has no don't-fragment setter at all, and no platform
offers one as a per-datagram control message — it is a socket option
everywhere it exists.

`utp.DontFragmentWriter` is the same optional interface `PathMTUProvider` is:
a `Conn` that can send one datagram unfragmentable implements it, everything
else is untouched and keeps today's behaviour. The connection marks the
datagram the MTU search chose as a probe, the mark travels on the socket
event, and `UtpSocket.writeLoop` asks the `Conn` to honour it. A `Conn` that
cannot answers `ErrDontFragmentUnsupported` and the datagram goes out
normally, so nothing can turn a missing socket option into a lost packet.

`UdpConn` implements it by setting the option, sending one datagram and
clearing it again. That is only sound because every datagram this library
sends leaves through one goroutine, so no other write can land between the
two — worth knowing before anyone adds a second write path.

No dependency is added. Constants come from the standard library's `syscall`
where it has them and are spelled out with header citations where it does not;
Darwin's `IP_DONTFRAG` is `0x1c` against FreeBSD's `0x43`, so those are
separate files. NetBSD and OpenBSD get IPv6 only, since neither defines an
IPv4 equivalent. Every other port compiles against a stub. 23 GOOS/GOARCH
pairs build with `CGO_ENABLED=0`.

Linux uses `IP_PMTUDISC_PROBE`, not `IP_PMTUDISC_DO`: `DO` defers to the
kernel's cached path MTU and refuses the send, which would cap this library's
own search at whatever the kernel already believed.

`netem` grew `Config.FragmentOversized`, which makes a link fragment an
oversized datagram and forward it unless the sender forbade it — an IPv4
router, where the default models IPv6. Without it the bit changes nothing on
an emulated path, because the link dropped everything oversized regardless.

**This found a second gap, and it is the more important one.** It is PR 49.

## PR 49 — the MTU ceiling from duplicate acknowledgements

libutp lowers its MTU ceiling from a lost probe in two places: a retransmission
timeout with the probe as the only packet outstanding
(`utp_internal.cpp:1152-1167`), and a third duplicate acknowledgement pointing
at the packet before the probe (`:1927-1940`). Only the first was implemented.
`mtu.go` cited both and `onProbeLost`'s comment described both, but nothing
counted duplicate acknowledgements.

The first alone cannot fire during a bulk transfer. It requires exactly one
packet outstanding — faithfully, because only then does a loss say something
about size rather than congestion — and a saturated send window never leaves it
that way. So a probe dropped for being too big was retransmitted fragmentable,
arrived, was acknowledged, and the search concluded the size was fine.

`connection.noteDuplicateAck` is the missing half, with libutp's three rules:
only bare `ST_STATE` packets count (`:1911-1920` explains why at length — an
`ST_DATA` carrying an acknowledgement was most likely sent because the peer had
data of its own); the acknowledgement must repeat the number just before the
oldest outstanding packet, and anything else resets the count; and nothing
counts while nothing is outstanding, which is also not a reset. On the third,
the repeated number either points just before the probe, in which case the
probe is the hole and the ceiling drops below it, or it does not, in which case
some other packet was lost ahead of the probe and libutp forgets the probe
without concluding anything.

Measured on a 1000-byte emulated path that fragments rather than drops, with a
1400-byte ceiling: the search comes down from 1384 to 996, and fragmentation
from 3809 datagrams to 32. Disabling the call site restores both to their old
values exactly.

**Two existing tests were built on the claim this closes.**
`TestMtuSearchCannotRecoverFromAPathLimitBelowItsChoice` was skipped as a
limitation "shared with libutp" whose fix would mean "deliberately diverging".
That was reached without knowing about this route and was wrong: the fix is a
libutp mechanism. The search does now recover there. It stays skipped for a
narrower reason — the transfer still stalls, because uTP numbers packets and
the ones already built too big cannot be re-cut.

`TestIcmpBringsTheSearchWithinThePath` failed on its own control guard, which
is the best thing it could have done. Its control assumed the silent search
stays above the path. What the ICMP report is still worth is the *ceiling*:
silent, the search reaches a workable size but leaves its ceiling at 1184,
free to climb back above the 1100-byte path; with the report it is exactly
1100 every run, because the report names the next hop's MTU instead of
bisecting towards it.

## Not a PR — the M4b sweep's findings

Reading libutp end to end against this implementation turned up two missing
mechanisms and one policy divergence. Neither mechanism is fixed here, so
there is nothing to send upstream yet; they are listed so the next person does
not have to find them again. Full write-up in KNOWN-LIMITATIONS.md, "The M4b
sweep".

- **No cap on accepted connections** against libutp's 3000 (`:2967-2974`), a
  policy choice rather than a defect.

## PR 50 — the clock-drift penalty

The second of libutp's two clock-drift mechanisms, and the one this fork was
missing. The M4b sweep found it; this implements it.

The mechanism it already had is the delay-base shift
(`utp_internal.cpp:2009-2015`), which corrects the delay *measurement* for
ordinary drift between honest clocks. This one corrects nothing: it watches
the long-run slope of the delay the peer reports and, past -200000
microseconds per five-second slot, adds a penalty to the delay the congestion
controller sees (`:1646-1650`). libutp is explicit that the threshold is aimed
at intent rather than hardware — 40,000 ppm, where a crystal drifts by tens.

`driftEstimator` is the `average_delay` / `clock_drift` machinery (`:2040-2107`)
kept as its own type, because the arithmetic is wrapping uint32 throughout and
a wrap read the wrong way round would penalise an honest peer on every
connection, invisibly. Nine cases check it against hand-worked values,
including both wrap directions and the renormalisation that keeps the average
bounded over a long connection.

Measured, with the sender's clock drifting over the emulated network: +100 ppm
gives a drift estimate of -222 and no penalty; +200,000 ppm gives -518,801 and
a 45.5ms penalty. Both halves of libutp's claim hold. At the controller,
identical acknowledgements settle on a 49,027-byte window honestly and 2,800
with the penalty reachable. Disabling the penalty fails every case.

Two properties worth carrying upstream in the commentary rather than
rediscovering. The estimate converges slowly by design — each slot contributes
an eighth, so it reaches 1-(7/8)ⁿ of the true slope and a twelve-second run at
200,000 ppm stops at -179,151, short of the threshold. And the penalty acts on
the window, not on throughput: a flow that is not window-limited shows a
tenth off its window and no change in rate at all.

One deviation, in DEVIATIONS.md: the penalty is applied to LEDBAT++ as well,
where libutp has no opinion because it has no LEDBAT++. A peer manipulating
its clock does not care which controller this end runs, and leaving the opt-in
algorithm unprotected would make it the weaker choice.

## PR 51 — `utp_read_drained`, and a hang that was never its fault

libutp acknowledges as soon as its application drains the read buffer
(`utp_internal.cpp:3242-3261`); this fork freed the buffer and said nothing.

The reason this took three attempts is worth more than the patch. The hang
that caused the first two to be reverted is **pre-existing**: with the
mechanism compiled out, the same scenario stalls in 4 runs out of 20, against
about 1 in 20 with it. The earlier revert rested on six control runs of a less
sensitive test, and six runs cannot see a one-in-five rate.

Two things were genuinely wrong and are fixed. The **ordering**: libutp hands
bytes up inside `utp_process_incoming` and acknowledges afterwards, so its
acknowledgement already carries the window the drain produced; draining on a
later pass made every drain look like growth and doubled the reverse traffic.
`processReads` now runs immediately before `flushAck` in the same pass. The
**condition**: firing only when the last advertised window was zero wedged a
connection, because a window of 861 bytes is not zero but is less than a
packet — libutp's `rcvwin > last_rcv_win` is right precisely because a window
too small to use blocks as completely as a closed one.

The end-to-end benefit is now measured, on a scenario cleared of two other
defects first: the gap deadlock (PR 53), and this fork's sender overrunning the
peer's window (PR 54). A 32KB receive buffer, 512KB to send, a reader paused
two seconds, nothing dropped on the link, 10 runs each. With the change the
transfer finished in 2.158–2.163s. Without it, it finished in 29.698–29.702s,
with the keep-alive checked every 500ms to approximate libutp, which checks
it on every timeout pass. The window fell to 176–260
bytes, which is less than a packet but not zero, so the zero-window probe never
armed, and the only recovery was the receiver's keep-alive. That was shown by
moving the keep-alive interval and watching recovery move with it. The
stall-rate figures first credited to this change are still retracted: they
were the gap deadlock.

Two things a reviewer should know before touching this area. The zero-window
probe arms on a window of exactly zero, so any change that turns a zero window
into a small one disarms the peer's recovery path. And the stall in the same scenario is
fixed (PR 53); it was *not* the receive window, though it looked like it. This
fork used to charge every byte held out of order against the advertised window
where libutp charges none, driving it to 1,376 bytes with a gap open. Fixing
that (PR 52) did not move the stall rate at all -- about 3 runs in 20 against 4
before -- with the window then wide open at its full size while the transfer
trickled to a stop. The cause was elsewhere entirely.

## PR 52 — the receive window stops charging for reordered data

`receiveBuffer.Available()` subtracted every byte held behind a gap from the
window this fork advertised. libutp's `get_rcv_window`
(`utp_internal.cpp:590-596`) is `opt_rcvbuf` less what its embedder has not yet
read, and a packet held out of order sits in `conn->inbuf` and never reaches
`utp_call_on_read` until the gap is filled, so it never enters that figure.

Measured with both implementations driven through the same packets: 40,000
bytes held behind a gap cost us exactly 40,000 of advertised window and libutp
none; 800 packets drove ours to 1,376 bytes, under the size of one packet, so
the peer could not even retransmit the packet that would have cleared the gap.

The fix is a split, not a deletion. `Window()` is what goes on the wire;
`Available()` still charges the held bytes and still does admission control,
because the collapse loop in `Write` indexes `rb.buf[rb.offset:end]` and panics
if offset plus pending exceeds capacity. Advertising the larger figure cannot
break that: a peer respecting the window sends only what fits in what has not
been delivered, and a peer ignoring it is dropped at admission rather than
written. Over-advertising costs a retransmission, never a panic.

Both now advertise 1,048,576 with 1.12 MB held behind a gap.

**It did not fix the stall it was expected to.** That was the stated
motivation, and the measurement disproved it: about 3 runs in 20 after,
against 4 before. What changed is the trace — the window now sits wide open at
its full size while the transfer trickles to a stop, so whatever holds the
sender was never the receive window. Written up separately rather than folded
into this.

## PR 53 — reserve room for the packet that fills a gap

A gap in the sequence space was a deadlock. Data arriving behind it was
admitted until the receive buffer was full, and the packet that would release
all of it -- the peer's retransmission of the missing one -- was then refused
for want of space. Refused every time, and the peer sends it forever. Nothing
else can free the buffer, because nothing behind a gap can be delivered until
the gap closes.

Instrumented, the receiver showed `pending 32613, readable 0,
held-behind-gap 32613` unchanged for the rest of the run with packets still
arriving and being dropped; the sender showed `cwnd 13424, inflight 13424` --
its window permanently full of bytes that would never be acknowledged, with
326KB of application data waiting behind them.

`receiveBuffer.Write` now admits the gap-filling packet against the whole of
what is free, and requires anything arriving out of order to leave a reserve
behind for it. The reserve is the largest packet this peer has sent, because
that is exactly what the retransmission will be.

Measured on the same harness both ways: 0 stalls in 40 runs with the reserve,
3 in 40 without. Forty samples make that suggestive rather than conclusive, so
the mechanism is pinned deterministically as well --
`TestGapFillingPacketIsAdmittedWhenTheBufferIsFull` builds the state directly
and shows the packet refused without the reserve and accepted with it,
releasing the whole run behind it.

libutp does not have this problem to fix: its reorder buffer is separate from
its embedder's read buffer and is bounded by entry count rather than by bytes,
so data behind a gap never competes for the space the gap-filler needs. This
is a fix for a structure this fork chose, not a port of anything.

It costs one packet's worth of receive buffer to out-of-order data, and
`largestPacket` only grows, so an unusually large packet early reserves that
much for the rest of the connection. Both are bounded by the maximum packet
size.

## PR 54 — the sender spends the peer's window as libutp does

libutp sends the next queued packet only while
`cur_window + packet_size <= min(max_window, opt_sndbuf, max_window_user)`:
that is `is_full()` with no argument, as `flush_packets` calls it
(`utp_internal.cpp:933-936`, `:956`, `:974`). This fork computed
`min(cwnd - in_flight, peer_window)`, which differs twice:

- Bytes in flight did not count against the peer's window, so a receiver whose
  application fell behind was overrun by up to a full window, and every packet
  past its room was dropped and retransmitted.
- Packets were cut down to fit the room left, down to tens of bytes. libutp
  sends nothing into less than a full packet of room.

On a link that dropped nothing, a slow reader's receive buffer refused 54 to
262 packets a run before the change, with 1 or 2 sender timeouts; after it,
none and none. The second half is checked against libutp by the conformance
corpus. The first cannot be isolated against libutp at connection start,
where libutp's one-packet congestion window hides it, so it is asserted on
this side by two unit tests, each of which fails with its half disabled.

The controller gains `BytesInFlight` and `CongestionWindow`, the two halves of
the old `BytesAvailableInWindow` that the rule needs separately. Libutp's
Nagle check in the same loop is not ported; that is an existing, measured
deviation.

## PR 55 — the keep-alive leaves one interval after the last packet

libutp sends a keep-alive when `current_ms - last_sent_packet >=
KEEPALIVE_INTERVAL` at a timeout pass (`utp_internal.cpp:1271-1274`), so it
leaves 29 seconds after the last packet. This fork checked the same condition
only when a 29-second ticker fired, counted from connection start. A
connection that went quiet just after a tick waited for the tick after next:
up to 58 seconds, against a NAT mapping that commonly lasts 30.

The ticker becomes a timer aimed at the instant the silence reaches the
interval, re-aimed from `lastSentPacket` whenever it fires. Measured end to
end, a connection recovered by a keep-alive went from 58.21s to 29.34-29.36s.
`TestKeepAliveLeavesOneIntervalAfterTheLastPacket` asserts the instant on a
virtual clock at the real 29 seconds and fails against the ticker.

It also removes a case from the zero-window probe timer's drain that received
from the keep-alive ticker. Harmless with a ticker; with a timer it would take
a firing without re-aiming it and silence the keep-alive for good.

## Not for upstream

- `DefaultSocketBufferSize` and the `Bind` buffer sizing — defensible, but it
  is a policy choice about a socket upstream may not consider its own. Offer
  it, do not assume it. Recorded in [DEVIATIONS.md](DEVIATIONS.md).
- `REFERENCE.md` and `scripts/check-libutp-reference.sh` — useful to upstream,
  but they exist to serve a libutp-conformance goal upstream has not signed up
  for. Mention rather than push.

## Order of operations

PR 1, PR 4, PR 6, PR 8, PR 9, PR 9b, PR 9c and PR 10 are close to unarguable and should go first.

PR 9d is the largest throughput win and the one most worth reviewing carefully.

PR 2 and PR 3 both need a conversation, for the same reason: any tuning or
measurement upstream has done was taken against a controller with a constant
delay signal and a timer that fired at random within a one-second window.
Both will move once these land, and that is the point, but it should not be a
surprise.

PR 5 needs review of the per-packet-timer subtlety above. PR 12 is the largest behavioural change in the fork and needs the most discussion. PR 11 is optional for upstream and the most invasive; offer it last.
