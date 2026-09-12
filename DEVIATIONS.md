# Deviations from libutp

The overriding rule for this fork: **when unsure, do exactly what libutp
does.** uTP has no adequate specification, so libutp's behaviour is the
specification, because that is what every peer in the wild is running. See
[REFERENCE.md](REFERENCE.md) for which copy is normative.

This file lists every intentional difference. A difference that is not listed
here is a bug, not a decision.

## Status

**This list is not yet trustworthy as a complete inventory.**

Establishing that requires the M4b compatibility sweep — reading
`bittorrent/libutp` end to end and confirming, behaviour by behaviour, that
this implementation agrees. That sweep has **not been done**, and there is no
`COMPATIBILITY.md`. Nor is there a conformance harness (M2) that could catch
divergences automatically.

So what follows is only what has been observed incidentally while fixing the
transfer-path defects. Divergences almost certainly remain, unrecorded,
because nobody has looked.

## Intentional deviations

### 1. Larger default UDP socket buffers

`Bind` requests 4 MiB for the underlying socket's send and receive buffers
(`DefaultSocketBufferSize`). libutp does not manage the socket at all — it is
an embedding library and leaves the socket to the host application.

**Reason:** this package owns the socket, and a single `readLoop` goroutine
drains it. Anything the kernel drops before that goroutine runs is invisible
loss the protocol then has to recover from. At the OS default a few hundred
concurrent connections overflowed the receive queue and handshakes failed
outright. The kernel silently clamps the request to `net.core.rmem_max`, so
asking for more than the system allows is harmless.

This affects how fast packets are drained, not what is on the wire.

### 2. `ReadToEOF` returns nil at a clean end of stream

Not a wire behaviour — a Go API choice, matching `io.ReadAll`. Abnormal
closes still return their error. Noted because the previous behaviour was
inconsistent between code paths rather than a considered deviation.

### 3. `Controller` exposes a `Stats()` snapshot

The `Controller` interface gained `Stats() ControllerStats`, and
`ConnectionConfig` gained an optional `Metrics` observer. libutp has no
equivalent; it exposes state through `utp_get_stats` and its logging callback.

**Reason:** a controller that can only be observed through its effect on
throughput cannot be told apart from one doing nothing. That is not a
hypothetical concern here -- see the delay-signal and RTT findings in
[KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md), both of which were invisible
until the window and RTT could be plotted.

Observers are called from the connection's own event-loop goroutine, so they
add no locking to the data path and nothing to the wire.

### 4. `MaxConnAttempts` counts transmissions, not timeouts

libutp gives up on a connection attempt when `retransmit_count` reaches 2
while in `CS_SYN_SENT`, which works out to three transmissions of the SYN.
This implementation counts transmissions directly, so the equivalent default
is `MaxConnAttempts: 3`.

The SYN retransmission *schedule* now matches: the timeout doubles from its
current value on each attempt, starting at 3000 ms, as libutp does
(`utp_internal.cpp:1179` applied at `:1203`, initial value at `:2762`). The
only addition is a cap at `MaxTimeout`, which libutp does not have because its
own two-timeout limit bounds the backoff; it is unreachable at the default
`MaxConnAttempts` and only matters if a caller raises it.

## Selective acks whose length is not a multiple of 4 bytes

**We reject them. libutp accepts them.**

libutp's extension loop does not validate the length of a selective-ack
extension (`utp_internal.cpp:1844` — `case 1: selack_ptr = data; break;`); the
only check applied is the generic one that the extension's length fits inside
the datagram. A 1, 3, 5 or 7-byte bitfield is read and acted on.

`DecodeSelectiveAck` requires a whole number of 4-byte words and fails the
packet otherwise.

Reason: a bitfield that is not a whole number of words is truncated, and there
is no defined reading of it — acting on one means guessing which of the peer's
packets were meant to be acked. libutp itself never emits such an extension:
it always writes exactly four bytes (`utp_internal.cpp:806-818`). So no libutp
peer can send a packet this rejects, and the divergence is unreachable against
the reference implementation.

