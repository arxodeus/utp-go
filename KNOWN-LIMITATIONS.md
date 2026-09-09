# Known limitations

Everything I am not confident about is here rather than in a footnote.

Read this before trusting any part of this fork. It is honest about what has
been done, what has only been done on loopback, and what has not been done at
all.

## Status against the brief's milestones

| Milestone | State |
| --- | --- |
| **M0** — pin the reference, prove the two libutp copies agree | **Done.** See [REFERENCE.md](REFERENCE.md) and `scripts/check-libutp-reference.sh`. |
| **M1** — emulated network harness | **Done.** See [HARNESS.md](HARNESS.md) and `netem/`. |
| **M2** — conformance harness against real libutp | **Partly done.** Corpus comparing emitted packets field by field, including malformed and hostile headers, plus retransmission-schedule and ack-count comparisons measured against libutp on each run; see [CONFORMANCE.md](CONFORMANCE.md). Responder role only, and ack *latency* is still not compared. |
| **M3** — fix the failing transfer tests | **Done.** Root causes below; gates in "Verification". |
| **M4** — audit transfer paths against libutp | **Done.** The ack path, the loss-recovery path, the retransmission timers and the send path are all audited against libutp — the timers by measurement rather than by reading, see [CONFORMANCE.md](CONFORMANCE.md). Three findings from the send path below; the packet-size one is deliberately left to M6. |
| **M4b** — exhaustive libutp compatibility sweep | **Done.** [COMPATIBILITY.md](COMPATIBILITY.md) records every protocol area, the *kind* of evidence behind it — differential, measured, interop, cited, or none — and what is unchecked. Writing it found one defect (an ack per data packet, below), and the largest gap it named — libutp had never been run over the emulated network — has since been closed: `netem.TestLibutpOverEmulatedNetwork` runs real libutp over the same links as the benchmark suite, which found two more defects. Both were in the harness, and both made libutp look worse than it is. |
| **M5** — verify LEDBAT, add LEDBAT++ | **Done.** Classic LEDBAT verified against `apply_ccontrol` and corrected (seven defects, +42% to +89% goodput). LEDBAT++ implemented from the draft, opt-in. Deference measured against a loss-based competitor over a shared bottleneck: classic LEDBAT takes 60% of the link from it, LEDBAT++ takes 44%. See [BENCHMARKS.md](BENCHMARKS.md). |
| **M6** — MTU path discovery | **Done.** Binary search between a 576-byte floor and a 1400-byte ceiling, probing with ordinary data packets, as libutp does. The ceiling could be raised from 1024 only because discovery makes it safe: an untested path still gets 988 bytes. One gap: probes are not sent with the don't-fragment bit, so on IPv4 an oversized probe is fragmented rather than dropped. See below. |
| **M7** — anacrolix/torrent integration | **Done, with one gap.** `utpnet` presents a uTP socket as `net.PacketConn`/`net.Conn` and satisfies torrent's uTP interface, checked against the real interface by reflection in `integration/anacrolix`. The gap: torrent selects its uTP implementation at build time, so wiring it in needs a `replace` or a patched file — recipes in that module's README. |
| **M8** — soak and hardening | **Partly done.** Six fuzz targets — three on the decoder, one driving a live connection, and two differential against real libutp covering both the responder and initiator roles — plus three soak tests. Six defects found and fixed. See [FUZZING.md](FUZZING.md). Not done: anything running for hours. |

**The interoperability gate now passes.** libutp is vendored at the pinned
commit in `native/libutp/`, with a bridge that gives it a UDP socket and an
event loop, and both directions transfer verified payloads over real sockets:

| Direction | Result |
| --- | --- |
| our initiator to libutp responder | 512 KB verified, ~600 Mbps |
| libutp initiator to our responder | 512 KB verified, ~430 Mbps |

Both numbers rose — from ~430 and ~374 — when the bridge was given the
path-MTU callback it had been missing all along. See "libutp was wired up
wrongly" below; the earlier figures were real measurements of libutp sending
288-byte packets.

That is the difference between conformance by citation -- code read against
`utp_internal.cpp` and matched by hand -- and evidence that the implementation
every peer in the wild runs will actually complete a transfer with this one.

What remains unmet in M7 is a hash-verified torrent transfer: the adapter
exists and is checked against torrent's real interface by reflection, but no
actual torrent has moved through it. `CGO_ENABLED=0 go build ./...` succeeds
for the whole module.

Writing the gate immediately found two defects that nothing else had, both
described below: `Accept` could not accept, and `Close` took up to half a
minute.

## The congestion controller had no delay signal

This is the most important thing in this document.

`onPacket` computed the peer timestamp difference as

```go
peerTsDiff := time.Microsecond * time.Duration(nowMicros - int64(packet.Header.TimestampDiff))
```

which subtracts the incoming packet's `timestamp_difference_microseconds`
field — the wrong field — from our own full-width `int64` epoch clock. Two
errors compounded: the wrong field, and mixing an epoch microsecond count with
a `uint32` wire value.

The result was approximately 1.79e15 microseconds on every packet, which the
sanity cap immediately pinned to exactly one second. Every packet this
implementation sent therefore advertised a constant `1,000,000` µs delay, and
the peer fed that constant into LEDBAT as its only delay sample.

Measured on loopback, the `timestamp_difference_microseconds` field received
from the peer was `1000000` on every packet after the first. After the fix it
varies between roughly 88 and 229 µs.

**Consequences:**

- Any measurement of this library's congestion control taken before commit
  `4a0f0ec` is meaningless. The controller was inert: it had a constant input.
- The existing `congestion_test.go` unit tests pass either way. They exercise
  the controller's arithmetic in isolation and never observe that the delay
  fed to it in production is a constant. This is exactly the failure mode the
  brief warns about — unit tests that tell you nothing.
- **The M5 gates have not been run.** They should now be run against the fixed
  controller before any LEDBAT++ work begins, because the "classic LEDBAT
  baseline" that LEDBAT++ is supposed to be compared against has never
  actually run.

The fix matches libutp, which computes `reply_micro` as a `uint32` subtraction
of the packet's timestamp from the current time.

## The RTT estimate tracked nothing

Found by the M1 harness within minutes of it first carrying a flow, which is
the entire argument for building the harness first.

`defaultController.OnAck` updated its smoothed RTT like this:

```go
rttAdjustment := computeRTTAdjustment(c.rtt.Microseconds(), ack.RTT.Microseconds())
c.rtt = time.Duration(maxInt64(c.rtt.Milliseconds()+rttAdjustment, 0))
```

Three units in two lines. `computeRTTAdjustment` takes and returns
microseconds, but its result was added to `c.rtt.Milliseconds()`, and the sum
was handed to `time.Duration`, which counts nanoseconds.

The estimate converges to about 5 µs on any path, whatever its real RTT.
Measured over the emulated network:

| Path RTT | RTT estimate before | after |
| --- | --- | --- |
| 20 ms | 3 µs | 22.0 ms |
| 40 ms | 5 µs | 43.1 ms |

`applyTimeoutAdjustment` had the same class of error: `rttVarianceMicros` is a
microsecond count and was passed to `time.Duration` directly, understating the
variance term by 1000x and leaving the retransmission timeout pinned at
`minTimeout`.

Both are fixed. The consequence of the old behaviour is that the RTO could
never adapt to the path: on any link with an RTT above the floor, uTP would
have retransmitted continuously.

The estimator now matches libutp's arithmetic exactly
(`utp_internal.cpp:1362-1380`), including three things it previously lacked:

- **The first sample is adopted outright** -- `rtt = ertt`,
  `rtt_var = ertt/2` -- rather than easing an average up from zero, which
  took about twenty samples to converge and left the RTO wrong throughout.
- **The RTO floor is 1000 ms**, not 500 ms. On the emulated 2% loss path this
  moved recovery entirely onto duplicate-ack fast retransmit (58 fast
  retransmits, zero timeouts, against 57 and one before), which is the
  healthier path: an RTO firing while dup-acks would have recovered the loss
  is a lost round trip.
- **Initial `rtt_var` is 800 ms**, matching libutp, so a timeout computed
  before any ack is conservative rather than zero.

`libutp_conformance_test.go` pins each of these, including Karn's algorithm --
an ack for a retransmitted packet must not move the estimate, since it cannot
be attributed to a particular transmission.

## Retransmission timing was quantised to one second

