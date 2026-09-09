# Compatibility with libutp

What has been checked against the reference implementation, **how** it was
checked, and what has not. One table per protocol area.

This is the M4b sweep. It is a map of the evidence, not a summary of the
findings — those are in [KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md), and every
deliberate difference is in [DEVIATIONS.md](DEVIATIONS.md).

## How to read the evidence column

The kinds of evidence are not equally strong, and the difference matters more
than any individual row:

| Evidence | What it establishes |
| --- | --- |
| **Differential** | Both implementations were run on the same input and their emissions compared field by field. The strongest: it cannot be fooled by a misreading of the source. |
| **Measured** | libutp's behaviour was measured on its virtual clock and ours compared against it. As strong as differential, for the things a transcript cannot show — timing. |
| **Interop** | A real transfer completed against real libutp over real sockets. Proves the whole stack agrees, but only on the paths a healthy transfer takes. |
| **Cited** | The code was read against `utp_internal.cpp` and matched by hand, with the line recorded in a comment. Weakest. It is how the selective-ack bit reversal survived: the code was self-consistent, hand-checked, and wrong. |
| **None** | Not checked. |

A row marked *cited* is a claim about a reading. Three defects in this fork
survived hand-checking and were caught only when something ran both
implementations side by side, so *cited* should be read as "believed" rather
than "verified".

## Wire format

| Area | Evidence | Notes |
| --- | --- | --- |
| Header layout, all fields | Differential + fuzz | `FuzzDecodePacketHeader`, and every corpus case compares all nine header fields |
| Version validation | Differential | `TestMalformedWrongVersion`; we accepted anything before |
| Packet-type validation | Differential | `TestMalformedUnknownPacketType` |
| Extension chain parsing | Differential | Five malformed cases: absent, overrunning, looping, unknown type, unknown first byte |
| Selective-ack bitfield | Differential + fuzz | Bit order was reversed here; `FuzzDecodeSelectiveAck` round-trips it |
| Selective-ack length validation | Differential | A deliberate divergence: we reject non-multiples of 4, libutp accepts |
| Encoder self-consistency | Fuzz | `FuzzDecodePacket`: everything we encode, we can decode |
| Unknown extensions preserved on re-encode | **None** | We discard them. Nothing forwards packets, so nothing depends on it |

## Connection setup

| Area | Evidence | Notes |
| --- | --- | --- |
| SYN contents | Differential | `TestDifferentialHandshakeIsEmitted` compares our SYN to libutp's field by field |
| Connection-id derivation | Differential | Both roles; a SYN reaching an established connection was found here |
| SYN retransmission schedule | **Measured** | 1x, 3x its 3000ms base, gives up at 7x. Ours matches |
| Initial ack number (SYN-ACK seq minus one) | Cited | `utp_internal.cpp:1871-1874`, verified during M4 |
| STATE carries the next sequence number | Cited | `:781` with `:1088-1089` |
| Completing an incoming connection | Differential | A deliberate divergence: libutp completes only on ST_DATA |
| Duplicate/retransmitted SYN | Differential | Corpus + both fuzz roles |

## Receiving

| Area | Evidence | Notes |
| --- | --- | --- |
| In-order data, ack generation | Differential | Corpus, and both differential fuzz targets |
| Out-of-order data, selective acks | Differential | `Reordering`, `WideReordering` |
| Selective-ack window width | Differential | Was unbounded; libutp scans a fixed 30 entries |
| Reorder-window bound | Differential | Was absent; a peer could push us anywhere in the sequence space |
| Ack-number validation | Differential | Was absent, and previously *claimed* to match |
| Duplicate data | Differential | `DuplicateData` |
| FIN handling | Differential | Corpus; libutp acks twice, we once |
| Data past a reached FIN | Differential | A deliberate divergence: we have no half-close |
| RESET handling | Differential | Corpus, plus the rate limiting under flood |
| Zero peer window | Differential | Corpus |
| **Ack coalescing** | **Differential + measured** | libutp emits one ack per batch; we emitted one per packet. Now deferred, with the residual measured under load and recorded as a deviation |

