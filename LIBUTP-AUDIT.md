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

**Status: in progress.** Done: `utp_internal.cpp` lines 1-2478 (constants,
types, the send path, timeouts, the MTU search, acknowledgement, selective
ack, congestion control, `utp_process_incoming`). Still to read: 2479-3489
(socket lifecycle, options, connect, `utp_process_udp`, ICMP, the public API)
and the support files (`utp_api.cpp`, `utp_utils.cpp`, `utp_callbacks.cpp`,
`utp_hash.cpp`, `utp_packedsockaddr.cpp`, `utp_internal.h`,
`utp_templates.h`). Until they are done, DEVIATIONS.md's own caveat stands.

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
| N1 | `is_full`, `:939` | libutp never has more than 1023 packets outstanding (`OUTGOING_BUFFER_MAX_SIZE - 1`); no such cap found here | Open: to be verified with small writes |
| N1b | `send_data`, `:768` | Any packet sent takes the socket off the deferred-ack list; ours sent the pending acknowledgement anyway | Fixed, as an alignment: acknowledgements per data packet measured 0.90 before and after |
| N2 | `send_keep_alive`, `:834-844` | libutp decrements `ack_nr` *around* `send_ack`, so the keep-alive's selective ack is built from the decremented number; ours was built first, and named every packet one position early | Fixed: `TestKeepAliveSelectiveAckMatchesItsAckNumber` |
| N3 | `send_rst`, `:846-865` | A defensive reset sent from a connection with no handshake state carries a timestamp and a 100000-byte window where libutp sends zeros | Open (minor): the branch needs a connection with no handshake state, which nothing constructs |
| N4 | `check_timeouts`, `:1191-1247` | A lost MTU probe's timeout did not count towards giving up or set `fast_timeout` | Fixed: covered by the existing MTU timeout tests, not isolated |
| N5 | `check_timeouts`, `:1184-1188` | libutp destroys a `CS_SYN_RECV` socket at its first retransmission timeout; our acceptor ignores the timeout | Open: needs the accept path read (`:2982-2992`) |
| N6 | `mtu_search_update`/`mtu_reset`, `:1289-1327` | A search redone after 30 minutes keeps sending at the size found (`mtu_last` is untouched); ours dropped to the midpoint | Fixed: `TestMtuSearchIsRedoneAfterTheInterval` |
| N7 | `DelayHist::get_value`, `:383-391`; `apply_ccontrol`, `:1621` | The delay the window rule works from is the least of the last three samples, not the latest | Fixed: `TestConformanceDelayFilter`, differential against libutp's own log |
| N8 | `utp_process_incoming`, `:2139` | No window update on an acknowledgement reporting no delay | Fixed: part of the N9 change |
| N9 | `utp_process_incoming`, `:1956-1987`, `:2139` | One window update per acknowledgement, with the bytes of every packet it covers and their minimum round trip; ours updated per packet, which compounds | Fixed: `TestOneWindowUpdatePerAcknowledgement`. Closes the DEVIATIONS.md entry on the clamp's round trip |
| N9b | `utp_process_incoming`, `:2127-2133` | A delay longer than the round trip raises the base by the excess | Fixed: `TestDelayOverTheRoundTripRaisesTheBase` |
| N10 | `selective_ack`, `:1612` | A selective ack sets the duplicate-ack count to the packets it names, which moves when the MTU probe is judged too big | Fixed: `TestSelectiveAckSetsTheDuplicateCountOnArrival` |
| N11 | `ack_packet`, `:1380`; `check_timeouts`, `:1179` | libutp's timeout has no upper bound; ours stops at `MaxTimeout` | Deviation: "The retransmission timeout is capped at `MaxTimeout`" |
| N12 | `ack_packet`, `:1362-1380` | libutp's round-trip estimator works in whole milliseconds; ours carried microseconds, so the timeout differed by the fractions | Fixed: `TestRTTSamplesAreWholeMilliseconds`. The loss probe, which libutp does not have, keeps a microsecond estimate of its own |
| N13 | `send_data`/`send_packet`, `:1080`; `send_ack`, `:780-796` | Only libutp's `ST_STATE` carries a selective ack; ours attached one to data, FIN and resent packets, and reserved room for it in every packet's payload | Fixed: `TestConformanceDataPacketCarriesNoSelectiveAck`, differential. Two-way transfers over loss, 15 seeds each, before and after: medians 1.39/1.86/5.53 s against 1.41-1.47/1.91-1.92/4.76 s at 1/3/5% loss, and five more repetitions at 5% gave 4.97-5.55 s; retransmissions 238/752/1277 against 244-248/737-748/1254. No loss of recovery visible within run-to-run spread |
| N14 | `utp_process_incoming`, `:1850-1856` | An "extension bits" extension (type 2) of any length but 8 makes libutp drop the packet; ours accepts it | Open: the SYN path decides where the check belongs |
| N15 | `utp_process_incoming`, `:2000-2002` | A zero timestamp is no timestamp: libutp echoes no delay and takes no sample; ours echoed `now - 0` | Fixed: `TestConformanceZeroTimestampIsNotADelay`, differential |
| N16 | `utp_process_incoming`, `:2144` | The peer's window is read only from a packet that has passed the acknowledgement and reorder-window checks; ours took it from every packet, so a forged one could close it | Fixed: `TestDiscardedPacketDoesNotSetThePeerWindow` |
| N17 | `utp_process_incoming`, `:2148-2151` | Every zero-window packet restarts the 15-second probe deadline; ours kept the first | Fixed: `TestZeroWindowProbeDeadlineRestartsOnEachZeroWindow` |
| N18 | `utp_process_incoming`, `:2381-2386` | Data numbered past the peer's FIN is dropped unacknowledged; ours closed the connection | Fixed: `TestConformanceDataPastTheFinIsDropped`, differential |
| N19 | `utp_process_incoming`, `:2316` | A second FIN with another number does not move the end of the stream and is handled as data; ours reset the connection | Fixed: `TestConformanceSecondFinDoesNotMoveTheEnd`, differential |
| N20 | `utp_process_incoming`, `:2425-2431` | An out-of-order packet already held is discarded unacknowledged; ours acknowledged it | Fixed: `TestConformanceDuplicateOutOfOrderData`, differential |
| N21 | `flush_packets`/`is_full`, `:931-985` | The FIN waits for room for a packet like any queued packet; ours went as soon as the send buffer emptied, and with more in flight than a halved window the controller refused it and `transmit` panicked -- one two-way run in six at 5% loss crashed | Fixed: `TestFinWaitsForRoomInTheWindow` (panics without the fix) |
| N22 | `utp_process_incoming`, `:2028` | The clock-drift average's base is re-seeded whenever it is zero; ours seeded it once | Fixed: `TestDriftBaseOfZeroIsReseeded` |
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
| `OUTGOING_BUFFER_MAX_SIZE` 1024 | none | Open (N1) |
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
| the ack list (`schedule_ack`, `removeSocketFromAckList`) | `ackPending` | Matched in effect; batching is a deviation ("Acks are deferred, but not batched") |
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
| data packets carry no extension (`:1080`) | ours carry a selective ack | Open (N13) |

