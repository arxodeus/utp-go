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
| Unmatched ack numbers | **Measured** | libutp discards the count and carries on (`if (acks > cur_window_packets) acks = 0;`, `:1907`); we reset the connection. Fixed. Not visible to the corpus -- both sides answer with silence -- so it is asserted on connection state instead: `TestStaleAckIsIgnoredNotFatal`, `TestAckForAPacketNeverSentIsDropped` |
| Duplicate data | Differential | `DuplicateData` |
| Half-close, read side (`utp_shutdown(SHUT_RD)`) | **Measured** | `UtpStream.CloseRead`, `utpnet.Conn.CloseRead`. libutp's `read_shutdown` acknowledges what it discards -- `ack_nr++` sits outside the `!read_shutdown` guard (`:2344-2355`, `:2392-2395`) -- so the peer is never throttled. `utpnet.TestCloseReadKeepsThePeerSending` pushes 4MB through a 1MB receive buffer after the read side closes; the variant that buffers instead of discarding stalls at 31s |
| FIN handling | Differential | Corpus; libutp acks twice, we once |
| Data past a reached FIN | Differential | Silence on both sides after a full `Close`. After `CloseWrite` it is delivered, which is the half-close: `netem.TestCloseWriteDeliversWhatThePeerSendsAfterIt` reads 4096 bytes real libutp sent after our FIN |
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
| Path-MTU discovery | **Measured + interop** | The emulated network can now refuse oversized datagrams (`netem.Config.MTU`), which found two defects here and one shared limitation; libutp was run over the same link to establish which was which |
| Don't-fragment on probes | **None** | Not implemented; we write through an abstract `Conn` |
| ICMP fragmentation-needed | **Measured** | `UtpSocket.ProcessICMPFragmentation`, libutp's `utp_process_icmp_fragmentation`. `netem.TestIcmpBringsTheSearchWithinThePath` runs the same transfer over a 1100-byte link with the router silent and with it reporting: the search settles at 1191 bytes against 1094. The link-MTU-to-payload conversion is a deviation, recorded with its reason |
| ICMP error teardown | **Measured** | `UtpSocket.ProcessICMPError`, libutp's `utp_process_icmp_error`, including the `UTP_ECONNREFUSED` / `UTP_ECONNRESET` split on whether only the SYN had gone out. `TestProcessICMPErrorResetsAnEstablishedConnection`, `TestProcessICMPErrorRefusesAPendingConnect`, `TestProcessICMPMatchesAnAcceptedConnection` |
| ICMP connection lookup | **Measured** | libutp's three lookups (`utp_internal.cpp:3056-3058`), including a quoted SYN, which carries a receive id rather than a send id. `TestProcessICMPIgnoresWhatItCannotMatch` covers the runt, wrong-version, unknown-type, unknown-id and wrong-peer cases; `netem.TestIcmpWorksFromAMinimalQuote` covers a router that quotes only the 20 bytes it must |
| Vectored write (`utp_writev`) | **Measured** | `UtpStream.WriteV`, `utpnet.Conn.WriteBuffers`. Saves one copy against joining the buffers first: 136KB against 155KB allocated per 16.5KB vectored write, back to back (`BenchmarkWriteV` / `BenchmarkWriteJoined`). Follows Write's blocking contract rather than libutp's partial-write one, which is Write's existing deviation |
| Packet coalescing / Nagle | **Investigated, deliberately absent** | libutp holds a partial last packet while anything else is in flight (`utp_internal.cpp:976-983`, released at `:2246-2252`). Implemented here, measured, and reverted: no workload showed a benefit and it cost ~3% across the benchmark suite. See DEVIATIONS.md |

## Congestion control

| Area | Evidence | Notes |
| --- | --- | --- |
| LEDBAT window adjustment | **Partly measured** | Seven defects found by reading `apply_ccontrol` against ours. Its *delay response* is now compared directly: `netem.TestCongestionRespondsToSelfInflictedQueue` cuts the bottleneck mid-transfer and measures the standing queue each side settles at -- libutp 112-113ms, ours 118ms, neither overflowing. The individual rules below are still only read |
| Slow start | **Measured** | Was absent entirely. `netem.TestCongestionSlowStartRamp` compares the ramp against libutp's on an idle path: time to 90% of the link, libutp 588-640ms against ours 536-660ms |
| Window decay rate limit | **Measured** | `netem.TestWindowDecayIsRateLimited`: 25ms of 50% loss costs one halving (72212 to 37652 bytes, 52%, from 23 retransmissions and no timeouts). Unlimited, the same burst reaches the floor at 4% |
| Timeout window handling | **Differential** | Both branches. Idle: `netem.TestLibutpIdleWindowDecay` reads libutp's window through its first flight -- 29% kept across 8s idle against our 30%, compared as decays-by-a-third so a differing expiry count is not read as a differing rule. In flight: `netem.TestTimeoutWithDataInFlightCollapsesWindow` -- our window to 2800 bytes with slow start on, and 43560 against 44233 bytes delivered in the 250ms after recovery. The direct assertions catch a wrong branch; the shared observable does not, and is labelled as the sanity check it is |
| Timeouts for acknowledged packets | **Measured** | `netem.TestQuietConnectionDoesNotTimeOut`: a quiet connection on a lossless link took 2 timeouts and 12 retransmissions of delivered data before the fix, 0 and 0 after |
| Delay clamp to RTT | **Measured** | `TestDelayClampedToRTT`: 20 acknowledgements each claiming 30s of delay on a 20ms path leave the window at 86722 bytes; without the clamp the same acknowledgements take it to the 2800-byte floor. Measured at the controller, not on the wire -- see the note below |
| Application-limited guard | **Measured** | `netem.TestApplicationLimitedWindowDoesNotGrow`: after slow start ends, five seconds of 1 KB writes every 20ms leave the window unchanged to the byte (15677 -> 15677). Removing the guard makes it fail on every run |
| **Behaviour under load** | **Measured** | `netem.TestLibutpOverEmulatedNetwork` runs real libutp over the same links as the benchmark suite. Ours is faster on every profile, which reads as ours being more aggressive rather than better; libutp's LAN result is bimodal on its 1000ms RTO floor |

