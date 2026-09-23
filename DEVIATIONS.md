# Deviations from libutp

The overriding rule for this fork: **when unsure, do exactly what libutp
does.** uTP has no adequate specification, so libutp's behaviour is the
specification, because that is what every peer in the wild is running. See
[REFERENCE.md](REFERENCE.md) for which copy is normative.

This file lists every intentional difference. A difference that is not listed
here is a bug, not a decision.

## Status

**Better founded than it was, and still not a complete inventory.**

This note used to say that there was no `COMPATIBILITY.md` and no conformance
harness, and that everything below had been noticed incidentally while fixing
transfer-path defects. All three have changed:

- [`COMPATIBILITY.md`](COMPATIBILITY.md) now records, area by area, what kind
  of evidence stands behind each claim — differential, measured, interop, cited
  or none — and says plainly where the answer is "none".
- There is a conformance corpus driven against real libutp, two differential
  fuzz targets covering both roles, an emulated network that runs libutp over
  the same links, a real-socket interop gate including concurrent connections,
  the standard library's own `net.Conn` suite, and a hash-verified torrent
  transfer through `anacrolix/torrent`.
- Every mechanism in the congestion controller now has a measurement behind it
  rather than a citation.

The M4b sweep as originally conceived — reading `bittorrent/libutp` end to end
and confirming agreement behaviour by behaviour — has now been done for the
areas that were left: `utp_process_incoming`, `utp_process_udp`, the
ack-deferral and window-reopening path, and the connection defaults. It found
two missing mechanisms and one policy divergence; see "The M4b sweep" in
[KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md). The clock-drift penalty has
since been implemented and measured. The other, `utp_read_drained`, is
implemented too -- after two reverts that turned out to rest on a
misattribution, the hang having been present with the mechanism compiled out --
and measured: 2.16s against 29.7s for a stalled reader. Measuring it found two
defects the sweep had missed, both in the sending path: the sender did not
count bytes in flight against the peer's window, and the keep-alive could
leave up to 29 seconds late. Both are fixed. Neither was a deviation; both are
recorded in KNOWN-LIMITATIONS.md.

So: the deviations below are recorded deliberately rather than noticed by
accident, and the list is worth trusting for what it contains. It is still not
a proof that nothing else differs — the sweep read the paths that carry
packets, not every line of a 3,500-line file.

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

## Unknown extension types in the header are rejected, not skipped

**Both implementations depart from BEP 29 here, in the same direction.**

BEP 29 describes the extension chain as skippable: each extension carries the
type of the next one and its own length, so a reader that does not know a type
can step over it. A well-formed extension of an unknown type ought to cost
nothing.

libutp does not skip one that appears *first*. Its extension loop would —
there is no default case, and an unrecognised type falls through to
`data += data[-1]` (`utp_internal.cpp:1844-1866`) — but the packet never gets
that far. `UTP_Version` (`utp_internal.cpp:2480`) reads:

```c
return (pf->type() < ST_NUM_STATES && pf->ext < 3 ? pf->version() : 0);
```

A first extension byte of 3 or more makes the packet report version 0. Version
0 is not uTP, so the datagram is discarded before any socket sees it.

We reject it too, so there is nothing to reconcile between the two — but the
shared behaviour is a departure from the specification rather than an
implementation of it, and that is worth stating rather than leaving for
someone to rediscover.

The gate reads only the first extension byte. An unknown type reached *through*
the chain — a selective ack whose `next` field names type 99 — passes
`UTP_Version` and is then skipped by the loop, exactly as BEP 29 says. Both
implementations accept that packet. So the rejected packet and the accepted one
differ only in the order of two extensions.

`TestMalformedUnknownExtensionType` and
`TestMalformedUnknownExtensionTypeInChain` pin both halves, each with a control
step that proves the silence is the extension's doing and not a connection that
had stopped answering for some other reason.

## The clock-drift penalty is applied to LEDBAT++ as well

**libutp has no opinion here, because it has no LEDBAT++.**

libutp applies its clock-drift penalty inside `apply_ccontrol`
(`utp_internal.cpp:1646-1650`), which is its only congestion controller. This
library also offers LEDBAT++, opt-in, implemented from the draft. The penalty
is applied in both.