Direction matters here. Being *stricter* than libutp risks interoperability;
being *more permissive* is an attack surface. This is the stricter direction,
with the interoperability risk shown to be empty. It is asserted explicitly by
`TestMalformedSelectiveAckLength` in the M2 corpus, which fails if either side
changes: if we start accepting these, or if libutp starts rejecting them.

## Completing an incoming connection

**libutp completes one only on an `ST_DATA` packet. We complete it on the
handshake.**

`if (pk_flags == ST_DATA && conn->state == CS_SYN_RECV) conn->state =
CS_CONNECTED;` (`utp_internal.cpp:2158-2161`). Until data arrives libutp sits
in `CS_SYN_RECV`, and the guard at `:2314` drops everything else without a
word — so a peer that completes the handshake and immediately sends a FIN gets
silence until its idle timeout, and a zero-length transfer never completes at
all.

Reason: that is a defect in the reference, not a rule worth reproducing. A
peer that opens a connection, sends nothing and closes is doing something
legitimate. Our answer is one STATE for one FIN, from a peer that has already
completed a handshake, so it is neither an amplification vector nor reachable
without completing one — the two things that would make being more talkative
than libutp dangerous.

Pinned by `TestConformanceFinBeforeAnyData`, which fails if either side
changes. It is also why `FuzzDifferentialResponder` primes both implementations
with one data packet before feeding them generated input: without that, every
sequence not starting with data reports this divergence instead of finding a
new one.

## No half-close

**This one has a measured cost.** See below.

**We tear the connection down when the peer's FIN is reached. libutp keeps it
alive.**

libutp moves the socket to `CS_GOT_FIN` and waits for the local application to
close as well. `connection.eventLoop` moves to `ConnClosed` as soon as the
remote FIN is reached and everything we sent is acked, so an application here
cannot read or write after its peer has closed its sending side.

Reason: none that justifies it. This is a gap, not a choice — recorded here
because it is a live behavioural difference, and in
[KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md) as work still to do.


Running libutp over a lossy path showed what it costs: 4 of 20 transfers ended
with libutp reporting `UTP_ECONNRESET` on data that had in fact all arrived.
We acknowledge the peer's FIN and tear down; if that acknowledgement is lost,
the peer's retransmission finds no connection and draws a RESET. libutp, which
holds the socket in `CS_GOT_FIN`, simply acknowledges it again.

The socket now keeps the closing connection's acknowledgement for ten seconds
and answers a late packet with it instead of a RESET; for a packet *past* that
acknowledgement — data the peer sent after we closed, which libutp would have
delivered to its application — it stays silent, as libutp's `CS_GOT_FIN` does.
Measured: 0 of 30 seeds fail where 4 of 20 did, and a peer that writes after
our FIN sees no error and no RESET
(`netem.TestPeerMayWriteAfterOurFin`, `netem.TestLibutpCleanCloseSurvivesLostFinAck`).

So the *wire* behaviour now matches. What does not is above it: libutp would
hand that late payload to its application and let it reply, and we drop it.
That is the half-close itself, and it is still unmade. What has gone is a
finished connection telling its peer that it broke.

## The selective-ack window

Matched to libutp as of the M4 ack-path audit: a fixed 30-entry window from
`ack_nr + 2`, encoded as exactly four bytes
(`utp_internal.cpp:797`, `:805-818`). The one detail not reproduced is
libutp's `min(14+16, inbuf.size())` — the second term is the capacity of its
circular reorder buffer, which has no equivalent here, so our window is 30
entries always. That makes ours at least as wide as libutp's and never wider.

## Acks are deferred, but not batched the way libutp's are

Both implementations defer acknowledgements rather than sending one per data
packet. libutp's `utp_process_incoming` calls `schedule_ack()`
(`utp_internal.cpp:2377`), which sets a flag, and the embedder flushes it once
per read batch via `utp_issue_deferred_acks()` (`utp.h:512-517`,
`utp_internal.cpp:3796-3808`). We set `connection.ackPending` and flush at the
end of the event-loop pass that received the data.

