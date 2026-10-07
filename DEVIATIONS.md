# Deviations from libutp

The overriding rule for this fork: **when unsure, do exactly what libutp
does.** uTP has no adequate specification, so libutp's behaviour is the
specification, because that is what every peer in the wild is running. See
[REFERENCE.md](REFERENCE.md) for which copy is normative.

This file lists every intentional difference. A difference that is not listed
here is a bug, not a decision.

## Status

**Complete for libutp as read line by line, with the limits stated below.**

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
- Every mechanism in the congestion controller has a measurement behind it
  rather than a citation. The last to get one was LEDBAT's window adjustment,
  now compared rule by rule by replaying libutp's own `apply_ccontrol`
  decisions into ours (`TestConformanceLedbatRules`); it found one defect.

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

That reading has now been done. [LIBUTP-AUDIT.md](LIBUTP-AUDIT.md) goes
through every line of `utp_internal.cpp` and its support files and gives each
function a verdict: matched, fixed, or recorded here. It found thirty-three
differences no earlier pass had: twenty-five fixed, each with a test that
fails without the fix; seven recorded below; and one that turned out to be
dead code in libutp. It also found one difference that had been measured and
written up elsewhere but never entered here ("The base delay is the lowest
over two minutes"), and two notes here that claimed agreement where there was
none (the duplicate SYN, and the per-packet delay clamp's equivalence). Two of
the fixed differences were serious: a FIN sent into a window a loss had just
halved panicked the process, and one direction of a two-way transfer could sit
idle for half a minute (KNOWN-LIMITATIONS.md, "The line-by-line audit").

So the list below is meant to be every difference there is, not every one
someone happened to find. What stands behind that is a reading, checked by
measurement wherever libutp could be driven, and a reading can miss things:
two of the audit's own verdicts were wrong until a measurement corrected them.
A difference not listed here is still a bug.

## What kind of deviation each one is

"Intentional" means chosen, not better. Only some of these are improvements on
libutp; many are simply different, and several are costs this library carries
knowingly. A reader should not assume which from the fact of an entry.

**Better than libutp, measured.** A libutp-matching variant was built and ran
worse.

- *The congestion window starts at, and never falls below, two packets.*
  libutp's rule ran 15% slower at 5% loss and no faster anywhere; deference to
  a loss-based flow was unchanged.
- *A selective ack does not restart the retransmission timeout.* libutp's rule
  ran slower at 5% loss (0.883 of the old throughput against 0.943). Measured
  on that profile only, under classic LEDBAT.
- *The zero-window probe fits a packet at any MTU.* At an Ethernet interface's
  MTU libutp's probe never fires; ours fires at 15s. Measured against libutp.
- *A loss probe resends before the retransmission timeout* (classic LEDBAT).
  A lost fast retransmission no longer waits a second: no LAN stalls in 120
  runs against about 7% without, 15-30% more throughput at 1-5% loss, and no
  change in deference. Not applied to LEDBAT++, whose deference it cost.
- *Acknowledgements cover several packets once they queue on the way back:*
  libutp sends one per read, which on a link is one per packet, whatever the
  return path can carry; ours does the same until its acknowledgements are
  seen queueing on the way back. Measured against libutp, 20 runs each: 2.03 s
  against 2.99 s for 4 MB over a 160 kb/s return path, 2.43 s against 7.33 s
  over 64 kb/s; libutp's rule, built here, took 3.01 s and 7.33 s. Elsewhere
  the same times and as many acknowledgements as libutp's.
- *A refused MTU probe is judged at three duplicates or more, not exactly
  three.* With a receiver that batches its reads, libutp's rule judged no
  probe in 3 runs of 3 and the search stayed at 1402 bytes on a 1000-byte
  path; ours judged 5 and narrowed to 999, in 5 runs of 5. Unchanged with
  a receiver that acknowledges each datagram.

**Better on reasoning.** libutp has a defect or a hazard here that was not
copied. Not measured as an improvement.

- *A reset answering our SYN fails the dial as refused:* libutp means to
  report `UTP_ECONNREFUSED` there and cannot, because it sets `CS_RESET`
  before testing for `CS_SYN_SENT` (`utp_internal.cpp:2865-2870`); every
  reset reads as `ECONNRESET`. go-utp's pure Go port corrects the same line.
  `TestInitiatorResetInsteadOfSynAck`.
- *A socket that cannot read closes:* after 100 consecutive read errors, as
  go-libutp, go-utp and rust-utp do, so everything waiting on it fails at
  once. libutp leaves reading to its embedder.
- *Closing the socket delivers what closed streams still hold:* libutp's
  embedder must keep its context pumping or lose the tail; `UtpSocket.Close`
  waits for it while the peer answers. Closing a stream returns at once in
  both. Measured on this side only: a 512 KB tail over 2 Mbps delivered in
  full, where without the wait it was lost.
- *Completing an incoming connection:* libutp never completes a zero-length
  transfer.
- *A retransmitted SYN is answered:* libutp ignores a SYN for a connection it
  already has, so a lost SYN-ACK ends its handshake. Measured against libutp:
  retries at 3 s and 9 s drew nothing in 30 s.
- *Reaching the end of the peer's stream does not time out what is in
  flight:* libutp's does, 60 ms later, which cuts its window to one packet.
  Measured against libutp: its first resend of an unacknowledged response
  moves from 3.1 s to 100 ms when the peer's FIN arrives; ours stays at
  3.1 s.
- *An initiator can fast-retransmit from its first packet:* a libutp initiator
  whose random first sequence number lands in the wrong half never can.
  Measured against libutp: its sender took twice as long at 3-5% loss on
  those connections.
- *`WriteV` has no 1024-buffer limit:* libutp silently drops buffers past 1024.
- *ICMP: a report below the known floor lowers the floor:* libutp's search
  is left inverted, sending at sizes the router has refused. Not measured.
- *ICMP: the next-hop MTU is converted to a payload size:* libutp's ceiling
  ends up 28 bytes too large. The tests measure what an ICMP report saves, not
  this conversion against libutp's.
- *Selective acks whose length is not a multiple of 4 bytes are rejected:*
  stricter, and libutp never sends one, so it costs nothing against libutp.
- *A selective ack riding on an unmatched acknowledgement is dropped:* a
  defence against a spoofed acknowledgement, at the cost of information the
  next acknowledgement repeats. The cost is not measured.

**Different, neither better nor worse.** An API choice, a structural
consequence, or equivalent on the wire.

- *An IPv6 zone is part of the peer's address* (libutp drops it, so two
  link-local peers on different interfaces can collide),
  *larger default UDP socket buffers* (this library owns the socket; libutp
  does not), *a packet that does not fit the receive buffer is dropped*
  (this library owns that buffer; libutp has none), *`ReadToEOF` returns nil*, *`Controller.Stats()`*,
  *`MaxConnAttempts` counts transmissions*, *the selective-ack window* (always
  30 entries), *read-side half-close discards what is buffered* (libutp has no
  buffer to discard), *ICMP: no CS_IDLE state*, *`WriteV` blocks*.
- *A path MTU report lowers the ceiling but does not raise it past
  `MaxPacketSize`:* the default ceiling is libutp's default, 1402 (1232 for
  IPv6), which libutp's own `get_udp_mtu` returns without asking the
  interface. libutp with an embedder callback that reports a 1500-byte
  interface would send 1472-byte datagrams; this library does that when
  `MaxPacketSize` is raised, the report still bounding it. The default's cost
  on a plain 1500-byte path is 5% more packets and 0.16 points more header
  overhead per byte (48 bytes against 1402, and against 1472); its gain is a
  PPPoE or GRE hop beyond the local interface carried without a probe.
