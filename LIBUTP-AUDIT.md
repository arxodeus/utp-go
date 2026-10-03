# libutp, line by line

DEVIATIONS.md lists every difference from libutp that anyone has found. Until
this audit it could not claim to list every difference there *is*: the sweep
behind it read every path that carries packets, not every line of
`utp_internal.cpp`. This document reads every line, function by function, and
records for each one what this library does instead and on what evidence. A
difference is either fixed here, with a test that fails without the fix, or
recorded in DEVIATIONS.md with its reason.

The reference is `github.com/anacrolix/go-libutp@v1.3.2`, the copy this
repository builds and tests against (REFERENCE.md). Line numbers are that
copy's.

**Status: complete.** Every line of `utp_internal.cpp` has been read, and
the support files with it (`utp_api.cpp`, `utp_utils.cpp`,
`utp_callbacks.cpp`, `utp_hash.cpp`, `utp_packedsockaddr.cpp`,
`utp_internal.h`, `utp_templates.h`). Every difference found is either fixed,
with a test that fails without the fix, or recorded in DEVIATIONS.md with its
reason. The findings table below is the list; nothing in it is still open.

What this does not prove: that a reading missed nothing. Two rows here were
first marked "matched" and turned out not to be (N24, N25). Both were caught
by measuring, not reading. Where a difference could be measured against
libutp it was, and the tests named are what stands behind each verdict.

## How to read the verdicts

- **Matched**: same behaviour, and the evidence named (a citation, a
  differential test against libutp, or a measurement).
- **Fixed**: differed; now matches; the named test fails without the fix.
- **Deviation**: differs on purpose; the DEVIATIONS.md entry is named.
- **Open**: differs, not yet resolved.
- **N/A**: no behaviour a peer or caller can observe (logging, statistics,
  storage layout).

## Findings

