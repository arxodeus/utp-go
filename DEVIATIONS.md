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

## Inherited notes that claim consistency with the reference

Two comments in `conn.go` describe behaviour as matching the reference
implementation. They are recorded here because they are load-bearing claims
that **have not been re-verified** against `utp_internal.cpp` in this fork:

- The initiator initialises its ACK number to the sequence number of the
  SYN-ACK minus one, described in the source as "a deviation from the
  specification ... consistent with the reference implementation and the
  libtorrent implementation" (`connection.onState`).
- STATE packets always carry the next sequence number, described as
  "[c]onsistent with the reference implementation and the libtorrent
  implementation" (`connection.statePacket`).

Both are inherited from the upstream port of `ethereum/utp`. Verifying them is
M4b work.

## Not a deviation: LEDBAT++

The brief anticipates LEDBAT++ being nearly the only entry in this file.
LEDBAT++ has **not been implemented**. The existing controller is classic
LEDBAT.

Note that until commit `4a0f0ec` that controller was being fed a constant
delay value and so was not performing delay-based control at all — see
[KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md). Any comparison between classic
LEDBAT and LEDBAT++ has to start from a controller that actually works, and
the classic baseline has never been measured.
