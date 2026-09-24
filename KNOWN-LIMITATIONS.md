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
| **M2** — conformance harness against real libutp | **Partly done.** Corpus comparing emitted packets field by field, including malformed and hostile headers, plus retransmission-schedule and ack-count comparisons measured against libutp on each run; see [CONFORMANCE.md](CONFORMANCE.md). Both roles are covered: `conformance_initiator_corpus_test.go` adds nine curated cases in the dialling role, on the virtual clock, comparing the SYN itself. Ack *latency* is still not compared, for a reason that is not about clocks — see [COMPATIBILITY.md](COMPATIBILITY.md). |
| **M3** — fix the failing transfer tests | **Done.** Root causes below; gates in "Verification". |
| **M4** — audit transfer paths against libutp | **Done.** The ack path, the loss-recovery path, the retransmission timers and the send path are all audited against libutp — the timers by measurement rather than by reading, see [CONFORMANCE.md](CONFORMANCE.md). Three findings from the send path below; the packet-size one is deliberately left to M6. |
| **M4b** — exhaustive libutp compatibility sweep | **Done, and the line-by-line sweep it deferred has since been done too** — see "The M4b sweep" below. It found the clock-drift penalty missing, now implemented and measured, and `utp_read_drained` missing, now implemented after two reverts that rested on a misattributed hang, and measured: a stalled reader's transfer finishes in 2.16s with it against 29.7s under libutp's own behaviour without it. Measuring it found two more defects: our sender overran the peer's window (fixed), and our keep-alive could leave 29 seconds late (also fixed). [COMPATIBILITY.md](COMPATIBILITY.md) records every protocol area, the *kind* of evidence behind it — differential, measured, interop, cited, or none — and what is unchecked. Writing it found one defect (an ack per data packet, below), and the largest gap it named — libutp had never been run over the emulated network — has since been closed: `netem.TestLibutpOverEmulatedNetwork` runs real libutp over the same links as the benchmark suite, which found two more defects. Both were in the harness, and both made libutp look worse than it is. |
| **M5** — verify LEDBAT, add LEDBAT++ | **Done.** Classic LEDBAT verified against `apply_ccontrol` and corrected (seven defects, +42% to +89% goodput). LEDBAT++ implemented from the draft, opt-in. Deference measured against a loss-based competitor over a shared bottleneck: classic LEDBAT takes 60% of the link from it, LEDBAT++ takes 44%. See [BENCHMARKS.md](BENCHMARKS.md). |
| **M6** — MTU path discovery | **Done.** Binary search between a 576-byte floor and a 1400-byte ceiling, probing with ordinary data packets, as libutp does. The ceiling could be raised from 1024 only because discovery makes it safe: an untested path still gets 988 bytes. Probes now carry the don't-fragment bit through `utp.DontFragmentWriter`, implemented for a real UDP socket on Linux, Darwin, the BSDs and Windows without adding a dependency, and compiling everywhere else against a stub. Implementing the bit exposed a second gap, since closed: libutp's duplicate-acknowledgement route to lowering the ceiling (`:1927-1940`) was missing, so a probe dropped for size during a bulk transfer taught the search nothing. With both, the search comes down from 1384 to 996 on a 1000-byte path and fragmentation falls from 3809 datagrams to 32. |
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

M7's hash-verified torrent transfer is no longer unmet:
`integration/anacrolix/TestTorrentTransferOverUtp` moves a 4 MiB torrent in
128 pieces between two `torrent.Client`s with this library as the only
transport, with BitTorrent's own piece hashes deciding whether what arrived is
what was sent. See "A real torrent" below. `CGO_ENABLED=0 go build ./...`
succeeds for the whole module, and so does `CGO_ENABLED=0 go vet ./...`. The
second was not true until recently: the root package's tests failed to compile
without cgo, because the scripted transport that pure-Go tests share lived in a
cgo-only file. It now has its own file, and 180 of the root package's 226 tests
run and pass without cgo. The 46 left out are the comparisons against libutp.

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

### ~~No half-close~~ — implemented

**Was: the one capability libutp had and this library did not.** Now closed.

libutp keeps the socket in `CS_GOT_FIN` when the peer closes its sending side,
goes on delivering to its application, and offers `utp_shutdown(s, SHUT_WR)`
for the other direction. This library moved straight to `ConnClosed` once the
remote FIN was reached, so a peer that closed its sending side took the whole
connection with it and whatever it was still sending back was lost.

`UtpStream.CloseWrite` and `utpnet.Conn.CloseWrite` now do what
`utp_shutdown(SHUT_WR)` does, and reaching the peer's FIN no longer ends the
connection. Proven against the reference:
`netem.TestCloseWriteDeliversWhatThePeerSendsAfterIt` half-closes, libutp sees
end of stream and sends 4096 bytes back, and all of it is read.

Three things had to be right, and each was wrong first:

- **`CloseWrite` cannot reuse `Close`'s wake-up event.** The event loop sets
  `stream.shutdown` from `streamShutdown`, so every half-close promoted itself
  to a full close and the read side died immediately. It has its own event now.
- **A FIN being acknowledged must not end a half-closed connection.** libutp
  destroys on that only when `close_requested` is set
  (`utp_internal.cpp:2178-2182`), and `utp_shutdown` does not set it where
  `utp_close` does. Without the same distinction the read side died as soon as
  the FIN came back.
- **The side that closes second still has to finish.** Removing the
  FIN-reached teardown outright left it waiting for a FIN acknowledgement that
  could never come, because the first closer's connection was already gone: the
  soak test measured 29 connections still tracked after 60 closed cycles and 48
  goroutines grown. It ends once its application has closed, its own FIN has
  gone out and everything it sent is acknowledged. libutp gets there by another
  route — the peer's socket answers the unmatched FIN with a RESET, and a
  socket with `close_requested` treats a RESET as a clean destroy
  (`:2865-2868`) — which this library cannot use, because its socket
  deliberately stays silent there so a half-closing peer is not told its
  connection broke.

Two details of that last point are worth keeping. `LocalFin != nil` is part of
the condition because `shutdown()` only emits the FIN once the send buffer has
drained, which makes its presence the proof that nothing is left to send;
without it, the momentary gap between a window emptying and the next packet
going out counted as "finished" and truncated a 512 KB reply. And the check had
to move out of `onPacket` into the event loop as well, because the second
closer sends its FIN into a peer that is already gone and no packet ever
arrives to trigger it again.

The soak test now finishes in **0.93s where it took 131s**, with 0 tracked
connections against 5 before: teardown no longer waits for a packet to notice
it.

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

Two things libutp did that this did not, when this was written. The first is
now closed and the second partly; what remains of it is recorded as a
deviation.