| # | libutp | What differed | Resolution |
| --- | --- | --- | --- |
| N34 | `outbuf` (`:1071-1077`), the receive test at `:1887` | libutp's send buffer holds the window and its receive test is relative to `ack_nr`, so sequence numbers wrap freely. Ours kept every packet ever sent, indexed from the first sequence number modulo 65536, and counted a packet received if it lay between the first and the ack number. Past 65,535 packets (about 90 MB) the sender booked new packets as retransmissions of acknowledged ones and flooded, and the receiver dropped every new packet as a duplicate. Found after the line-by-line audit, by a five-minute run | Fixed: the sent packets are a sliding window, the controller's records give way to a wrapped number, and receipt is judged from the ack number (`netem.TestTransferPastSequenceWrap`, which fails with any one of the three undone) |
| N1 | `is_full`, `:939-947` | libutp never has more than 1023 packets outstanding (`OUTGOING_BUFFER_MAX_SIZE - 1`), however small; ours had no packet bound, so small writes into a large window could outrun a libutp receiver's 1024-packet reorder window, and past 32768 the sequence space | Fixed: `TestOutstandingPacketsAreCappedAt1023` |
| N1b | `send_data`, `:768` | Any packet sent takes the socket off the deferred-ack list; ours sent the pending acknowledgement anyway | Fixed, as an alignment: acknowledgements per data packet measured 0.90 before and after |
| N2 | `send_keep_alive`, `:834-844` | libutp decrements `ack_nr` *around* `send_ack`, so the keep-alive's selective ack is built from the decremented number; ours was built first, and named every packet one position early | Fixed: `TestKeepAliveSelectiveAckMatchesItsAckNumber` |
| N3 | `send_rst`, `:846-865` | A defensive reset sent from a connection with no handshake state carries a timestamp and a 100000-byte window where libutp sends zeros | Fixed by construction: it now builds the reset as the socket's own does (zero timestamps and window, the triggering packet's sequence number as `ack_nr`). No test reaches the branch: it needs a connection with no handshake state, which nothing constructs |
| N4 | `check_timeouts`, `:1191-1247` | A lost MTU probe's timeout did not count towards giving up or set `fast_timeout` | Fixed: covered by the existing MTU timeout tests, not isolated |
| N5 | `check_timeouts`, `:1184-1188` | libutp destroys a `CS_SYN_RECV` socket at its first retransmission timeout; our acceptor ignores the timeout | No difference: the branch cannot run. Nothing arms `rto_timeout` in `CS_SYN_RECV` -- `send_ack` does not, `utp_writev` refuses outside `CS_CONNECTED` (`:3181`), and `utp_close` destroys at once (`:3386-3388`). Measured: a libutp acceptor whose SYN-ACK was lost was still waiting after 30 s |
| N28 | `mtu_search_update`, `:1291`; `utp_process_icmp_fragmentation`, `:3086` | An ICMP report below the known floor leaves libutp's search inverted (an assertion in debug builds), sending above the new ceiling until the 30-minute re-search; ours lowers the floor | Deviation: "ICMP: a report below the known floor lowers the floor" (not measured) |
| N29 | `utp_utils.cpp:158-205` | libutp stamps the wire from CLOCK_MONOTONIC, guarded against running backwards; ours used the wall clock, which an NTP step moves, and the peer reads as delay | Fixed: `TestWireClockIsMonotonic` (structural: a test cannot step the host clock) |
| N30 | `utp_packedsockaddr.cpp:63-81` | libutp's peer key drops the IPv6 zone; ours keeps it | Deviation: "An IPv6 zone is part of the peer's address" |
| N31 | `utp_utils.cpp:228` | libutp's default IPv4 MTU is `UDP_IPV4_MTU`, 1402; ours was 1400 | Fixed: the ceiling is 1402 |
| N26 | `check_timeouts`, `:1239-1240`; `send_keep_alive` | libutp never closes an idle connection whose peer vanished; ours closed after `MaxIdleTimeout`, 60 s by default. Measured: libutp still open after 600 s | Fixed: no idle timeout by default; `MaxIdleTimeout` sets one (`TestSilentPeerDoesNotCloseAnIdleConnectionByDefault`) |
| N27 | `utp_process_udp`, `:2955-2958` | libutp ignores a SYN for a connection it already has, so a lost SYN-ACK ends its handshake; ours repeats the SYN-ACK. Earlier notes claimed libutp did too | Deviation: "A retransmitted SYN is answered" (`TestConformanceDuplicateSynBeforeData`) |
| N6 | `mtu_search_update`/`mtu_reset`, `:1289-1327` | A search redone after 30 minutes keeps sending at the size found (`mtu_last` is untouched); ours dropped to the midpoint | Fixed: `TestMtuSearchIsRedoneAfterTheInterval` |
| N7 | `DelayHist::get_value`, `:383-391`; `apply_ccontrol`, `:1621` | The delay the window rule works from is the least of the last three samples, not the latest | Fixed: `TestConformanceDelayFilter`, differential against libutp's own log |
| N8 | `utp_process_incoming`, `:2139` | No window update on an acknowledgement reporting no delay | Fixed: part of the N9 change |
| N9 | `utp_process_incoming`, `:1956-1987`, `:2139` | One window update per acknowledgement, with the bytes of every packet it covers and their minimum round trip; ours updated per packet, which compounds | Fixed: `TestOneWindowUpdatePerAcknowledgement`. Closes the DEVIATIONS.md entry on the clamp's round trip |
| N9b | `utp_process_incoming`, `:2127-2133` | A delay longer than the round trip raises the base by the excess | Fixed: `TestDelayOverTheRoundTripRaisesTheBase` |
| N10 | `selective_ack`, `:1612` | A selective ack sets the duplicate-ack count to the packets it names, which moves when the MTU probe is judged too big | Fixed: `TestSelectiveAckSetsTheDuplicateCountOnArrival` |
| N11 | `ack_packet`, `:1380`; `check_timeouts`, `:1179` | libutp's timeout has no upper bound; ours stopped at `MaxTimeout`, 60 s by default | Fixed: no cap by default; `MaxTimeout` sets one |
| N12 | `ack_packet`, `:1362-1380` | libutp's round-trip estimator works in whole milliseconds; ours carried microseconds, so the timeout differed by the fractions | Fixed: `TestRTTSamplesAreWholeMilliseconds`. The loss probe, which libutp does not have, keeps a microsecond estimate of its own |
| N13 | `send_data`/`send_packet`, `:1080`; `send_ack`, `:780-796` | Only libutp's `ST_STATE` carries a selective ack; ours attached one to data, FIN and resent packets, and reserved room for it in every packet's payload | Fixed: `TestConformanceDataPacketCarriesNoSelectiveAck`, differential. Two-way transfers over loss, 15 seeds each, before and after: medians 1.39/1.86/5.53 s against 1.41-1.47/1.91-1.92/4.76 s at 1/3/5% loss, and five more repetitions at 5% gave 4.97-5.55 s; retransmissions 238/752/1277 against 244-248/737-748/1254. No loss of recovery visible within run-to-run spread |
| N14 | `utp_process_incoming`, `:1850-1856` | An "extension bits" extension (type 2) of any length but 8 makes libutp drop the packet; ours accepts it | Fixed: `TestConformanceExtensionBitsMustBeEightBytes`, differential. Checked on a connection's packets only: libutp answers a SYN whatever its extensions say, because `utp_process_udp` sends the SYN-ACK whatever `utp_process_incoming` returns (`:2984-2990`) |
| N15 | `utp_process_incoming`, `:2000-2002` | A zero timestamp is no timestamp: libutp echoes no delay and takes no sample; ours echoed `now - 0` | Fixed: `TestConformanceZeroTimestampIsNotADelay`, differential |
| N16 | `utp_process_incoming`, `:2144` | The peer's window is read only from a packet that has passed the acknowledgement and reorder-window checks; ours took it from every packet, so a forged one could close it | Fixed: `TestDiscardedPacketDoesNotSetThePeerWindow` |
| N17 | `utp_process_incoming`, `:2148-2151` | Every zero-window packet restarts the 15-second probe deadline; ours kept the first | Fixed: `TestZeroWindowProbeDeadlineRestartsOnEachZeroWindow` |
| N18 | `utp_process_incoming`, `:2381-2386` | Data numbered past the peer's FIN is dropped unacknowledged; ours closed the connection | Fixed: `TestConformanceDataPastTheFinIsDropped`, differential |
| N19 | `utp_process_incoming`, `:2316` | A second FIN with another number does not move the end of the stream and is handled as data; ours reset the connection | Fixed: `TestConformanceSecondFinDoesNotMoveTheEnd`, differential |
| N20 | `utp_process_incoming`, `:2425-2431` | An out-of-order packet already held is discarded unacknowledged; ours acknowledged it | Fixed: `TestConformanceDuplicateOutOfOrderData`, differential |
| N21 | `flush_packets`/`is_full`, `:931-985` | The FIN waits for room for a packet like any queued packet; ours went as soon as the send buffer emptied, and with more in flight than a halved window the controller refused it and `transmit` panicked -- one two-way run in six at 5% loss crashed | Fixed: `TestFinWaitsForRoomInTheWindow` (panics without the fix) |
| N22 | `utp_process_incoming`, `:2028` | The clock-drift average's base is re-seeded whenever it is zero; ours seeded it once | Fixed: `TestDriftBaseOfZeroIsReseeded` |
| N25 | `utp_process_incoming`, `:2302-2308` | libutp makes the socket writable after any incoming packet that leaves the window below full; ours woke the writer only for ST_STATE. With data both ways the peer's acknowledgements ride on its data packets, and in 3 runs of 15 one direction sat idle with 1 MB queued and room in its window for about 30 s | Fixed: `TestAcknowledgementOnADataPacketWakesTheWriter`, and `netem.TestTwoWayTransferOverLoss` (8 runs at 5% loss, each under 15 s; failed at 36 s without the fix) |
| N24 | `utp_process_incoming`, `:2358`; `check_timeouts`, `:1144-1200` | Once the peer's stream has ended, libutp arms its timeout 60 ms out (`min(rto * 3, 60)` with `rto` at least 1000). With nothing in flight that only decays an idle window early; with data in flight it is a spurious retransmission timeout that cuts the window to one packet. Measured: libutp's first resend moves from 3.1 s to 100 ms; ours stays at 3.1 s | Deviation: "Reaching the end of the peer's stream does not time out what is in flight" (`TestPeerFinDoesNotTimeOutWhatIsInFlight`) |
| N23 | `utp_process_incoming`, `:2339-2343` | libutp hands every in-order byte to its embedder and has no buffer to overflow; ours drops a packet that does not fit its receive buffer | Deviation: "A packet that does not fit the receive buffer is dropped" |
| -- | `DELAY_BASE_HISTORY`, `:50` | Base delay over about thirteen minutes; ours two | Deviation, and now recorded as one: "The base delay is the lowest over two minutes" |