## Sending

| Area | Evidence | Notes |
| --- | --- | --- |
| Data retransmission schedule | **Measured** | 1x, 3x, 7x, 15x its 1000ms floor. Ours matches |
| Retransmission timeout computation | Cited | `rto = max(rtt + rtt_var*4, 1000)`, `:1380` |
| Fast retransmit threshold and cap | Cited | Three duplicate acks, at most four packets per ack, `:1538`, `:1605-1606` |
| `fast_resend_seq_nr` | Cited | Was absent; one packet was resent up to 16 extra times |
| Zero-window probe | Cited | Was absent, and its absence was a deadlock |
| Keep-alive | Cited | Was absent |
| Path-MTU discovery | Cited | Implemented from `:890-925`, `:1289-1322`, `:1969-1974` |
| Don't-fragment on probes | **None** | Not implemented; we write through an abstract `Conn` |
| Packet coalescing / Nagle | **None** | Not investigated |

## Congestion control

| Area | Evidence | Notes |
| --- | --- | --- |
| LEDBAT window adjustment | Cited | Seven defects found by reading `apply_ccontrol` against ours |
| Slow start | Cited | Was absent entirely |
| Window decay rate limit | Cited | `MAX_WINDOW_DECAY`, `:51`, `:602-605` |
| Timeout window handling | Cited | Idle vs in-flight, `:1216-1228` |
| Delay clamp to RTT | Cited | `:1617-1621` |
| Application-limited guard | Cited | `last_maxed_out_window`, `:1681-1686` |
| **Behaviour under load** | **Measured** | `netem.TestLibutpOverEmulatedNetwork` runs real libutp over the same links as the benchmark suite. Ours is faster on every profile, which reads as ours being more aggressive rather than better; libutp's LAN result is bimodal on its 1000ms RTO floor |

Every *row* above the last is still *cited*: the mechanisms — slow start, the
decay limiter, the delay clamp, the application-limited guard — were matched by
hand against `apply_ccontrol`, and only the aggregate behaviour they add up to
has been compared against the reference. That aggregate comparison is worth
having and it is not the same thing. Two implementations can reach the same
goodput on a link by different routes, and this table would not tell them
apart.

Getting the load comparison to mean anything took two harness fixes, both of
which produced plausible-looking tables first: libutp's clock was in a
different epoch from ours, so what it read as queueing delay was an epoch
offset, and neither the driver nor the socket bridge told libutp the path MTU,
so it sent 288-byte packets. Both are written up in
[KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md).

## Whole-stack

| Area | Evidence | Notes |
| --- | --- | --- |
| Transfer, our initiator to libutp | **Interop** | 512 KB verified over real sockets |
| Transfer, libutp initiator to ours | **Interop** | 512 KB verified over real sockets |
| Transfer under loss against libutp | **None** | The interop gate runs on loopback, which does not lose packets |
| Many concurrent connections against libutp | **None** | Interop is one connection at a time |

## What is not covered at all

- **Per-mechanism congestion-control comparison.** libutp now runs over the
  emulated network, so the *aggregate* behaviour is measured. What is still
  uncompared is each mechanism on its own — the window trace under a step
  change in delay, the decay limiter's effect, what each does at the moment a
  queue fills. Goodput on five links is a coarse instrument for that.
- **libutp against libutp on the emulated network.** Every flow measured joins
  libutp to this library. A libutp-to-libutp flow over the same links would
  separate the reference's behaviour from what our end contributes to it.
- **Interop under adverse conditions.** The gate transfers over loopback. Loss,
  reordering and delay against real libutp are untested.
- **The initiator role in the hand-written corpus.** The differential fuzzer
  drives both roles; the M2 corpus is responder-only.
- **Timing beyond retransmission.** Ack *latency* is not compared, only ack
  *count* — and the count is compared as a bound, because our ack batching is
  load-dependent where libutp's is embedder-driven. Doing better needs an
  injectable clock in our connection.
- **IPv6.** Everything here runs on IPv4 loopback or an emulator with no
  address family at all.