Reason: the penalty defends against a peer manipulating its clock so that this
end under-measures the queue, and that attack does not care which controller
this end happens to be running. Leaving LEDBAT++ unprotected would make the
opt-in algorithm the weaker choice against a hostile peer, which is not a
trade anyone would be making knowingly.

The threshold, the formula and the estimator are libutp's unchanged; only the
second call site is new.

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

## Read-side half-close discards what is already buffered

`utp_shutdown(SHUT_RD)` sets libutp's `read_shutdown`, and from then on
arriving data is acknowledged but never handed to the application. libutp has
no receive buffer of its own -- bytes go straight to the embedder's `on_read`
callback or nowhere -- so the question of what to do with data already held
never arises there.

It arises here. `UtpStream.CloseRead` discards it: it is owed to a reader that
has said it will not come back, and holding it would keep the advertised
window closed by exactly that much, which is the one thing the feature exists
to avoid.

## ~~No half-close~~ — closed

**This was the one place libutp could do something an application here could
not.** It is implemented.

libutp holds a socket in `CS_GOT_FIN` when the peer closes its sending side and
keeps delivering to its application, and offers `utp_shutdown(s, SHUT_WR)`
(`utp.h:176`) for the other direction. This library tore the connection down on
reaching the peer's FIN, so a peer closing its sending side took the whole
connection with it — including whatever it was still trying to send back.

`UtpStream.CloseWrite`, and `utpnet.Conn.CloseWrite` so that anything holding a
`net.Conn` can find it by type assertion, now do what `utp_shutdown(SHUT_WR)`
does: flush, send the FIN, keep reading. And reaching the peer's FIN no longer
ends the connection — the reader gets its end-of-stream marker and the writer
carries on.

Measured against real libutp
(`netem.TestCloseWriteDeliversWhatThePeerSendsAfterIt`): we write, half-close,
libutp sees end of stream and sends 4096 bytes back, and we read all of them.
The same exchange with `Close` still discards the reply, correctly, because
there is nobody left to give it to —
`netem.TestPeerMayWriteAfterOurFin` pins that half.

What the implementation turns on, which is not obvious: libutp destroys a
socket whose FIN has been acknowledged only when `close_requested` is set
(`utp_internal.cpp:2178-2182`), and `utp_shutdown` does not set it where
`utp_close` does. Without that distinction a half-close would end the moment
its own FIN came back, which is the opposite of the point. `closeRequested`
here is the same flag.

**The other direction is implemented too**, as of the parity audit:
`UtpStream.CloseRead` / `utpnet.Conn.CloseRead` are `utp_shutdown(SHUT_RD)`.
See the note above on what happens to data already buffered, which is the only
place the two implementations can differ, and
[KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md) for why acknowledging what you
discard is the whole of it.

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

## Close waits; libutp's does not

**libutp's `utp_close` never blocks the application.** It sends the FIN, sets
`close_requested` and returns (`utp_internal.cpp:3232-3247`); the socket stays
in `CS_FIN_SENT` until `utp_check_timeouts` retires it, and whether the tail
was delivered is the embedder's problem.

`UtpStream.Close` waits for the connection to flush what is queued. The reason
is an API difference rather than a protocol one: `Write` here returns once the
data is in the send buffer, so `Write(payload); Close()` -- the shape every Go
caller writes -- would otherwise lose whatever had not gone out yet. libutp's
embedders arrange that themselves by keeping the context alive and pumping it;
this library has no equivalent thing for a caller to keep alive.

