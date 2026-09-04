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
| **M2** — conformance harness against real libutp | **Not done.** |
| **M3** — fix the failing transfer tests | **Done.** Root causes below; gates in "Verification". |
| **M4** — audit transfer paths against libutp | **Not done** (needs M2). |
| **M4b** — exhaustive libutp compatibility sweep | **Not done.** No `COMPATIBILITY.md` exists. |
| **M5** — verify LEDBAT, add LEDBAT++ | **Not done.** But see "The congestion controller had no delay signal" — this milestone's premise has changed. |
| **M6** — MTU path discovery | **Not done.** Not investigated. |
| **M7** — anacrolix/torrent integration | **Not done.** No adapter, no interop test against libutp. |
| **M8** — soak and hardening | **Not done.** |

**No interoperability testing against real libutp has been done at all.** The
M7 interop gate is described in the brief as non-negotiable, and it is
unmet. Nothing here has been shown to talk to a real BitTorrent peer. Until
that gate passes, treat this as a library that talks to itself correctly.

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
never adapt to the path: on any link with an RTT above `minTimeout` (500 ms),
uTP would have retransmitted continuously.

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

`native/cgo` also did not build: it needs `utp_lib.h` and `libutp_lib.a`,
which are not vendored, so `go build ./...` failed for the whole module. It is
now behind the `utp_cgo_harness` build tag.

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