**Fixed.** Recorded in full because it was the largest defect in the library
and because the measurement is the point: it was invisible until the M1
harness existed, and it contaminated everything downstream of it.

On a link that dropped nothing, this implementation retransmitted about **6%
of packets**. Measured on an 8 Mbps / 40 ms emulated path with `LossRate: 0`,
confirmed by the link's own counters reporting zero drops.

The cause was the retransmission timer wheel. Each connection owned one,
constructed with `interval = InitialTimeout/4` -- **one second** -- while the
retransmission timeout is 500 ms. A wheel with a one-second tick cannot
represent "500 ms from now": the timer landed on the next tick, uniformly
0-1000 ms away, so a packet inserted shortly before a tick was declared lost
long before its ack could arrive. The expected spurious rate is roughly
`RTT / interval` = 43/1000 ≈ 4.3%, against 6.1% measured.

Simply adding a rounds counter at the same interval did **not** help (6.25%
measured): with a one-second tick there is no way to express 500 ms at all.
The resolution itself had to change.

Raising the resolution on a per-connection wheel fixed the timing but cost
about 25% at a thousand connections, because 25 ms per connection means
40,000 timer wake-ups per second. The fix is therefore **one wheel per
socket**, at 25 ms, with:

- delays rounded **up** to whole ticks, so a timer never fires early;
- a rounds counter, so delays beyond one revolution are scheduled rather than
  clamped -- which also fixes RTO backoff, previously capped at
  `slotNum * interval`;
- O(1) removal via a key-to-slot index, since scanning every slot is no longer
  acceptable when the wheel holds every connection's packets;
- per-connection key scoping, and a per-connection set of armed sequence
  numbers so acking a range disarms only that connection's timers instead of
  scanning the whole socket's wheel.

Measured before and after:

| | before | after |
| --- | --- | --- |
| Retransmits, lossless 8 Mbps / 40 ms path | 6.18% | **0.00%** |
| Timeouts on that path | 2 | **0** |
| Goodput on that path | 2.43 Mbps | **3.73 Mbps** (+53%) |
| Mean cwnd | 14.5 KB | 26.4 KB |
| `TestUdpTransfer`, loopback | 257 Mbps (mean of 2) | **350 Mbps** (mean of 2) |
| `TestManyConcurrentTransfers`, 1000 conns | 560 Mbps (mean of 4: 547/662/412/621) | **587 Mbps** (mean of 4: 613/601/654/479) |

The 1000-connection figure is noisy on this machine -- the spread is wider
than the difference between the two configurations -- so the honest reading is
that the shared wheel costs nothing measurable at that scale, not that it
helps.

`netem/utp_test.go:TestSpuriousRetransmitsOnLosslessLink` asserts the rate
stays at zero.

## RESET storms

**Fixed.**

Any packet arriving for a connection this socket does not have produced a
RESET, unconditionally. A 300-connection run emitted over 3000 of them. That
is both an incompatibility and an amplification vector: a peer that keeps
sending to a torn-down connection drew one RESET per packet.

libutp remembers what it has already answered, keyed on
`(connection id, address, seq nr)`, and stays quiet for repeats
(`RST_INFO_TIMEOUT`, 10 s). Past `RST_INFO_LIMIT` (1000) stored entries it
stops answering entirely rather than let the table grow
(`utp_internal.cpp:2907-2945`, constants at `utp_internal.cpp:71-72`). The
same policy is now implemented here, with those line numbers cited in the
code.

Checked at the same time and found already correct: an unmatched RESET is
dropped and never answered, which is what libutp does in its own `ST_RESET`
branch (`utp_internal.cpp:2850-2881`). Replying to a reset with a reset would
let two hosts trade them indefinitely.

Measured by `reset_limit_test.go`, which counts what a socket actually puts on
the wire:

| Injected | RESETs before | after |
| --- | --- | --- |
| 50 repeats of one unknown packet | 50 | **1** |
| 20 distinct unknown packets | 20 | 20 (unchanged: suppression must not silence a genuinely new peer) |
| 20 unknown RESETs | 0 | 0 |
| 1500 distinct unknown packets | 1500 | **1001** (capped at the limit) |

## A connection had no death condition of its own

**Fixed.**

An established connection retransmitted forever. There was no limit on
consecutive timeouts, so the only thing that could end a connection whose peer
had stopped acking was the 60-second idle timer -- and that timer is reset by
*any* inbound packet. A half-broken peer that keeps sending anything at all
held a connection open indefinitely while our data went unacked. For a
BitTorrent client with many peers, that is a lot of dead connections held
open.

libutp kills the connection once `retransmit_count` reaches 4, resetting that
counter whenever a packet is acked (`utp_internal.cpp:1191`, reset at
`:1398`). The same rule is now implemented here.

One subtlety worth recording, because the first attempt was wrong: libutp has
a single connection-wide RTO and counts one event per expiry, while this
implementation arms one timer per outstanding packet, so a single RTO delivers
one callback per packet in flight. Counting those directly killed healthy
connections under load -- ten packets in flight meant ten "consecutive
timeouts" from one RTO. The counter belongs inside the existing
timeout-amplification guard, which is what collapses per-packet callbacks into
one event per RTO.

Measured with the harness: a write to a blackholed peer now fails with
`ErrTimedOut` after about 10 seconds, rather than hanging until the 60-second
idle timer. `netem/utp_test.go:TestConnectionGivesUpWhenPeerGoesSilent`.

`MaxConnAttempts` also changed, from 6 to 3. libutp gives up on a connection
attempt once `retransmit_count` reaches 2 while in `CS_SYN_SENT`, which is
three transmissions of the SYN in total (`utp_internal.cpp:1191`). This is a
default, still overridable per connection.

## A zero-length ST_DATA was silently dropped

**Fixed.**

`DecodePacket` rejected any `ST_DATA` carrying no payload, so such a packet
never reached the connection at all: it was logged as undecodable and
discarded. Because it was never acked, a peer that sent one would retransmit
it forever and the connection would stall. A second, unreachable check in
`onData` reset the connection on the same condition.

libutp accepts it. In-order delivery guards only the hand-off to the
application with `count > 0` and advances `ack_nr` unconditionally
(`utp_internal.cpp:2342-2355`), so the packet is acked and the sequence space
moves on like any other. Both checks are removed, and the receive buffer
already did the right thing: a zero-length write advances the ack number
without copying anything.

`libutp_conformance_test.go` covers the decoder accepting it, the ack number
advancing, and data after an empty packet still being delivered.

## ConnectWithCid ignored its own context

**Fixed.** It waited on a bare channel receive with no context arm, so
cancelling the context during connection setup never returned -- the
connection's event loop exits on that same cancellation without signalling the
channel, leaving the caller blocked forever. `Connect` already had the arm;
`ConnectWithCid` simply lacked it. Found by a test that cancelled mid-handshake.

## Accept could not accept

**Fixed.** Found by the M7 interop gate: libutp, as initiator, could not
connect to us at all.

`Accept` -- the path that takes no connection id, and therefore the only one
usable against a peer that picks its own -- polled once for an
already-arrived SYN and failed outright with "no incoming conn" if none had
come yet. That made it unusable for the ordinary server pattern: call
`Accept`, then have a client connect. The SYN arriving a moment later was
parked in the incoming-connection map with nobody left to claim it, and the
peer retried until it gave up.

The cid-specific path had always queued such requests. This one now does too.

Behind that sat a second defect on the same path: `awaitConnected`
dereferenced the accept's connection id, which is nil when none was given, so
it panicked as soon as the first fix let it get that far. It now reports
against the stream's id, which always exists.

Both were reachable by any caller of `Accept`. Nothing in the repository used
it, so nothing had ever run them.

## Close took up to half a minute

**Fixed.** Also found by the interop gate: a transfer completing in 10 ms was
followed by a 29-second `Close`.

`UtpStream.Close` set its shutdown flag and then waited on the connection's
event loop. But the loop blocks in a `select` on packet, write, read and timer
channels, and an atomic flag is not one of them. It only noticed the shutdown
when some unrelated packet or timer happened to arrive, so `Close` took
anywhere from about a second to the full idle timeout depending on what was in
flight. Trace logging perturbed the timing enough to hide it, which is what
made it look intermittent.