- **~~Probes are not sent with the don't-fragment bit.~~ Closed.** Probes now
  carry it through the optional `utp.DontFragmentWriter`, implemented for a
  real UDP socket on Linux, Darwin, the BSDs and Windows (PR 48; "The libutp
  API surface, audited function by function" in this file). What follows is
  the original note. libutp passes
  `UTP_UDP_DONTFRAG` for the probe (`utp_internal.cpp:925`). This library
  writes through an abstract `Conn` — a UDP socket in production, an emulated
  link or a scripted transport in tests — and has nowhere to put that flag.
  The consequence is asymmetric: on IPv6, where routers do not fragment, an
  oversized probe is dropped and the search learns correctly; on IPv4 it is
  fragmented and acknowledged, so the search settles on a size that works but
  costs fragmentation. That is a performance loss, not a failure, which is why
  it is recorded rather than blocking.
- **~~The ceiling is a fixed 1400, not the interface MTU.~~ Partly closed.**
  libutp asks the socket (`get_udp_mtu`, `:1316`). The ceiling is now the
  smaller of 1400 and what the interface reports, through the optional
  `utp.PathMTUProvider` (PR 40), so a narrow local link is found before the
  first packet. 1400 stays as a cap, which is recorded in DEVIATIONS.md ("A
  discovered path MTU only lowers the ceiling"). So a jumbo-frame path is still
  not found, and about 6% of a 1500-byte one is still given up.

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

## An extra retransmission, from a timer wheel that counts ticks and not time

Found, diagnosed and fixed, after three failed attempts. It is worth the space
because what made it hard was not the bug.

`TestConformanceDataRetransmitSchedule` normally measures our data
retransmissions at `[1 3 7 15]` times the RTO floor, which is libutp's
schedule. Roughly once in ten to twenty runs, under load, it measured five
retransmissions instead of four, at `[1 2 4 8 16]`: one extra transmission,
and every later one early.

### The cause

The retransmission wheel counts ticks. `timeWheel.put` places an item a whole
number of ticks away and never fires it early *in ticks*, which is what its
comment promises and what a previous fix established. Wall-clock time is a
different matter: under scheduling pressure the wheel's goroutine is
descheduled past a tick, and on resume it processes the pending tick
immediately, so eight ticks can elapse in slightly less than eight intervals of
real time. Measured, with the wheel instrumented:

    WHEEL-FIRE seq=7001 delay=200ms elapsed=198.644817ms early=true
    RTO-CHK    seq=7001 now-deadline=-1.314361ms isTimeout=false

`onTimeout` already guarded the *backoff* against this, comparing the clock
against `rtoDeadline` exactly as libutp does (`current_ms - rto_timeout >= 0`,
`utp_internal.cpp:1147-1148`). What it did not guard was the resend below it:
`c.retransmit` sat outside the branch, so an early callback sent the packet
again and re-armed at the **undoubled** RTO. From there the schedule ran
`1, 2, 4, 8, 16` -- correct doubling, one step out of phase.

### The fix, and why it is narrow

An early callback with **one** packet outstanding now re-arms for the remaining
time instead of resending. It is deliberately restricted to that case.

When several packets are outstanding they share an expiry: the first callback
is the real timeout, and its siblings arrive just after it, already past the
new deadline that branch sets. Every one of them still needs resending, because
libutp marks all outstanding packets `need_resend` on an RTO (`:1230-1237`), and
an earlier attempt to skip them left holes fast retransmit could not fill and
stopped the 5%-loss benchmark completing at all. With one packet outstanding
there are no siblings and nothing to be careful of.

Verified: 60 runs under the load that reproduced it, no occurrence. At the
observed rate of roughly 1 in 15, seeing none in 60 would happen about 2% of
the time by chance, so this is good evidence and not proof. The benchmark suite
at seven repeats is unchanged, including the 5%-loss profile that the
hole-filling regression destroyed -- 1.43 Mbps against 1.33 before, inside its
own range.

### Three attempts that found nothing, and why

The first two hunts blamed the measurement rather than the code, and both were
right about something and wrong about the bug.

The test originally timestamped its own *observation* of each packet, polling
every 2ms, so on a loaded machine it reported retransmissions tens of
milliseconds after they happened -- a 27% error against the 200ms base, enough
to fail the tolerance on its own. Fixing that (reading each packet's own
`Timestamp` header, stamped by the sender as it transmits) removed a real
source of false failures and left the anomaly untouched, which is what first
established it as a real emission.

The second attempt logged every ST_DATA emission and showed all six carried the
same sequence number and body: a duplicate of the original packet, with a
correct backoff from it. That ruled out a new packet reusing a sequence number,
and ruled out the socket write path duplicating a datagram.

Only the third -- logging the call site, the RTO at transmit time, and then the
wheel's own fire time against its delay -- named it. The chain that mattered
was: the extra transmission reports `rto=200ms` (undoubled), so `OnTimeout`
never ran, so `isTimeout` was false, so the callback was early, so the wheel
fired early in wall-clock terms despite being correct in ticks.

The instrumentation is not checked in. It is about fifteen lines across
`transmit`, `armRetransmit`, `onTimeout` and the wheel's expire function, and
the sequence above is what to re-derive if something like this recurs.

## Path-MTU discovery had never met a path that limits size

The emulated network had no MTU. Every link carried any datagram, however
large, so the MTU search always converged on whatever ceiling it was given and
the half of it that *lowers* the ceiling was never exercised end to end. The
search was tested against a harness that could not disagree with it.

`netem.Config.MTU` now drops datagrams larger than the path allows, counted
separately as `DroppedByMTU` because it is a statement about size and not about
congestion. The first run against it found two things.

### A fast retransmission could be adopted as an MTU probe

libutp will only probe with a packet that has never been transmitted --
`pkt->transmissions == 0` (`utp_internal.cpp:911`), and the comment above it
says why: a packet larger than the ceiling "was probably used as a probe
already and failed, now we need it to fragment just to get it through". This
library passed `firstTransmission=true` on the fast-retransmit path, so a
packet that had already been lost once could become the probe, and losing it
again lowered the ceiling on evidence about congestion rather than about size.

Found by reading, while looking for something else. **Its behavioural effect is
not demonstrated**: the search converges within the first few round trips, and
fast retransmit needs a window wide enough for three duplicate acks, so in
practice the two rarely overlap. It is fixed because it is a plain deviation
from the reference, not because a measurement forced it.

### The probe was never cleared unless it was the one that timed out

libutp clears `mtu_probe_seq` and `mtu_probe_size` on *every* retransmission
timeout, outside the branch that lowers the ceiling (`:1166-1167`): *"we
dropped the probe, clear these fields to allow us to send a new one"*. The
ceiling may only come down when the probe was the sole outstanding packet,
because only then does its loss say anything about size -- but the probe is
gone either way.

This library only touched probe state in the branch that lowered the ceiling.
A probe lost alongside other packets left `probing` set forever, and since
`eligibleProbe` refuses to start a second probe while one is outstanding, the
search froze. Now fixed, to match.

## The libutp API surface, audited function by function

**Two features were missing, and both are now implemented.** The audit ran
libutp's 27 public functions and 15 callbacks against ours rather than against
memory.

### `utp_shutdown(SHUT_RD)` — the read-side half-close

We had `CloseWrite` and no counterpart. `UtpStream.CloseRead` and
`utpnet.Conn.CloseRead` are it.

What makes this more than a flag is one line of libutp. `read_shutdown`
suppresses delivery to the application, but `conn->ack_nr++` sits *outside*
the guard (`utp_internal.cpp:2344-2355`, and again for the reorder buffer at
`:2392-2395`). Arriving data is acknowledged and dropped, not refused. The
obvious implementation — stop delivering, let the receive buffer fill — closes
the advertised window, and a peer that cannot finish sending cannot finish
closing either.

Measured: `utpnet.TestCloseReadKeepsThePeerSending` pushes 4MB through a
connection whose receive buffer is 1MB after the read side has closed, and
requires the peer to finish. The buffering variant was built and run: it
stalls, and the test fails at 31 seconds. `TestReadShutdownAcksWithoutBuffering`
asserts the two properties directly — the acknowledgement number advances, the
window does not shrink — and `TestReadShutdownStillReorders` covers the gap
case, since a discarded packet's sequence number still has to be recorded.

Data already buffered when `CloseRead` is called is discarded. libutp never
faces that question; the reasoning is in [DEVIATIONS.md](DEVIATIONS.md).

### `utp_writev` — the vectored write

`UtpStream.WriteV`, and `utpnet.Conn.WriteBuffers` in `net.Buffers` terms.

It was never a capability gap — `Write` can express anything `writev` can —
but it is a copy. A caller holding a header and a payload either joins them
(allocate the total, copy into it) and calls `Write` (which allocates the
total again and copies again), or calls this. Measured back to back, per
16.5KB write in three buffers:

| | Allocated per write |
| --- | --- |
| join, then `Write` | ~155KB |
| `WriteV` | ~136KB |

The ~19KB difference is one payload plus the noise of everything else a write
allocates. The wall-clock figures also favoured `WriteV` in every run, by more
than one 16KB `memcpy` can account for, so that difference is not claimed:
loopback throughput here is too noisy to attribute.

Two deviations, both recorded in [DEVIATIONS.md](DEVIATIONS.md): it blocks
(following `Write`'s contract rather than libutp's partial-write one), and it
has no `UTP_IOV_MAX`, because that cap is the size of a static array rather
than a protocol rule.

### What the audit found still missing, and why

**~~The MTU ceiling is a fixed 1400 rather than `UTP_GET_UDP_MTU`.~~ Closed.**
`utp.PathMTUProvider` is the optional interface this section proposed:
`PathMTU(ConnectionPeer) (int, bool)`, implemented by `UdpConn` and by
netem's `Endpoint`, and ignored by any other `Conn`. The `Conn` interface
itself is untouched, so the libutp driver and every other implementation are
unaffected and keep the configured ceiling.

`UdpConn` discovers it by asking the routing table which local address a
datagram to that peer would leave from — a UDP "dial", which sends nothing —
and then reading the MTU of the interface that owns that address. Per peer
rather than per socket, because split tunnelling routes two peers on one
socket out of two different interfaces.

**It only ever lowers the configured ceiling.** libutp adopts `get_udp_mtu`
outright; loopback reports 65488 and a jumbo-frame link 8972, and adopting
either would put this library on a probe schedule nothing has measured it at.
Raising the ceiling is a separate decision from fixing the case where 1400 is
too big. Recorded in [DEVIATIONS.md](DEVIATIONS.md).

What it is worth is in the stall section below: on the 1100-byte link that
defeats both implementations, the full megabyte now arrives.

A kernel's own path-MTU cache would be better still — Linux answers
`getsockopt(IP_MTU)` with what it has learned from ICMP for that route — but
it is Linux-only, needs a connected socket, and would make `golang.org/x/sys`
a direct dependency. Feeding ICMP in directly is the portable route to the
same information.

**~~Probes carry no don't-fragment bit.~~ Implemented, with a caveat that
matters.**

The old text called this blocked on `golang.org/x/net/ipv4`'s per-write
control messages. That was wrong twice over. `x/net/ipv4` has no
don't-fragment setter at all — its `dgramOpt` covers TTL, TOS, multicast and
BPF, and `DontFragment` appears only in `header.go`, for raw sockets. And no
platform offers DF as a per-datagram control message: it is a socket option
everywhere it exists.

Two things made it tractable. libutp does not set the option either — it
passes `UTP_UDP_DONTFRAG` to its embedder's sendto callback
(`utp_internal.cpp:928`) and leaves the mechanism to it — so parity means
carrying the signal, which `utp.DontFragmentWriter` now does, on the same
optional-interface pattern as `PathMTUProvider`. And every datagram this
library sends leaves through one goroutine, `UtpSocket.writeLoop`, so setting
the option, sending one datagram and clearing it again cannot race with
another write. That is what makes a socket-wide option usable per packet.

No new dependency. The constants come from the standard library's `syscall`
package where it has them (Linux, FreeBSD) and are spelled out with their
header citation where it does not (Darwin, Windows). Darwin's `IP_DONTFRAG` is
`0x1c` and FreeBSD's is `0x43`, which is why they are separate files. NetBSD
and OpenBSD get IPv6 only, because neither defines an IPv4 equivalent and
guessing a number would quietly set some unrelated option. Everything else —
js/wasm, wasip1, plan9, any future port — compiles against a stub and behaves
exactly as before. Verified by building 23 GOOS/GOARCH pairs with
`CGO_ENABLED=0`.

Linux uses `IP_PMTUDISC_PROBE` rather than `IP_PMTUDISC_DO`. Both set the bit;
the difference is whose estimate wins. `DO` defers to the kernel's cached
path MTU and refuses the send when the datagram exceeds it, which would cap
this library's own search at whatever the kernel already believed. The point
of a probe is to find out whether a size works, not to be told what is
currently assumed.

The bit alone was necessary and not sufficient: it makes a probe fail, and
something has to notice. Implementing it exposed that the noticing was missing
too, which is the section "A dropped probe teaches the search nothing during a
bulk transfer" below — now closed. With both,
`netem.TestDontFragmentBringsTheSearchWithinThePath` measures the search
coming down from 1384 to 996 on a 1000-byte path, and fragmentation with it
from 3809 datagrams to 32.

**No context-wide statistics.** `utp_get_context_stats` (packet-size
histograms), `UTP_ON_OVERHEAD_STATISTICS` (per-packet overhead accounting) and
`utp_socket_stats.nduprecv` have no equivalent. Pure telemetry; nothing has
needed it. `ConnectionMetrics` covers the rest of `utp_socket_stats` and a
good deal more.

**~~No injectable clock.~~ Half of it, and the half that pays.**
`ConnectionConfig.NowMicros` is where the connection reads the wall clock, in
the uint32 microseconds uTP puts on the wire. Every timestamp it stamps and
every one-way delay it derives goes through it — eleven call sites — and the
conformance corpus pins it to the libutp driver's virtual clock.

That removed `Timestamp` and `TimestampDiff` from the corpus's tolerated
differences, where they had sat since the corpus was written because the two
implementations could never agree on a wall clock. Comparing them found three
real divergences in the field that carries the peer's entire delay signal, all
now fixed and written up in [CONFORMANCE.md](CONFORMANCE.md): a measured delay
echoed on the SYN-ACK where libutp echoes zero, that value being updated from
packets libutp rejects, and a one-second cap that put a number on the wire no
libutp would send.

**It is not the whole thing, and the remaining half is the larger one.**
`NowMicros` governs what is stamped and measured, not scheduling: a connection
handed a frozen clock still retransmits on real timers and still emits when
its goroutine happens to run. Comparing ack *latency* needs time to advance
only when the harness says so, which means the retransmit wheel and the event
loop running on the virtual clock too. The old text is kept below because the
reason still stands for that half.

**~~No virtual timers.~~ Implemented.** `Clock` and `IdleBarrier`: the
connection's deadlines, its four event-loop timers and the socket's
retransmission wheel all come from an injectable clock, and `virtualClock`
moves only when a test says so.

Making a clock is trivial; knowing when to move it is not. Three things had to
be right, and each was wrong first.

**The loop must say when it is parked.** From outside there is no way to tell
"blocked with nothing to do" from "about to emit a packet". The obvious
design marks idle before the event loop's select and busy in each case body —
and that select has ten cases, so missing one puts the nondeterminism straight
back. The select was restructured to *receive without acting*: it records
which case fired, the barrier is marked busy once, and the bodies run from a
switch afterwards. The invariant became structural instead of a rule to
remember.

**A parked state can be stale.** A channel send returns long before the
receiver runs, so "everything is parked" immediately after a delivery is an
answer about the past. Acting on it dropped the next fire on a full channel:
the retransmission wheel ticks every 25ms, so a two-second advance should fire
it eighty times, and with the stale check it took delivery of one and dropped
seventy-nine. Nothing ever retransmitted.

**Parked is not the same as idle while something is in flight.** The wheel
delivers a timeout and parks; the connection has not woken yet; the system
looks quiet by every observable measure while work is outstanding. Time then
moved before the connection had re-armed, and the second retransmission landed
at 9.05s, 9.075s, 9.125s or 9.15s depending on the scheduler. Handoffs are now
counted — noted by the sender, taken by the receiver at the one point each
that owns the item — and deliberately *not* folded into MarkBusy, because a
participant wakes for reasons unrelated to any handoff and would clear the
count while the item was still queued.

With all three, the same run gives the same instants: bit-identical across
five runs and five `-race` runs.

### What it made measurable, and what that found

`netem` and the corpus could compare *what* a packet contained. This is the
first comparison of *when* one was sent. Both sides are read the same way --
from the timestamp field of the packets themselves, which each implementation
re-stamps on every transmission -- so neither number is inferred from wall
time and neither is sampled.

An unanswered SYN, retransmitted:

| | first | second |
| --- | --- | --- |
| ours | 3.025s | 9.05s |
| libutp | 3.0s | 9.0s |

**The error compounds, and that is a real property, not measurement noise.**
The retransmission wheel rounds a delay up to a whole 25ms tick and so fires
up to one tick late -- deliberately, since firing early resends a packet the
peer was still going to acknowledge. Each backoff then re-arms *relative to
the moment the last one fired*, so the lateness carries forward: one tick,
then two. libutp cannot accumulate it, because it compares the clock against
an absolute `rto_timeout` (`utp_internal.cpp:1147-1148`) rather than trusting
a timer.

The drift is bounded by the number of retransmissions times the tick, is
one-directional, and is in the safe direction. It is recorded rather than
fixed: making deadlines absolute is a change to the most defect-prone code in
this library for an error of tens of milliseconds against multi-second
timeouts, and the wheel's lateness is already a stated design choice.

### The inbound path, closed

The socket's read and event loops are barrier participants too, so the whole
chain from wire to connection and back is accounted for: read loop, socket
event loop, connection event loop, write loop, with each hop between them
counted as a handoff.

Two things were needed beyond registering the loops.

**The accounting has to be symmetric by construction, not by convention.** A
connection's event channel has several writers -- the socket's event loop, the
ICMP entry points, and the application's own `CloseWrite`, `CloseRead` and
`Close` -- and only the first is a clock participant. A global count would let
an application-driven close consume the note belonging to an inbound packet,
declaring the system quiet while that packet was still queued. Both halves are
keyed on the event type instead: only `streamIncoming` is noted, and only
`streamIncoming` is taken.

**A test's own injection is not a participant either.** Injecting a packet and
then asking whether everything is parked gets an answer about the moment
before the injection, when everything was indeed parked. `AwaitReactionTo`
captures the wake count first, so the wait cannot be satisfied by the past.

**What it measured, and it found nothing wrong.** How long after a packet
arrives the acknowledgement goes out:

| | reply |
| --- | --- |
| ours | same instant the packet arrived |
| libutp | same instant the packet arrived |

Both defer acknowledgements and flush them when something external says so --
libutp when its embedder calls `utp_issue_deferred_acks`, this library at the
end of the event-loop pass that received the packet -- so the same instant is
the right answer for both, and a non-zero delay on either side would have
meant the acknowledgement had slipped to a later pass than the packet that
prompted it. That it agrees is now checked rather than assumed.

**Ack latency specifically is still uncompared**, and for a reason that is not
about clocks: both implementations flush deferred acks when something external
says so -- libutp when its embedder calls `utp_issue_deferred_acks`, ours when
an event-loop pass ends -- so the comparison would measure harness cadence
rather than either implementation.

The old text is kept below, because the `time.Now()` count in it is what the
work above replaced.

**Formerly: no injectable clock at all.** The connection read `time.Now()` in dozens of places
and the retransmit wheel uses real timers. Threading a clock through is
mechanical but touches the whole timing surface, which is the part of this
library that has produced the most defects. It is also the gap with the
largest payoff: it is what stops the conformance corpus comparing ack
*latency*, and it would remove the settle-loop timing assumptions that had to
be repaired when the ICMP tests shifted the load.

### ~~`their_hist` is a missing mechanism~~ — implemented

**Closed.** The history below is kept because the route to it was instructive.

**Was open.** This was first recorded as observability — "`utp_get_delays`'
`theirs` is not tracked" — and that was wrong. `their_hist` has a functional
role in libutp: correcting for clock skew between the two ends.

```cpp
uint32 prev_delay_base = conn->their_hist.delay_base;
if (their_delay != 0) conn->their_hist.add_sample(their_delay, conn->ctx->current_ms);

// if their new delay base is less than their previous one
// we should shift our delay base in the other direction in order
// to take the clock skew into account
if (prev_delay_base != 0 &&
    wrapping_compare_less(conn->their_hist.delay_base, prev_delay_base, TIMESTAMP_MASK)) {
    // never adjust more than 10 milliseconds
    if (prev_delay_base - conn->their_hist.delay_base <= 10000)
        conn->our_hist.shift(prev_delay_base - conn->their_hist.delay_base);
}
                                        (utp_internal.cpp:2002-2014)
```

Two clocks drifting apart move *both* directions' delay bases together, so a
drop in the peer's base is evidence of drift rather than of an emptier queue —
and libutp shifts its own base by the same amount, at once.

This library has the raw value: `connection.peerTsDiff`, reported as
`ConnectionMetrics.PeerTsDiff`, is exactly libutp's `their_delay`. What it has
no history of, so no base for, and therefore no correction from. Drift is
corrected only by the 120-second sliding-window minimum in `delayAccumulator`
ageing out its stale samples.

**Now measured.** `netem.DriftingClock` wraps a `Conn` and rewrites the
timestamp its packets carry, which is exactly what a clock with a rate error
does; before it, both endpoints read the same process clock and no skew could
be produced at all.

### The rule

The reported queueing delay settles at **the delay window multiplied by the
drift rate**. The base delay is a sliding-window minimum, so it does follow the
drift — but with a lag: if the measured delay grows at rate *r*, the lowest
sample still inside a window of length *w* is the one from *w* ago, so the
sender believes in *w × r* of queue that does not exist.

Measured on an application-limited flow over a link carrying a fraction of its
capacity, so the only queue in the signal is the phantom one
(`netem.TestClockDriftInflatesTheDelaySignal`):

| Drift | Predicted *w × r* | Measured | |
| --- | --- | --- | --- |
| 5000ppm | 10ms | 9.86ms | 0.99x |
| 10000ppm | 20ms | 19.77ms | 0.99x |
| 20000ppm | 40ms | 39.63ms | 0.99x |

The link's real queue was 329µs throughout and the undrifted control saw
782µs. Measured with a 2-second window and large rates rather than the real
window and real rates, because only the product matters and the real
combination needs minutes per point; the linearity that extrapolation rests on
is itself what the three rows measure.

At the default 120-second window: 100ppm (a good crystal) is 12ms of phantom
queue, 500ppm is 60ms, and 1000ppm — which an oversubscribed virtual machine's
clock really does reach — is 120ms, above the 100ms target the controller aims
for.

libutp is exposed to this **6.5x more**, not less: its base is the minimum over
thirteen one-minute buckets (`DELAY_BASE_HISTORY`, `utp_internal.cpp:50`),
about 780 seconds against this library's 120. That is a large part of why it
also carries the `their_hist` correction.

### What it costs, which is not what it looks like

A single flow with the link to itself loses almost nothing. At 139ms of phantom
queue — well above target — throughput was within **1%** of the undrifted run.
The controller does back off, and the real queue it left at the bottleneck fell
from 29ms to 24ms; but a delay-based controller that backs off while still
filling the pipe has lost nothing worth measuring. That run is not kept as a
test, because a 1% difference on a saturated link is not distinguishable from
noise and asserting on it would be asserting on noise.

**The cost is fairness.** LEDBAT exists to yield, and a flow that believes it is
above target when it is not yields more than its share. Two flows through one
20Mb/s bottleneck for 14 seconds, one drifted and one not
(`netem.TestClockDriftYieldsShareAtASharedBottleneck`):

| | Delivered | Share | Queue it believed in |
| --- | --- | --- | --- |
| drifted | 12,713,984 | 35.0% | 125ms |
| clean | 23,658,496 | 65.0% | 28ms |

So the drifted flow takes a little over half what its neighbour does. Real, and
worth correcting, but a fairness loss rather than the collapse the mechanism's
absence sounded like.

### The correction, and three things that had to be got right

`defaultController.OnPeerDelay` is libutp's `their_hist` bookkeeping and the
shift it triggers. Result, against the same measurements:

| | Phantom queue left | Share of a shared bottleneck |
| --- | --- | --- |
| uncorrected | 9.87 / 19.79 / 40.56ms (99% of *w x r*) | 35.0% |
| corrected | 51 / 79 / 112µs (0.3-0.5%) | 51.8% |

Three things had to be right, and each was wrong first.

**The drift model was half a clock.** `DriftingClock` rewrote the timestamps on
outgoing packets and nothing else. That is enough to produce a perfectly
convincing phantom queue — the peer's view of this sender is what LEDBAT runs
on — but a host does not have one clock for stamping and another for reading.
The same slow clock also shrinks the `now - peer_timestamp` it computes for
arriving packets, and *that* is the only evidence clock drift can be told apart
from a real queue by. The first correction, built against the half model, did
nothing at all, for the right reason.

**The evidence was being thrown away by a cap.** `peerTsDiff` is capped at one
second as an unusable clock reading before being echoed back to the peer. A
clock drifting slow makes the inbound measurement shrink until it passes zero
and the subtraction wraps — at 100ppm that is ten milliseconds every hundred
seconds, so on an ordinary path it happens within minutes — and a wrapped value
capped to one second is a base that has stopped moving. The correction
plateaued at about 10ms in every run whatever the drift rate, which is how long
it took the accumulated skew to pass the emulated path's one-way delay. libutp
has no such problem: `their_delay` is a raw wrapping `uint32` throughout and
every comparison goes through `wrapping_compare_less`. `peerDelayHist` now does
the same, and the controller is fed the uncapped value.

**The correction has to age out.** Shifts arrive for as long as the peer's base
keeps falling, which under sustained drift is forever, and they accumulate at
the full drift rate — while the error they cancel is only *w x r*, a constant.
libutp bounds this without appearing to: its shift raises stored history
buckets, and one bucket is rotated onto a fresh unshifted sample every minute,
so no correction outlives its bucket. An unbounded version drove the base past
every sample and left the flow **delay-blind** — 4ms of perceived queue against
its undrifted neighbour's 40ms, and **56.6%** of a shared link, winning by
ignoring congestion rather than by being treated fairly. Samples are now stored
net of the correction standing when they were pushed, so each carries only what
has accumulated since it entered the window: libutp's rotation expressed
continuously.

The residual 0.3-0.5% is the lag between a fall in the peer's base and the
shift that answers it. It scales with the drift rate rather than vanishing.

`ConnectionConfig.DelayWindow` was added to make any of this testable —
the window was a hard-coded two minutes, and no drift experiment can run in a
reasonable time against that.

### A metric that subtracted two different directions

**Fixed.** Found while writing up the reasons above.
`ConnectionMetrics.QueueingDelay` computed `PeerTsDiff - BaseDelay`, and those
are measurements of opposite paths: `BaseDelay` is the base of the series the
peer reports about *our* outbound packets (libutp's `our_hist`,
`utp_internal.cpp:2016-2021`), while `PeerTsDiff` is our own measurement of the
*inbound* path (`their_delay`, `:2000-2001`). The difference between two
directions is not a queue in either.

On a symmetric emulated link the error is small, which is why it survived. On
an asymmetric one it is the whole asymmetry:
`netem.TestQueueingDelayMeasuresTheOutboundQueue` runs a 10ms outbound and
100ms inbound path with no bottleneck at all, and reports both figures from the
same samples — 1.9ms from the corrected metric, 90.7ms from the old one, on a
path with no queue.

`ControllerStats` now carries `CurrentDelay`, the newest sample of the same
series `BaseDelay` is the minimum of, and the metric is their difference.
`netem.TestQueueingDelayTracksARealQueue` checks the other half against the
emulated link's own measurement at the bottleneck: 78.9ms reported against
84.7ms measured.

**The congestion controller was never affected.** It compares a sample against
the base of the same series, which it has in hand at the point it acts
(`defaultController.OnAck`). What the broken metric fed was the queueing delay
`netem.Recorder` reports, which is diagnostic.

The doc comment also claimed this was "the number the M5 gates are judged on".
It was not — those gates read the emulated link's `MeanQueueDelay`, measured at
the bottleneck rather than inferred from timestamps, which is why they stayed
sound while this did not. **A comment asserting that something is load-bearing
is not evidence that it is**, and this one had been read as such more than
once.

## An acknowledgement the connection could not match killed it

**Fixed.** Found by the ICMP work, not by looking for it: the new tests shifted
the load enough to make `integrated.TestCloseSucceedsIfOnlyFinAckDropped` fail
about one run in eight, reporting "invalid ack number" where the test expects
the idle timeout. On the tree before those tests it still failed 1 run in 8;
under the load of a full run, 2 in 4.

`sentPackets.onAck` returned `ErrInvalidAckNum` for an acknowledgement number
outside the range it was tracking, and `processAck` turned that into
`connection.reset`. libutp does no such thing:

```cpp
int acks = (pk_ack_nr - (conn->seq_nr - 1 - conn->cur_window_packets)) & ACK_NR_MASK;
// this happens when we receive an old ack nr
if (acks > conn->cur_window_packets) acks = 0;
                                        (utp_internal.cpp:1904-1907)
```

The acknowledgement covers nothing. The connection carries on.

This was already written down: [CONFORMANCE.md](CONFORMANCE.md) listed it under
"State is compared only through the wire" as a divergence the packet-injection
corpus *cannot see*, because both implementations answer such a packet with
silence and the transcripts therefore agree while one connection is dead and
the other is not. It sat there as a note because nothing had shown it costing
anything. It was costing a connection.

Two ways it bites:

- **An ordinary stale acknowledgement.** libutp's own pre-filter
  (`invalidAckNum` here, `utp_internal.cpp:1794-1807`) already drops anything
  acking a packet never sent, so what reached the reset was an acknowledgement
  *behind* the tracked range but still inside the three-packet tolerance below
  the last sequence number sent. A delayed or duplicated `ST_STATE` early in a
  connection is exactly that, and on a lossy path it is routine.
- **A forged one.** Anyone who can guess a connection id and source-spoof a
  single `ST_STATE` inside that narrow band ends an established transfer.

`processAck` now ignores the acknowledgement and returns. The selective-ack
extension on such a packet goes with it, where libutp would still apply it;
that is deliberate and recorded in [DEVIATIONS.md](DEVIATIONS.md), because our
extension is applied relative to the same acknowledgement number that has just
been established to be outside the window.

Asserted on connection state rather than on emitted packets, since that is the
whole reason the corpus missed it: `TestStaleAckIsIgnoredNotFatal` (which
first checks that the pre-filter does *not* reject its packet, so it cannot
pass by never reaching the branch), `TestAckForAPacketNeverSentIsDropped`, and
`TestOnPacketInvalidAckNum`, which previously required the opposite. The
integrated test then passed 8 runs out of 8; more to the point, the error it
was failing with no longer exists.

## A path MTU below the size already adopted stalls the connection

**Half closed, and the remaining half is libutp's limitation as much as ours.**
The most serious thing the MTU harness found.

The search raises the size it sends at whenever a probe is acknowledged. If the
path limit falls between two probe sizes, the size it adopts next cannot
arrive -- and because every data packet is then built at that size, nothing
arrives at all.

**What has changed: the search recovers now.** This section used to say the
ceiling only comes down when a probe times out as the *only* packet outstanding
(`utp_internal.cpp:1152-1160`), which a stalled window never reaches, and
concluded that fixing it "means diverging deliberately". That conclusion was
reached without knowing libutp has a second route -- three duplicate
acknowledgements pointing at the packet before the probe (`:1927-1940`) -- and
was wrong on both counts. The route is libutp's own, so implementing it is a
correction rather than a divergence, and it works with the window full.
Measured over the same 1100-byte link, with the skip on
`TestMtuSearchCannotRecoverFromAPathLimitBelowItsChoice` lifted:

| | floor | current | ceiling |
| --- | --- | --- | --- |
| before the duplicate-ack route | 576 | 1191 | 1400 |
| after it | 982 | **1083** | 1184 |

**What has not changed: the transfer still stalls.** uTP numbers packets, not
bytes, so the packets already built at 1191 cannot be re-cut smaller -- that
would renumber everything behind them -- and the peer will never acknowledge
packets it cannot receive. The connection recovers its *sizing* and not its
*window*, so a 1MB transfer on a connection that has already stalled still
delivers a few kilobytes and times out.

That part is genuinely shared with the reference, and libutp's number is
measured rather than read: `TestLibutpStallsOnAPathItCannotFit` runs it over
the same link, where it sends ~1230-byte packets, delivers 20 bytes, and
reports a connection error. The distinction matters, because reading libutp's
source alone suggests it recovers.

Note also that the recovered ceiling, 1184, is still above the 1100-byte path:
the search reaches a workable size without being confined to one. Only an ICMP
report brings the ceiling onto the path exactly -- see below, where that is now
what the report is measured to be worth.

**It matters in practice.** A path MTU below 1400 is ordinary -- PPPoE at 1492,
and most VPN and tunnel paths -- and a BitTorrent client on one would stall.
The reason it is not already a known disaster in the wild is presumably that
libutp takes its ceiling from the local interface MTU, so the common case is a
ceiling that already matches the path. This fork caps its ceiling at 1400,
recorded in [DEVIATIONS.md](DEVIATIONS.md), and lowers it to the interface MTU
where the `Conn` can report one (below). On a tunnel whose narrow link is not
the local interface, 1400 can still be too big.

### Prevented, where the narrow link is the local one

**This is the case that is now fixed rather than mitigated.** The stall above
is unrecoverable because the search *adopts* a size the path will not carry;
the way out is never to adopt it. `utp.PathMTUProvider` sets the ceiling from
the local interface before the first packet goes out, which is exactly what
libutp's `UTP_GET_UDP_MTU` callback is for.

Measured over the same 1100-byte link, `netem.TestPathMTUReportPreventsTheStall`
against the identical run with the report withheld:

| | Delivered | Time | Refused for size | Search settled at |
| --- | --- | --- | --- | --- |
| sender told what its interface carries | 1,048,576 / 1,048,576 | 0.69s | 0 | 1084 |
| not told (the control) | 2,800 / 1,048,576 | timed out at 60s | 29 | 1191, ceiling 1400 |

This covers the common real case — a host behind a VPN or tunnel, where
WireGuard's default of 1420 and IPv6's floor of 1280 both sit below the
1400-byte datagram this library would otherwise adopt. It does **not** cover a
narrow link further along the path, where the local interface is wide and some
hop in the middle is not. For that, the ceiling still starts too high, and the
ICMP path below is what there is.

### Half of it is now addressable: the ICMP path

`UtpSocket.ProcessICMPFragmentation` is libutp's
`utp_process_icmp_fragmentation`, and an embedder that feeds it (on Linux,
`IP_RECVERR` on the UDP socket) gets the router's own figure instead of having
to infer the limit from a probe that vanished. Measured over the same 1100-byte
link, `netem.TestIcmpBringsTheSearchWithinThePath`:

| | Search settles at | Ceiling |
| --- | --- | --- |
| router silent | 1191 bytes -- above the path | 1400 |
| router reports | 1094 bytes -- within it | 1094 |

**It fixes the search, not the stalled window, and the test says so.** Packets
already built keep their size: uTP numbers packets rather than bytes, so one
cannot be re-cut smaller without renumbering every packet behind it. libutp has
the same constraint and gets away with it by not setting don't-fragment on
ordinary data -- "now we need it to fragment just to get it through"
(`utp_internal.cpp:898-905`) -- leaving the router to fragment what it cannot
forward whole. On a path that refuses oversized datagrams outright, which is
what IPv6 does and what Linux's default `IP_PMTUDISC_WANT` makes an IPv4 path
do, the packets already in the window stay stuck under either implementation.

So the report helps a connection that has not yet adopted an unusable size, and
every connection opened afterwards; it does not rescue one that already has.

## A completed transfer could end as a connection reset for the peer

Found by running libutp over a path that damages packets, which is the gap
COMPATIBILITY.md named: the interop gate runs on loopback, which loses nothing,
so nothing had ever exercised recovery *between* the two implementations.

**4 of 20 seeds failed**, on a path with 3% loss, 2% reordering and jitter,
with libutp sending and this library receiving — the direction a peer uses when
it sends us a file. libutp reported `UTP_ECONNRESET`. In every case this side
had already read all 131,072 bytes without error: the transfer arrived whole,
and the *close* failed.

### What happens

1. libutp finishes sending and closes, so it sends a FIN.
2. We acknowledge the FIN, hand the application its end of stream, and tear the
   connection down.
3. That acknowledgement is lost — ordinary on a lossy path.
4. libutp retransmits the FIN, or a data packet whose acknowledgement was also
   lost.
5. The socket no longer has the connection, so it answers with a RESET.
6. libutp reports the connection reset on a transfer that in fact succeeded.

libutp cannot do this in return, and the reason is the half-close recorded in
[DEVIATIONS.md](DEVIATIONS.md): it keeps the socket in `CS_GOT_FIN` until its
own application closes, so a retransmission of something it has already taken
is simply acknowledged again. Until now that deviation had no measured cost
attached to it. It has one.

### The fix

The connection hands the socket the acknowledgement it sent for the peer's FIN
on its way out, and the socket keeps it for ten seconds. A FIN or data packet
arriving for a connection that has just closed is answered with that
acknowledgement instead of a RESET.

Two restrictions matter. It is checked only after every connection lookup has
missed, so a connection that still exists always wins. And what it re-sends is
only ever the acknowledgement the connection actually sent, for a packet at or
below it — a retransmission of something already taken.

A packet *past* that acknowledgement is the other half of the same case: data
the peer wrote after we said we were finished. libutp, holding the socket in
`CS_GOT_FIN`, takes it and says nothing on the wire. We cannot take it, but we
can stop answering it with a RESET, and that is what we do — the socket drops
it in silence. `TestConformanceDataAfterReachedFin` pins both halves against
real libutp, and caught the first version of this fix twice: once when it was
too broad and re-acked data past the FIN, and once when it was too narrow and
still sent the RESET.

Making that work took one more thing than it looks like. The acknowledgement to
repeat has to exist, and a connection that only ever sent data — an initiator
that writes, then closes — has never emitted a standalone STATE. So `emit`
records the last STATE it built, `shutdown` records the acknowledgement it is
sending alongside its FIN, and both of `shutdown`'s FIN-sending paths do it;
the second path is the one `TestPeerMayWriteAfterOurFin` found still sending
RESETs after the first was fixed.

This is still not a half-close. A half-close would let the application keep
reading and writing after the peer's FIN, which is a larger change and still
unmade. This only stops a finished connection lying to its peer about how it
ended.

Measured after: **0 of 30 seeds fail, and the socket sends no RESETs at all.**
`netem.TestLibutpCleanCloseSurvivesLostFinAck` pins the deterministic case
(seed 3, which failed 5 times out of 5), `netem.TestPeerMayWriteAfterOurFin`
pins the half-close case — libutp writing 4 KiB after our `Close()`, which
failed 3 times out of 3 with `UTP_ECONNRESET` before this — and
`netem.TestLibutpInteropUnderAdverseConditions` covers loss, reordering, jitter
and all three together, in both directions, asserting on each profile that the
link really did damage packets.

## libutp is not thread-safe across contexts, and only concurrent interop found it

**A harness defect, fixed in the harness.** Everything the interoperability
gate proved, it proved one connection at a time. A BitTorrent client does not
run that way: it holds tens of connections on one UDP port, all live at once.
So the gate grew a concurrent case -- 16 libutp peers against one socket of
ours, in both roles, each payload stamped with its peer's index so a byte
delivered to the wrong stream names where it came from.

It failed immediately, and not in the way it was written to catch. Five
streams of eight on the first run and seven on the next -- the count moves with
the interleaving -- carried chunks of *other* peers' payloads, at matching
offsets:
peer 0's stream held 2904 bytes of peer 3's data, and peer 1's stream held
2904 bytes of peer 0's. At two peers instead of eight, libutp aborted the test
process on an assertion of its own:

> `utp_internal.cpp:1070: void UTPSocket::write_outgoing_packet(...): Assertion 'needed == 0' failed.`

The cause is upstream, and it is worth knowing for anyone embedding libutp:

```cpp
ssize_t utp_writev(utp_socket *conn, struct utp_iovec *iovec_input, size_t num_iovecs)
{
    static utp_iovec iovec[UTP_IOV_MAX];
                                        (utp_internal.cpp:3154-3156)
```

That `static` is process-wide. `write_outgoing_packet` copies payload out of
it and advances its pointers as it goes (`:1057-1066`), so two threads inside
`utp_writev` walk the same array: each takes bytes the other had already
claimed, and when the interleaving is unlucky one of them runs out and trips
the assertion at `:1068`. `utp_utils.cpp` has process-wide statics of its own
(`:86`, `:131`, `:159`, `:199`), so `utp_writev` is the one that corrupts
data, not the only thing that is shared.

"libutp is not thread-safe" was already written down here, in `bridge.h` and
in `VENDOR.md`. What was assumed, and is wrong, is that it means *per
context*: give every peer its own `utp_context`, its own thread and its own
mutex, and nothing is shared. The bridge did exactly that.

The fix is one recursive process-wide lock around every entry into libutp,
from socket-backed peers and deterministic drivers alike (`global_lock.h`,
`global_lock.cpp`). Recursive because libutp calls the embedder back from
inside its own calls -- `UTP_STATE_WRITABLE` during `utp_process_udp` -- and
the bridge answers some of those by calling libutp again. It lives outside the
vendored sources, which stay byte-for-byte upstream.

Nothing in this library changed. The Go side had been routing all sixteen
connections correctly the whole time; it was the reference that was mixing the
bytes before they reached the wire. That is worth stating precisely, because a
test that fails on its first run is usually finding a defect in the thing under
test, and this one was not -- the evidence for that is that our own end passed
the same concurrency in the other direction (`TestInteropConcurrentGoInitiators`,
one socket initiating to sixteen libutp readers) while libutp was only reading.

The causal check, rather than an assumption: with the lock removed the
concurrent test fails again on libutp's assertion, and with it restored it
passes -- 5 runs of 16 peers, and the whole package clean under `-race`.

## A packet to a peer type we did not define was dropped and reported as sent

**Fixed.** `UdpConn.WriteTo` switched on `*UdpPeer` and `*ConnectionId` and
ended in `return 0, nil` for anything else. `ConnectionPeer` is an exported
interface with one method, so a caller can implement it -- and a connection
built on such a peer would have every packet discarded while each send
reported success, then time out with nothing anywhere to say why.

Silence is the wrong answer either way. The address is now parsed from the
peer's `Hash`, which is the contract the rest of this repository already
relies on (`utpnet`'s `peerUDPAddr` has always done this), and a `Hash` that
is not an address is returned as an error rather than swallowed.
`TestUdpConnWritesToForeignPeerTypes` covers both, and fails against the old
code with "wrote 0 bytes, want 32".

## A connection died when its dial context was cancelled

**Fixed, and it made the library unusable behind `net.Conn` for the most
common dialling idiom in Go.**

`UtpSocket.Connect` and `ConnectWithCid` passed the caller's context straight
to `NewUtpStream`, where it became the connection's lifetime. `net.Dialer`
documents the opposite -- "Canceling ctx does not affect the connection after
it is established" -- and the standard shape relies on that:

```go
ctx, cancel := context.WithTimeout(ctx, dialTimeout)
defer cancel()
conn, err := dialer.DialContext(ctx, network, addr)
```

The `defer cancel()` fires as the dialling function returns, with the
connection still in the caller's hands. Here it took the connection with it.

Nothing in this repository saw it, and the reason is worth recording: every
test dials with a context it keeps alive for the whole transfer, because that
is the natural way to write a test. The idiom that breaks is the one a library
uses, not the one a test does.

What found it was running a real torrent. `anacrolix/torrent` cancels a dial
context on the way out of `establishOutgoingConn`, and the result was a
BitTorrent connection that completed its whole handshake and then stopped
mid-stream: the peer read 796 bytes of a 1007-byte exchange, saw EOF, dropped
the connection, and the torrent sat at 0 of 4194304 bytes with no error
anywhere that named a cause. The library's own log said `utp stream evenLoop
has error and return err="context canceled"` -- which is accurate and tells
you nothing about whose context or why.

The fix: the stream's lifetime is the socket's, and the caller's context
governs only the wait for the connection to come up. A caller that gives up
now tears the attempt down explicitly (`UtpStream.abandonDial`), because
cancelling no longer does it implicitly -- without that, an unanswered SYN
would go on retrying with nobody waiting for the answer. The accept path had
the same shape for the `Accept` call's context and changed with it; the
sibling branch two lines away had already been using the socket's context,
which is what the inconsistency should have suggested.

`utpnet.TestDialContextCancelDoesNotCloseTheConnection` pins it: it dials,
cancels immediately as `defer cancel()` would, and then requires a full
round-trip exchange. Against the old code it fails with "writing after the
dial context was cancelled: not connected".

## A real torrent, transferred and hash-verified over this library

`integration/anacrolix/TestTorrentTransferOverUtp`. Two `torrent.Client`s, a
generated 4 MiB torrent in 128 pieces, and BitTorrent's own piece hashes
deciding whether what arrived is what was sent. Both clients have TCP,
torrent's built-in uTP and the DHT disabled, so the only transport between
them is this library; if it did not work there would be no fallback, only
silence.

The module's README had recorded this as not covered, on the grounds that
wiring it up needed a fork of torrent or a `replace`. That is true of
torrent's built-in socket layer, which picks its uTP implementation at build
time -- and not true of `Client.AddListener` and `Client.AddDialer`, which are
exported and take exactly what `utpnet.Socket` already provides. The gap was
in the reading, not in torrent.

It earned its place on the first run, by finding the dial-context defect
above. Two later things it also caught, both in the test rather than the
library, are worth keeping as warnings:

- **`BytesCompleted() == Length()` is true for a moment before the leecher
  works out it has nothing**, so the obvious completion check passed in about
  240 ms with no file on disk, three runs in five. The test now also requires
  that the whole payload arrived as peer data, which is what makes it a
  measurement of a transfer rather than of a race.
- **The default storage writes `<name>.part` and renames only after the whole
  torrent completes**, which happens after the last piece is marked complete
  rather than with it. Reading the final path immediately found no file in
  four runs out of five, on a transfer that had genuinely finished. The test
  reads back through `Torrent.NewReader` -- what a consumer uses, and what the
  client will serve -- and treats the on-disk rename as a bounded afterthought.

## Close waited out the retransmission ladder

**Fixed.** `UtpStream.Close` waited for the connection's event loop to exit,
full stop. A loop with unacknowledged data does not exit until its
retransmission ladder runs out -- 1 + 2 + 4 + 8 + 16 seconds -- and where that
is not enough, the 60-second idle timeout behind it. So closing a connection
whose peer had gone held the caller for **31 seconds** in one shape and
**60.001 seconds** in another, both measured.

For a BitTorrent client, which drops peers constantly, that is not a close. It
is a leak with a timer on it.

libutp does not wait at all. `utp_close` sends the FIN, sets
`close_requested` and returns (`utp_internal.cpp:3232-3247`); the socket stays
in `CS_FIN_SENT` and `utp_check_timeouts` retires it later. The application is
never held.

The obvious fix -- cap the wait -- breaks the opposite case, and that is the
part worth recording. `Write` returns once the data is in the send buffer, not
once it is acknowledged (`conn.go`, `processWrites`), so a write followed by a
close leaves a tail of up to a send buffer still to go -- and how long that
takes is a property of the link rather than of the caller: 2.29 seconds for a
512 KB transfer over 2 Mbps, which is past any cap short enough to be worth
having. A clock cannot tell "still flushing"
from "peer is gone". Whether the peer is still answering can, so the wait is
bounded by silence instead: `Close` stops waiting once no packet has arrived
for `closeStallTimeout` (two seconds, twice libutp's 1000 ms RTO floor, so a
lost FIN and its first retransmission do not read as a stall).

Giving up means giving up on *waiting*, not on the connection. The event loop
keeps running, keeps retransmitting and exits on its own, and the stream
context is deliberately left uncancelled on that path -- cancelling it would
discard what was still to send, which is what the first version of this fix
did.

Two tests hold the two ends, both on the emulated network because neither case
can be staged on loopback: `netem.TestCloseFlushesASlowTail` writes 512 KB over
a 2 Mbps link and requires every byte to arrive from a `Close` that takes
2.29s -- past any flat bound -- and `netem.TestCloseDoesNotWaitForASilentPeer`
blackholes the link in both directions and requires `Close` to return in
around two seconds rather than 60. Each fails against the design the other
argues for.

Written against real sockets first, both passed while measuring nothing:
`Close` returned in 8ms with nothing left to send, and in 0s against a
"vanished" peer that had in fact sent a FIN on its way out. That is the third
time in this repository a test has passed by staging the wrong situation.

The clean before-and-after is the conformance suite itself, same tests and same
machine: **139.91s to 9.58s**, with `RacyRead` alone going from 31.15s to
0.03s. This repository's own `integrated` and `netem` packages also came down
by roughly a minute each across the same change, but tests were added to both
in between, so that pair of numbers is not a like-for-like comparison and is
recorded here only as the direction it went.

## Write kept the caller's buffer after the call returned

**Fixed.** `UtpStream.Write` queued the caller's slice and returned on
`ctx.Done()` when the write deadline expired -- with the entry still in the
connection's pending list, still pointing at the caller's memory. The caller is
entitled to reuse that buffer the moment Write returns; the connection would
copy whatever it had become.

This is not only a race-detector complaint. The bytes actually put on the wire
would be the mutated ones, silently, on a write the caller had already been
told had failed.

`Write` now copies what it queues. libutp does the same and for the same
reason: `utp_writev` copies out of the caller's iovec into packet buffers
before returning (`utp_internal.cpp:1057-1066`), so an embedder may reuse its
buffer straight away.

Found by `nettest.RacyWrite` under `-race` -- the one subtest that mutates the
write buffer immediately after every call, which nothing in this repository had
any reason to do. A near relative of this was fixed once before, on the
send-buffer path (see the anacrolix module's README); the cancellation path
kept it.

## The shared port leaked a goroutine per datagram

**Fixed.** `utpnet.Socket` is a `net.PacketConn` so that a BitTorrent client
can run its DHT on the same UDP port as its uTP connections -- every DHT query
and reply the client sends passes through `ReadFrom` and `WriteTo`. Both asked
the deadline for a timer channel, and the deadline started a goroutine to serve
it:

```go
if at.IsZero() {
    ch := make(chan time.Time)
    go func() {
        <-changed          // only ever closed by SetDeadline
        close(ch)
    }()
    return ch, false
}
```

With no deadline set -- the normal case, and the only case for a DHT -- that
goroutine parks until the deadline is next *changed*, which for a caller that
never sets one is never. `WriteTo` was worse: it discarded the channel
outright, so it leaked one per call whether a deadline was set or not.

Measured: **500 `WriteTo` and 500 `ReadFrom` calls left exactly 1000 extra
goroutines**, 40 before and 1040 after. A client doing a few hundred DHT
messages a second accumulates a few hundred parked goroutines a second, for
the life of the process.

`wait` now returns a stop function that every caller invokes on every path out,
and `WriteTo` uses a new `expired` that starts nothing, because the answer is
all it wanted. Same measurement after the fix: 20 goroutines before, 20 after.

Two things about the measurement were wrong first, and both would have hidden
the leak rather than exposing it:

- **Sending in bulk and draining afterwards loses datagrams.** The passthrough
  queue is bounded and a UDP socket may drop; the drain stalled at 257 of 500.
  The send and the read are now interleaved, each read satisfied by the
  datagram just sent.
- **Setting a deadline releases the leak.** `SetReadDeadline` closes `changed`,
  which frees every goroutine parked on the old deadline. A drain loop bounded
  by a read deadline would have freed exactly what it was counting. The test
  sets no deadline anywhere.

`TestSharedPortDoesNotLeakGoroutines` pins the packet path.
`TestConnIODoesNotLeakGoroutines` pins the connection path next door, which
goes through `deadline.context` instead and was already clean -- checked rather
than assumed, because it is the same class of defect in the adjacent function.

The rest of the `net.PacketConn` contract went untested with it and is now
covered: a deadline already in the past failing both directions and continuing
to until it is moved, a deadline moved under a blocked `ReadFrom`, and `Close`
unblocking a waiting `ReadFrom` and keeping every later call closed. Those
three passed first time.

## Measuring the application-limited guard, and four ways to measure nothing

**No defect. The mechanism was right; what took the work was proving it.**

libutp refuses to grow the congestion window when the application has not been
filling it:

```cpp
if (scaled_gain > 0 && ctx->current_ms - last_maxed_out_window > 1000) {
    // if it was more than 1 second since we tried to send a packet
    // and stopped because we hit the max window, we're most likely rate
    // limited (which prevents us from ever hitting the window size)
    // if this is the case, we cannot let the max_window grow indefinitely
    scaled_gain = 0;
}
                                        (utp_internal.cpp:1681-1686)
```

This library has had the same rule since the M5 correction, as a citation.
`netem.TestApplicationLimitedWindowDoesNotGrow` now measures it: after slow
start has ended, five seconds of 1 KB writes every 20 ms leave the window
unchanged **to the byte** (15677 to 15677, on three runs of three). Removing
the guard makes it fail on every run.

The measurement is recorded here because four successive versions of it passed
or failed while measuring nothing, and each failure mode is one a similar test
would hit again.

**It passed with the guard disabled.** The first version used a 90 KB window
and 200-byte writes. The gain the guard suppresses is
`3000 * bytes_acked/max_window * delay_factor` — about 7 bytes per ack there,
so roughly 170 bytes over the whole phase, invisible against 90 KB. The guard
is only observable where the window is small and the acks are dense: 3 Mbps,
1 KB every 20 ms, a window around 15-25 KB.

**Write returns before the data is sent.** The phase boundary was taken where
`Write` returned, which is when the data is *buffered*. A megabyte was still in
flight, so a further second of ordinary slow-start growth — 53 KB to 90 KB —
was attributed to the application-limited phase and failed the test.

**Slow start is not gated by the guard, in either implementation.** The guard
zeroes `scaled_gain`, but in slow start libutp takes
`max_window = max(ss_cwnd, ledbat_cwnd)` (`:1699`) and `ss_cwnd` is not gated.
A connection still in slow start therefore grows while the application sends
nothing, correctly. The test could not see which phase it was in, so
`ControllerStats` and `ConnectionMetrics` now carry `SlowStart` and
`AppLimitedSince` — the whole point of those structs being that a controller
observable only through its throughput cannot be told from one doing nothing.
The link also needed a queue small enough (8 KB) to end slow start with a loss;
on a clean link one 512 KB transfer left the connection in slow start one run
in four, so phase A now repeats until slow start is actually over.

**The guard does not apply for the first second.** libutp suppresses growth
only once it has been *more* than a second since the window was last filled, so
the first second after hard sending grows legitimately. Measuring from the
start of the quiet phase counted that as failure on two runs in three. The
baseline is now the first sample where the precondition actually holds.

The test asserts all four preconditions rather than assuming them: that phase A
grew the window at least fourfold, that slow start is over, that the sender
really went more than a second without filling its window, and that at least
three seconds of the phase ran with the guard engaged. Any of them failing
stops the run with a message saying the guard was not measured, rather than
passing quietly.

## A quiet connection punished itself: timeouts for packets already acknowledged

**Fixed.** On a link with no loss configured, a connection that finished a bulk
transfer and then wrote 200 bytes every 200 ms took retransmission timeouts.
Measured: **2 timeouts, 12 retransmissions of data the peer already had, and
the congestion window taken from 55513 to 24831 bytes** over five seconds in
which nothing was lost.

That is the shape a BitTorrent peer connection spends most of its life in --
a burst of blocks, then quiet, then a request -- so every peer was paying a
window collapse for having been idle.

### What was happening

Retransmission timers are armed one per packet and cancelled when the ack
arrives. Cancelling cannot stop a timer that has *already fired*: it is out of
the wheel and on its way to the event loop, where `disarmAcked` will not find
it. `onTimeout` then ran for a packet that had been acknowledged in the
meantime.

On its own that would be harmless, because the early-timer guard sees the
deadline has not passed and returns. But that guard re-arms the packet first:

```go
if now.Before(c.rtoDeadline) && c.state.SentPackets.UnackedCount() == 1 {
    if remaining := c.rtoDeadline.Sub(now); remaining > 0 {
        c.armRetransmit(originPacket, remaining)
        return
    }
}
```

so every dead timer went straight back into the wheel. The armed set grew by
one per write and never shrank -- 5, 6, 7, 8 ... 22 in a five-second phase --
and whenever a dozen of them landed past the deadline together they were taken
for a real timeout: the window halved and delivered packets were sent again.

libutp cannot reach this state. It keeps one deadline rather than a timer per
packet, and it retransmits out of its outgoing buffer
(`utp_internal.cpp:1230-1244`), which no longer holds a packet the peer has
acknowledged.

The fix is to ask the same question libutp's data structure answers
implicitly: `sentPackets.Outstanding(seq)`, and return without acting if the
packet has been acked. Same measurement after: **0 timeouts, 0
retransmissions, and the window holds** (55524 to 55649 bytes), on three runs
of three.

### Why nothing had caught it

Every congestion profile in the benchmark suite is a continuous transfer. None
of them ever goes quiet, so none of them can produce a timer that outlives its
own ack by a second. The defect showed up in the trace of a test written for
something else entirely -- the application-limited guard -- which is the first
test in this repository to leave a connection idle while still watching it.

`netem.TestQuietConnectionDoesNotTimeOut` pins it.

### What it did not change

Throughput on the loss profiles, as far as five repeats can tell. Back to back,
classic LEDBAT: broadband 1% loss 4.44 Mbps (3.82-5.25) without the fix against
5.19 (4.39-5.21) with it, and 5% loss 1.43 (1.33-1.79) against 1.19
(1.00-1.62). The medians move in both directions and every range overlaps, so
the honest reading is no measurable effect -- which is what should be expected
of profiles that never go quiet. The fix is worth having for the idle case, not
for these.

## An idle connection kept a window it had not measured in seconds

**Found and fixed, and the finding is the measurement.**

libutp decays the congestion window of a connection that is sitting idle. It
keeps one retransmission deadline per socket rather than one timer per packet,
and never clears it when the window empties: it is set when a packet goes into
an empty window (`utp_internal.cpp:997`), reset on every acknowledged packet
(`:1389`), and re-armed on every expiry (`:1204`). When it passes with nothing
in flight, the idle branch runs:

```cpp
if ((cur_window_packets == 0) && ((int)max_window > packet_size)) {
    // we don't have any packets in-flight, even though
    // we could. This implies that the connection is just
    // idling. No need to be aggressive about resetting the
    // congestion window. Just let it decay by a 3:rd.
    max_window = max(max_window * 2 / 3, size_t(packet_size));
                                        (utp_internal.cpp:1216-1222)
```

`retransmit_count` is only incremented when something *is* outstanding
(`:1240`), so this repeats indefinitely without killing the connection: the
window falls by a third at each expiry while the timeout doubles, so the decays
land at one, three, seven and fifteen RTOs.

This library could not do that. Its retransmission timers are armed per packet,
so a connection with nothing outstanding has none, and its event loop is
completely quiescent -- idle for eight seconds it does not even produce a
metrics sample. So the window stayed exactly where the last transfer left it,
and the next write would put a window last measured seconds ago straight back
onto a path nothing had probed since.

### Measured before assuming

libutp's `max_window` is private and `utp_socket_stats` does not report it, so
it was measured the way a peer sees it: idle for a while, then write a burst,
and count what goes out before the first acknowledgement can return. Over the
same emulated link, after a 512 KB transfer:

| | no idle | after 8s idle |
| --- | --- | --- |
| libutp (first flight) | 55176 B | 15972 B (29%) |
| ours, before the fix | 55375 B | 55375 B (100%) |
| ours, after | 55447 B | 16428 B (30%) |

29% is (2/3)³ to two figures -- three decays, exactly where a doubling RTO
from a 1000 ms floor puts them in eight seconds. That the arithmetic predicted
the measurement is the reason to believe the reading of the source.

The fix gives the event loop one wake-up for the case: `scheduleIdleRto` arms a
timer for the retransmission deadline whenever nothing is outstanding, and
`onIdleRto` applies the decay through the same call the in-flight path uses --
`sentPackets.OnTimeout`, which asks `HasUnackedPackets` and so takes libutp's
`cur_window_packets == 0` branch by construction.

### The comparison had to be made as a rule, not a ratio

`netem.TestLibutpIdleWindowDecay` first compared the two ratios directly and
failed one run in three, for a reason that is not a divergence: how many
expiries fit in eight seconds depends on where the RTO floor lands, so one run
takes two decays where another takes three -- 44% against 30% -- under exactly
the same rule. It now converts each ratio to the number of thirds it
represents and requires the two to agree within one expiry. That still fails a
genuinely different rule: three halvings would read as 5.1 steps against
libutp's 3.06.

Measured across five runs: libutp 3.06 decays, ours 3.00 four times and 2.00
once.

### It was found by a test written to pin the divergence

The test asserted that ours held its window where libutp decayed. Implementing
the decay made it fail, with a message saying so and asking to be updated --
which is what a test pinning a known divergence is for, and cheaper than
noticing later that a comparison had silently inverted.

## Measuring the window decay rate limit

**No defect.** The rule was already right; it had only ever been read.

libutp halves the congestion window on loss at most once per 100 ms:

```cpp
bool can_decay_win(int64 msec) const {
    return (msec - last_rwin_decay) >= MAX_WINDOW_DECAY;   // 100 ms, :51
}
void maybe_decay_win(uint64 current_ms) {
    if (can_decay_win(current_ms)) {
        max_window = (size_t)(max_window * .5);
        last_rwin_decay = current_ms;
                                        (utp_internal.cpp:602-615)
```

and calls it once per acknowledgement that resent anything (`:1610`), not once
per packet resent. So a queue overflow losing a dozen packets in one window
costs one halving.

`netem.TestWindowDecayIsRateLimited` stages exactly that -- the link drops half
of what crosses it for 25 ms -- and measures: the window goes **72212 to 37652
bytes, 52%, one halving**, from 23 retransmissions, five runs of five within a
point. With the limit removed the same burst takes it to **2800 bytes, the
floor, 4%**, on every run. That is the failure the rule prevents: halving per
lost packet reaches a sixteenth on four losses and the floor on eight, after
which the connection crawls.

### Dropping everything measured nothing

The first version blackholed the link for 25 ms and recovered nothing at all --
zero retransmissions. With no packet arriving after the gap the receiver has
nothing to acknowledge, so there are no duplicate acknowledgements, no fast
retransmit, and recovery waits for the retransmission timeout. That is the one
branch the test has to avoid, because a timeout collapses the window to a
single packet through `:1223-1228` and would mask whatever the decay limiter
did. Losing *half* a burst leaves plenty arriving to report the holes.

The test asserts all three preconditions rather than assuming them: that the
window was at least eight times its floor when the loss was staged, that at
least three packets were actually retransmitted, and that no timeout fired
during the recovery. The first version failed on the second of those, which is
how the mistake was caught rather than measured around.

## Measuring the delay clamp, and why one lie was not enough

**No defect.** The last of the congestion mechanisms that had only ever been
read.

libutp clamps the delay it feeds the controller to the round-trip time
(`utp_internal.cpp:1615-1621`). What makes that load-bearing rather than
housekeeping is where the delay comes from: it is not measured locally. It
arrives in the timestamp-difference field of every incoming packet and reaches
the controller unaltered —

```go
delay := time.Duration(packet.Header.TimestampDiff) * time.Microsecond
```

— so it is a 32-bit number under the remote peer's control, and the clamp is
the only thing between it and the congestion window.

`TestDelayClampedToRTT` measures it: twenty acknowledgements each claiming
**30 seconds** of queueing delay on a path whose round trip is 20 ms leave the
window at 86722 bytes. With the clamp removed the same acknowledgements take it
from 85956 bytes to **2800, the floor**.

### One lie was not enough

The first version injected a single poisoned acknowledgement and **passed
without the clamp**. The arithmetic says why, and it is worth keeping: the gain
is scaled by that packet's share of the window,

    3000 × (1400/85956) × (100000 − 29995000)/100000 ≈ −14.6 KB

so one lie costs 17% of the window and no more. A peer that lies does not lie
once. Twenty acknowledgements is what a second of a hostile or broken peer
looks like, and it separates the two behaviours completely.

This is the second time in this session a congestion test has passed against a
disabled mechanism because the stimulus was too small to move the window — the
application-limited guard was the first. The pattern is the same both times:
the gain scales with `bytes_acked/max_window`, so any test of a rule that acts
*through* the gain needs either a small window or a sustained stimulus, and
checking that the test fails without the mechanism is the only thing that
catches it.

### What is measured and what is read

The clamp is measured at the controller. That the wire value reaches it
unaltered is established by reading the line above, not by a test — a
wire-level version would need a peer that lies, which means the scripted
conformance harness rather than netem, where both ends are honest by
construction.

The bound also differs slightly from libutp's, which is now recorded in
[DEVIATIONS.md](DEVIATIONS.md): libutp clamps to the minimum round trip across
the packets one acknowledgement covers, and this library's controller, which
runs per acknowledged packet, clamps to that packet's own. Ours is therefore at
most as tight and never tighter.

## Measuring the other half of the timeout branch

**No defect.** The in-flight half of libutp's timeout handling, which was the
last part of that rule still only cited.

A retransmission timeout with data outstanding collapses the window to one
packet and re-enters slow start:

```cpp
} else {
    // our delay was so high that our congestion window
    // was shrunk below one packet, preventing us from
    // sending anything for one time-out period. Now, reset
    // the congestion window to fit one packet, to start over
    // again
    max_window = packet_size;
    slow_start = true;
}
                                        (utp_internal.cpp:1223-1228)
```

`netem.TestTimeoutWithDataInFlightCollapsesWindow` blackholes the link
mid-transfer for 1.6 seconds, long enough for a timeout to fire with data in
flight. Ours goes to **2800 bytes with slow start on**, and libutp and this
library then deliver **43560 and 44233 bytes** respectively in the quarter
second after recovery starts -- within 1.5%, and close to the 43.4 KB that slow
start from one packet doubling over the ~5.7 round trips in 250 ms predicts.

### Which measurement is doing the work

Worth separating, because the answer is not the flattering one. The direct
assertions on our own window and slow-start flag are what catch a wrong
branch: forcing the idle branch instead leaves the window at 25569 bytes with
slow start off, and both fail.

The shared receiver-side observable does **not** catch it. Under the forced
idle branch it read 33091 bytes against libutp's 43560 -- *lower*, and well
inside any tolerance worth setting, because a 25 KB window outside slow start
ramps more slowly over 250 ms than a one-packet window inside it. So the
cross-implementation number is evidence that the two agree, and the direct
assertions are the test. The bound on it is deliberately loose and labelled as
a sanity check.

### Two instants that are not the same

The first version measured the 250 ms after *the link came back* and read
**zero bytes for both implementations** -- a comparison of two zeroes, which
passes. The retransmission timeout doubles on each expiry, so after a 1.6
second blackout the next attempt is not due for another second and a half:
nothing was flowing yet. The measurement now starts when the first byte
actually arrives, and a recovery that never starts is a failure.

### A fixed wall-clock window is not safe under load

`TestLibutpIdleWindowDecay`, written earlier, opened its 40 ms first-flight
window at the moment of the probe write. On its own that was fine. Run inside
the full package it returned **zero**, because under load the driver's pump
goroutine can be descheduled past the entire window -- and the test then
compared a real number against nothing and reported that libutp had not
decayed. It now starts the window at the first packet that actually goes out,
and treats a flight that never starts as a failure rather than as a zero.

That one only appeared when the whole suite ran, which is the argument for
running it that way rather than test by test.

## The firewall callback was accepted and ignored

**Fixed.** libutp asks its embedder about every SYN for a connection it does
not already have, after the duplicate-connection check and before it creates
anything:

```cpp
// true means yes, block connection.  false means no, don't block.
if (utp_call_on_firewall(ctx, to, tolen)) {
    ...
    return 1;
}
                                        (utp_internal.cpp:2975-2982)
```

This library had no equivalent. `integration/anacrolix`'s `NewUtpSocket` took
torrent's `FirewallCallback` and discarded it — the worst of the three options
available, because a caller that passed a blocklist got no blocking *and* no
sign that it was not happening. A silent no-op is worse than an unimplemented
one.

`utp.WithFirewall` is the hook, `utpnet.Options.Firewall` reaches it in
`net.Addr` terms because that is what a blocklist is keyed by, and the adapter
passes torrent's callback through.

Two properties beyond "the connection does not happen", both asserted:

- **A refusal creates no state.** Otherwise a blocklist becomes a way to make
  a node allocate. The check sits after every connection lookup has missed and
  before anything is constructed.
- **A refusal is silent.** Not a RESET: answering confirms to a refused peer
  that something is listening here, which is the opposite of what a blocklist
  is for. libutp's refusal is a bare `return 1`, and so is this one. The
  visible consequence is that a refused dial fails by timing out rather than
  promptly, which is the same thing libutp's peers see.

The translation from `ConnectionPeer` to `net.Addr` fails closed: a peer whose
address cannot be recovered is refused rather than admitted unchecked, since a
firewall that fails open is worse than no firewall.

`utpnet.TestFirewallRefusesBeforeAnyStateExists` and
`TestFirewallAdmitsWhatItDoesNotRefuse`. The first fails against the old
behaviour with "the dial succeeded against a socket whose firewall refuses
everything".

## ~~A dropped probe teaches the search nothing during a bulk transfer~~ — closed

**Found by implementing the don't-fragment bit, which is what made it
visible.** Before that, a probe was never dropped for being too big on an IPv4
path at all — it was fragmented and delivered — so there was nothing to learn
from and nothing to notice.

libutp lowers its MTU ceiling from a lost probe in two places:

- a retransmission timeout with the probe as the only packet outstanding
  (`utp_internal.cpp:1152-1167`), and
- a third duplicate acknowledgement pointing at the packet before the probe
  (`utp_internal.cpp:1927-1940`), whose comment says why: "It's likely that
  the probe was rejected due to its size, but we haven't got an ICMP report
  back yet".

Only the first was implemented. `mtu.go` cited both and
`mtuSearch.onProbeLost`'s comment described both, but nothing counted
duplicate acknowledgements: there was no equivalent of libutp's
`conn->duplicate_ack`.

The first alone cannot fire during a bulk transfer, because it requires
`UnackedCount() == 1` — faithfully, since only then does a lost packet say
something about size rather than congestion — and a saturated send window
never leaves it that way. So a probe dropped for size was retransmitted
fragmentable, arrived, was acknowledged, and the search concluded the size was
fine.

`connection.noteDuplicateAck` is the missing half. Three rules, all libutp's:
only bare `ST_STATE` packets count (an `ST_DATA` carrying an acknowledgement
was most likely sent because the peer had data of its own, so counting those
would give three "duplicates" after every payload packet on a bidirectional
connection — `:1911-1920`); the acknowledgement must repeat the number just
before the oldest outstanding packet, and anything else resets the count; and
nothing counts while nothing is outstanding, which is also not a reset.

On the third, with a probe outstanding, libutp draws a different conclusion
from each of two cases, and so does this: if the repeated number is the one
before the probe, the probe is the hole and the ceiling drops below it; if it
is anything else, some other packet was lost ahead of the probe, which says
nothing about size, so the probe is forgotten and the ceiling left alone.

**Measured**, on a 1000-byte emulated path that fragments rather than drops,
with a 1400-byte ceiling:

| | search settles at | datagrams fragmented |
| --- | --- | --- |
| without the don't-fragment bit | 1384 | 3809 |
| with it, before this | 1384 | 3794 |
| with it, after this | **996** | **32** |

Disabling the call site puts both columns back to 1384 and zero probe losses.

### What it also changed, two tests over

Two cases were built on the claim this closes, and both had to be rewritten
rather than left to pass by luck.

`TestMtuSearchCannotRecoverFromAPathLimitBelowItsChoice` was skipped as a
limitation "shared with libutp" whose fix would mean "deliberately diverging".
That conclusion was reached without knowing about the duplicate-acknowledgement
route, and it was wrong: the fix is a libutp mechanism, not a divergence. The
search does now recover on that path — from `floor=576 current=1191
ceiling=1400` to `floor=982 current=1083 ceiling=1184`. It stays skipped for a
narrower reason: the *transfer* still stalls, because uTP numbers packets and
the ones already built too big cannot be re-cut without renumbering everything
behind them.

`TestIcmpBringsTheSearchWithinThePath` failed outright, on its own control
guard — "this path does not exercise what the report is for". Its control
assumed the silent search stays above the path, which is no longer true. What
the report is still worth is the *ceiling*: silent, the search comes down to a
workable size but leaves its ceiling at 1184, free to climb back above the
1100-byte path; with the report it is exactly 1100, every run, because the
report names the next hop's MTU instead of bisecting towards it. The case now
asserts that.

Both are worth noting as a pattern rather than as two incidents: a test whose
control depends on a limitation will pass or fail for the wrong reason the
moment the limitation is lifted, and the ones that fail loudly are the lucky
ones.

## The M4b sweep: reading libutp against this implementation

The M4b milestone was always recorded as incomplete in one specific way:
`DEVIATIONS.md` said what had **not** been done was "reading
`bittorrent/libutp` end to end and confirming agreement behaviour by
behaviour". Sections had been audited that way — the ack path, loss recovery,
the congestion controller, the close handshake, the send path, the timers, the
MTU search, the ICMP path, and the API surface function by function — and each
audit found defects, which is the reason to expect the unaudited remainder to
contain more.

This is the sweep of that remainder: `utp_process_incoming` (the 700-line
inbound path), `utp_process_udp` (dispatch, SYN and RESET), the ack-deferral
and window-reopening path, and the connection defaults.

Four findings, two of them clean. Both mechanisms it found missing are now
implemented. The second of them took three attempts, and what the first two
got wrong -- including a hang blamed on the change that was present without
it -- is recorded below rather than glossed.

### 1. ~~The clock-drift penalty in `apply_ccontrol`~~ — implemented

libutp has **two** clock-drift mechanisms and this library had one.

The one it had is the delay-base shift: when the peer's base delay falls,
`our_hist` is shifted to match (`utp_internal.cpp:2009-2015`). That corrects
the *measurement* for ordinary drift between two honest clocks, bounded to
10ms per adjustment and aged out.

The one it lacked corrects nothing. It watches the long-run slope and, past a
threshold, inflates the delay the congestion controller sees
(`utp_internal.cpp:1646-1650`):

```c
int32 penalty = 0;
if (clock_drift < -200000) {
    penalty = (-clock_drift - 200000) / 7;
    our_delay += penalty;
}
```

`clock_drift` is a rolling average weighted 7:1 of the slope of
`average_delay`, itself a five-second mean of the peer's reported
`reply_micro` taken relative to a renormalised base (`:2040-2107`). All of
that is now in `driftEstimator`, kept as its own type with its own tests
because the arithmetic is wrapping uint32 throughout and a wrap read the wrong
way round would apply a penalty to an honest peer — a failure that would cost
throughput on every connection, invisibly.

**The threshold is not about hardware.** -200000 microseconds per five seconds
is 40,000 ppm, where an ordinary crystal drifts by tens. libutp says what it
is for: "to compensate for people trying to 'cheat' uTP by making their clock
run slower, and this definitely catches that without any risk of false
positives".

**Measured**, over the emulated network, with the sender's clock drifting:

| sender's clock | drift estimate | penalty | mean congestion window |
| --- | --- | --- | --- |
| +100 ppm (an ordinary crystal) | -222 | **none** | — |
| +30,000 ppm (below the threshold) | -77,797 | **none** | 338,437 |
| +200,000 ppm | -518,801 | **45.5 ms** | 307,803 (90.9%) |

Both halves of libutp's claim hold: it fires on the drift it is for, and a
real crystal at 100 ppm earns nothing. Disabling the penalty fails both cases.

Two things worth keeping. **The estimate converges slowly, by design.** Each
five-second slot contributes an eighth, so after *n* slots it has reached
1-(7/8)ⁿ of the true slope — two slots is 23%, six is 55%. Measured at twelve
seconds and 200,000 ppm the estimate reached -179,151 against a -200,000
threshold: the mechanism working, and the run ending before it crossed. An
estimate that reacted within a slot would swing on ordinary delay noise, and
the penalty it drives costs throughput.

**Throughput did not move, and the test does not pretend it did.** 7.21
against 7.20 Mb/s, because even unpaced that flow is not window-limited on a
50 Mb/s link, so a window a tenth smaller still covers the rate. What is
asserted is the window, which is what the penalty acts on.

### 2. `utp_read_drained` — implemented, and a hang I had blamed on it

libutp tells the peer as soon as its application drains the read buffer
(`utp_internal.cpp:3242-3261`):

```c
const size_t rcvwin = conn->get_rcv_window();
if (rcvwin > conn->last_rcv_win) {
    if (conn->last_rcv_win == 0) conn->send_ack();
    else conn->schedule_ack();
}
```

This library freed the buffer and said nothing.

**The important finding is not the mechanism. It is that the hang which caused
this to be reverted twice was never caused by it.** With the mechanism
compiled out entirely, the same scenario — a 32KB receive buffer, a sender
with 512KB to push, a reader that pauses for two seconds — stalls past a
25-second budget in **4 runs out of 20**. With the mechanism it stalls in about
1 in 20. The earlier judgement, that "a change that can deadlock a connection
is not worth a second of recovery latency", rested on six control runs of a
less sensitive test that happened not to stall. Six runs cannot see a one-in-
five rate reliably, and they did not.

Two things were genuinely wrong with the earlier attempts, and both are fixed:

- **The ordering.** libutp hands bytes to its embedder *inside*
  `utp_process_incoming` and acknowledges afterwards, so the acknowledgement
  carries the window the drain produced and `utp_read_drained` finds nothing
  to report. This library drained on a later pass of the event loop, so the
  acknowledgement went out first and every drain then looked like the window
  growing — doubling the reverse traffic, which the corpus caught at once.
  `processReads` now runs immediately before `flushAck`, in the same pass.
  Since `ackPending` is a bool and `flushAck` runs once per pass, the drain
  and the data that prompted it share one acknowledgement.
- **The condition.** The second attempt fired only when the window last
  advertised was zero. That wedged a connection one run in three: the trace
  shows the window falling to **861 bytes**, which is not zero but is less
  than a packet, so the sender could not fit one and went quiet; nothing
  arrived, so nothing was acknowledged; the application read and freed the
  buffer, and the zero-only test declined to mention it. libutp's condition is
  `rcvwin > last_rcv_win` — any growth — and it is right because a window too
  small to use blocks exactly as a closed one does.

**What is not claimed.** Three attempts at a stable network measurement of the
end-to-end benefit failed. When this was written the reason was unknown: the
window would not reopen while the reader was paused, even with a 100-slot read
queue that should have absorbed the whole buffer.

**The reason has since been found, and it was not this mechanism.** The buffer
was full of data held behind a gap — `readable 0` — so there was nothing for
any drain to hand up and no window for this mechanism to report; see 2c. That
also retracts the stall-rate credit given above: the 4-in-20 and 1-in-20
figures were the gap deadlock, and at twenty samples they are one rate, as 2c
records. This mechanism did not measurably reduce that stall and should not
be said to.

The rules are pinned by unit tests: growth owes an acknowledgement, no growth
owes none, 861 bytes counts as growth, and `lastAdvertisedWindow` follows the
packet actually sent.

**Measured end to end, once two defects in the way were fixed.** The first
attempt at a clean scenario was not clean. With the link dropping nothing, the
receiver refused 54 to 262 packets a run, and the sender timed out once or
twice. Counting every drop site put all of them in the receive buffer, and the
cause was our *sender*: it did not count bytes in flight against the peer's
window (2d). With that fixed, the scenario closes the window only because the
application is slow, which is what this mechanism is for.

Same harness, all three arms: a 32KB receive buffer, 512KB to send, a reader
paused for two seconds, and a 5ms, 50 Mb/s link whose queue never overflows.
10 runs an arm, with the link, connection-queue and receive-buffer drop
counters at zero in all 30:

| arm | delivered in | drops | sender timeouts |
| --- | --- | --- | --- |
| `utp_read_drained` on | **2.158–2.163s** | 0 | 0 |
| off, keep-alive checked every 500ms, which is libutp's own cadence | 29.698–29.702s | 0 | 0 |
| off, keep-alive on this library's 29s ticker | 58.206–58.212s | 0 | 0 |

In every run the window fell to between 176 and 260 bytes: less than a
packet, but not zero. The zero-window probe arms only on exactly zero, so it
never fired. Nothing was in flight either, so no retransmission timer was
running. With the mechanism off, the connection sat until the receiver's
keep-alive happened to carry the fresh window. That recovery path was
**established, not assumed**. With the receiver's keep-alive interval set to 7s
and then 10s, recovery moved to 14.2s and 20.2s, the second keep-alive tick
each time. A timeline of both ends shows the sender learning of a
32,768-byte window at 14.01s and the remaining 358KB leaving within 0.18s.

So, in this scenario, the benefit is about 27.5 seconds per stall against
libutp's own behaviour without the mechanism (the 500ms arm, which checks the keep-alive at libutp's cadence).
Against this library as it then was, it was about 56 seconds. The difference
between those two control figures was a separate defect, since fixed (2e).
With that fix, the control arm on the unmodified keep-alive finishes in
29.34–29.36s (3 runs): one keep-alive interval after the connection went
quiet, as libutp's would. The benefit applies whenever a slow reader leaves less than a
packet of window with nothing in flight. A window of exactly zero is bounded by
the 15-second zero-window probe instead, and that case was not measured here.

Of the 2.16s, 2.0s is the reader's pause. The rest is the transfer finishing
after one or two acknowledgements owed to the drain (`WindowReopenedAcks`)
reopened the sender.

`netem.TestReadDrainedRestartsASenderBlockedBySubPacketWindow` keeps it that
way. It checks the scenario first: a window under a packet but not zero, and
no receive-buffer drops. Then it asserts delivery within 10s. With the
mechanism disabled it fails on delivery (166KB of 512KB at 10s). With the old
sender rule it fails on the drops check (40 refused packets).

### 2b. ~~Out-of-order data shrinks our receive window and not libutp's~~ — fixed

**And it was not the cause of the stall, which is recorded in 2c below.** This
section claimed it was. That claim was wrong, and the fix disproved it.

**The two accountings.** `receiveBuffer.Available()` charged every byte held
out of order against the window this library advertised. libutp's
`get_rcv_window` (`utp_internal.cpp:590-596`) is `opt_rcvbuf` less what its
embedder has not yet read, and a packet held out of order sits in
`conn->inbuf`, never reaching `utp_call_on_read` until the gap before it is
filled, so it never enters that figure.

Measured before the fix, both implementations driven through the same packets
with a gap left open:

| bytes held behind the gap | we advertised | libutp advertised |
| --- | --- | --- |
| 40,000 | 1,008,576 | 1,048,576 |
| 1,120,000 (800 packets) | **1,376** | 1,048,576 |

The first row was exact: 1,048,576 − 40,000, every byte. The second was the
alarming one — 1,376 bytes is under the 1,400 a packet needs, so the peer
could send nothing, and above the zero that would have armed its zero-window
probe.

**The fix** is a split rather than a deletion. `receiveBuffer.Window()` is what
goes on the wire — capacity less the bytes delivered in order and not yet
handed up, which is libutp's figure. `Available()` keeps charging the
out-of-order bytes and keeps doing admission control, because the collapse
loop in `Write` indexes `rb.buf[rb.offset:end]` and panics if offset plus
pending ever exceeds the capacity.

Advertising the larger figure cannot break that invariant. The window is the
capacity less what has been delivered, so everything a peer respecting it
sends fits in what is left, in order or behind a gap. A peer ignoring it is
caught by admission, which drops rather than writes: over-advertising costs a
retransmission, never a panic.

After the fix both implementations advertise 1,048,576 with 1.12 MB held
behind a gap. `TestReorderedDataCostsUsNoMoreWindowThanLibutp` and
`TestReorderedDataDoesNotBlockOurWindow` pin the agreement; they were written
to pin the divergence and were inverted when it went.

### 2c. ~~A stall in the same scenario~~ — fixed: no room was held for the packet that fills a gap

The scenario — a 32KB receive buffer, a sender with 512KB to push, a reader
that pauses two seconds — stalled past a 25-second budget in about 3 runs in
20, and neither `utp_read_drained` nor the receive-window fix moved it. This is
what it actually was.

**A gap in the sequence space was a deadlock.** Data arriving behind the gap
was admitted until the receive buffer was full. The packet that would release
all of it — the peer's retransmission of the missing one — was then refused for
want of space. It was refused every time the peer sent it, and the peer sent it
forever. Nothing else could free the buffer, because nothing behind the gap can
be delivered until the gap closes.

Instrumenting both ends made it unmistakable:

```
receiver: pending 32613   readable 0   held-behind-gap 32613   pkts-recv 285
receiver: pending 32613   readable 0   held-behind-gap 32613   pkts-recv 296
HUNG: delivered 152250 of 524288 in 25s
```

The whole buffer, none of it deliverable, packets still arriving and being
dropped. On the sending side the matching picture was `cwnd 13424, inflight
13424` — the congestion window permanently full of bytes that would never be
acknowledged, with 326KB of application data waiting behind them.

**The fix** is a reserve. `receiveBuffer.Write` admits the packet that fills
the gap against the whole of what is free; anything arriving out of order must
leave `gapReserve()` behind for it. The figure is the largest packet this peer
has sent, because that is exactly what the gap-filling retransmission will be —
the peer is resending something it already sent.

Memory safety is unchanged. The collapse loop indexes `rb.buf[rb.offset:end]`
and needs offset plus pending never to exceed the capacity; reserving *more*
than before can only help.

**Measured**, same harness both ways:

| | stalls in 40 runs |
| --- | --- |
| reserve held back | **0** |
| reserve disabled | 3 |

Forty samples make that suggestive rather than conclusive on its own, which is
why the mechanism is pinned deterministically as well:
`TestGapFillingPacketIsAdmittedWhenTheBufferIsFull` builds the state directly
and shows the gap-filling packet refused without the reserve and accepted with
it, releasing the whole run behind it.

**Two things it costs, neither of them free.** One packet's worth of receive
buffer is unavailable to out-of-order data, always. And `largestPacket` only
grows: a peer that sends one unusually large packet early reserves that much
for the rest of the connection. Both are bounded by the maximum packet size and
neither can deadlock anything, but a very small receive buffer will hold
correspondingly less out-of-order data than it used to.

### 2d. ~~Our sender overran the peer's receive window~~ — fixed

libutp sends the next queued packet only while `!is_full()`
(`utp_internal.cpp:963-985`, `:3197-3212`), and `is_full` refuses when
`cur_window + packet_size > min(max_window, opt_sndbuf, max_window_user)`
(`:933-936`, `:956`). This library computed
`min(cwnd − in_flight, peer_window)`, and differed in two ways:

- **Bytes in flight did not count against the peer's window.** The
  advertisement is room beyond what the peer has acknowledged, and everything
  sent since spends it. We compared each packet with the whole advertisement,
  so a receiver whose application fell behind was overrun by up to a full
  window. Each packet past its room was dropped and had to be retransmitted.
  The first logged refusal was a 1358-byte in-order packet against 74 bytes of
  advertised window, and the five packets already in flight behind it were
  refused in turn.
- **Packets were cut down to fit.** libutp calls `is_full()` there with no
  argument, which charges a whole `packet_size` whatever the queued packet
  holds (`:934`, `:974`). So nothing is sent into less than a packet of room,
  and nothing is shrunk to fit. We filled a window to the byte, sending 71-byte
  and 718-byte fragments into the tail of it.

Measured on the read_drained harness above, with the link dropping nothing.
Four runs before the fix: 54 to 262 receive-buffer drops a run, 1 or 2 sender
timeouts, up to 21KB held behind a self-inflicted gap, delivery in 2.33–3.41s.
Fourteen runs after: no drops, no timeouts, nothing held behind a gap,
delivery in 2.158–2.163s.

`TestInFlightBytesSpendThePeersWindow` and
`TestNothingIsSentIntoLessThanAPacketOfRoom` pin the two halves, and each fails
with its half disabled. `TestAWidePeerWindowSendsEveryPacket` is their control.
`TestConformanceWriteIsNotCutToFitPeerWindow` checks the second half against
libutp directly. The first half cannot be isolated against libutp at
connection start: libutp's congestion window is one packet there (`:2567`),
which holds a second packet back whatever the peer advertises. Its first packet
is also 1452 bytes where ours is 962 (M6), so a window that separates the two
rules for one implementation does not separate them for the other.

`ConnectionMetrics.RecvBufferDrops` now counts data packets refused for want of
room, excluding duplicates of packets already held. A non-zero figure on a link
that drops nothing means a peer sent past the window it was given.

### 2e. ~~Keep-alive fires up to 29 seconds late~~ — fixed

libutp checks `current_ms - last_sent_packet >= KEEPALIVE_INTERVAL` on every
timeout pass (`utp_internal.cpp:1271-1274`), and it runs that pass at most
every 500ms however often its embedder calls it (`TIMEOUT_CHECK_INTERVAL`,
`:37`, `:3284`). This said earlier that the rate was the embedder's, which
was wrong; found while measuring the retransmission timeout (2g).
So a keep-alive leaves about 29 seconds after the last packet. This library
checked the same condition only when a 29-second ticker fires,
counted from connection start. A connection that went quiet just after a tick
failed the check at the next one and waited for the one after: anywhere from
29 to 58 seconds of silence. The comment at the ticker said it "ticks at the
same interval", which was true of the ticker's period and not of when the
keep-alive left.

Measured, as the control arm above: 58.21s against 29.70s with the ticker's
period set to 500ms and nothing else changed. It mattered beyond that scenario,
because 29 seconds was chosen to sit under a 30-second NAT mapping timeout, and
a keep-alive that can wait 58 seconds does not.

**The fix** is a timer aimed at the instant the silence reaches the interval,
re-aimed from `lastSentPacket` each time it fires. Anything sent in between
moves the instant later; a keep-alive sent resets it to one interval. The
keep-alive now leaves exactly one interval after the last packet. libutp's
leaves at the first timeout pass after that, so it can be up to 500ms later
than ours.

`TestKeepAliveLeavesOneIntervalAfterTheLastPacket` runs on the virtual clock
at libutp's 29 seconds. The connection goes quiet 5 seconds in, away from any
multiple of the interval. The test asserts nothing at 29s less a millisecond
and one keep-alive at 29s, three times over, with the peer answering each one
as a live peer would. Against the ticker it fails at the first: nothing at 29
seconds. The old `TestKeepAlive` could not see the defect, because it gives
the keep-alive six intervals to turn up.

End to end, the read_drained control arm went from 58.21s to 29.34–29.36s.

The ticker's replacement also dropped a stray case from the zero-window probe
timer's drain. That `select` received from the keep-alive ticker as well as
from its own timer, and handled a keep-alive there. With a timer, taking its
firing there without re-aiming it would silence the keep-alive for good. No
other drain in the loop touches another timer's channel.

### 2f. ~~A retransmission timeout resends the whole window~~ — fixed

libutp, on a retransmission timeout, marks every packet in flight
`need_resend`, stops counting its bytes in `cur_window`, and resends only the
oldest (`utp_internal.cpp:1230-1252`). The rest wait for `flush_packets`,
which sends them oldest first and only while `is_full()` allows, against a
congestion window the timeout has just cut to one packet (`:1225`). Each is
counted in flight again when it is resent (`:878`).

This library arms a timer per packet and resends each one as its timer fires,
with no window check. Its in-flight count never drops a lost packet either:
the only loss call passes `retransmitting=true`, which leaves the bytes
counted. And only the timers that fire after the new, doubled deadline count
as a timeout. The rest were armed at the old value, so the window is resent
again an undoubled interval later.

Measured with `netem.TestRTOBurstMeasure` (opt-in:
`UTP_RTO_BURST_MEASURE=1`). A 16MB transfer over a 10ms, 20 Mb/s link, the
data direction blacked out for 3 seconds from 1 second in, and the receiver
ours in both runs. Data packets the sender offered during the blackout,
three runs each:

| | libutp sending | this library sending |
| --- | --- | --- |
| first timeout | 1 packet, the oldest (at 2.0–2.5s) | the whole window, 75 packets, out of order (at ~2.0s) |
| ~1s later | nothing | 48–66 packets again |

On a path that is congested or down, that is the opposite of what a timeout is
for. The comment above `onTimeout`'s resend cites libutp marking every packet
`need_resend` as the reason to resend them all, which conflates marking with
sending. That comment also records why the obvious fix is not safe on its own:
an earlier change that skipped the sibling resends left holes fast retransmit
could not fill, and the 5%-loss benchmark stopped completing. libutp fills
those holes from the marked set as the window regrows, and a fix has to do the
same.

**The fix** is libutp's, in three parts:

- At the timeout, every packet in flight is marked to be resent and stops
  counting in flight (`sentPackets.MarkAllForResend`, and the controller's
  `MarkForResend`), and only the oldest is resent. A marked packet counts
  again when it is resent, and its acknowledgement does not subtract it twice
  (`:877-881`, `:1390-1396`).
- Another packet's timer firing before the connection's deadline resends
  nothing. It re-arms against the deadline, which is what libutp's single
  `rto_timeout` amounts to.
- The marked packets come back two ways, as in libutp. `processWrites` sends
  them oldest first, ahead of any new data and under the same window rule
  (`flush_packets`, `:970-985`). And libutp's fast-timeout retry, which this
  library did not have, resends the oldest on each acknowledgement that
  leaves it next in line (`fast_timeout`, `:1247`, `:2256-2282`).

`TestRetransmissionTimeoutResendsOnlyTheOldest` pins it on the virtual clock,
with three packets in flight and nothing acknowledged. Exactly one packet, the
oldest, goes at the timeout, and nothing more before the next deadline. The
acknowledgement for it brings the rest back first, in order, before any new
data. A second variant sends that acknowledgement with less than a packet of
window, so only the fast-timeout retry can send anything. Each part fails on
its own when disabled: the old `conn.go` sends several packets at the timeout;
without the fast-timeout retry nothing comes back through the narrow window;
without the resend loop new data goes out ahead of an owed packet.

On the blackout above, after the fix: one packet, the oldest, at 2.04–2.06s,
and nothing more (two runs), as libutp.

**What it costs, measured, and how not to measure it.** The first comparison
ran the lossy benchmark profiles seven times on each side and showed LEDBAT++
at 1% loss falling from 2.14 to 1.46 Mb/s with ranges that did not overlap.
That was not the fix. A profile's seed fixes which packets the emulator drops,
*as long as the sender offers the same packets in the same order*. The fix
changes what is sent at the first timeout, and every later loss then lands on
different packets. Timelines of one run each confirm it: identical up to the
timeout at 3.68s, different afterwards. Seven repeats of one seed are one loss
pattern, not seven samples. `UTP_BENCHMARK_SEED` now overrides a profile's
seed so the comparison can sweep it.

Twelve seeds, one run each per side, paired by seed (geometric mean of the
per-seed ratio, and how many seeds the fix won):

| profile | LEDBAT | LEDBAT++ |
| --- | --- | --- |
| LAN, no loss | 0.997 (6/12) | 0.998 (5/12) |
| 1% loss | 1.002 (5/12) | 0.977 (6/12) |
| 5% loss | **0.943** (4/12); timeouts 32 → 47 | **0.826** (2/12); timeouts 77 → 155 |
| 16KB queue | 0.991 (4/12) | 1.053 (6/12) |

No measurable change except at 5% loss. There it costs about 6% under classic
LEDBAT, the default, and about 17% under LEDBAT++. The cause was traced rather
than assumed. On two seeds of LEDBAT++ at 5%, 15 of 22 and 18 of 19 timeouts
were for a packet already resent after the previous timeout, whose resend or
its acknowledgement was lost again. After a timeout there are only one or two
packets in flight, too few for fast retransmit, which needs three later
packets acknowledged. So only another timeout, with the backoff doubled,
recovers it. The old code sidestepped that by resending the whole window,
which drew enough selective acknowledgements to fast retransmit the lost
resend. The burst was the cost of that, and it is the behaviour removed here.

libutp is more exposed to this than we are, not less: its post-timeout window
is one packet (`:1225`), where ours cannot fall below two
(`minWindowSizeBytes`, a recorded deviation: see DEVIATIONS.md, "The
congestion window starts at, and never falls below, two packets"). Against real libutp as sender, on the same 5%-loss link, the same
twelve seeds and the same receiver, classic LEDBAT only: libutp 1.27 Mb/s
median, this library 1.69 before the fix and 1.68 after (1.28 and 1.22 times
libutp, geometric mean).

Not investigated: our first timeout came about 1.0s after
the last send, libutp's after 1.0–1.46s. It may be only different RTT
estimates.

### 2g. ~~The retransmission timeout ran from the wrong place~~ — fixed

Two defects, found by comparing the retransmission timeout with libutp's on
virtual clocks (`TestConformanceRetransmissionTimeoutComputation`). The
estimator itself was right: our RTT smoothing is libutp's arithmetic line for
line (`utp_internal.cpp:1362-1380`). What fed it and what used it were not.

**Every acknowledgement that retired everything in flight stopped half way.**
After acknowledging, the sent-packet bookkeeping asks for the first packet
still unacknowledged, and when there was none it returned that as an error.
`processAck` then returned before it disarmed the retired packets' timers,
restarted the retransmission deadline, reset the consecutive-timeout count, or
told the MTU search a probe had arrived. On a connection sending flat out an
acknowledgement rarely retires everything; on any other -- request and
response, a trickle of protocol messages, the end of every transfer -- it
retires everything every time. The next packet then found stale timers still
armed, so it started no deadline of its own, and was timed from whatever
deadline an earlier packet had left. In the trace that found it, a packet
sent with a 1.15s timeout was resent after 2.0s, from a deadline set at the
handshake. libutp's `ack_packet` has no such exit and restarts `rto_timeout`
for every packet it retires (`:1388-1389`).

`TestAckRetiringEverythingRestartsTheDeadline` pins it without libutp: one
packet acknowledged after 100ms, a second sent, and the second must be resent
within a wheel tick of the 1-second timeout. It fails without the fix. Fixing
it also exposed that the unit-test connection fixture had no MTU search, which
nothing had reached before.

**No RTT sample was taken from the SYN.** libutp's SYN sits in its outgoing
buffer like any packet, and the SYN-ACK acknowledges it through the same
`ack_packet`, which samples any packet sent once (`:1362`). Ours built its
congestion controller when the SYN-ACK arrived and started it with no
estimate, so a connection's first timeout was the 3-second initial value where
libutp's was computed from the handshake: 1 second on any path under about
330ms. A SYN sent more than once still gives no sample, as in libutp.

Measured after both fixes, five chains of round trips, libutp to 10ms and ours
to the 25ms wheel tick:

| round trips (SYN first) | libutp | ours |
| --- | --- | --- |
| 0 | 1.00s | 1.025s |
| 800ms | 2.40s | 2.425s |
| 0, 800ms | 2.40s | 2.425s |
| 500, 300, 1200, 200ms | 1.97s | 2.00s |
| 100, 100, 900ms | 1.12s | 1.15s |

Before the fixes ours was 3.025, 3.025, 2.425, 1.65 and 2.025 seconds: one
right in five.

**Throughput: no measurable change.** Classic LEDBAT, paired by seed against
the code before: LAN 1.006, broadband 1.002, 1% loss 1.000 (eight seeds
each). At 5% loss two sweeps disagreed -- 0.914 over eight seeds, then 1.075
over twelve with the same binaries on the same first eight seeds -- which
says that profile's run-to-run noise from real goroutine scheduling is larger
than any effect these fixes have on it. Each fix alone, same twelve seeds:
the acknowledgement fix 1.142, the SYN sample 1.006.

**Measuring libutp needed care, and it corrected an earlier claim.** libutp
runs its timeout pass at most every 500ms however often its embedder calls it
(`TIMEOUT_CHECK_INTERVAL`, `:37`, `:3284`), so it resends on the first pass
after its deadline, not on it. Measured naively, its timeouts all looked like
multiples of 500ms. The deadline is recovered by moving the phase of those
passes across the interval and taking the shortest time to the resend. Section
2e said earlier that libutp's pass ran as often as the embedder called it; it
does not, and 2e now says so.

### 3. ~~No cap on accepted connections~~ — closed

libutp refuses a new incoming connection when the context already holds more
than 3000 sockets (`:2967-2974`). This library has no count bound. The
pending-SYN map is bounded only by time — `AWAITING_CONNECTION_TIMEOUT` is 20
seconds — so a SYN flood at rate R leaves R×20 entries where libutp holds at
most 3000.

It was left open because 3000 is a policy choice and a BitTorrent client can
legitimately want more. It is now closed with a configurable cap, defaulting
to libutp's 3000: `utp.WithMaxConnections`, `utpnet.Options.MaxConnections`.
The count, the "more than" comparison, the position before the firewall and
the silent refusal are libutp's. A negative value removes the cap.

Six unit cases on the scripted transport pin it, and each rule has one that
fails when that rule is broken: no check at all, `>=` for libutp's `>`, a
retransmitted SYN for a parked connection counted as new, parked SYNs not
counted, dialled connections not counted. `utpnet.TestMaxConnectionsRefusesPastTheCap`
runs it over real UDP: under a cap of 1 two dials succeed and the third times
out, with nothing accepted and no RESET sent; with the option not passed
through, the third gets in.

**It changes a default.** A socket used to take any number of incoming
connections; it now refuses a SYN while it holds more than 3000. That matches
every libutp peer, and a client that needs more has to say so.

### 4. Things checked that turned out to be fine

Recorded because a sweep that only lists what it found is indistinguishable
from a sweep that stopped early.

- **The three-way connection-id lookup.** We derive three candidate ids for an
  inbound packet where libutp looks up the receive id alone for a non-SYN
  (`:2884-2892`), which looked like it could deliver a packet carrying our
  *send* id into an established connection. Tested directly: both
  implementations answer an identical RESET. The derived id matches no
  registered connection.
- **Re-acking an old packet.** libutp schedules an acknowledgement for a
  packet behind the reorder window that is not a STATE (`:1889-1897`), so a
  peer still retransmitting something we already have can stop. Present, in
  `outsideReorderWindow`, found earlier by the differential fuzzer.
- **Data arriving past a FIN already seen.** libutp drops it (`:2412-2420`);
  `onData` checks the same range.
- **Connection defaults.** `rto = 3000`, `rtt_var = 800`, `slow_start = true`,
  `ssthresh = opt_sndbuf`, target delay from the context. All present and
  cited.
- **The initial peer window.** libutp starts `max_window_user` at
  255 × `PACKET_SIZE` = 365,925 bytes (`:2613`) where this library starts at
  `math.MaxUint32`. Not reachable: at connection start the congestion window is
  in slow start and is the binding constraint in
  `min(max_window, opt_sndbuf, max_window_user)`.

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
- **Packets can still be dropped when a connection's event queue is full**
  (`handleIncomingBuf` falls through to `default`), and this is now counted
  rather than only logged — `UtpSocket.PacketsDroppedFullConnQueue`. The
  condition that made it routine — a connection whose reader stopped draining
  stopped draining its own event queue too — is fixed above, but the branch
  remains reachable if a connection ever falls far enough behind.

  Unlike the rest of this list, it does have a libutp analogue. libutp has no
  queue between receiving a datagram and processing it, so it cannot drop one
  there — but an embedder that falls behind overflows the kernel's UDP receive
  buffer instead, and those packets are just as lost. The remaining difference
  is that ours discards a packet it has already decoded and attributed, which
  is more wasteful, not less correct.

  Making the dispatcher block instead would be worse by the same standard it is
  being judged against: one slow connection would stall every other connection
  on the socket, and libutp, which has no shared dispatcher, has no such
  failure mode at all.
- **An intermittent data race was observed once**, in an early `-race` run of
  `TestManyConcurrentTransfers` under conditions where many connections were
  hitting the 60 s idle timeout. The report was never captured, so there was
  nothing to fix — only a suspicion. It has now been pressed on hard, and
  found nothing:

  | | Connections torn down under `-race` | Races |
  | --- | --- | --- |
  | `TestTeardownRace`, concurrency 8 to 64 | 23,200 | 0 |
  | of which via the idle timeout specifically | 5,800 | 0 |
  | `TestManyConcurrentTransfers`, 100-300 concurrent, 5 runs | 900 | 0 |

  `TestTeardownRace` exists for this. It drives the four ways a connection can
  end — clean close, idle timeout, abandoned without reading, context cancelled
  mid-transfer — concurrently, with the sockets closed underneath while
  connections are still winding down, and with `MaxIdleTimeout` cut to 400 ms
  because at the 60 s default nothing reaches that path in bulk. It asserts
  that all four modes were actually taken, so it cannot quietly stop testing
  what it claims to.

  **This does not prove the package is race-free, and it is not evidence the
  original observation was mistaken.** A race is a timing accident; a negative
  result bounds its likelihood rather than excluding it. Two gaps are worth
  naming: the original sighting was at 1000 concurrent transfers, and these
  runs reach 300 (the full test peaks around 5 GB RSS, which `-race` makes
  worse); and a race the detector never sees because the two accesses never
  interleave during a run is exactly the kind that survives a soak.

  What has changed is the standing of the suspicion. It was "unexplained and
  unreproduced"; it is now "unreproduced across 24,100 teardowns under the
  detector, including 5,800 through the path it was seen on". The harness is
  checked in, so the next change to the teardown paths is measured against it
  rather than against a memory.
- **Per-read allocation, mostly closed.** `processReads` allocated
  `MaxPacketSize` for every chunk and handed the whole buffer to the reader
  with a separate length, so a 40-byte chunk kept 1400 bytes alive. It now
  sizes the buffer to `receiveBuffer.Readable()`, and skips the allocation
  entirely when the buffer holds only out-of-order bytes, which are not
  readable yet.

  **Measured, and smaller than the description suggests: about 2%.** Five runs
  each way, 2000 messages of 100 bytes: 11.74-11.85 MB allocated after, against
  11.90-12.16 MB before. On a bulk transfer there is no difference at all
  (16.95 against 16.94 MB). The reason is that `Read` drains *all* contiguous
  bytes at once, so under load the buffer is full regardless of how small the
  individual packets were, and the over-allocation only showed up on the
  trailing chunk. The reader still receives `Data` plus a separate `Len` rather
  than a right-sized slice; changing that is an API change for no measured
  gain.

## API notes

- `ReadToEOF` returns a nil error on a clean end of stream, matching
  `io.ReadAll`. It previously returned `io.EOF` on one path and nil on
  another; the two in-tree callers disagreed about which to expect. Abnormal
  closes still carry their error (`ErrTimedOut`, `ErrReset`).
- `Accept`/`AcceptWithCid` can now return `ErrAcceptTimedOut`. Callers that
  previously blocked forever will now see an error.
- `AWAITING_CONNECTION_TIMEOUT` is a fixed 20 s and is not configurable.