## Ledger

### 1-111: constants

| libutp | Ours | Verdict |
| --- | --- | --- |
| `TIMEOUT_CHECK_INTERVAL` 500 | timers fire when due | Deviation: "Timeouts act when due" |
| `MAX_CWND_INCREASE_BYTES_PER_RTT` 3000 | `defaultMaxWindowSizeIncBytes` | Matched: `TestConformanceLedbatRules` |
| `CUR_DELAY_SIZE` 3 | `curDelaySize` | Fixed (N7) |
| `DELAY_BASE_HISTORY` 13 | `DelayWindow` two minutes | Deviation |
| `MAX_WINDOW_DECAY` 100 | `maxWindowDecayInterval` | Matched, measured |
| `REORDER_BUFFER_MAX_SIZE` 1024 | `reorderBufferMaxSize` | Matched, differential |
| `OUTGOING_BUFFER_MAX_SIZE` 1024 | `maxOutstandingPackets` | Fixed (N1) |
| `PACKET_SIZE` 1435 | zero-window probe | Deviation: "The zero-window probe fits a packet at any MTU" |
| `MIN_WINDOW_SIZE` 10 | two packets | Deviation: "The congestion window starts at, and never falls below, two packets" |
| `DUPLICATE_ACKS_BEFORE_RESEND` 3 | `LossThreshold`, `duplicateAcksBeforeResend` | Matched, measured |
| `ACK_NR_ALLOWED_WINDOW` 3 | `ackNrAllowedWindow` | Matched, differential |
| `RST_INFO_TIMEOUT`, `RST_INFO_LIMIT` | `utp_socket.go` | Matched, flood corpus |
| `KEEPALIVE_INTERVAL` 29000 | `conn.go` | Matched, measured |
| `PACKET_SIZE_*` buckets | none | N/A (statistics) |