`Close` now sends the `streamShutdown` event -- the same one the socket
already uses to wind a connection up -- which wakes the loop. Measured: 29
seconds to 0 ms, and `TestCloseReturnsPromptly` guards it.

For a BitTorrent client, which opens and closes connections constantly, this
was the more consequential of the two.

## The selective-ack bitfield was bit-reversed

**Fixed.** Found by the M2 conformance corpus on its first run, and the best
argument in this repository for building one.

Our encoder placed the first entry at the *most* significant bit of each byte
(`1 << (7-j)`). BEP 29 specifies the least significant bit, and libutp builds
its mask that way -- `m |= 1 << i` for the i'th packet past `ack_nr+2`, low
byte first (`utp_internal.cpp:806-818`). The reordering case produced
`ours=80000000` against `libutp=01000000`.

The decoder reversed the bits identically, so the implementation agreed with
itself perfectly. Every Go-to-Go transfer worked, every unit test passed, and
**the M7 interoperability gate passed too** -- because a clean loopback path
never drops a packet, and a selective ack is only sent when something is
missing.

Against a real peer under loss, every selective ack sent would have been
misread and every one received misread in turn. Nothing that compares an
implementation against itself can see a bug that is self-consistent, and
nothing that tests only a lossless path can see one that only appears under
loss.

## Root causes of the M3 failures

The brief asked for a written explanation of each. Both named tests failed,
but not for the reasons the brief assumed.

### `TestUdpTransfer` was not testing this library

It opened two `net.UDPConn`s and blasted raw datagrams between them. It never
called the uTP library. Worse, its send loop never advanced `offset` — the
increment was commented out — so it looped forever and the test could only
ever end at the timeout.

It was a scratch benchmark of UDP loopback, left in the tree. The brief
describes it as "the raw-UDP path — precisely what BitTorrent needs", which is
what its name suggests but not what it did. It has been rewritten to transfer
16 MB over uTP on real UDP sockets and verify the payload byte for byte.

### `TestManyConcurrentTransfers` — three defects in series

1. **A lost SYN-ACK stranded the connection permanently.** When an acceptor
   received a retransmitted SYN, it responded with nothing: the code only sent
   a SYN-ACK when `c.synState == nil`, which is never true for an established
   acceptor. Nothing else retransmits the SYN-ACK. So a single dropped SYN-ACK
   meant the initiator retried `MaxConnAttempts` times into silence and gave
   up. libutp acks a duplicate SYN; this now does too.

2. **Readers were never told the stream had ended.** The event loop's close
   path guarded its final drain with `if !c.eof()`, but `eof()` returns true by
   definition once the state is `ConnClosed`, so the drain never ran. The
   end-of-stream marker was only ever delivered from one other path — remote
   FIN fully received. Any connection that closed by idle timeout, RESET, or a
   local close left its reader blocked in `ReadToEOF` forever. This is why
   (1) produced a permanent hang rather than an error: the initiator gave up,
   and its peer waited for data that would never come and an EOF that would
   never be sent.

3. **The UDP receive buffer was left at the OS default.** A single `readLoop`
   goroutine drains the socket, so anything the kernel drops before it gets
   there is invisible loss. At the default (~208 KiB on Linux) a few hundred
   concurrent connections overflow the receive queue, which is what produced
   the lost SYN-ACKs in (1) in the first place. `Bind` now requests 4 MiB.

### Further defects found while fixing those

- **`UtpSocket.Close()` was not idempotent** and panicked closing an
  already-closed channel. `defer sock.Close()` alongside an explicit close is
  ordinary Go.
- **`UtpSocket.Close()` never closed the underlying UDP socket**, leaking the
  fd and leaving `readLoop` parked in `ReadFrom` — which cancelling the
  context does not interrupt. The port stayed bound for the life of the
  process.
- **An expired accept was dropped silently**, so `AcceptWithCid` blocked
  forever when the caller's context had no deadline of its own. Both accept
  paths also discarded the error field entirely.
- **`ConnectionId` built as a struct literal hashed to `""`.** The type is
  exported with exported fields, but its cached hash is only populated by
  `NewConnectionId`. Every literal-built id therefore collided with every
  other one, silently breaking connection lookup.
- **Accepts starved under load.** The socket event loop drained `incomingBuf`
  in a non-blocking select before looking at the accept channels; under load
  that channel is never empty.
- **The time wheel ran its expiry callback while holding its own lock.** The
  callback hands a packet to the connection event loop over a channel, and
  that same loop calls `put`/`remove`, which need the lock — a deadlock once
  the timeout channel fills.
- **The socket shutdown fan-out did a blocking send while holding
  `connsMutex`**, so one wedged connection could stall packet delivery for
  every other connection on the socket.

### Broken tests that the brief did not mention

Three further tests could not pass on a clean checkout, independent of any
library defect:

- `TestSocketReportsTwoConns` hung forever — it builds its `ConnectionId`s as
  struct literals, hitting the empty-hash defect above.
- `TestCloseErrorsIfAllPacketsDropped` gave `Close()` exactly the idle timeout
  it waits on, a dead heat it always lost.
- `TestManyConcurrentTransfers` and `TestManyTimeHugeData` both registered
  `/debug/fgprof` on `http.DefaultServeMux`, so running the package's tests
  together panicked with a duplicate registration.

`native/cgo` also did not build: it needed `utp_lib.h` and `libutp_lib.a`,
which were not vendored, so `go build ./...` failed for the whole module.

That package has since been removed rather than fixed. Despite the name, it
was not a libutp harness at all: its API (`udp_socket_create`,
`udp_stream_write`) is the FFI of the Rust `ethereum/utp` implementation this
codebase was ported from, not libutp's (`utp_init`, `utp_create_socket`,
`utp_process_udp`). It could never have tested interoperability with the C
library. `native/libutp/` replaces it and does.

## Verification, and what the numbers mean

All measurements here are **loopback on a single Linux machine**. No test has
run over a real network, an emulated one with configurable loss and delay, or
against another implementation.

Gates that pass:

- `go test ./...` — green.
- `go test ./integrated/ -run 'TestUdpTransfer|TestManyConcurrentTransfers' -count=10` — green, at the full default of 1000 concurrent transfers.
- `go test -race .` — green.
- `go test -race ./...` — green, whole suite, no data races, at
  `UTP_TEST_TRANSFERS=150 REPRO_N=120` (283 s for the integrated package).
- `go test -race ./integrated/ -run 'TestUdpTransfer|TestManyConcurrentTransfers' -count=3` — green, **but at `UTP_TEST_TRANSFERS=150`, not the default 1000.** See "Memory" below: the full 1000 does not fit under the race detector on a 16 GB machine.
- `go test ./netem/ -count=3` — green. The M1 gate; figures in [HARNESS.md](HARNESS.md).
- `go test -race ./netem/` — green, no data races.
- `go test -run TestConformance .` — the M2 corpus, nine cases, green.
- `scripts/check-libutp-reference.sh` — pass.

The `-race` gate is therefore met at 150 concurrent transfers and **unmet at
1000**. That is a limit of the environment rather than a known defect, but it
does mean the highest-concurrency path has not been proven race-free.

Throughput figures observed on loopback, for scale only — these are not
congestion-control results and say nothing about behaviour on a real path:

- `TestManyConcurrentTransfers`: 1000 concurrent 1 MB transfers in ~11 s.
  Previously this test failed after its 120 s budget.
- `TestUdpTransfer`: 16 MB single transfer in ~0.65 s.

### Memory

`TestManyConcurrentTransfers` at its default 1000 concurrent 1 MB transfers
peaks at approximately **5 GB RSS** without the race detector (sampled from
`/proc/<pid>/status` `VmHWM`). Under `-race` the same run reached 13.9 GB and
was OOM-killed by the container's cgroup at ~507 s.

Roughly 1-2 GB of that is the test's own buffers, and `ReadToEOF` grows its
result with `append` from zero capacity, so it transiently holds about twice
the final size per connection. The rest is the library's: `processReads`
allocates a full `MaxPacketSize` buffer for every read and hands the whole
buffer to the reader regardless of how many bytes were used, `readLoop`
allocates per packet, and each socket holds `socketEvents` and `incomingBuf`
channels with capacity 1,000,000 each.

This is not unbounded growth — the run completes and the memory is reclaimed —
but it is far more than the workload needs, and the M8 gate asks specifically
about memory behaviour. It has not been profiled properly.