libutp emits exactly one ack per batch, whatever the batch size. We emit
somewhere between one and one-per-packet, depending on load.

The difference is where the batch boundary falls, not whether acks are
deferred. libutp is handed a whole batch of datagrams by its embedder before
the flush, so a batch cannot split. Our packets cross three goroutines between
the socket and the connection's event loop, so a pass covers however many
happen to have arrived — several when they arrive faster than the connection
drains them, one when they do not.

Measured, on a 512 KB transfer over the emulated network: 0.82 acks per data
packet at 20 Mbps, 0.37 at 100 Mbps, 0.17 at 1 Gbps, against exactly 1.000
before deferring was implemented.

Reason for not closing it: matching libutp's count means the socket
accumulating datagrams and handing the connection a batch, which trades read
latency for return-path packets, or a delayed-ack timer, which libutp does not
have. Neither is worth it for a saving that is already largest exactly when it
matters most — at high packet rates. `TestConformanceAckCoalescing` asserts the
structural bound against real libutp and `netem.TestAckCoalescingUnderLoad`
asserts the load-dependent one, so neither can silently drift back to one ack
per packet.

A FIN is acked immediately rather than deferred, matching libutp's direct
`send_ack()` at `:2369-2370`.

## No Nagle

libutp holds back a partial packet while anything else is in flight, and
releases it when the next packet arrives:

	if (i != ((seq_nr - 1) & ACK_NR_MASK) ||
	    cur_window_packets == 1 ||
	    pkt->payload >= packet_size) {
	    send_packet(pkt);
	}

(`utp_internal.cpp:976-983`, with "flush Nagle" at `:2246-2252`.) This library
sends whatever is in the send buffer when it composes packets.

It was implemented, measured and reverted. The reasoning is worth keeping,
because the case for it looked strong and was not.

**What made it look necessary.** Counting datagrams for the same bytes on a
50 Mbps path: 4000 writes of 20 bytes produced 4020 datagrams averaging 40
bytes, where libutp moved the same payload in 58. That is a seventy-fold
difference, and it is not a real comparison -- libutp's test driver takes a
single `Write` and cannot express many small ones, so it was never asked to do
the same thing. Comparing our own sender given one large write puts it at 881
datagrams of 1210 bytes, so the packetisation is fine; what differs is the
write pattern.

**Why the implementation changed nothing.** With libutp's rule in place the
hold fired 623 times out of 4000 writes, and the packet count did not move.
The other 3377 writes had *nothing else outstanding*, which is exactly the case
Nagle is defined to send immediately (`cur_window_packets == 1`). An
application that waits for each write to be accepted before issuing the next is
never writing fast enough for Nagle to have anything to coalesce, and that is
what this library's `Write` does.

Making it bite would mean holding the bytes until an acknowledgement arrives
rather than releasing them on the next pass of the write path. That was tried.
It still did not coalesce, and it introduces a way for buffered data to sit
unsent if no acknowledgement comes -- a stall of exactly the kind this
repository has been bitten by before.

**What it cost.** The benchmark suite at five repeats came back consistently
lower: broadband 6.19 against 6.40 Mbps, shallow queue 4.54 against 4.77, long
transfer 8.73 against 8.85, two flows 6.42 against 6.63. Several of those sit
outside the previous run-to-run ranges. A run of `FuzzDifferentialResponder`
also failed during that suite and did not reproduce afterwards; unattributed,
but not dismissed either.

So: a change with no measured benefit, a measured cost, and an unexplained
differential failure alongside it. Reverted. If a workload ever appears where
this library writes faster than a round trip -- which a BitTorrent peer sending
many small protocol messages plausibly does -- this is worth revisiting, with
that workload as the gate.

## Inherited notes that claim consistency with the reference

Two comments in `conn.go` describe behaviour as matching the reference
implementation. **Both have now been verified** against `utp_internal.cpp` at
the pinned commit, and the citations sit next to them in the source:

- The initiator initialises its ACK number to the sequence number of the
  SYN-ACK minus one (`connection.onState`). libutp does exactly this:
  `conn->ack_nr = (pk_seq_nr - 1) & SEQ_NR_MASK` on receiving a SYN-ACK in
  `CS_SYN_SENT` (`utp_internal.cpp:1871-1874`).
- STATE packets always carry the next sequence number
  (`connection.statePacket`). libutp's `send_ack` writes `pfa.pf.seq_nr =
  seq_nr` (`utp_internal.cpp:781`), and `seq_nr` is the number the next data
  packet will take — assigned and then incremented in `send_packet`
  (`:1088-1089`) — so a STATE packet never consumes a sequence number.

Neither is a deviation. They are left in this file as a record that the claims
were checked rather than inherited.

## LEDBAT++

**Implemented, and opt-in.** `ConnectionConfig.CongestionAlgorithm =
AlgorithmLEDBATPP` selects it; the default is `AlgorithmLEDBAT`, which is
libutp's.

This is the largest deliberate divergence in the library and the only one the
brief asks for. It implements
[draft-irtf-iccrg-ledbat-plus-plus-01](https://datatracker.ietf.org/doc/html/draft-irtf-iccrg-ledbat-plus-plus-01):

| Mechanism | Section | What it changes |
| --- | --- | --- |
| Modified slow start | §4.1 | Starts at two packets, grows scaled by the gain, exits at 3/4 of the delay target |
| Gain scaled to the path | §4.2 | `GAIN = 1 / min(16, ceil(2*TARGET/base))` — six times slower than Reno on a 20 ms path, up to Reno's rate on a 120 ms one |
| Multiplicative decrease | §4.3 | `W += max(GAIN - W*(delay/target - 1), -W/2)` when the delay is over target |
| Periodic slowdowns | §4.4 | Drop to two packets for two round trips, then ramp back, once per ten slowdown-durations |
| A 60 ms delay target | §4.5 | Against RFC 6817's and libutp's 100 ms |

Reason: classic LEDBAT, as libutp implements it, is not less than best
effort. Measured on a sustained transfer over a 40 ms path, it leaves **33 ms
of standing queue** — its own base-delay estimate has drifted up to include
that queue, so it reports causing 500µs and cannot see the rest. LEDBAT++
leaves 1.55 ms on the same link. Full numbers in
[BENCHMARKS.md](BENCHMARKS.md).

### Two readings of an ambiguous specification

Both are stated here because they are interpretations, not transcriptions,
and a reader checking this implementation against the draft should know where
it exercised judgement.

**The §4.3 formula is applied only when the delay exceeds the target.** The
draft introduces it as what replaces LEDBAT's adjustment "when delay exceeds
target", but presents it as the whole rule. Read literally at *below* target
it yields `GAIN + W*(1 - delay/target)` — for a hundred-packet window on an
empty path, a hundred packets of growth in one round trip, which is far more
aggressive than the LEDBAT it is meant to be a gentler version of and makes
§4.2 meaningless. Below target this implementation uses RFC 6817's increase
scaled by the §4.2 gain.

**The ramp out of a slowdown is a full slow start, not a gain-scaled one.**
§4.4 says only "ramp up the congestion window according to the slow start
algorithm", and separately that a slowdown should cost "not more than a 10%
drop in the utilization of the bottleneck". Those hold together only if the
ramp doubles per round trip. Gain-scaled, on a 20 ms path where the gain is
1/6, climbing back from two packets to fifty takes about twenty-one round
trips; measured, that turned a 1.3 s transfer into 2.8 s. The initial slow
start is gain-scaled per §4.1; the slowdown ramp is not, and it stops at a
window this connection was using a moment earlier.

## Not a deviation: nothing else in the congestion controller

Apart from LEDBAT++ above, the controller now matches libutp's
`apply_ccontrol` (`utp_internal.cpp:1615-1712`) and its timeout branch
(`:1206-1228`). Seven differences were found and closed; they are listed in
[KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md), not here, because they were
defects rather than choices.