### 185-392: containers, sequence arithmetic, `DelayHist`

| libutp | Ours | Verdict |
| --- | --- | --- |
| `SizableCircularBuffer` | slices and a B-tree | N/A (storage; its bound is N1) |
| `wrapping_compare_less` | `wrappingLessThan` | Matched; libutp's initiator defect through it is a deviation ("An initiator can fast-retransmit from its first packet") |
| `DelayHist::add_sample`, `shift`, `clear` | `delayAccumulator` | Matched except the history length (deviation above); `shift` measured (clock drift) |
| `DelayHist::get_value` | `filteredDelayMicros` | Fixed (N7) |

### 394-706: the socket and its inline methods

| libutp | Ours | Verdict |
| --- | --- | --- |
| `get_rcv_window` | `receiveBuffer.Window` | Matched, differential |
| `can_decay_win`, `maybe_decay_win` | `decayOnLoss` | Matched, measured |
| `get_udp_mtu`, `get_udp_overhead`, `get_overhead` | `PathMTUProvider`; statistics | Matched (MTU rows in COMPATIBILITY.md); overhead is statistics only, N/A |
| `extensions[8]` from a SYN (`:1857`) | not kept | N/A: libutp stores and logs it and never reads it |
| `average_delay`, `clock_drift` | `driftEstimator` | Matched, measured |
| the ack list (`schedule_ack`, `removeSocketFromAckList`) | `ackPending` | Matched in effect; an acknowledgement may cover several packets, a deviation ("Acknowledgements: one per read, fewer when they would crowd the way back") |
| `utp_register_sent_packet` | none | N/A (statistics) |

### 707-930: the send path

| libutp | Ours | Verdict |
| --- | --- | --- |
| `send_to_addr` | socket write | N/A beyond the write |
| `send_data`: stamps, and leaves the ack list on any send (`:768`) | `transmit` | Fixed: a pending acknowledgement is now cleared by a data packet carrying the same number. Measured no change in acknowledgements per data packet (0.90 before and after, two-way transfer); an alignment, not an improvement |
| `send_ack`: selective ack only while reordered and before the FIN, window of 30 | `statePacket` | Matched (`TestConformanceDataAfterReachedFin`); the window size is a deviation ("The selective-ack window") |
| `send_keep_alive` | `keepAlivePacket` | Fixed (N2) |
| `send_rst` | `utp_socket.go`, and a defensive branch in `conn.go` | Matched; the defensive branch is N3 |
| `send_packet`: window accounting, ack refresh, probe eligibility, don't-fragment | `transmit`, `mtuSearch.eligibleProbe` | Matched, measured (MTU rows). The `seq_nr != 1` guard is libutp's "no probe" sentinel; ours is a flag: N/A |
| data packets carry no extension (`:1080`) | `transmit`, `retransmit`, `resendSentPacket` | Fixed (N13): ours carried a selective ack on data, FIN and resent packets; now only `ST_STATE` does (`TestConformanceDataPacketCarriesNoSelectiveAck`) |