`numTransfers` is overridable via `UTP_TEST_TRANSFERS` so the test can be run
where the default does not fit.

**Not measured at all:** goodput under loss, queueing delay, fairness against
TCP or against a second uTP flow, recovery from loss, behaviour at any RTT
above loopback. Those are the M1/M5 gates and they have not been built.

## The ack path, audited against libutp (M4)

Four findings, three fixed. Each was found by reading `send_ack`
(`utp_internal.cpp:770-830`) against `receiveBuffer.SelectiveAck` and
`connection.statePacket` line by line, then pinning the difference with a
corpus case.

### The selective-ack bitfield had no width limit

**Fixed.** `MAX_SELECTIVE_ACK_COUNT` was `32 * 63`, and the loop that built
the bitfield ran until every pending packet was covered — up to 2016 bits, a
252-byte extension.

libutp scans a fixed 30-entry window and always writes exactly one 4-byte word
(`utp_internal.cpp:797`, `:805-818`). A peer that leaves a gap open and then
delivers a packet far past it made us answer *every* subsequent packet with a
254-byte extension where libutp answers with six bytes.

`TestConformanceWideReordering` is the case: with a gap at `seq+1` and packets
at `seq+2`, `seq+3` and `seq+41`, we emitted `0300000080000000` against
libutp's `03000000`.

Measured on the 2%-loss emulated link (seed 23, deterministic), before and
after:

| | Goodput | Packets sent | Fast retransmits |
| --- | --- | --- | --- |
| Unbounded window | 1.82 Mbps | 451 | 56 |
| 30-entry window | 1.84 Mbps | 436 | 56 |

Identical recovery, fifteen fewer packets. The extra bits carried real
information; libutp does without them and lets the window slide forward as the
gap fills.

### Selective acks were still sent after the peer's FIN was reached

**Fixed**, though barely reachable — see the comment in
`connection.statePacket`. libutp suppresses the extension once
`got_fin_reached` is set: "we never need to send EACK for connections that are
shutting down" (`utp_internal.cpp:786-788`).

### The two inherited "consistent with the reference" claims

**Both verified**, and the citations now sit next to them. They had been
carried from the upstream port of `ethereum/utp` without anyone checking them
against `utp_internal.cpp` in this fork.

- The initiator initialises its ACK number to the SYN-ACK's sequence number
  minus one. libutp: `conn->ack_nr = (pk_seq_nr - 1) & SEQ_NR_MASK` on
  receiving a SYN-ACK in `CS_SYN_SENT` (`utp_internal.cpp:1871-1874`).
- STATE packets carry the next sequence number. libutp: `pfa.pf.seq_nr =
  seq_nr` in `send_ack` (`:781`), where `seq_nr` is the number the next data
  packet will take — it is assigned and then incremented in `send_packet`
  (`:1088-1089`), so a STATE never consumes one.

### No half-close: we tear the connection down on the peer's FIN

**Found, not fixed.** libutp keeps the socket in `CS_GOT_FIN` when the peer
closes its sending side; the connection stays alive until the local
application closes it too. We move straight to `ConnClosed` once the remote
FIN is reached and everything we sent is acked. Data arriving after that
point reaches a socket with no connection for it and draws a RESET, where
libutp stays silent.

Supporting a half-close means a new connection state and a write path that
survives the peer's FIN — a larger change than the audit that found it, and
one that should be made deliberately rather than as a drive-by.

`TestConformanceDataAfterReachedFin` pins the current behaviour and fails if
either side changes, so this cannot drift unnoticed.

## The loss-recovery path, audited against libutp (M4)

Four defects, all fixed, all on the same path: what happens after a selective
ack reveals a hole. Together they cost 48% of the goodput on a 2%-loss link.

### A packet declared lost was fast retransmitted on every subsequent ack

**Fixed.** libutp keeps `fast_resend_seq_nr` (`utp_internal.cpp:470`),
advances it past each packet as it resends it (`:1603`) and past the
cumulative ack (`:2186-2188`), and refuses to fast retransmit anything below
it (`:1537`, `:1560`). This fork had no equivalent: a packet went into
`lostPackets` and stayed there until its own ack came back, so every ack that
arrived in the meantime resent it again — and, because `DetectLostPackets`
kept returning it, halved the congestion window again each time.

Measured on the 2%-loss emulated link before the fix:

> fast resends: 52 total, 7 distinct sequence numbers, worst single packet
> resent 16 times

After: 8 total, 8 distinct, worst 1.

### One ack could fast retransmit an unbounded number of packets

**Fixed.** libutp resends at most four per ack — "Re-send max 4 packets"
(`utp_internal.cpp:1605-1606`). We resent every packet in the lost set, so a
single ack revealing a burst of losses answered with a burst of
retransmissions, onto a path that had just demonstrated it could not carry
one.

### The congestion window was halved once per lost packet

**Fixed.** libutp halves at most once per `MAX_WINDOW_DECAY` = 100 ms
(`utp_internal.cpp:51`, `:602-605`), and once per ack that resent anything
rather than once per packet resent (`:1609-1610`). We halved on every
`OnLostPacket` call, so four losses in one event — one queue overflow — took
the window to a sixteenth, and with the window pinned at its floor the
connection then crawled.

The metric that shows it: the fraction of the transfer spent with the window
at its minimum fell from 11.2% to 1.6%.

### A selective ack that acked nothing new was discarded entirely

**Fixed.** `sentPackets.onAck` skipped all processing when the packet's
`ack_nr` equalled the connection's initial sequence number — the guard exists
because that number names no packet we sent, so acking it would index out of
range. But the *selective* ack in the same packet names packets that did
arrive, and those are exactly what declares the first packet lost.

So when the very first data packet of a connection was lost, every selective
ack the peer sent about it was thrown away: nothing was ever declared lost,
no fast retransmit happened, and recovery waited for the retransmission
timeout. libutp processes the extension whatever `ack_nr` says
(`utp_internal.cpp:2289`).

### Together

On the 2%-loss emulated link (seed 23, deterministic, 256 KB):

| | Goodput | Elapsed | Retransmit rate | Window at floor |
| --- | --- | --- | --- | --- |
| Before | 1.88 Mbps | 1.118 s | 12.84% | 11.2% |
| After | 2.78 Mbps | 0.754 s | 2.03% | 1.6% |

The retransmit rate is the clearest of these: the link drops 2% of packets,
and the sender now retransmits 2.03% of what it sends. Before, it retransmitted
six times more than the link lost.

The loss-free link is unchanged (6.38 Mbps against 6.33 before, within
run-to-run noise), as it must be — none of this code runs when nothing is
lost. Both libutp interoperability directions still pass.

## Classic LEDBAT, verified against libutp (M5, first half)

The brief asks for LEDBAT to be verified before LEDBAT++ is added, on the
grounds that comparing a new controller against a broken one tells you
nothing. Seven defects, read out of `apply_ccontrol`
(`utp_internal.cpp:1615-1712`) and the timeout branch (`:1206-1228`).

Full before-and-after tables are in [BENCHMARKS.md](BENCHMARKS.md); the
summary is +42% to +80% goodput depending on the link, with queueing delay
flat or lower and Jain fairness unchanged at 1.000.

### There was no slow start

**Fixed.** libutp starts every connection in slow start with
`ssthresh = opt_sndbuf` (`utp_internal.cpp:2620-2621`), grows by a packet per
acked packet, and leaves on either crossing the threshold or the delay
reaching 90% of target (`:1691-1702`). It re-enters slow start on a
retransmission timeout (`:1227`) and leaves it on any window decay
(`:616-617`).

This fork had none of that. Every connection began at two packets and grew
only by the LEDBAT increment.

### The window factor was computed against the wrong window

**Fixed, and this is the largest of the seven.** libutp's factor is
`min(bytes_acked, max_window) / max(max_window, bytes_acked)`
(`utp_internal.cpp:1668`) — the acked bytes as a fraction of the *congestion
window*. We divided by the bytes currently **in flight**.

In flight is never more than the window and is often far less, so our factor
was systematically too large: with a single packet outstanding it reached 1,
and one ack claimed a whole round trip's worth of increase. There was also no
`min`/`max` pair, so the factor could exceed 1 outright.

### The window could grow by only 1024 bytes per round trip