### 931-1328: `is_full`, `flush_packets`, `check_timeouts`, the MTU search

| libutp | Ours | Verdict |
| --- | --- | --- |
| `is_full` | window checks in `processWrites` | Matched for bytes; the packet cap is N1 |
| `check_timeouts`: MTU probe branch | `onTimeout` | Fixed (N4) |
| `check_timeouts`: `CS_SYN_RECV` destroyed at the first timeout | acceptor ignores it | Open (N5) |
| `check_timeouts`: give up at the fifth timeout, two for a SYN | `maxConsecutiveTimeouts`, `MaxConnAttempts` | Matched; the SYN count is a deviation ("`MaxConnAttempts` counts transmissions") |
| `check_timeouts`: timeout doubling, `fast_timeout`, `need_resend` | `onTimeout` | Matched, measured (`TestConformanceRetransmissionTimeoutComputation`); the cap is N11 |
| `check_timeouts`: keep-alive only before our FIN | `keepAlive` | Matched |
| `mtu_search_update`, `mtu_reset` | `mtuSearch` | Matched, measured, except the start (deviation: "The MTU search starts at the midpoint") and N6 (fixed) |

### 1329-1400: `ack_packet`

| libutp | Ours | Verdict |
| --- | --- | --- |
| already acknowledged / never sent | `OnAck`'s `Acked` guard | Matched |
| round trip only from a packet sent once (Karn) | `NumTransmissions == 1` | Matched: `TestRetransmittedPacketDoesNotUpdateRTT` |
| estimator in whole milliseconds | `updateRTT` | Fixed (N12) |
| `rtt_hist` | none | N/A (feeds only a log field) |
| `rto = max(rtt + 4 rtt_var, 1000)` | `applyTimeoutAdjustment` | Matched; upper bound N11 |
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
| `get_packet_size`: MTU less the 20-byte header | `mtuSearch.payloadSize`: less 26, room for a selective ack | Open (N13) |

### 1767-2478: `utp_process_incoming`

| libutp | Ours | Verdict |
| --- | --- | --- |
| type past `ST_NUM_STATES` dropped | `DecodePacketHeader` | Matched |
| acknowledgement number outside `[seq_nr - 1 - max(cur_window_packets + 3, 3), seq_nr - 1]` dropped, SYN in `CS_SYN_RECV` excepted (`:1794-1807`) | `invalidAckNum` | Matched, differential (`TestStaleAckIsIgnoredNotFatal`, corpus) |
| header shorter than its size dropped | `DecodePacket` | Matched |
| extension chain: truncated chain dropped; last selective ack wins; type 2 must be 8 bytes; other types skipped | `DecodeRawExtensions` | Matched except type 2's length (N14); a first extension of 3 or more is rejected by both (DEVIATIONS.md, "Unknown extension types") |
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
| Nagle flush of a lone unsent packet (`:2237-2245`) | none | Deviation: "No Nagle" |
| fast timeout-retry (`:2247-2276`), before the selective ack | `onFastTimeout`, before `retransmitLostPackets` | Matched in effect: the same packet goes once, in the same order |
| selective ack (`:2289`) | `onAck`, `TakeLostPackets` | Matched, and N10 |
| writable again when the window drops below full | `writable` channel | Matched in effect |
| ST_STATE stops here; data refused outside `CS_CONNECTED` | `onData` state switch | Matched |
| first FIN sets `eof_pkt` (`:2316`) | `onFin` | Fixed (N19) |
| in-order data: delivered unless read-shut, `ack_nr++` regardless (`:2339-2343`) | `receiveBuffer.Write` | Matched (`TestReadShutdownAcksWithoutBuffering`); no buffer limit is N23 |
| end of stream reached: immediate ack | `st_fin` handling | Matched (the double acknowledgement is asserted in the corpus) |
| end of stream reached: `rto_timeout = now + min(rto * 3, 60)` (`:2358`) | deadline unchanged | Deviation (N24) |
| reorder buffer drained in order | `receiveBuffer` | Matched |
| out-of-order data past the FIN dropped (`:2381-2386`) | `onData` | Fixed (N18); ours wraps the comparison, libutp's does not |
| out-of-order duplicate dropped unacknowledged (`:2425-2431`) | `onData` | Fixed (N20) |
| out-of-order data stored, acknowledgement scheduled | `receiveBuffer.Write` | Matched, differential |