### 931-1328: `is_full`, `flush_packets`, `check_timeouts`, the MTU search

| libutp | Ours | Verdict |
| --- | --- | --- |
| `is_full` | window checks in `processWrites`, `finFits` | Matched; the packet cap was N1 |
| `flush_packets`: the Nagle check (`:974-982`) | the hold in `processWrites` | Was absent (recorded as "No Nagle"); now matched for data, with `NoDelay` to turn it off. Our FIN is not held by it (DEVIATIONS.md, "Nagle: libutp's rule, with an opt-out") |
| `check_timeouts`: MTU probe branch | `onTimeout` | Fixed (N4) |
| `check_timeouts`: `CS_SYN_RECV` destroyed at the first timeout | acceptor ignores it | No difference (N5): the branch cannot run in libutp, as nothing arms `rto_timeout` in `CS_SYN_RECV` |
| `check_timeouts`: give up at the fifth timeout, two for a SYN | `maxConsecutiveTimeouts`, `MaxConnAttempts` | Matched; the SYN count is a deviation ("`MaxConnAttempts` counts transmissions") |
| `check_timeouts`: timeout doubling, `fast_timeout`, `need_resend` | `onTimeout` | Matched, measured (`TestConformanceRetransmissionTimeoutComputation`); no cap by default (N11) |
| `check_timeouts`: keep-alive only before our FIN | `keepAlive` | Matched |
| `mtu_search_update`, `mtu_reset` | `mtuSearch` | Matched, measured; the start, at the midpoint until it was moved to libutp's ceiling (DEVIATIONS.md, "The MTU search starts at the ceiling"), and N6 (fixed) |

### 1329-1400: `ack_packet`

| libutp | Ours | Verdict |
| --- | --- | --- |
| already acknowledged / never sent | `OnAck`'s `Acked` guard | Matched |
| round trip only from a packet sent once (Karn) | `NumTransmissions == 1` | Matched: `TestRetransmittedPacketDoesNotUpdateRTT` |
| estimator in whole milliseconds | `updateRTT` | Fixed (N12) |
| `rtt_hist` | none | N/A (feeds only a log field) |
| `rto = max(rtt + 4 rtt_var, 1000)` | `applyTimeoutAdjustment` | Matched; no upper bound by default (N11) |
| deadline restarted by every acknowledged packet, selectively acknowledged ones included | cumulative only | Deviation: "A selective ack does not restart the retransmission timeout" |
| `cur_window -= payload` unless `need_resend` | `OnAck` | Matched |
| `retransmit_count = 0` | `processAck` | Matched |

### 1403-1613: `selective_ack_bytes`, `selective_ack`

| libutp | Ours | Verdict |
| --- | --- | --- |
| bytes and minimum round trip of selectively acknowledged packets, 50 ms if the clock has not moved | `ackBatch`, `ackBatchRTTFallback` | Fixed (N9) |
| loss count: set bits above each packet, within the window | `DetectLostPackets`'s count of acknowledged packets above | Matched in effect: the receiver repeats every out-of-order packet in each selective ack, so the two agree unless the mask is truncated |
| resend when at or past `fast_resend_seq_nr` with three above | `DetectLostPackets` | Matched, measured |
| at most four resends, lowest first, `fast_resend_seq_nr` past each | `TakeLostPackets` | Matched |
| `MAX_EACK` resend buffer | none | Matched in effect: with four resends lowest first, the same packets go |
| one `maybe_decay_win` if anything was resent | `decayOnLoss` per loss, rate-limited to one per 100 ms | Matched in effect |
| `duplicate_ack = count` | `noteSelectiveAckCount` | Fixed (N10) |

### 1615-1730: `apply_ccontrol`