- *Timeouts act when due, not on a 500ms pass:* retransmissions, keep-alives
  and the zero-window probe fire within our 25ms timer tick of their
  deadline; libutp's fire on the first of its 500ms passes after it.
- *Unknown extension types are rejected:* libutp does the same. It is a shared
  departure from BEP 29, not a departure from libutp.
- *ICMP collection is the embedder's job:* the same in both; listed so it is
  not mistaken for automatic.

**A trade-off.** Better on one measure, worse on another.

- *LEDBAT++* (opt-in): takes about a third of a link from a loss-based flow
  where classic LEDBAT takes two thirds; twice the throughput on a
  high-BDP path, 60-75% less on lossy ones. (It used to be credited with 7
  times less standing queue; that was the one-way-delay version, and a lone
  LEDBAT++ flow now leaves as much as classic LEDBAT.)
- *The base delay is the lowest over two minutes, not about thirteen.* A
  clock drift's phantom queue grows with the window, measured linear, so
  libutp's is 6.5 times ours. The cost -- a queue that stands for two
  minutes is taken for the empty path -- measured against a Reno flow
  holding a queue for five minutes: 5.31 Mbps to the 13-minute window's
  4.96, 7% less yielding, within the run-to-run spread of three runs each.

**Worse than libutp, accepted.** Each has a stated reason for being carried.

- *An acknowledgement leaves about 40 us later than libutp's:* at 10 Mb/s,
  where both acknowledge every packet, the time from a data packet reaching
  the receiver's socket to its acknowledgement reaching the relay is 90 us
  here and 50 us in libutp at the median, and 1.1 ms for both at the 90th
  percentile (`TestAckTurnaround`, 45 runs each, libutp sending; the relay
  runs in a process of its own and timestamps each acknowledgement in the
  kernel). Our sender is not in it: libutp's receiver answers ours as fast
  as its own. It is the receiver, and mostly its wake: from the kernel's
  arrival timestamp to just before the acknowledgement's sendto is 45-55 us
  in libutp's thread and 79-94 us here, of which the handling is about 10
  (`BenchmarkReceivePathCold`); the rest is Go's network poller waking the
  socket's reader in a runtime with nothing else running. Isolated on this
  machine, with nothing but a sender in another process, a C thread blocked
  in `select` sees a datagram 72-79 us after the kernel stamped it, a Go
  goroutine on the network poller 89-98 us. A reader blocked in `poll` on
  its own thread was tried and was no faster. No throughput cost has been
  measured. See KNOWN-LIMITATIONS.md, "Acknowledgement latency: what is
  left".

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

Observers are called with the connection's lock held, from whichever goroutine
is running it -- its event loop, or the socket's reader handling a packet for
it -- so they add no locking of their own to the data path and nothing to the
wire. They must not block: the reader serves every connection on the socket.

### 4. `MaxConnAttempts` counts transmissions, not timeouts

libutp gives up on a connection attempt when `retransmit_count` reaches 2
while in `CS_SYN_SENT`, which works out to three transmissions of the SYN.
This implementation counts transmissions directly, so the equivalent default
is `MaxConnAttempts: 3`.

The SYN retransmission *schedule* now matches: the timeout doubles from its
current value on each attempt, starting at 3000 ms, as libutp does
(`utp_internal.cpp:1179` applied at `:1203`, initial value at `:2762`), with
no cap unless `MaxTimeout` sets one.

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

## ~~The clock-drift penalty is applied to LEDBAT++ as well~~ — no longer

**libutp has no opinion here, because it has no LEDBAT++.**

libutp applies its clock-drift penalty inside `apply_ccontrol`
(`utp_internal.cpp:1646-1650`), which is its only congestion controller. This
library applied it in LEDBAT++ too, on the reasoning that the penalty defends
against a peer manipulating its clock so that this end under-measures the
queue, and that attack does not care which controller is running.

LEDBAT++ now measures its queueing delay from round trips, as the draft's §4.5
says, and a round trip is timed entirely on this end's clock. The peer's clock
takes no part, so there is no attack to defend against, and the penalty would
only slow a flow whose peer's clock happens to drift. It is applied in classic
LEDBAT only, as in libutp.

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

## Reaching the end of the peer's stream does not time out what is in flight

When the last packet before the peer's FIN arrives, libutp acknowledges it and
then sets its retransmission deadline:

```c
conn->rto_timeout = conn->ctx->current_ms + min<uint>(conn->rto * 3, 60);
                                        (utp_internal.cpp:2358)
```

`rto` is in milliseconds and never below 1000 (`:1380`), so the `min` is always
60: the deadline is 60 ms away whatever the path. If nothing is in flight, the
timeout that follows only brings forward the decay of an idle window (`:1207`),
which this library leaves on its ordinary schedule. If
something is in flight, it is an ordinary retransmission timeout: every packet
is marked for resend, the window drops to one packet, slow start begins again,
and the oldest packet is resent (`:1144-1235`). Nothing was lost. The peer
had simply finished sending.

That is the request-and-response shape: one side writes a request and closes
its write half, and the other is still sending its answer when the FIN
arrives. libutp pays for it with its window every time. This library keeps
its retransmission deadline where the packets in flight put it.

**Measured** (`TestPeerFinDoesNotTimeOutWhatIsInFlight`), each side sending a
response and receiving nothing back. Without the FIN, both first resend it at
3.1 s, libutp's initial 3000 ms timeout on both. When the peer's FIN arrives
after the response has gone, libutp resends it at 100 ms, on the driver's
first timeout pass after the 60 ms deadline; ours still resends at 3.1 s. The
test asserts libutp's half as well, so a change in the vendored copy shows.

The constant reads like a unit slip. That is a guess about intent, and it is
not the reason for the difference. The reason is that the behaviour is a
spurious timeout on a path that lost nothing.

## An IPv6 zone is part of the peer's address

libutp keys every connection on a packed address: sixteen bytes of IPv6
address, with IPv4 stored as IPv4-mapped, and the port
(`utp_packedsockaddr.cpp:63-81`). The scope id of a link-local IPv6 address is
not copied, so `fe80::1%eth0` and `fe80::1%wlan0` are the same peer to
libutp. They are not the same host. Two of them that happen to choose the
same connection ids collide: one's packets land on the other's connection.

This library keys on the address as `net.UDPAddr` prints it, zone included.
IPv4 and IPv4-mapped IPv6 still compare equal, as in libutp, because Go prints
a mapped address in dotted-quad form.