**Fixed.** `MAX_CWND_INCREASE_BYTES_PER_RTT` is 3000 in libutp
(`utp_internal.cpp:43`). This fork used its packet size, 1024. On a 100 ms
path that is the difference between 30 KB and 10 KB of window per second.

### The queueing delay was never clamped to the round-trip time

**Fixed.** "the delay can never be greater than the rtt" —
`our_delay = min(our_hist.get_value(), min_rtt)` (`utp_internal.cpp:1617-1621`).

Without the clamp, a peer reporting a wild timestamp — by malice, by a clock
step, or by a timestamp wrap — drives `off_target` arbitrarily negative and
collapses the window in a single ack. This is the delay-based analogue of the
malformed-header cases in M2: an input the peer controls, used unchecked.

### A window the application never filled grew anyway

**Fixed.** libutp refuses to grow the window when the sender has not been
blocked by it in the last second (`utp_internal.cpp:1681-1686`), because a
sender limited by the application rather than the path measures nothing useful
about the path. It records this in `last_maxed_out_window`, set by `is_full`
(`:945`, `:957`).

We had no equivalent, so a connection trickling data grew its window without
bound and then dumped it all at once when the application sped up.

### There was no ceiling on the congestion window

**Fixed.** libutp clamps to `opt_sndbuf` (`utp_internal.cpp:1710`), whose
default is 1 MB (`utp_api.cpp:91`) — the same as `DefaultWindowSize` here. The
`ctrlConfig.WindowSize` field existed already and was never read.

### A timeout collapsed the window even when nothing was in flight

**Fixed.** libutp splits the case (`utp_internal.cpp:1216-1228`). With packets
in flight, the window resets to one packet and the connection re-enters slow
start. With nothing in flight the connection was merely idling, nothing was
actually lost, and the window decays to two thirds instead: "No need to be
aggressive about resetting the congestion window."

We collapsed to the floor in both cases. An application that paused long
enough to hit an RTO — routine — restarted from two packets, and with no slow
start took hundreds of round trips to recover.

## The queueing-delay metric was measuring the controller's opinion

**Fixed, and it changed a conclusion.**

`ConnectionMetrics.QueueingDelay()` is `PeerTsDiff - BaseDelay`, where
`BaseDelay` is the congestion controller's own estimate of the empty path. So
the metric asks the controller how much queue it is causing.

A delay-based controller whose base-delay estimate has drifted upwards --
which is precisely the failure LEDBAT++ exists to fix -- answers "almost
none", because it is subtracting its own standing queue from itself. Measured
on a 40 ms path carrying a sustained transfer, classic LEDBAT reported 500µs
of queueing delay while its round trip sat at 80 ms.

`Summary` now also carries `RTTMin`, `StandingQueueP50` and
`StandingQueueP95`, computed as the round trip above the lowest round trip
seen. That is measured rather than believed, and it is what
[BENCHMARKS.md](BENCHMARKS.md) judges "less than best effort" on.

This matters beyond the metric. The M5 correction's write-up originally said
the classic-LEDBAT improvements were free because "queueing delay went down".
They were not free and the queueing delay did not go down; the number that
said so was the controller marking its own homework. The corrected reading is
in BENCHMARKS.md.

## Classic LEDBAT does not yield, and that is the point of the protocol

**Measured, not fixed.** This is the most important thing in this file, and it
is not a defect in the port -- it is a property of what libutp implements.

uTP exists so a background transfer does not hurt the user's interactive
traffic. That claim had never been tested here, because the harness had no
loss-based traffic to yield to: every measurement was uTP against uTP, which
is the easy case. Two additions closed that -- a shared bottleneck
(`Network.ConnectShared`) so two flows contend for one queue, and a
Reno-shaped competitor (`netem.RunRenoFlow`).

Over a shared 10 Mbps bottleneck with a 256 KB queue, with the competitor
started first and given time to fill it:

| | uTP's share of the link | Competitor kept | Bottleneck queue p50 |
| --- | --- | --- | --- |
| LEDBAT | **60%** | 69% | **24.6 ms** |
| LEDBAT++ | **44%** | 77% | **2.5 ms** |

Classic LEDBAT takes more than half the link from the traffic it is supposed
to defer to, and leaves 25 ms of queue for everyone else. It is faster than
LEDBAT++ in every throughput table in [BENCHMARKS.md](BENCHMARKS.md) *because*
it is not yielding.

The default is unchanged, because matching libutp is this library's default
and a silent behaviour change is not the place to spend a deviation. But a
BitTorrent client should set `CongestionAlgorithm: AlgorithmLEDBATPP`, and the
reasoning is in BENCHMARKS.md rather than left implicit.

## The send buffer retained the caller's slice

**Fixed.** `sendBuffer.Write` stored the caller's `[]byte` by reference:

```go
sb.pending = append(sb.pending, data)
```

`UtpStream.Write` returns as soon as the bytes are accepted into that buffer,
which is before they are transmitted. So the caller got control back while the
connection still held a reference to their array. Anything that reused its
buffer between writes had the queued bytes overwritten with whatever it wrote
next.

That is not an exotic pattern. `io.Writer`'s contract is explicit —
"Implementations must not retain p" — and `io.Copy`, `bufio.Writer` and every
echo loop rely on it.

It shows up in any **full-duplex** exchange: read into a buffer, write it
back, reuse the buffer. Every test in this repository passed throughout,
because every one of them is unidirectional and writes each buffer once and
never touches it again. It was found by the first test that exchanged data in
both directions, written for the anacrolix integration, and it reproduced in
sixteen lines with no adapter involved (`TestFullDuplexEcho`).

BitTorrent peer connections are full-duplex by nature, so this would have
corrupted data on essentially every connection a torrent client made.

## UtpSocket.Connect never succeeded

**Fixed.** The initiator reported a completed handshake by *closing*
`connectedCh`; `UtpSocket.Connect` read it with `err, ok := <-connectedCh` and
treated `!ok` as failure, returning "connection timed out" after a handshake
that had in fact completed.

`ConnectWithCid` used the bare receive form, where a closed channel yields a
nil error, and so worked. Every test in this repository uses `ConnectWithCid`,
which is why the simplest and most obvious entry point in the public API had
never worked and nothing noticed.

The connection now reports success by sending `nil`, as the acceptor path
already did, so the two paths agree and `ok` means what it looks like it
means.

## Two API gaps this exposed

Both found by writing the first caller that treats a uTP stream as an ordinary
byte stream.

- **No incremental read.** `UtpStream.ReadToEOF` only returns once the whole
  transfer is complete, which cannot serve a connection that stays open.
  `UtpStream.Read` was added: it returns as soon as anything is available and
  reports `io.EOF` at end of stream, so a stream can be an `io.Reader`.
- **No way to learn the local address.** `Bind` with port 0 — the usual thing
  to do — gave no way to find out which port the kernel chose.
  `UtpSocket.LocalAddr` was added.

## There was no zero-window probe

**Fixed.** A peer that advertises a zero receive window stops this sender, and
nothing restarted it.

The peer sends a window update when its application drains its buffer, but
that update is a single packet on an unreliable path. Lost, it leaves the
sender with nothing outstanding — so no retransmission timer — and no reason
of its own to transmit. The connection stops dead with data queued, until the
idle timeout kills it sixty seconds later.

libutp arms a timer when an acknowledgement reports a zero window and, when it
expires, forces the peer's window up to one packet
(`utp_internal.cpp:2149-2151` and `:1142-1145`). One packet goes out, the peer
acknowledges it, and the acknowledgement carries a fresh window. Recovery
costs one packet per interval and needs nothing from the peer beyond an ack.

This is now implemented, with libutp's 15-second interval as the default. It
needed a wake-up of its own in the event loop: every other reason that loop
runs is an event — a packet arrived, the application wrote, a timer expired —
and a closed window is the absence of all three.

`TestZeroWindowProbe` fails without it, reporting the connection stuck.

## Nothing was ever sent on an idle connection

**Fixed.** libutp sends a keep-alive after 29 seconds of silence on an
established connection (`utp_internal.cpp:74`, `:1271-1274`) — a STATE
acknowledging one *less* than it actually has, so the packet reads as a stale
acknowledgement and the peer answers it without any sequence number being
consumed or data delivered (`send_keep_alive`, `:834-844`).

29 seconds is chosen to sit under the 30-second UDP mapping timeout common in
NATs. A connection quiet for longer loses its mapping and cannot be reached
from outside again.