| libutp | Ours | Verdict |
| --- | --- | --- |
| delay = min(filtered delay, minimum round trip) | `ApplyAck`, `applyCongestionControl` | Fixed (N7, N9) |
| `utp_call_on_delay_sample` | none | N/A (embedder callback; nothing here subscribes) |
| target defaults to 100 ms | same | Matched |
| clock-drift penalty | `driftEstimator.penaltyMicros` | Matched, measured |
| window factor, delay factor, gain | same | Matched: `TestConformanceLedbatRules` |
| no growth when the window has not filled for a second | `applicationLimited` | Matched, differential |
| floor | two packets | Deviation |
| slow start step `get_packet_size()` | half our floor | Deviation (the same entry) |
| clamp to `[MIN_WINDOW_SIZE, opt_sndbuf]` | `clampUint32` | Matched |

### 1733-1766: `utp_register_recv_packet`, `get_packet_size`

| libutp | Ours | Verdict |
| --- | --- | --- |
| `utp_register_recv_packet` | none | N/A (statistics) |
| `get_packet_size`: MTU less the 20-byte header | `mtuSearch.payloadSize`: less the same 20 (`mtuHeaderOverhead`) | Fixed (N13): ours reserved 26, room for a selective ack data packets no longer carry |

### 1767-2478: `utp_process_incoming`

| libutp | Ours | Verdict |
| --- | --- | --- |
| type past `ST_NUM_STATES` dropped | `DecodePacketHeader` | Matched |
| acknowledgement number outside `[seq_nr - 1 - max(cur_window_packets + 3, 3), seq_nr - 1]` dropped, SYN in `CS_SYN_RECV` excepted (`:1794-1807`) | `invalidAckNum` | Matched, differential (`TestStaleAckIsIgnoredNotFatal`, corpus) |
| header shorter than its size dropped | `DecodePacket` | Matched |
| extension chain: truncated chain dropped; last selective ack wins; type 2 must be 8 bytes; other types skipped | `DecodeRawExtensions`, `malformedExtensionBits` | Matched; type 2's length was N14. A first extension of 3 or more is rejected by both (DEVIATIONS.md, "Unknown extension types") |
| `CS_SYN_SENT`: `ack_nr` from the SYN-ACK | `onState` | Matched (initiator corpus) |
| reorder window: 1024 ahead; an old packet re-acknowledged unless ST_STATE (`:1886-1899`) | `outsideReorderWindow` | Matched, differential |
| acknowledged count, old acknowledgements count none (`:1904-1907`) | `onAck` | Matched |
| duplicate acknowledgements and the MTU probe (`:1921-1941`) | `noteDuplicateAck` | Matched, and N10 |
| acked bytes and minimum round trip (`:1956-1987`) | `ackBatch` | Fixed (N9) |
| `reply_micro` and `their_hist` (`:1994-2016`) | `peerTsDiff`, `OnPeerDelay` | Matched except a zero timestamp (N15) |
| `our_hist` sample, skipped when zero (`:2017-2024`) | `OnAckDelay` | Fixed (N7, N8) |
| clock-drift average (`:2025-2107`) | `driftEstimator` | Matched, measured; base re-seeding was N22 |
| base raised when the delay exceeds the round trip (`:2127-2133`) | `ApplyAck` | Fixed (N9b) |
| one `apply_ccontrol` per acknowledgement (`:2139`) | `ApplyAck` | Fixed (N8, N9) |
| peer window and zero-window deadline (`:2143-2151`) | `onPacket` | Fixed (N16, N17) |
| `CS_SYN_RECV` completes on ST_DATA only (`:2157`) | completes on any packet | Deviation: "Completing an incoming connection" |
| `CS_SYN_SENT` completes on ST_STATE | `onState` | Matched |
| FIN acknowledged in full: `fin_sent_acked`, destroy if close was requested | `updateClosingState` | Matched (close corpus, netem close tests) |
| `fast_resend_seq_nr` advanced with the cumulative acknowledgement | `OnAckNum` | Matched |
| `ack_packet` loop; window shrunk past selectively acknowledged packets | `onAck` | Matched |
| Nagle flush of a lone unsent packet (`:2237-2245`) | every acknowledgement runs `processWrites`, which composes a held packet once nothing is ahead of it | Matched (DEVIATIONS.md, "Nagle: libutp's rule, with an opt-out") |
| fast timeout-retry (`:2247-2276`), before the selective ack | `onFastTimeout`, before `retransmitLostPackets` | Matched in effect: the same packet goes once, in the same order |
| selective ack (`:2289`) | `onAck`, `TakeLostPackets` | Matched, and N10 |
| writable again when the window drops below full, for any packet type (`:2302-2308`) | `writable` channel | Fixed (N25): ours woke only for ST_STATE. A first reading of this row said "matched in effect"; the two-way measurement for N13 is what showed otherwise |
| ST_STATE stops here; data refused outside `CS_CONNECTED` | `onData` state switch | Matched |
| first FIN sets `eof_pkt` (`:2316`) | `onFin` | Fixed (N19) |
| in-order data: delivered unless read-shut, `ack_nr++` regardless (`:2339-2343`) | `receiveBuffer.Write` | Matched (`TestReadShutdownAcksWithoutBuffering`); no buffer limit is N23 |
| end of stream reached: immediate ack | `st_fin` handling | Matched (the double acknowledgement is asserted in the corpus) |
| end of stream reached: `rto_timeout = now + min(rto * 3, 60)` (`:2358`) | deadline unchanged | Deviation (N24) |
| reorder buffer drained in order | `receiveBuffer` | Matched |
| out-of-order data past the FIN dropped (`:2381-2386`) | `onData` | Fixed (N18); ours wraps the comparison, libutp's does not |
| out-of-order duplicate dropped unacknowledged (`:2425-2431`) | `onData` | Fixed (N20) |
| out-of-order data stored, acknowledgement scheduled | `receiveBuffer.Write` | Matched, differential |

