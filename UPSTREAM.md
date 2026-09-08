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