Pinned by `utpnet.TestIPv6PeersDifferingOnlyInAddressAreDistinct`, which holds
connections with identical ids open to peers differing only in address, and
only in zone, and fails if either is dropped from the key. libutp's side is
by reading: there is no driver for two interfaces.

## A packet that does not fit the receive buffer is dropped

libutp has no receive buffer. It hands every in-order byte to its embedder's
`on_read` callback as it arrives (`utp_internal.cpp:2339-2343`) and works out
the window it advertises from `opt_rcvbuf` less what the embedder reports it
is still holding (`get_rcv_window`). Nothing in libutp ever refuses data for
want of room: if a peer sends past the advertised window, the embedder gets
it anyway.

This library owns a bounded receive buffer, advertises what is free in it,
and drops a data packet that does not fit. The acknowledgement that goes
out does not cover it, so the peer resends it once there is room. `ConnectionMetrics.RecvBufferDrops` counts
them.

The reason is structural: a buffer this library owns has to have a bound, or
a peer that ignores the advertised window decides how much memory this end
holds. Against a peer that respects the window -- libutp does, and so does
this library, since bytes in flight count against the peer's window -- no
packet is dropped. Found by the line-by-line audit (LIBUTP-AUDIT.md, N23);
already true before it, and not previously listed here.

## A retransmitted SYN is answered

An initiator that hears nothing back resends its SYN. If the first one
arrived and only the SYN-ACK was lost, the acceptor already has the
connection. libutp's `utp_process_udp` looks it up and returns without a word
(`utp_internal.cpp:2955-2958`). Nothing else on that side resends the SYN-ACK
either, and the `CS_SYN_RECV` timeout that would clean the socket up never
runs: `send_ack` does not arm `rto_timeout`, which stays 0, and
`check_timeouts` acts only on a non-zero one (`:1144-1145`). The initiator
gives up.

This library sends the same SYN-ACK again.

**Measured** against libutp's driver: an acceptor whose SYN-ACK was lost,
offered the same SYN again at 3 s and at 9 s, emitted nothing in 30 seconds
and still held the half-open connection.
`TestConformanceDuplicateSynBeforeData` asserts the difference packet by
packet: libutp silent, ours one SYN-ACK.

Earlier notes here and in KNOWN-LIMITATIONS.md said libutp acknowledges a
duplicate SYN, citing a line that turned out to be in the socket destructor.
The line-by-line audit found it.

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

## Acknowledgements: one per read, fewer when they would crowd the way back

Both implementations defer acknowledgements. libutp's `utp_process_incoming`
calls `schedule_ack()` (`utp_internal.cpp:2377`), which sets a flag, and the
embedder flushes it once per read batch via `utp_issue_deferred_acks()`
(`utp.h:512-517`, `utp_internal.cpp:3796-3808`): it reads until the socket
would block, then acknowledges.

The socket here does the same. `UdpConn.readBatch` reads every datagram the
kernel already holds, waiting only for the first (`batch_read_unix.go`; on
Linux with recvmmsg, up to sixteen a system call, `batch_drain_linux.go`), and
each connection that received data in the batch acknowledges once the whole
batch has been handed out (`UtpSocket.eventLoop`, `streamBatchEnd`,
`connection.flushAck`).

What libutp's rule means on a link: packets arrive spaced, its loop has
finished with each before the next comes, and it acknowledges nearly every
one -- 0.99 per data packet at 10 Mb/s and 20 Mb/s, 0.84 at 100 Mb/s,
measured over real sockets. A 20 Mb/s flow draws 1,750 acknowledgements a
second whatever the return path can carry. On an asymmetric line that is the
bottleneck: over a 160 kb/s return path, which carries 1,000 twenty-byte
acknowledgements a second, libutp receiving from libutp took 2.99 s for 4 MB
where the data path allows 2.0 s, and over 64 kb/s 7.33 s.

So `connection.ackEvery` lets an acknowledgement wait to cover more than one
packet once ours are seen to queue on the way back. The peer reports on every
packet how long ours took to reach it (`timestamp_difference`), and for a
connection that only receives, ours are its acknowledgements:

- *The return path:* each 0.5 ms of filtered queueing delay on that path adds
  one packet to the run, up to sixteen.
- *The rate:* for 10 s after that delay last reached 10 ms, while data packets
  arrive less than 1 ms apart, `round(1 ms / gap)` of them share one
  acknowledgement, at most four. Without it the queue the first term answers
  to stands: 2.13 s over 160 kb/s and 3.38 s over 64 kb/s.

The 10 ms is above what a path with nothing queued shows: the sender stamps
an acknowledgement's delay when it reads it, and a sender busy sending reads
late, so on symmetric paths the filtered delay peaked at 2-6 ms. Over 64 kb/s
it reaches 60 ms within the transfer.

An acknowledgement waits at most two of the latest inter-packet gaps per packet
it is still waiting for, and never more than 5 ms, so the end of a burst is
acknowledged once its packets stop coming. It never waits while a selective
ack is owed: that is how the sender learns of a loss.

Measured against libutp receiving from libutp, a libutp sender, over real
sockets through the relay, the data path 20 Mb/s, 10 ms, a 128 KB queue, 4 MB,
and 16 MB at 100 Mb/s and 1 ms both ways; 20 runs each, interleaved
(`native/libutp.TestAsymmetricAckPath` gates the 160 kb/s, 20 Mb/s and 100
Mb/s rows):

| Return path | libutp receiving | this library receiving | this library, libutp's rule |
| --- | --- | --- | --- |
| 64 kb/s | 7.33 s, 2,883 acks | 2.43 s, 818 acks | 7.33 s |
| 160 kb/s | 2.99 s, 2,795 acks | 2.03 s, 1,456 acks | 3.01 s |
| 320 kb/s | 1.97 s, 2,750 acks | 1.97 s, 2,750 acks | 1.97 s |
| 20 Mb/s | 1.96 s, 2,769 acks | 1.96 s, 2,744 acks | 1.96 s |
| 100 Mb/s both ways | 1.39 s, 9,967 acks | 1.40 s, 9,093 acks | 1.40 s |

Times are medians. At 100 Mb/s 6-7 runs in 20 took about 2.7 s for either
receiver: libutp's sender fills the relay's 256 KB queue near the end -- its
100 ms delay target is longer than the queue -- loses two packets, and waits
out a timeout for them.

The acknowledgement leaves when libutp's would, but for the runtime's wake:
the median from a data packet reaching our socket to its acknowledgement
reaching the relay is 70 us against libutp's 40 at 10 Mb/s and 50 against 30
at 100 Mb/s (`TestAckTurnaround`, 20 runs each); see "An acknowledgement
leaves about 40 us later than libutp's" above.