### 2479-3153: version gate, socket lifecycle, options, connect, `utp_process_udp`, ICMP

| libutp | Ours | Verdict |
| --- | --- | --- |
| `UTP_Version`: type below `ST_NUM_STATES`, first extension below 3, version 1 | `DecodePacketHeader` | Matched (DEVIATIONS.md, "Unknown extension types") |
| `~UTPSocket`: off the ack list, buffers freed | connection teardown | N/A (storage) |
| `utp_initialize_socket`: connection id avoiding ids in use; `average_sample_time` now + 5 s; `last_rwin_decay` allows an immediate decay; window one packet; MTU search from the ceiling | `GenerateCid`, `newDriftEstimator`, `decayOnLoss`, controller defaults, `mtuSearch` | Matched, except the initial window and the search's start (both recorded deviations) |
| `last_measured_delay` | none | N/A: written, never read |
| `utp_create_socket`: `rto` 3000; `rtt_var` 800; `max_window_user` 255 x 1435; `ssthresh` = `opt_sndbuf`; `fast_resend_seq_nr` = 1 | controller and connection defaults | `rto` matched (measured: both first resend at 3.1 s, `TestPeerFinDoesNotTimeOutWhatIsInFlight`); `rtt_var` is overwritten by the first sample, N/A; `max_window_user` matters only before the SYN-ACK, when nothing can be sent, N/A; `ssthresh` matched (`TestConformanceLedbatRules`); `fast_resend_seq_nr` is the recorded initiator deviation |
| context and socket options: target delay, send and receive buffers | `ConnectionConfig` | Matched (KNOWN-LIMITATIONS.md, "The libutp API surface, audited function by function") |
| `utp_connect`: random id and sequence number, SYN with no extension and our window, first timeout 3000 ms | `ConnectWithCid`, SYN path | Matched (initiator corpus); the attempt count is a recorded deviation |
| `utp_process_udp`: short or non-version-1 datagram dropped | `DecodePacket` | Matched |
| RESET looked up by id, id + 1 or id - 1 with a matching send id; `ECONNREFUSED` while connecting | `utp_socket.go` | Matched (corpus) |
| unknown non-SYN answered with a rate-limited RESET (`RST_INFO_*`) | `utp_socket.go` | Matched, flood corpus |
| SYN for an existing connection ignored (`:2955-2958`) | SYN-ACK repeated | Deviation (N27) |
| more than 3000 sockets refuses new SYNs; firewall callback | `MaxIncomingConnections`, `Firewall` | Matched (DEVIATIONS.md, "No cap on incoming connections -- closed") |
| new acceptor: ids from the SYN, random sequence number, `CS_SYN_RECV`, SYN-ACK sent | accept path | Matched (corpus); completion on the handshake is a recorded deviation |
| `CS_SYN_RECV` timeout (`:1184-1188`) | none | Unreachable in libutp (N5) |
| `parse_icmp_payload`: version gate, lookup as for RESET | `utp_socket.go` ICMP routing | Matched |
| `utp_process_icmp_fragmentation` | `mtuSearch.icmpFragmentationNeeded` | Matched, except the unit conversion and a report below the floor (N28), both recorded |
| `utp_process_icmp_error` | `onICMP` | Matched; the states are a recorded deviation ("ICMP: no CS_IDLE state") |
| idle connection with a vanished peer | `MaxIdleTimeout`, off by default | Matched by default (N26) |