Every row above now has a measurement behind it. That was not true when this
file was written: each mechanism had been matched by hand against
`apply_ccontrol`, and the only thing compared against the reference was the
aggregate behaviour they add up to. That aggregate comparison is worth having
and it is not the same thing — two implementations can reach the same goodput
on a link by different routes, and the last row alone would not tell them
apart.

Two caveats on what these rows do and do not say. **The delay clamp is measured
at the controller, not on the wire.** The value it clamps arrives in the
timestamp-difference field of every incoming packet and reaches the controller
unaltered (`conn.go`: `delay := time.Duration(packet.Header.TimestampDiff) *
time.Microsecond`), so the controller's clamp is the only thing standing
between a 32-bit field under the peer's control and the congestion window. That
the wire value reaches the clamp unaltered is established by reading that one
line, not by a test. **And the timeout row's two halves are not equally
well pinned:** both branches are compared against real libutp, but for the
in-flight branch the cross-implementation number agrees without discriminating
— forcing the wrong branch still lands inside its tolerance — so what actually
catches a regression there is the direct assertion on our own window and
slow-start flag.

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
| Transfer under loss against libutp | **Measured** | `netem.TestLibutpInteropUnderAdverseConditions`: loss, reordering, jitter and all three, both directions. Found a close-handshake defect that loopback could not -- 4 of 20 transfers ended in `UTP_ECONNRESET` on data that had all arrived |
| Many concurrent connections against libutp | **Interop** | `TestInteropConcurrentLibutpInitiators` and `TestInteropConcurrentGoInitiators`: 16 libutp peers and one socket of ours, both directions, 256 KB each verified byte for byte with per-peer stamps so a byte on the wrong stream names its origin |
| The `net.PacketConn` contract | **Measured** | `utpnet`: goroutine accounting across 500 datagrams each way, past and moved deadlines, and Close under a blocked read. Found a goroutine leaked per datagram on the port a DHT shares |
| The `net.Conn` contract | **Differential** | `integration/nettest`: the standard library's own `golang.org/x/net/nettest` suite, eleven subtests under `-race`, with kernel TCP run as a control in the same process. Found a `Write` that retained the caller's buffer past a deadline, and a `Close` that held the caller for up to 60s |
| Firewall callback | **Measured** | `utpnet.TestFirewallRefusesBeforeAnyStateExists`: refused before any state exists and without an answer, as libutp does (`:2975-2982`). Was accepted and silently ignored |
| Real torrent transfer | **Interop** | `integration/anacrolix.TestTorrentTransferOverUtp`: two `anacrolix/torrent` clients with TCP, built-in uTP and the DHT disabled, a 4 MiB torrent in 128 pieces, verified by BitTorrent's own piece hashes. Found the dial-context defect that made every `net.Conn` consumer's connection die as its dial returned |
| IPv6 | **None** | The plumbing that differs -- address rendering, network-name mapping, peer hashing -- is unit-tested (`utpnet.TestIPv6AddressPlumbing`, `TestUdpPeerHashIdentifiesTheAddressNotItsEncoding`). An end-to-end v6 transfer is written (`utpnet.TestDialAcceptTransferIPv6`) but skips wherever there is no IPv6 stack, and the machine this was developed on has none, so it has never run |

## What is not covered at all

- **~~Most of the congestion controller, mechanism by mechanism.~~** No longer
  true, and left here because what replaced it is the more useful statement.
  Every mechanism in the table above now has a measurement behind it, and four
  of them are compared directly against real libutp. What remains uneven is the
  *kind* of evidence: the delay clamp is measured at the controller rather than
  on the wire, and for the in-flight timeout branch the cross-implementation
  number agrees without discriminating. Both are noted against their rows.

  Reading libutp's window directly would still be worth more than any of these
  and is not available: `max_window` is private to `UTPSocket`,
  `utp_socket_stats` does not report it, and reaching in means modifying the
  copy REFERENCE.md pins. Where it was needed it was inferred from libutp's
  first flight instead, which is what a peer can see.
- **libutp against libutp on the emulated network.** Every flow measured joins
  libutp to this library. A libutp-to-libutp flow over the same links would
  separate the reference's behaviour from what our end contributes to it.
- **Interop under adverse conditions over *real sockets*.** The loopback gate
  still transfers on a clean path. Loss, reordering and jitter against libutp
  are now covered over the emulated network, which is where the close-handshake
  defect was found, but nothing damages packets between two real UDP sockets.
- **The initiator role in the hand-written corpus.** The differential fuzzer
  drives both roles; the M2 corpus is responder-only.
- **Timing beyond retransmission.** Ack *latency* is not compared, only ack
  *count* — and the count is compared as a bound, because our ack batching is
  load-dependent where libutp's is embedder-driven. Doing better needs an
  injectable clock in our connection.
- **IPv6.** Everything here runs on IPv4 loopback or an emulator with no
  address family at all.