Over the emulated network, where this receiver now acknowledges about every
packet on a symmetric path, the benchmark suite against the previous rule
(five runs each, then twenty where a profile moved more than 1.5%): LAN at
100 Mb/s +1.5% under classic LEDBAT and +2.9% under LEDBAT++, the 5% loss
profiles +0.8% and +2.8% (within noise), and one cost, classic LEDBAT on the
high-BDP link (100 ms, 20 Mb/s): 2.05 Mbps against 2.09, every run, 8.20 s
against 8.04 s for 2 MB. There our sender's window, which never reaches the
path's 500 KB in the transfer, ends about 1 KB behind after the first second
when acknowledgements cover one packet rather than two, and stays behind.
libutp's sender is not affected the same way: on that link it takes 1.95
Mbps to libutp's receiver and to ours under either rule, and ours, at 2.05,
is still ahead of it. Why our sender's ramp depends on how many packets an
acknowledgement covers, when its window update, like libutp's, is linear in
the bytes acknowledged, is not yet known (KNOWN-LIMITATIONS.md, "Things found
but deliberately not fixed"). The emulated network hands this receiver each
datagram on its own, so it sends 0.78 acknowledgements per data packet there
where libutp's receiver, driven over the same network, sends 0.63; over real
sockets the two send the same number.

*Earlier versions of this entry* applied the rate term on every path. It
measured as a trade-off -- 1.43 s against libutp's best of 1.51 s at 100
Mb/s, half libutp's acknowledgements at 320 kb/s and 20 Mb/s, for a median
acknowledgement 210-240 us after its packet instead of libutp's 100-160 --
and both halves of the gain were the harness's. The bridge that runs libutp
over a socket moved its whole send queue after each packet, so every
acknowledgement cost libutp's sender about a millisecond of copying
(KNOWN-LIMITATIONS.md, "the libutp bridge moved its whole send queue for every
packet"): libutp's rule from our receiver took 3.3-3.9 s at 100 Mb/s, libutp's
own receiver 2.6-4.0 s in half its runs, and fewer acknowledgements read as
faster. With the bridge fixed, libutp's rule from our receiver matches libutp's
own everywhere, and the rate term gains nothing where the return path keeps
up and costs 190 us an acknowledgement at 100 Mb/s. Before that, the relay
slept between deliveries on Go timers, which wake about 1.1 ms late when the
runtime is idle, and delivered packets in clumps that libutp's loop drained
at once, which made libutp look like it sent 0.22-0.33 acknowledgements per
data packet at 100 Mb/s; it now spins for the last 1.5 ms before each delivery
(`relaySpin`).

A FIN is acked immediately rather than deferred, matching libutp's direct
`send_ack()` at `:2369-2370`.

## Nagle: libutp's rule, with an opt-out

libutp holds back a partial packet while anything else is in its window, and
later writes join it:

	if (i != ((seq_nr - 1) & ACK_NR_MASK) ||
	    cur_window_packets == 1 ||
	    pkt->payload >= packet_size) {
	    send_packet(pkt);
	}

(`flush_packets`, `utp_internal.cpp:974-982`; the join is
`write_outgoing_packet`, `:1013-1023`; the release when the window empties is
"flush Nagle", `:2246-2252`.) `connection.processWrites` now applies the same
rule: a packet short of a full one is not composed while anything is
unacknowledged or composed ahead of it, and every acknowledgement brings the
loop round again. Closing the write side releases it, as libutp's FIN does by
queueing behind it.

Two defects stood in the way, and the second is why this library's earlier
attempt at the rule, measured and reverted, changed nothing:

- No rule at all: a short write behind an unacknowledged packet went out at
  once.
- The send buffer handed out at most one write per packet
  (`sendBuffer.Read`). A run of small writes queued together still became one
  packet each, and a bulk writer whose writes were not a multiple of the
  packet size left a short packet at the end of every write. libutp fills the
  last unsent packet before starting another.

Measured against libutp over real sockets (`native/libutp.TestSmallWritesAgainstLibutp`):
2,000 messages of 100 bytes written every 0.5 ms over a 20 ms round trip, to
this library's receiver.

| Sender | Data packets | Message latency, median | 99th percentile |
| --- | --- | --- | --- |
| libutp | 139 | 15.3 ms | 44.6 ms |
| this library | 139 | 14.8 ms | 22.9 ms |
| this library, `NoDelay` | 1,921-1,925 | 11.0 ms | 19.0-19.5 ms |
| this library before | 2,000 | 10.9 ms | 19.9 ms |

Both at the 1472-byte datagram the bridge reports to libutp; at this
library's default 1402 it sends 146, the difference being packet size alone.
libutp's latency includes its bridge's loop, which can wait up to 20 ms for
its socket before it hands a write to libutp, so the latency columns are not a
comparison of the two libraries.

`ConnectionConfig.NoDelay` turns the rule off, as `TCP_NODELAY` does, for an
application that would rather have each message sooner than fewer packets.

A FIN is not held by the rule here. libutp's is a packet with no payload, so
its Nagle check holds it until everything before it is acknowledged; ours goes
out as soon as the data before it has been sent, and the peer learns of the
end of the stream up to a round trip sooner. Not measured against libutp: its
driver's window of one packet holds the data before the FIN in the corpus,
which hides the difference.

## Closing a stream returns at once; closing the socket delivers what is left

**libutp's `utp_close` never blocks the application.** It queues the FIN, sets
`close_requested` and returns (`utp_internal.cpp:3358-3380`); the socket goes
on sending in `CS_FIN_SENT` until `utp_check_timeouts` retires it. Whether the
tail is delivered is the embedder's problem: it has to keep the context alive
and pumping, and destroying the context throws away whatever its sockets still
held.

`UtpStream.Close` now does the same: it queues the FIN and returns, and the
connection goes on delivering in the background. It used to wait, for the
reason libutp leaves to its embedders -- `Write` returns once the data is in
the send buffer, so `Write(payload); Close()` followed by the program exiting
would lose whatever had not gone out yet. That wait has moved to where the
data is actually lost: `UtpSocket.Close`, this library's equivalent of
destroying the context, first waits for every stream already closed to finish
(`UtpSocket.awaitLingering`). It waits while the peer is still answering and
gives up after two seconds of silence (`closeStallTimeout`, twice libutp's
1000 ms RTO floor), the bound that used to apply to the stream's Close.

So against libutp: closing a stream holds its caller no longer than
`utp_close` does, and closing the socket delivers what libutp's context
teardown would discard. Measured (`netem.TestCloseFlushesASlowTail`,
`TestCloseDoesNotWaitForASilentPeer`): the stream's Close returns at once; the
socket's Close delivered a 512 KB tail over 2 Mbps in full in 2.26 s, and
returned after 2.02 s for a peer that had gone silent. Without the socket's
wait the tail was lost and the reader timed out.

The wait was once unbounded, which was a defect rather than a deviation, and
cost 31 to 60 seconds per close on a connection whose peer had gone. See
[KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md).

## ~~The delay clamp uses one packet's RTT, not the batch minimum~~ — no longer

libutp runs `apply_ccontrol` once per incoming acknowledgement, with the bytes
of every packet it covers and the smallest round trip among them
(`utp_internal.cpp:1956-1987`, `:1403-1436`, called at `:2139`). This library
used to run its update once per acknowledged packet, clamping to that packet's
own round trip, and this entry said the two were equivalent for the gain.
They were not quite: per packet, each later packet sees the window the earlier
ones grew, so the updates compound. `TestOneWindowUpdatePerAcknowledgement`
measures the difference and fails if the update is applied per packet.

The controller now gathers an acknowledgement's packets and updates once
(`ApplyAck`), clamping to their minimum round trip as libutp does, and taking
libutp's 50 ms when the clock has not moved since a packet was sent. The
line-by-line audit (LIBUTP-AUDIT.md) found three more differences in the same
path, all closed with it: the delay the rule works from is the least of the
last three samples, not the latest (`DelayHist::get_value`, `:383-391`;
`TestConformanceDelayFilter` compares it with libutp's own log); an
acknowledgement reporting no delay does not run the rule (`:2139`); and a
delay longer than the round trip raises the base by the excess (`:2127-2133`).

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

## The congestion window starts at, and never falls below, two packets

libutp starts a connection at one packet (`max_window = get_packet_size()`,
`utp_internal.cpp:2567`), resets to one packet on a retransmission timeout
(`:1225`), and lets loss and delay take the window down to `MIN_WINDOW_SIZE`,
10 bytes (`:614`, `:1710`). A window under one packet sends nothing until the
next timeout, which libutp's own comment describes ("preventing us from
sending anything for one time-out period", `:1223-1224`). Its "packet" is the
current `get_packet_size()`, which follows the MTU search. Classic LEDBAT here
starts at two maximum-size packets (2,800 bytes), resets to that on a timeout,
and never goes below it; slow start grows by a maximum-size packet.

LEDBAT++ is not affected by this entry: its draft specifies the two-packet
start (§4.1).

**Why.** libutp's rule was implemented behind a switch and measured against
ours, classic LEDBAT, twelve seeds per profile, paired by seed:

| profile | libutp's window / ours (geometric mean) | seeds it won |
| --- | --- | --- |
| LAN, no loss | 1.013 | 9/12 |
| Broadband, no loss | 0.989 | 0/12 |
| 1% loss | 0.983 | 3/12 |
| 5% loss | **0.850**; timeouts 45 → 60 | 4/12 |
| High BDP | 0.997 | 1/12 |
| 16KB queue | 0.990 | 1/12 |

It is no better anywhere that matters and 15% worse at 5% loss, where the
window falls under a packet and waits for a timeout. Deference to a Reno flow
on a shared bottleneck was unchanged within noise: libutp's window took 59-60%
of the link in three runs, ours 60-62%. Combined with libutp's MTU start
(below), a path that fragments ordinary packets but not the don't-fragment
probe costs 3.8 seconds per 1MB transfer against 0.7: the first packet is a
full-size probe, the one-packet window lets nothing else out, and the
connection waits out the 3-second initial timeout.

## The MTU search starts at the ceiling — closed; it started at the midpoint

libutp sends its first packets at the ceiling: `mtu_last = mtu_ceiling` when a
socket is created (`utp_internal.cpp:2561-2562`), and the first of them is the
probe. This library started at the midpoint between 576 and the ceiling, 989
bytes against 1402, and moved up as probes were acknowledged, on the argument
that an untested path should get a small packet (KNOWN-LIMITATIONS.md, M6).
It now starts at the ceiling, as libutp does (`newMtuSearch`,
`TestConnectionStartsAtTheCeiling`).

**Why.** The midpoint was kept as a trade-off: libutp's start was 1-2% faster
on healthy paths (twelve seeds), and the midpoint was better only on paths
narrower than the ceiling. Measured again against the midpoint, on the
benchmark profiles, twenty runs each, classic LEDBAT, medians:

| profile | midpoint | ceiling |
| --- | --- | --- |
| Broadband, 20 ms, 10 Mbps | 6.21 | 6.25 |
| High BDP, 100 ms, 20 Mbps | 2.04 | 2.08 |
| Long transfer, 8 MB | 8.96 | 8.99 |
| Reordering, 2% | 3.50 | 3.96 |
| Broadband, 1% loss | 5.85 | 5.90 |
| Broadband, 5% loss | 2.47 | 2.49 |

LEDBAT++ gains 5.6% on high BDP and is level on the rest. (Five runs had put
the 5%-loss profile 13% lower; twenty did not.)

On the narrow paths the midpoint was better on, it no longer matters much:

- *Narrower than the ceiling, dropping what does not fit (IPv6-like), no
  report:* both stall, the shared limitation in KNOWN-LIMITATIONS.md ("A path
  MTU below the size already adopted stalls the connection"); the midpoint
  delivered 1,938 bytes of a megabyte before stalling, the ceiling none.
  IPv6 peers get 1232, which every IPv6 path carries, and a narrower local
  interface lowers the ceiling before the first packet (`PathMTUProvider`).
- *Narrower, fragmenting (IPv4-like):* the midpoint was 0.72-0.77 s against
  0.77-0.78 s for 1 MB when this was first measured.

## ~~No cap on incoming connections~~ — closed

libutp refuses a new incoming connection when its context already holds more
than 3000 sockets (`utp_internal.cpp:2967-2974`). This library had no count
bound, so a SYN flood at rate R held R×20 pending entries where libutp holds at
most 3000.

It now has libutp's: `utp.WithMaxConnections`, and `utpnet.Options.MaxConnections`,
with the same count (every connection the socket holds, dialled or accepted,
plus SYNs parked waiting for Accept), the same comparison (refused while it
holds *more than* the cap), the same position (after the duplicate lookup,
before the firewall), and the same silent refusal. The default is libutp's
3000.

The only difference is that the figure can be changed, or the cap removed with
a negative value, because 3000 is a policy choice and a client may want more.
At its default this is not a deviation.

## The zero-window probe fits a packet at any MTU

When a peer's window has been zero for 15 seconds libutp lets one packet
through by opening the window to PACKET_SIZE (`utp_internal.cpp:1142-1145`),
which is 1435 (`:57`). The flush that should send it charges a whole
`get_packet_size()` against that (is_full with no argument, `:933-936`,
`:974`): the embedder's MTU less the 20-byte header. With libutp's default MTU
callback, 1402 on IPv4 (`utp_utils.cpp:228`), a packet is 1382 bytes and the
probe goes. With an embedder reporting a real Ethernet interface, 1472, a
packet is 1452 bytes, never fits, and the probe never goes: a peer that
advertised a zero window and then fell silent leaves the connection stalled
until it dies.

Ours opens the window to `MaxPacketSize`, the ceiling the payload is always
below, so it probes whatever the MTU.

**Measured** (`TestConformanceZeroWindowProbeTiming`): at MTU 1402 both probe
after 15 seconds, libutp at 15.01s on its first timeout pass after the
deadline; at 1472 libutp sends nothing in 40 seconds and ours probes at 15s.
The test asserts libutp's failure as well as our success, so a change in the
vendored libutp shows.

## The base delay is the lowest over two minutes, not about thirteen

libutp's base delay is the minimum over thirteen one-minute buckets
(`DELAY_BASE_HISTORY`, `utp_internal.cpp:50`; rotated at `:367-380`), so it
remembers between twelve and thirteen minutes. This library's is a sliding
minimum over `ConnectionConfig.DelayWindow`, two minutes by default. This was
recorded in COMPATIBILITY.md ("Delay base history") and KNOWN-LIMITATIONS.md
but not here, which this file's own rule makes a defect in the file; the
line-by-line audit found it.

The reason is clock drift. Two clocks running at different rates make every
sample drift, and the base lags the drift by up to the window's length, so the
phantom queue a drifting pair sees is the drift rate times the window --
measured linear in the product (KNOWN-LIMITATIONS.md, "`their_hist` is a missing mechanism -- implemented"). At
1000 ppm, which an oversubscribed virtual machine reaches, two minutes is
120 ms of phantom queue, above the 100 ms target; thirteen would be about
780 ms. libutp leans on its `their_hist` correction for this; so does this
library (`OnPeerDelay`), with the shorter window as well.

The cost is the other side of the same memory. A queue that stands for longer
than the window becomes the base, after which the controller reads it as an
empty path and adds its own queue on top: LEDBAT's latecomer problem, which a
longer memory postpones. Setting `DelayWindow` to 13 minutes recovers libutp's
length, though not its bucket granularity.

Measured: a uTP flow learns the empty path alone for 20 seconds on a 10 Mbps
link with a 256 KB queue, then a Reno flow joins and keeps the queue standing
for five minutes. Over the last three minutes, three runs of each, the uTP
flow averaged 5.31 Mbps with the two-minute window (per run 5.25, 5.28, 5.42)
and 4.96 with a 13-minute one (4.24, 4.99, 5.64): the shorter memory yields
about 7% less, which is the direction the argument above predicts, by about
one standard error. Both take about half the link from Reno; deferring to a
loss-based flow on a queue deeper than the target is LEDBAT's weakness either
way. (The first attempt at this measurement ran the Reno emulator with a
payload too large for its 32-bit byte offsets and found, instead, a defect of
this library's own: KNOWN-LIMITATIONS.md, "A connection stopped after 65,535
packets".)

## ~~A connection whose peer falls silent is closed after `MaxIdleTimeout`~~ — closed

libutp has no idle timeout. A connection with nothing in flight is only ever
ended by its application, by a reset from the peer, or by the retransmission
timeout giving up -- and that last one counts only while something is
outstanding (`retransmit_count++` under `cur_window_packets > 0`,
`utp_internal.cpp:1239-1240`). The keep-alive it sends every 29 seconds is a
bare ST_STATE that nothing retransmits (`:834-844`). Measured against
libutp's driver: a connection whose peer sent one data packet and then
nothing was still open after 600 seconds, having sent 20 keep-alives.

This library closed such a connection with `ErrTimedOut` after
`ConnectionConfig.MaxIdleTimeout`, 60 seconds by default. That was a
trade-off, better on one measure and worse on another: an embedder that
never closed connections to vanished peers no longer leaked them, but an
outage longer than a minute -- a laptop asleep, a route down -- ended a
connection that libutp would have resumed when the path came back.

The default is now no idle timeout, as libutp's, and
`TestSilentPeerDoesNotCloseAnIdleConnectionByDefault` holds it there: ten
minutes of silence, 20 keep-alives, and data from the peer afterwards is
acknowledged and read. It fails on the 60-second default, with two
keep-alives and the connection closed. `MaxIdleTimeout` sets one for an
embedder that wants vanished peers reclaimed without a timer of its own; a
live peer sends a keep-alive every 29 seconds, so a timeout well above that
never closes a live connection (`netem.TestQuietConnectionDoesNotTimeOut`).
What it costs is what it cost before, and the embedder now chooses it.

Not previously listed here; found by the line-by-line audit while settling
the half-open acceptor (LIBUTP-AUDIT.md, N5 and N26).

## ~~The retransmission timeout is capped at `MaxTimeout`~~ — closed

libutp's timeout is `max(rtt + rtt_var * 4, 1000)` milliseconds with no upper
bound (`utp_internal.cpp:1380`), and it doubles on each timeout with none
either (`:1179`). This library capped both at `ConnectionConfig.MaxTimeout`, 60
seconds by default, which bound only the last of the five waits before giving
up (`:1191`), and only once the timeout itself was past 3.75 seconds: there it
gave up a little sooner than libutp. The default is now no cap, as libutp's;
`MaxTimeout` sets one for a caller that wants it.

## Timeouts act when due, not on a 500ms pass

libutp runs its timeout pass -- retransmission, keep-alive, the zero-window
probe, idle window decay -- at most every 500ms, however often its embedder
calls it (`TIMEOUT_CHECK_INTERVAL`, `utp_internal.cpp:37`, `:3284`). Each of
those fires on the first pass after its deadline: up to 500ms late. Ours are
timers aimed at the deadline, fired by a wheel with a 25ms tick, so they fire
within 25ms of it.

Within 25ms of the deadline itself, not of the last firing. The wheel used to
count ticks from the moment a timer was armed and add one, so as never to fire
early, and a retransmission re-arms right after the tick that fired it: every
backoff landed nearly a tick later than the last, 208, 633, 1458 and 3084 ms
for deadlines at 200, 600, 1400 and 3000. It now places a timer by its
deadline against its own tick schedule, to libutp's millisecond (`timeWheel.put`),
and the same schedule measures 215, 615, 1415, 3015 on the virtual clock: one
fixed offset to the tick, no growth (`TestConformanceDataRetransmitSchedule`,
which allows less than one tick late and fails the old wheel on every
retransmission after the first). Each libutp backoff, in a real embedder, starts
from the pass that fired it, so its lateness grows by up to 500ms a step.

Nothing is gained by reproducing the delay, and it is not a protocol rule: it
is how often libutp's sockets are walked. The measurements that compare the
two account for it (`libutpRTO` in `conformance_recovery_test.go` recovers
libutp's deadline from under it).

## A refused MTU probe is judged at three duplicates or more

**libutp concludes it at exactly three; we conclude it at three or more,
once per run of duplicates.**

libutp counts duplicate acknowledgements of the packet before the oldest
outstanding one, and on the third judges an outstanding MTU probe: if the
probe is the hole, the ceiling drops below it (`utp_internal.cpp:1921-1941`).
The selective ack also sets the count, to the number of later packets it
names (`duplicate_ack = count`, `:1612`). The judgement is an equality test,
`duplicate_ack == DUPLICATE_ACKS_BEFORE_RESEND` (`:1928`), so a count that
jumps from below three to above it never equals three. That is what an
acknowledgement covering a read batch does: it can name many later packets
at once. libutp's embedders batch reads that way (`utp.h:512-517`), and so
does this library on a real socket. The run then teaches the MTU search
nothing, and a probe refused for its size is resent fragmentable, arrives,
and the search concludes the size was fine.

We judge on the first duplicate, or the first selective ack, that finds the
count at three or more, once per run; a run ends when the acknowledgement
moves (`connection.noteDuplicateAck`, `judgeProbeFromDuplicates`). The
evidence is the same as libutp's -- at least three packets after the probe
arrived and the probe did not -- and it is what libutp's own selective ack
resends on (`count >= DUPLICATE_ACKS_BEFORE_RESEND`, `:1590`).

Measured over netem, 1000-byte path, 1400-byte ceiling, the probe sent with
the don't-fragment bit, the receiver reading in batches
(`TestDontFragmentBringsTheSearchWithinThePath/batched_reads`): with libutp's
equality, no probe judged in 3 runs of 3 and the search left at 1402; with
this rule, 5 probes judged and the search at 999 in 5 runs of 5. With a
receiver that acknowledges each datagram the two rules agree.
`TestSelectiveAckPastThreeJudgesTheProbe` and `TestDuplicateRunJudgesOnce`
pin the rule.

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

So the fixed ceiling remains as a cap -- 1402, libutp's default for IPv4, and
1232 for IPv6 -- and it is no longer also a *floor* on the ceiling: a tunnel
that carries 1392 is discovered before the first packet rather than after a
stall.

Against libutp's defaults this is not a deviation at all: its default
`get_udp_mtu` returns those same two figures and never looks at the interface
(`utp_utils.cpp:211-235`). It differs only from libutp configured by an
embedder whose callback reports the interface, and an embedder of this
library gets that by raising `MaxPacketSize`: the interface report then sets
the ceiling, as libutp's callback would. It used to be listed as worse than
libutp with "about 6% of the packet given up"; that was payload arithmetic.
Per byte sent, a 1472-byte datagram carries 3.26% header (20 of uTP and 28 of
IPv4 and UDP) and a 1402-byte one 3.42%.

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

## ICMP: a report below the known floor lowers the floor

libutp's search assumes `mtu_floor <= mtu_ceiling` and asserts it
(`utp_internal.cpp:1291`). An ICMP report can break that: a route that
shrinks after the search has proved a larger size reports a next-hop MTU
below the floor, and libutp sets the ceiling to it regardless (`:3086`).
In a release build the assertion is gone. The next size becomes the midpoint
of an inverted range, which is above the new ceiling, so the packets that
follow are the ones the router just refused. `ceiling - floor` wraps
unsigned, so the search never counts as finished. No probe can be sent
either: eligibility needs a size above the floor and at most the ceiling.
That lasts until the 30-minute re-search.

This library lowers the floor to the new ceiling (`mtuSearch.searchUpdate`),
so the next packets are the size the router quoted.

Better on reasoning, and not measured: none of the emulated paths changes
its MTU under a running search.

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

## A selective ack does not restart the retransmission timeout

libutp restarts `rto_timeout` in `ack_packet` (`utp_internal.cpp:1388-1389`),
which runs for every newly acknowledged packet: the cumulative acknowledgement
(`:2195`) and each packet a selective ack covers (`:1529`). Here only the
cumulative acknowledgement restarts it. A packet acknowledged selectively
cancels its own timer but leaves the connection's deadline where it was.

**Why.** Adopting libutp's rule was measured, and it was slower. Twelve seeds
of the 5%-loss profile under classic LEDBAT, with the retransmission-timeout
fix in KNOWN-LIMITATIONS.md 2f in place, each compared with the code before
that fix on the same seed. Without libutp's rule, 0.943 of the old throughput
(geometric mean); with it, 0.883, and a median of 1.59 Mb/s against 1.70.
LEDBAT++ was no better with it either. Against real libutp on the same links
and seeds (1.27 Mb/s median), both variants are faster, so this is not a case
where matching libutp would have brought us to libutp.

The likely reason, not separately measured: at 5% loss the holes that fast
retransmit cannot fill are recovered only by the timeout, and a deadline that
every selective ack pushes back fires later for them.

## A loss probe resends before the retransmission timeout

**Classic LEDBAT only.** libutp has nothing between fast retransmission and
its retransmission timeout, whose floor is a second (`rto = max(rtt +
rtt_var*4, 1000)`, `utp_internal.cpp:1380`). It fast-retransmits a packet
once (`fast_resend_seq_nr`, `:1537`, `:1603`), so a packet whose fast
retransmission is lost as well waits for the timeout, and so does the last
packet of a transfer, which has nothing after it to raise duplicate
acknowledgements.

Under classic LEDBAT, the default, this library adds a loss probe: RFC
8985's tail loss probe, with one change. When nothing has been acknowledged
for a probe timeout, max(2 x SRTT, 10 ms), it resends the *oldest*
outstanding packet, the one known to be missing, rather than the newest. One
probe per episode, as RFC 8985 allows: another only after the cumulative
acknowledgement moves, none once a retransmission timeout has fired, and none
when the timeout is already due. Sending it does not move the timeout's
deadline or touch the congestion window; what its acknowledgement shows is
below. `onLossProbe` in `conn.go`; `ConnectionMetrics.LossProbes` counts
them.

**Why.** On a 1 ms LAN path with a 64 KB queue, about 7% of 4 MB transfers
took 1.4 s instead of 0.4 s, in every pairing of this library and libutp.
Traced at the link, each was a fast retransmission lost in the same full
queue as the packet it replaced, or a lost final packet, followed by the
one-second timeout (KNOWN-LIMITATIONS.md). Measured with and without, in
alternating runs, classic LEDBAT:

| | without | with |
| --- | --- | --- |
| LAN, runs under 40 Mbps (ours to ours and ours to libutp) | 2 of 40 | 0 of 120 |
| 1% loss | 4.98 Mbps | 5.73 Mbps |
| 5% loss | 2.00 Mbps | 2.60 Mbps |
| share taken from a loss-based flow (15 runs) | 66.7% (63.9-86.3) | 67.4% (60.3-72.0) |

Five runs each unless stated. No other profile moved beyond its own spread,
the retransmission rate did not rise on any, and the two-flow splits were
unchanged. Reordering and the 16 KB queue, which looked worse over five runs,
were the same over fifteen.

**Why not LEDBAT++.** It helped LEDBAT++'s throughput too -- 0.56 to 0.70
Mbps at 5% loss, 2.60 to 3.14 Mbps (mean of 15) on the 16 KB queue -- but it
cost the thing LEDBAT++ is for. Against the loss-based flow, fifteen
alternating runs each: without the probe, fourteen took 24.8-34.4% of the link
and one 5.4%; with it, eleven took 28.0-35.9% and four took 40.6, 57.2, 59.7
and 68.1% (and three of fifteen in a second measurement). The timeout the
probe avoids is part of how LEDBAT++ yields: after one, its window collapses
to two packets. So LEDBAT++ keeps libutp's behaviour
(`TestLossProbeIsNotUsedByLedbatPP`).

**Cost.** One packet per episode into a path that may be dead: on a
blackholed connection, one extra retransmission before the first timeout
(`TestLossProbeSendsOnePacketBeforeTheTimeout`). The conformance tests that
pin libutp's timeout schedule switch it off, since that schedule is what they
measure.

**After the probe is answered.** The acknowledgement that retires the packet
the probe resent arrives a round trip after it; any packet sent before the
probe and still unacknowledged then is lost -- RFC 8985's other half, RACK.
It is charged as one detected loss (the window decays; it does not
collapse), and those packets are resent one per acknowledgement, libutp's
fast-timeout retry, up to the first packet sent after the probe. That
acknowledgement does not restart the timeout. As first written, the probe
did none of this: after a burst lost at the tail of a window, with too few
packets past it for fast retransmission, each probe repaired one packet and
its acknowledgement restarted the timeout, so the burst came back one packet
per probe timeout and the timeout that would have ended it never fired.
Measured on the virtual clock, 24 packets lost at a 50 ms round trip:
5.3 s, where libutp, through its driver in the same scenario, takes 1.75 s;
now 1.3 s (`TestTailBurstLossAgainstLibutp`). It was what made
`TestManyConcurrentTransfers` run past its budget in about one run in four
(KNOWN-LIMITATIONS.md, "Retransmission timeouts under overload"). The
emulated-network benchmarks, five runs of every profile against the commit
before: within 1.5% everywhere but four profiles, and at twenty runs those
four are classic LEDBAT at 5% loss at 2.52 Mbps median before and after, and
three LEDBAT++ profiles, where the change cannot run (no probe is sent under
LEDBAT++), at -0.4%, -0.3% and -5.2%. The last, at 5% loss, is not
distinguishable from noise (Mann-Whitney z = 1.89, ranges 0.48-0.64 and
0.49-0.64 Mbps).

## An initiator can fast-retransmit from its first packet

**libutp's initiator often cannot.** `utp_create_socket` sets `seq_nr = 1` and
`fast_resend_seq_nr = seq_nr` (`utp_internal.cpp:2611-2615`), and
`utp_connect` then replaces `seq_nr` with a random number (`:2768`) without
touching `fast_resend_seq_nr`. It advances only through a wrapping less-than
on each acknowledgement (`:2186-2188`), so when the random start lies more
than half the sequence space past 1, it never advances, and every fast
retransmission is refused (`:1537`, `:1560`) until the sequence numbers wrap.
The accepting side sets it after choosing `seq_nr` (`:2988-2989`).

This library starts it at the first sequence number in both roles, as
libutp's accepting side does.

**Why.** It is a defect, measured: over the real-socket relay, forty
libutp-sender transfers at 3-5% loss split by the SYN's sequence number, a
median of 4.31 s and no refused fast retransmissions below 32768, against
8.46 s and 61 refused ones above (KNOWN-LIMITATIONS.md). Copying it would
make half of this library's outgoing connections recover every loss by
timeout. `TestFastRetransmitFromAnyInitialSequenceNumber` fails with
libutp's initiator behaviour substituted.

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
| Delay from round trips | §4.5 | Queueing delay is the minimum of the last four round trips less the lowest within the delay window, not the one-way delay the peer reports |

Reason: classic LEDBAT, as libutp implements it, is not less than best
effort. Measured on a sustained transfer over a 40 ms path, it leaves **33 ms
of standing queue** — its own base-delay estimate has drifted up to include
that queue, so it reports causing 500µs and cannot see the rest. LEDBAT++'s
case now rests on deference, not on that queue: a lone LEDBAT++ flow aims for
§4.5's 60 ms and leaves as much queue as classic LEDBAT, or more (1.55 ms was
measured when it read one-way delay), but against a loss-based flow it takes
about a third of the link where classic LEDBAT takes two thirds. Full numbers
in [BENCHMARKS.md](BENCHMARKS.md).

### Two readings of an ambiguous specification

Both are stated here because they are interpretations, not transcriptions,
and a reader checking this implementation against the draft should know where
it exercised judgement.

**~~The §4.3 formula is applied only when the delay exceeds the target~~ —
not a reading after all.** This section used to say the draft gave only the
decrease and left the increase ambiguous, and used RFC 6817's
`GAIN * (1 - delay/target)` below the target. The draft is explicit: "In
LEDBAT++, with multiplicative decrease, the per RTT window when delay is less
than target is: W += GAIN". It now does that. Measured against the old
increase, fifteen runs each: the worst split of two flows on a 2MB queue from
45/55 to 48/52, goodput at 1% loss from 1.51 to 2.22 Mbps, and no measurable
change on the other profiles tried; on a 64KB queue, where losses decide the
split, more runs fell below 45% (six of fifteen against two). See
KNOWN-LIMITATIONS.md.

**The ramp out of a slowdown is a full slow start, not a gain-scaled one.**
§4.4 says only "ramp up the congestion window according to the slow start
algorithm", and separately that a slowdown should cost "not more than a 10%
drop in the utilization of the bottleneck". Those hold together only if the
ramp doubles per round trip. Gain-scaled, on a 20 ms path where the gain is
1/6, climbing back from two packets to fifty takes about twenty-one round
trips; measured, that turned a 1.3 s transfer into 2.8 s. The initial slow
start is gain-scaled per §4.1; the slowdown ramp is not, and it stops at a
window this connection was using a moment earlier.

**A loss during a slowdown is charged to the window it was sent from.** The
draft does not say what a loss during a slowdown does. Two rules here, both
LEDBAT++ only: a packet sent before the window was last cut belongs to the
congestion event that cut it and is not charged again (NewReno's "recover",
RFC 6582), and a packet sent before a slowdown halves the window the slowdown
saved rather than the freeze's two packets or the ramp's partial window
(RFC 5681's halving of the data in flight). Without them two LEDBAT++ flows
on a shallow bottleneck, where slowdowns and overflows coincide, split it as
unevenly as 36/64. Classic LEDBAT keeps libutp's rule, one halving per
100ms, exactly.

## The congestion controller: what still differs

This section used to say that, apart from LEDBAT++, the controller matched
libutp's `apply_ccontrol` (`utp_internal.cpp:1615-1712`) and its timeout
branch (`:1206-1228`). That stopped being true as the parity review went on.
What differs in classic LEDBAT, each with its own entry above:

- The window starts at two maximum-size packets and never falls below them,
  where libutp starts at one current-size packet and can fall to 10 bytes.
  Slow start grows by a maximum-size packet here and by libutp's current
  `get_packet_size()` there. ("The congestion window starts at, and never
  falls below, two packets.")
- A selective ack does not restart the retransmission timeout. ("A selective
  ack does not restart the retransmission timeout.")

Everything else in `apply_ccontrol` and the timeout branch matches. Seven
differences that were defects rather than choices were found and closed; they
are in [KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md), not here, and an eighth
-- a sender not yet application-limited before its window first filled, where
libutp's is (2h) -- was found when the rules were finally compared against
libutp rather than read. `TestConformanceLedbatRules` replays libutp's own
`apply_ccontrol` decisions into ours and matches every one; the one deviation
above that it has to neutralise to do so is the window floor, whose value also
sets our slow-start step.