This library sent nothing at all. It relied entirely on the peer speaking
first, which against another copy of this library means neither side ever
does.

## The packet size was smaller than libutp's, and now discovers itself (M6)

**Fixed, by implementing path-MTU discovery.**

libutp sizes packets from the interface MTU less the header, discovered by
probing (`get_packet_size`, `utp_internal.cpp:1757-1762`), which lands around
1400 bytes. This fork used a fixed 1024, because without discovery the only
safe fixed size is a small one: a datagram above the path's MTU is either
fragmented — costing more than it saves — or, on IPv6 and on any IPv4 path
with the don't-fragment bit set, silently dropped. A connection that picks too
large a size and cannot detect it does not run slowly; it stops.

Raising the constant to 1400 was measured first, as the send-path audit: +2.4%
on the long transfer, +17% on the reordering profile, within noise elsewhere.
It was reverted, because a few percent is not worth a connection that fails
outright on a PPPoE or VPN path.

`mtu.go` now implements libutp's search: a binary search between a 576-byte
floor and a ceiling, where each probe is an ordinary data packet sized at the
midpoint. Acknowledged, the floor rises to it; lost, the ceiling drops to just
below. The search stops when the two are within 16 bytes and restarts every 30
minutes, because paths change. All of it is libutp's —
`mtu_search_update` (`:1289-1312`), `mtu_reset` (`:1314-1322`), the probe
decision in `send_packet` (`:890-925`), the acknowledged path (`:1969-1974`),
and the two failure paths, retransmission timeout (`:1152-1167`) and duplicate
acknowledgements (`:1927-1940`).

**Why raising the ceiling to 1400 is now safe**, and the argument is asserted
as a test rather than left as prose: the ceiling is no longer what gets sent.
The search starts at the midpoint between 576 and the ceiling — 988 bytes,
below the 1024 this library used before — and grows only once a probe of a
given size has been acknowledged. An untested path gets a smaller packet than
it did before this change. A path that drops every probe settles on 576.

Two things libutp does that this does not:

- **Probes are not sent with the don't-fragment bit.** libutp passes
  `UTP_UDP_DONTFRAG` for the probe (`utp_internal.cpp:925`). This library
  writes through an abstract `Conn` — a UDP socket in production, an emulated
  link or a scripted transport in tests — and has nowhere to put that flag.
  The consequence is asymmetric: on IPv6, where routers do not fragment, an
  oversized probe is dropped and the search learns correctly; on IPv4 it is
  fragmented and acknowledged, so the search settles on a size that works but
  costs fragmentation. That is a performance loss, not a failure, which is why
  it is recorded rather than blocking.
- **The ceiling is a fixed 1400, not the interface MTU.** libutp asks the
  socket (`get_udp_mtu`, `:1316`). A fixed conservative ceiling cannot find a
  jumbo-frame path, and gives up about 6% of a 1500-byte one.

Measured with discovery, against the fixed 1024 it replaces: +2.1% on the long
transfer, +11% on reordering, +2.6% on two flows sharing a bottleneck, flat on
LAN, broadband and high-BDP. See [BENCHMARKS.md](BENCHMARKS.md).

## The retransmission backoff doubled every other timeout

**Fixed.** This connection arms one timer per outstanding packet, where libutp
has a single connection-wide RTO. The guard that reconciled the two compared
the time since the *last* timeout against the *current* RTO — a different
question, with a different answer. After a timeout doubled the RTO, the next
expiry arrived exactly one (old) RTO later, which is not more than the new
one, so it was not counted as a timeout: the backoff did not double, and the
packet was resent anyway. The result was two retransmissions per RTO value
instead of one.

The connection now keeps a single deadline of its own, as libutp does
(`rto_timeout`, `utp_internal.cpp:494`): armed when the first packet enters an
empty window (`:994-998`), reset when an ack retires something (`:1388-1389`),
compared against the clock before a timeout is declared (`:1147-1148`), and
moved forward by the doubled timeout on expiry (`:1204`).

Measured against libutp with a 200ms base, before and after:

| | retransmissions at |
| --- | --- |
| libutp | 1x, 3x, 7x, 15x |
| before | 0.6x, 2.6x, 4.6x, 8.6x, 12.6x, 20.6x, 28.6x |
| after | 1x, 3x, 7x, 15x |

Two things about the fix are worth recording because both were got wrong on
the way:

- **Resetting the deadline on every ack is not the same as resetting it on
  every acked packet.** Duplicate acks are how a peer reports a hole, so on a
  lossy path they arrive in a stream; resetting on each one pushes the
  deadline out indefinitely, the RTO never fires, and a packet fast
  retransmit cannot recover is never retransmitted at all. The 5%-loss and
  broadband benchmark profiles stopped completing — the receiver timing out
  after 76 seconds on a transfer that takes one.
- **The backoff happens once per expiry; the retransmission happens for every
  packet whose timer fired.** An attempt to return early without
  retransmitting, on the theory that only one callback per expiry is a real
  timeout, left holes fast retransmit could not fill. libutp marks *every*
  outstanding packet `need_resend` on an RTO (`utp_internal.cpp:1230-1237`).

## The timer wheel could fire a full interval early

**Fixed**, and its own test said it could not.

An item due in n ticks was placed n-1 slots ahead, on the reasoning that "the
next tick processes slots[current]". That holds only if the next tick is a
full interval away, which is true exactly when the wheel has just started. In
a live connection a timer is armed at an arbitrary point in the tick cycle, so
the item fired on the n'th tick from then — between n-1 and n intervals away,
up to a full interval **early**.

Early is the wrong direction: it resends a packet the peer was still going to
acknowledge. libutp cannot do it, because it compares the clock against
`rto_timeout` rather than trusting a timer (`utp_internal.cpp:1147-1148`).

`TestTimeWheelNeverFiresEarly` existed and passed throughout, because it armed
every timer immediately after creating the wheel — the one moment when the
off-by-one is harmless. `TestTimeWheelNeverFiresEarlyWhenArmedMidCycle` arms
part-way through the cycle instead, and against the old code a 20ms timer
fires at 5.4ms.

## A SYN could be delivered into an established connection

**Fixed.** The socket derived three candidate connection ids for every
incoming packet and delivered to whichever matched. Two of the three treat the
packet's id as a *send* id, which is right for an established connection and
wrong for a SYN: a SYN carries the sender's own **receive** id, so those
derivations alias it onto whatever local connection happens to hold that
number.

A SYN whose id equalled an outgoing connection's receive id was delivered into
that connection, where `onSyn` answered it with a RESET. libutp cannot do
this: it has exactly one lookup per case, and a SYN matching an existing
socket is rejected outright — "rejected incoming connection, connection
already exists" (`utp_internal.cpp:2957-2965`).

The spurious RESET is the visible symptom. What matters is that an off-path
attacker who guessed a connection id could otherwise inject a SYN into an
established connection.

Found by `FuzzDifferentialInitiator` on its first run — the target that
models a malicious *server*, which is the direction a BitTorrent client is
exposed to every time it dials a peer address from a tracker or the DHT.

## A peer could push us arbitrarily far ahead in sequence space

**Fixed.** libutp drops any packet whose sequence number is more than 1024 past
the next expected one, without buffering it, acking it, or letting it touch
congestion control (`REORDER_BUFFER_MAX_SIZE`, `utp_internal.cpp:54`, applied
at `:1890`). One that is *old* — up to 1024 behind — is dropped too, but
re-acked first, because the peer evidently missed the ack already sent.

This fork had no bound at all. A peer could name any sequence number in the
16-bit space and we would buffer the packet as out-of-order data and answer it
with a STATE. Unbounded pending state driven by a remote party, one emitted
packet per junk packet where the reference emits none, and a selective ack
naming nothing (the sequence number being far outside the 30-entry window).

## Packets acking data we never sent were processed

**Fixed, and it corrects a claim this repository made.** libutp validates the
acknowledgement number before anything else (`utp_internal.cpp:1794-1807`):

> ignore packets whose ack_nr is invalid. This would imply a spoofed address or
> a malicious attempt to attach the uTP implementation. acking a packet that
> hasn't been sent yet!

CONFORMANCE.md previously recorded that we "already match" here. We did not.
Two corpus cases happened to produce the same silence for other reasons, so
the agreement was incidental and the rule was never implemented. A single
`ST_DATA` whose `ack_nr` named a packet we had never sent was enough to show
it — once there was something generating packets nobody had thought of, and
libutp on hand to say what the answer should have been.