### 3154-3489: the public API

| libutp | Ours | Verdict |
| --- | --- | --- |
| `utp_writev`: refused outside `CS_CONNECTED` or after the FIN; queued while `!is_full(size)`; small writes join the last unsent packet; `CS_CONNECTED_FULL` when full | `Write`, `WriteV`, send buffer, `processWrites` | Matched in effect: what goes on the wire follows `flush_packets`, which needs a whole packet of room (`TestNothingIsSentIntoLessThanAPacketOfRoom`). Small writes did not join: the send buffer gave each packet one write at most, found when the Nagle rule changed nothing; fixed (`TestSendBufferReadSpansWrites`, `TestInitiatorNagle`). `WriteV`'s blocking and the 1024-buffer limit are recorded deviations |
| `utp_read_drained` | `onReadDrained` | Matched, measured (KNOWN-LIMITATIONS.md, "The M4b sweep") |
| `utp_issue_deferred_acks` | `flushAck` once the socket read that brought the data has been handed out (`UdpConn.readBatch` reads until the socket would block, as libutp's embedder does) | Matched; letting an acknowledgement wait for more packets is a recorded deviation |
| `utp_check_timeouts`: 500 ms pass, `RST_INFO_TIMEOUT` expiry, sockets destroyed | timers, `utp_socket.go` | Deviation: "Timeouts act when due"; reset bookkeeping matched (flood corpus) |
| `utp_getpeername`, `utp_get_context`, user data | `Cid().Peer`, Go values | N/A (API shape) |
| `utp_get_delays`: our filtered delay, the peer's, and an age | `ControllerStats` | N/A: a diagnostic. Ours reports the base and the latest raw sample, not the filtered value |
| `utp_close`: read side shut, FIN queued, destroyed once it is acknowledged; destroyed at once if still connecting | `Close` | Matched: returns at once, and the connection goes on delivering. `UtpSocket.Close` waits for closed streams while their peers answer, where destroying a libutp context discards them (DEVIATIONS.md, "Closing a stream returns at once") |
| `utp_shutdown(SHUT_RD / SHUT_WR)` | `CloseRead`, `CloseWrite` | Matched (KNOWN-LIMITATIONS.md, "`utp_shutdown(SHUT_RD)`") |
| logging, `utp_get_stats` | `slog`, `ConnectionMetrics` | N/A |

### The support files

| libutp | Ours | Verdict |
| --- | --- | --- |
| `utp_api.cpp`: context defaults -- target delay `CCONTROL_TARGET` 100 ms, `opt_sndbuf` and `opt_rcvbuf` 1 MB, default callbacks | `defaultTargetMicros`, `DefaultWindowSize`, `DefaultBufferSize` | Matched |
| `utp_utils.cpp`: microsecond clock from CLOCK_MONOTONIC, never backwards | `NowMicro` | Fixed (N29) |
| `utp_utils.cpp`: default UDP MTU 1402 on IPv4 and 1232 on IPv6, assuming Teredo | `defaultMaxPacketSizeBytes`, `ipv6Ceiling` | IPv4 fixed (N31); IPv6 matched |
| `utp_utils.cpp`: UDP overhead 28 or 76 bytes | none | N/A (statistics) |
| `utp_utils.cpp`: `rand()` for ids and sequence numbers | `RandomUint16` | Matched in effect: any uniform source |
| `utp_callbacks.cpp`: each callback a no-op or 0 when unset; `get_read_buffer_size` 0, so the window is the whole `opt_rcvbuf` | the receive buffer | Matched (the advertised window is what the buffer has free) |
| `utp_packedsockaddr.cpp`: IPv4 stored IPv4-mapped, compared with port; the IPv6 zone dropped | `UdpPeer.Hash` | IPv4 and mapped forms matched; the zone is N30 |
| `utp_internal.h`: `RST_Info`; sockets keyed by address and receive id; the context | `utp_socket.go` | Matched (flood corpus; connection lookup corpus) |
| `utp_hash.cpp`, `utp_templates.h`: hash table, growable array, `min`/`max`/`clamp` | Go maps and slices | N/A (storage) |