The wait is bounded by the peer going silent rather than by a clock
(`closeStallTimeout`, two seconds -- twice libutp's 1000 ms RTO floor), and
when it gives up it gives up only on waiting: the connection goes on
retransmitting and ends on its own, exactly as libutp's would. So the
divergence is that a caller here is held for up to one flush or two seconds of
silence, whichever comes first, where libutp's is held for neither.

It was unbounded before, which was a defect rather than a deviation, and cost
31 to 60 seconds per close on a connection whose peer had gone. See
[KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md).

## The delay clamp uses one packet's RTT, not the batch minimum

libutp feeds its congestion controller a delay clamped to the minimum
round-trip time of the packets the acknowledgement covers:

```cpp
// the delay can never be greater than the rtt. The min_rtt
// variable is the RTT in microseconds
int32 our_delay = min<uint32>(our_hist.get_value(), uint32(min_rtt));
                                        (utp_internal.cpp:1615-1621)
```

`apply_ccontrol` there runs once per acknowledgement, over a batch of packets,
so `min_rtt` is the smallest round trip among them. This library's controller
runs once per *acknowledged packet* -- the two are equivalent for the gain,
which scales by each packet's share of the window -- so the batch does not
exist at the point the clamp is applied, and it clamps to that packet's own
round trip instead.

The difference is a bound: a per-packet RTT is never smaller than the minimum
across the batch it belongs to, so our clamp is at most as tight as libutp's
and never tighter. The gap is whatever the round trip varied by within one
acknowledgement's coverage, and it only matters at all for a peer reporting a
delay that falls between the two -- larger than the batch minimum, smaller than
this packet's own RTT.

What the clamp is for is unaffected, and is measured:
`TestDelayClampedToRTT`.

## A selective ack riding on an unmatched acknowledgement is dropped

libutp applies the selective-ack extension whatever `ack_nr` says. When the
cumulative acknowledgement covers nothing -- `if (acks > cur_window_packets)
acks = 0;` (utp_internal.cpp:1907) -- the extension is still read, relative to
that same acknowledgement number.

`processAck` drops both together. The reason is concrete and narrow: this
implementation applies the extension relative to the cumulative
acknowledgement number, and that number has just been established to name
nothing this connection is tracking. Honouring the extension would mean
indexing the loss-detection machinery off a figure already known to be
outside the window, which is reachable by anyone who can source-spoof one
`ST_STATE`.

What is lost is small and self-correcting: the peer's next acknowledgement
carries the same selective-ack information against a cumulative number that
does match. What is gained is that no untrusted acknowledgement number reaches
loss detection.

The cumulative half of the rule is not a deviation -- ignoring the
acknowledgement is exactly what libutp does, and resetting the connection was
the bug. See [KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md).

## `WriteV` blocks, and has no 1024-buffer limit

`UtpStream.WriteV` is libutp's `utp_writev` (utp_internal.cpp:3154-3239), with
two differences.

**It blocks until everything is queued.** libutp's writev is non-blocking: it
sends what fits in the window, returns how much that was, and leaves the rest
to the caller. `Write` here has always blocked instead, and two different
contracts for the same operation in one package would be worse than the
difference. The deviation is `Write`'s, not this one's.

**It has no `UTP_IOV_MAX`.** libutp caps at 1024 buffers and silently
truncates beyond that:

```cpp
static utp_iovec iovec[UTP_IOV_MAX];
if (num_iovecs > UTP_IOV_MAX)
    num_iovecs = UTP_IOV_MAX;
memcpy(iovec, iovec_input, sizeof(struct utp_iovec)*num_iovecs);
                                        (utp_internal.cpp:3156-3172)
```

That limit is the size of a fixed static array, not a protocol rule -- the same
process-wide `static` that forced a global lock around the vendored library in
the interop harness (see `native/libutp/VENDOR.md`). A Go slice has no such
constraint, and silently dropping a caller's data to respect an array bound we
do not have would be a defect rather than fidelity.

## A discovered path MTU only lowers the ceiling, never raises it

libutp takes `get_udp_mtu` as its MTU ceiling outright:

```cpp
void UTPSocket::mtu_reset()
{
    mtu_ceiling = get_udp_mtu();
    // Less would not pass TCP...
    mtu_floor = 576;
                                        (utp_internal.cpp:1314-1318)
```

`utp.PathMTUProvider` is the same callback, but its answer is combined with
`ConnectionConfig.MaxPacketSize` as a minimum rather than replacing it.

The reason is what the figure actually is on the machines this runs on.
Loopback reports an MTU of 65536, which is a 65508-byte datagram; a
jumbo-frame link reports 9000. Adopting either would put this library on a
probe schedule and a window-sizing regime nothing has measured it at — the
minimum congestion window is two packets, so a 65-kilobyte packet size makes
the floor 128KB — and it would do so on the *loopback* path every test in this
repository runs over. Raising the ceiling is a separate decision from fixing
the case where 1400 is too big, and only the second one has evidence behind
it.

So the fixed 1400 ceiling remains as a cap, and the deviation that recorded it
stands. What has changed is that it is no longer also a *floor* on the
ceiling: a tunnel that carries 1392 is now discovered before the first packet
rather than after a stall.

## ICMP: the next-hop MTU is converted from a link MTU to a payload size

libutp takes the figure an ICMP fragmentation-needed message carries and
compares it directly against its own MTU ceiling:

```cpp
if (next_hop_mtu >= 576 && next_hop_mtu < 0x2000) {
    conn->mtu_ceiling = min<uint32>(next_hop_mtu, conn->mtu_ceiling);
    conn->mtu_search_update();
    conn->mtu_last = conn->mtu_ceiling;
}
                                        (utp_internal.cpp:3085-3093)
```

Those are not the same quantity. The ceiling comes from `get_udp_mtu`
(`mtu_reset`, :1316) and is what fits in a **UDP payload**; `next_hop_mtu` is
the largest **IP datagram** the hop will carry, which also has to hold the IP
and UDP headers. So when the router's figure is the binding one, libutp sets a
ceiling 28 bytes too large on IPv4 and the next packet it builds at that size
is refused by the same router.

`mtuSearch.icmpFragmentationNeeded` subtracts the 28 bytes before comparing.
The reason is concrete: the whole point of honouring ICMP rather than waiting
for a probe to disappear is to avoid a round trip, and a ceiling that is
knowingly too big spends that round trip anyway.

IPv6 is not separated out. Its header is 40 bytes rather than 20, so a v6 path
gets a figure at most 20 bytes too generous -- and a probe at that size then
fails and lowers the ceiling, which is the correction the search already runs
on. Treating every path as IPv4 is therefore conservative in the direction
that matters and needs no address-family plumbing through the search.

One consequence is stated rather than hidden: on a link narrow enough that the
converted figure falls below 576, the search settles below libutp's floor.
libutp's floor is 576 of *payload* ("Less would not pass TCP...", :1318) while
its ceiling is a *link* MTU, so on such a link it keeps building 576-byte
payloads for a hop that carries 572. The floor is a heuristic, not a
guarantee, and 572 is what the link actually carries.

Measured: `TestMtuIcmpFragmentationNeededLeavesRoomForTheHeaders`,
`TestMtuIcmpFragmentationNeededBelowTheFloor`,
`TestMtuIcmpFragmentationNeededSavesProbes` (six probe rounds and three losses
without the report against four and one with it), and
`netem.TestIcmpBringsTheSearchWithinThePath`.

## ICMP: no CS_IDLE state, and one terminal state instead of two

`utp_process_icmp_error` switches on the connection's state
(utp_internal.cpp:3125-3146). Two of its cases have no exact counterpart here.

`CS_IDLE` is the state a libutp socket object is in before `utp_connect` or
`utp_accept` is called. This library has no such window -- the connection is
created *by* the call -- so there is nothing in that state to ignore. The
reason libutp gives for ignoring it ("don't pass on errors for idle/closed
connections") applies to a connection that has already finished, and
`onICMPUnreachable` skips those.

`CS_DESTROY` and `CS_RESET` both become `ConnClosed`. libutp uses the
difference to decide how soon the socket object is freed, not what the
application is told: both branches fall through to the same
`utp_call_on_error`, so the error is reported either way, which is what
happens here.

`onICMPUnreachable` also deliberately does not go through `connection.reset`.
That helper treats a reset arriving after our own FIN as a clean close and
discards the error; libutp has no such branch on the ICMP path, and "the peer
went away" is exactly what an application half way through a close needs to
hear.

## ICMP: collecting it is the embedder's job, and so is deciding what is fatal

`UtpSocket.ProcessICMPFragmentation` and `UtpSocket.ProcessICMPError` (and the
`utpnet.Socket` pass-throughs) take the quoted uTP datagram and the address it
was sent to, exactly as `utp_process_icmp_fragmentation` and
`utp_process_icmp_error` do. Neither this library nor libutp opens a raw
socket to collect ICMP, and neither decides which ICMP types are fatal. On
Linux the usual source is `IP_RECVERR` on the UDP socket.

This is not a deviation; it is recorded here because the capability is easy to
mistake for automatic.

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