Both of these were found by `FuzzDifferentialResponder`, which is the M2
conformance harness and the M8 fuzzer wired together: the fuzzer generates the
packets, libutp decides whether the answer was right. Neither half could find
these alone. See [FUZZING.md](FUZZING.md).

## Failed connection attempts leaked socket state

**Fixed.** The socket was told to forget a connection from exactly one place:
the branch of the connection's event loop where the state machine reached
`ConnClosed`. A connection torn down by a cancelled context — which is what an
abandoned or failed connection attempt *is* — exited by a different path and
left its entry in the socket's connection table forever.

Measured before the fix: 40 attempts to a port with nothing on it left 40
tracked connections behind, permanently. A BitTorrent client dialling
unreachable peers, which is most of them, accumulates one per attempt.

The goroutines were fine throughout, which is why nothing that counted
goroutines had noticed — it is the map entry and its channel that leaked. The
event loop now reports shutdown on every exit path, with a bounded wait so a
connection outliving its socket cannot block on the report.

Found by `TestSoakAbandonedConnections`; see [FUZZING.md](FUZZING.md).

## packet.Encode could produce a packet this decoder rejects

**Fixed.** `Encode` took the header's extension byte from `p.Header`, which is
only correct for packets built by `PacketBuilder`. A *decoded* packet keeps
whatever extension byte the peer sent, while `Eack` is nil unless a selective
ack was retained — so a packet carrying a zero-length selective ack, the
extension-bits extension, or a chain continuing past the selective ack,
re-encoded to a header claiming an extension with no extension bytes after it.
This library's own decoder rejects that, with "insufficient length for
extension".

Latent: nothing on the live path re-encodes a decoded packet. But
`DecodePacket` is exported and its result has exported methods, so an external
caller can reach it, and a proxy or relay built on this library would have
emitted unparseable packets.

Found by `FuzzDecodePacket`; both minimised inputs are kept as regression
cases in `testdata/fuzz/`.

## Every data packet was acknowledged separately (M4b)

We sent one STATE packet for every ST_DATA packet received. libutp does not:
`utp_process_incoming` calls `schedule_ack()` (`utp_internal.cpp:2377`), which
only sets a flag, and the embedder calls `utp_issue_deferred_acks()` once after
draining a batch of datagrams (`utp.h:512-517`, `utp_internal.cpp:3796-3808`).
A batch of *n* data packets costs libutp one ack and cost us *n*.

Found by running both: with the harness already in place it took one test to
ask each side how many packets it emitted for a batch.

`connection.ackPending` now records that an ack is owed and `flushAck` sends at
most one at the end of the event-loop pass, with the priority drain extended to
pull up to 64 further queued events into the same pass. A FIN is still acked
immediately, because libutp acks it directly at `:2369-2370`, and because
deferring it emitted *nothing*: the connection is torn down in the same pass
and `statePacket()` then returns nil. Two conformance cases caught that within
a minute of the change.

### The first version of this measurement was measuring the scheduler

The conformance test initially asserted a fixed bound — libutp emits one ack
per batch, we emit at most two — and that is what a plain run showed, for
batches of 1, 2, 4, 8 and 16. Under `-race` it failed: 7 acks for a batch of 8,
and 5 for a batch of 16.

The first diagnosis was that the test injected packets too slowly, so a "batch"
was only as batched as the injecting goroutine was fast. That was real, and the
harness was fixed for it: `scriptedConn.hold`/`release` queue a whole batch
before any of it is delivered, which is the starting position libutp's embedder
gives it. Under `-race` the numbers improved and still failed — 9 to 14 acks
for a batch of 16.

The bound was wrong, not the harness. Our packets cross three goroutines
between the socket and the connection's event loop, so how many land in one
pass depends on whether they arrive faster than the connection drains them.
When they do not, each is acknowledged alone — which is correct, and is what
libutp does too when its embedder reads one datagram per batch. There is no
fixed ratio to assert, and asserting one meant asserting a property of the Go
scheduler on one machine.

What the conformance test asserts now is the structural claim: libutp emits
exactly one ack per batch (measured on every run, not hard-coded), and we emit
at least one and never more than one per data packet — which is precisely where
this fork used to sit.

### What the change is actually worth, measured under load

The saving is load-dependent, so it is measured under load, on the emulated
network, by `netem.TestAckCoalescingUnderLoad`: one verified 512 KB transfer,
counting packets offered to the forward link (data) against packets offered to
the return link (acks).

| Link | acks per data packet, before | after | after, under `-race` |
| --- | --- | --- | --- |
| 20 Mbps, 10 ms | 1.000 | 0.823 | 0.844 - 0.919 |
| 100 Mbps, 1 ms | 1.000 | 0.368 | 0.670 - 0.721 |
| 1 Gbps, 1 ms | 1.000 | 0.167 | 0.390 - 0.486 |

Before the change the ratio was exactly 1.000 at every rate — measured, by
stashing the change and re-running. After it, the ratio falls as the packet
rate rises, which is the behaviour deferring acks should produce: the busier
the connection, the more packets are already queued when a pass ends. At 1 Gbps
that is roughly six data packets per ack.

Only the highest rate is asserted, and loosely (at most 0.75). At the lowest
rate coalescing barely engages by design, so a slower machine could reach 1.000
there legitimately and the assertion would again be measuring the machine.

**Effect on throughput: none measurable.** Re-running the benchmark suite at
seven repeats put every profile inside its own run-to-run range (LAN 89.21 to
89.40 Mbps, broadband 6.40 to 6.40, long transfer 8.82 to 8.85, two flows 6.63
to 6.63 at Jain 1.000). That is the expected result: coalescing removes
*return-path* packets, and none of these profiles is ack-limited. It was kept
because it costs nothing and matches the reference, not because it was faster.
The claim being made here is only that it did not regress.

## libutp was wired up wrongly, twice, and both times it looked like a result

The largest gap COMPATIBILITY.md named was that libutp had never been run over
the emulated network: every congestion-control row there was *cited*, and every
number in BENCHMARKS.md compared this library against a previous version of
itself. `libutp_flow_test.go` closes it by driving `libutp.Driver` — which has
no socket, no clock and no threads by design — over a netem `Endpoint`, and
`netem.TestLibutpOverEmulatedNetwork` runs three flows over each of the
benchmark links: ours to ours, libutp to ours, ours to libutp.

The first two attempts produced tables that looked publishable and were wrong.
Both defects were in the harness, and both made libutp look worse than it is.

### The two clocks were in different epochs

The driver's virtual clock started at zero; this library stamps packets with
`time.Now().UnixMicro()` (`conn.go:1245`). uTP peers exchange timestamps and
each subtracts to get the other's one-way delay, so what libutp read as its
queueing delay was really the offset between two unrelated epochs. Its LEDBAT
controller then did exactly what it should with a delay signal pinned far above
target: it collapsed the window.

On the High BDP profile that was unmistakable — libutp emitted about two
packets per second and 2 MB had not transferred after 180 s. On the shorter
paths it was not: the offset is constant, so libutp's base-delay estimate
absorbs most of it, and the result was merely *low* rather than obviously
broken. Setting the driver's clock to the same wall-clock base fixed it, and
the High BDP transfer completed in 9.0 s.

The lesson is the one this whole file keeps repeating. The profile that failed
loudly was not the one the defect was worst on; it was the one where the
symptom could not be mistaken for a finding.

### libutp was never told the path MTU

Neither `driver.cpp` nor `bridge.cpp` registered `UTP_GET_UDP_MTU`. With no
callback, `utp_call_get_udp_mtu` returns 0 (`utp_callbacks.cpp:122`), so
`mtu_reset` sets `mtu_ceiling = 0` against a floor of 576
(`utp_internal.cpp:1314-1322`) and `mtu_search_update` settles on
`(576 + 0) / 2 = 288` bytes — the `mtu_ceiling - mtu_floor <= 16` test at
`:1303` underflows, so the search never converges and never recovers.

libutp was moving every payload in five times as many datagrams as it should.
That is not libutp's behaviour; it is the behaviour of libutp embedded
incorrectly, and this fork was the incorrect embedder. Both now report 1472
bytes — a 1500-byte Ethernet MTU less IPv4 and UDP headers.

It changed the interop numbers too, which is how much of an understatement it
was: our initiator to libutp went from ~430 to ~600 Mbps, and libutp to us from
~374 to ~430.

### What libutp does on these links

Seven repeats per cell, median with the range, 4 MB on the LAN profile and
1-2 MB elsewhere. Every transfer is byte-verified.

| Profile | go->go | libutp->go | go->libutp |
| --- | --- | --- | --- |
| LAN (1ms, 100Mbps) | 86.78 (84.10-88.95) | 33.50 (16.65-66.69) | 83.43 (72.99-90.57) |
| Broadband (20ms, 10Mbps) | 6.39 (6.28-6.40) | 4.19 (4.19-4.19) | 6.43 (6.21-6.45) |
| Broadband, 1% loss | 3.94 (3.91-4.05) | 2.80 (2.79-3.35) | 3.87 (2.69-4.06) |
| High BDP (100ms, 20Mbps) | 2.10 (2.08-2.10) | 1.86 (1.86-1.86) | 2.09 (2.07-2.10) |
| Shallow queue (16KB) | 5.62 (3.29-5.66) | 4.19 (3.35-4.19) | 5.56 (3.30-5.66) |

Two things are worth saying about this table and not more.

**Our sender is faster than libutp's on every profile**, by 34% on broadband
and 41% under 1% loss. That is a difference in how hard classic LEDBAT is
driven, not evidence that either is correct; BENCHMARKS.md already records that
our classic LEDBAT leaves 33 ms of standing queue on a 40 ms path, and a
controller that yields less will finish sooner. Read alongside that number, the
honest summary is that this fork's classic LEDBAT is *more aggressive* than the
reference, which is a finding about us, not a win.

**The LAN column is bimodal for libutp and not for us.** Twelve consecutive
runs came in at either ~505 ms or ~2.003 s, with queue drops in both — 6 to 20
per run — so it is not the presence of loss that separates them. The 1.5 s gap
is one retransmission timeout: libutp's RTO floor is 1000 ms, and
`utp_check_timeouts` refuses to do anything more often than every 500 ms
(`TIMEOUT_CHECK_INTERVAL`, `utp_internal.cpp:37`, `:3284-3285`), so a loss that
fast retransmit cannot cover costs between 1.0 and 1.5 s on a 1 ms path. Our
seven LAN runs showed no such mode. This fork has the same 1000 ms floor,
measured and asserted in `TestConformanceDataRetransmitSchedule`, so the
difference is in what reaches a timeout, not in the timeout.

### What this still is not

libutp is driven here by a Go loop, not by a production embedder: it is handed
packets, its clock is set, and `utp_check_timeouts` and
`utp_issue_deferred_acks` are called on a 200µs tick. That is a faithful
embedding — it is what `utp.h` asks for — but it is this fork's embedding, and
two defects in it have already been found by measuring rather than by reading.
Treat a third as likely rather than impossible. The numbers above are one
machine, emulated links, and no wide-area path.

## One retransmission schedule that came out doubling from the wrong origin

Seen once, in a full parallel `go test ./...`, and not reproduced since.
`TestConformanceDataRetransmitSchedule` measured our data retransmissions at:

    [189 413 839 1663 3289] ms, against a 200ms floor

The expected schedule is `[1 3 7 15] x 200ms` = `[200 600 1400 3000]`, which is
libutp's — an RTO that doubles, each retransmission timed from the one before
it. What was measured is `[1 2 4 8 16] x ~205ms`: the same doubling, but with
every deadline apparently taken from a fixed origin rather than from the last
transmission. There is also one retransmission too many.

**It has not been explained and it has not been reproduced.** Since then the
test has passed 3 times in isolation, 6 times alongside a running netem suite,
and 12 times with six CPU burners on a 4-core machine — the last of which
starves the Go scheduler far harder than the run that failed. Whatever the
condition is, plain CPU starvation is not it.

Two things are worth writing down rather than leaving implicit:

- **It is not ack coalescing.** That change touches only when a STATE packet is
  emitted; the RTO deadline, the backoff and the timer wheel are untouched by
  it, and this test passed on that commit under both `go test ./...` and
  `-race`.
- **The shape is specific enough to be a real bug rather than jitter.** Noise
  stretches a schedule; it does not turn `1, 3, 7, 15` into `1, 2, 4, 8, 16`.
  The most likely reading is that on some path the RTO deadline was not
  re-armed from the retransmission that had just gone out. This fork has had
  two defects in exactly that area already — the backoff that doubled every
  other timeout, and the deadline that duplicate acks pushed out forever — so a
  third would not be a surprise.

It is recorded here rather than fixed because a fix aimed at an unreproduced
observation is a guess, and a guess in the retransmission path is how the first
two got in. The test that caught it stays exactly as it is: it is real-clock
and it is load-sensitive, and both of those are why it saw this at all.

## Things found but deliberately not fixed

These are real and unresolved. Each needs a measurement harness (M1) or a
conformance corpus (M2) to change safely, and guessing at them without one
risks making things worse.

- **The per-connection event loop drains incoming packets ahead of everything
  else**, in a non-blocking select, before considering writes, reads or
  `unackTimeoutCh`. In principle a peer that keeps the inbound queue non-empty
  could defer this connection's retransmissions and writes indefinitely.

  **Measured, and deliberately left alone.** Three variants, on the loopback
  large-transfer test, which is library-bound rather than link-bound:

  | Variant | Throughput |
  | --- | --- |
  | As it stands (unbounded priority drain) | 333 Mbps (mean of 5) |
  | Priority drain removed entirely | **14 Mbps** (mean of 3) |
  | Drain bounded to 32 consecutive packets | 289 Mbps (mean of 9) |
  | Retransmission timeouts polled ahead of packets | 314 Mbps (mean of 6) |

  Removing the priority costs a factor of more than twenty. Processing acks
  is what opens the send window, so deprioritising them stalls the sender,
  and the loop then spends its time on `writable` signals with no window to
  use. This is load-bearing code, not an oversight, and anyone tempted to
  tidy it should read this table first.

  The two bounded variants cost somewhere between 6% and 13%, but this
  machine's run-to-run spread on that test is around +/-20%, so neither
  difference is resolvable without far more samples than the question
  deserves. The starvation they guard against is also self-limiting in
  practice: to keep receiving acks you must keep sending, which requires
  reaching the blocking select. It is a concern for an adversarial peer, not
  a well-behaved one.

  Revisit if an adversarial-peer scenario is ever tested (M8), with a
  measurement rig that can resolve 10%.
- **Packets are dropped when a connection's event channel is full**
  (`handleIncomingBuf` falls through to `default`). This is silent loss that
  uTP then has to recover from with retransmits.
- **Blocking channel sends inside the connection event loop.** `processReads`
  blocks on `c.reads` (capacity 100). A consumer that stops reading wedges the
  event loop; `UtpStream.Close()` waits on that same loop, so a close from a
  non-reading consumer can deadlock. The send is now context-aware, so a
  cancelled parent context breaks it, but the shape is still wrong.
- **`onData` resets the connection on an empty payload and then continues**
  processing the packet rather than returning.
- **An intermittent data race was observed once**, in an early `-race` run of
  `TestManyConcurrentTransfers` under conditions where many connections were
  hitting the 60 s idle timeout. It has not reproduced in any run since —
  including runs specifically targeting the teardown paths, three runs at 150
  concurrent transfers, and a full-suite `-race` pass — so I could not capture
  the report and cannot say what it was. It may have been fixed incidentally
  by the time-wheel or shutdown-fan-out changes. **Treat this package as not
  proven race-free under teardown-heavy load at high concurrency.**
- **Per-read allocation.** `processReads` allocates `MaxPacketSize` bytes per
  read and passes the entire buffer to the reader with a separate length,
  rather than a right-sized slice. See "Memory" above.

## API notes

- `ReadToEOF` returns a nil error on a clean end of stream, matching
  `io.ReadAll`. It previously returned `io.EOF` on one path and nil on
  another; the two in-tree callers disagreed about which to expect. Abnormal
  closes still carry their error (`ErrTimedOut`, `ErrReset`).
- `Accept`/`AcceptWithCid` can now return `ErrAcceptTimedOut`. Callers that
  previously blocked forever will now see an error.
- `AWAITING_CONNECTION_TIMEOUT` is a fixed 20 s and is not configurable.
