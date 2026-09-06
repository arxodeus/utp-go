# Conformance against libutp (M2)

Two implementations, driven with identical packet sequences, with every
emitted packet compared field by field.

This is the difference between agreeing with libutp *in the parts someone
thought to read* and agreeing with it in the parts nobody did. It found a
wire-format bug on its first run that neither the interoperability gate nor
several thousand lines of hand-comparison had caught.

## How it works

libutp is driven through `native/libutp`'s **deterministic driver**: virtual
clock, scripted random source, packets in and out by explicit call, nothing
running on its own. libutp reads time and randomness only through callbacks,
so both can be supplied, and the same script produces the same bytes every
run.

Our side is driven through a **scripted transport** — a `Conn` whose input the
test injects and whose output it captures.

Sequence numbers and connection ids are pinned on both sides, so the
comparison is exact rather than structural.

Run it with `go test -run TestConformance .` (needs cgo).

## What is compared, and what is not

Every header field, the body, and the selective-ack bitfield. Three fields are
exempt, named in `allowedToDiffer` so the corpus reports when one actually
differs rather than silently skipping it:

| Field | Why |
| --- | --- |
| `Timestamp` | libutp reads a virtual clock here and we read the real one. Cannot be equalised without an injectable clock in our connection. |
| `TimestampDiff` | Derived from the peer's timestamp, so it inherits the above. |
| `WndSize` | The advertised receive window is a local buffer-size choice, not a protocol requirement. |

## The corpus

| Case | Covers |
| --- | --- |
| `Handshake` | incoming SYN, the SYN-ACK's fields |
| `DataAndAck` | in-order data, ack generation |
| `DuplicateData` | the same packet arriving twice |
| `Reordering` | a gap in the sequence space, then the gap filled — this is what exercises selective acks |
| `IncomingFin` | FIN, and the ack sequence around it |
| `IncomingReset` | RESET terminates without a reply |
| `ZeroWindow` | a peer advertising a zero receive window |
| `InvalidAckNumIsIgnored` | a packet acking a sequence number never sent |
| `WildlyInvalidAckNumIsIgnored` | the same, thousands out of range |
| `MalformedEmptyPacket` | a zero-byte datagram |
| `MalformedTruncatedHeader` | 1..19 bytes of a 20-byte header |
| `MalformedWrongVersion` | every version nibble other than 1 |
| `MalformedUnknownPacketType` | every type nibble above `ST_SYN` |
| `MalformedExtensionPromisedButAbsent` | a header claiming an extension with no bytes after it |
| `MalformedExtensionLengthOverrunsPacket` | an extension whose length runs past the datagram |
| `MalformedExtensionChainLoops` | extensions chained so the list never terminates |
| `MalformedUnknownExtensionType` | a first extension byte naming no known extension |
| `MalformedZeroLengthSelectiveAck` | a selective ack of length zero |
| `MalformedSelectiveAckLength` | a selective ack of 1, 3, 5, 7 bytes (a divergence — see below) |
| `MalformedUnknownConnectionId` | a non-SYN packet for a connection neither side has |
| `WideReordering` | a gap, then a packet 40 past it — the selective-ack window's width |
| `DataAfterReachedFin` | data arriving past a FIN already reached in order (a divergence — see below) |

## What it found

### The selective-ack bitfield was bit-reversed

**Fixed.** The first run of the reordering case produced `ours=80000000`
against `libutp=01000000`.

Our encoder set the first entry at the *most* significant bit of each byte
(`1 << (7-j)`). BEP 29 specifies the least significant bit, and libutp builds
its mask accordingly — `m |= 1 << i` for the i'th packet past `ack_nr+2`,
low byte written first (`utp_internal.cpp:806-818`).

The decoder reversed the bits the same way. So the implementation agreed with
itself perfectly: every Go-to-Go transfer was unaffected, every unit test
passed, and the M7 interoperability gate passed too — because a clean
loopback path never loses a packet, and a selective ack is only sent when
something is missing.

Against a real peer under loss, every selective ack we sent would have been
misread, and every one we received misread in turn. A self-consistent bug is
invisible to any test that compares an implementation only against itself.

### Packets acking unsent data

Checked, and we already match: libutp drops any packet whose `ack_nr` acks a
sequence number it has not sent, calling it "a spoofed address or a malicious
attempt to attach the uTP implementation" (`utp_internal.cpp:1795-1806`). We
emit nothing for such a packet either.

Finding this was incidental — the first version of the corpus used
`ack_nr = seq_nr`, libutp silently dropped every packet, and the resulting
confusion led to the check.

### The version nibble was never checked

**Fixed** (`packet.go`). We decoded the type and extension fields out of any
packet regardless of what version it claimed, so a version-0 or version-15
header was processed as if it were version 1. libutp reads the version first
and drops anything that is not 1 (`utp_internal.cpp:2481`, `:2834`).

This is the dangerous direction — more permissive than the reference — and
it is the direction a fuzzer finds first. Nothing in the wild speaks another
version, so the leniency bought nothing.

### The first extension byte was never checked

**Fixed** (`packet.go`). Any value was accepted and, if it was not 1, treated
as "no extension". libutp requires `ext < 3` before it will even name the
packet's version (`utp_internal.cpp:2481`), so a header naming extension 7 is
dropped outright there.

The practical difference is small — both ends stop parsing — but the
malformed corpus is exactly where "small" needs to be demonstrated rather than
assumed, and matching the reference costs one comparison.

### The selective-ack bitfield had no width limit

**Fixed** (`recv_buffer.go`). The mask grew until it covered every pending
packet — up to 252 bytes. libutp scans a fixed 30-entry window and writes
exactly four bytes (`utp_internal.cpp:797`, `:805-818`). `WideReordering`
produced `ours=0300000080000000` against `libutp=03000000`.

Measured before and after on the 2%-loss emulated link: goodput 1.82 → 1.84
Mbps, packets sent 451 → 436, fast retransmits 56 in both. Full numbers in
[KNOWN-LIMITATIONS.md](KNOWN-LIMITATIONS.md).

### Our RESET for an unknown connection was missing two fields

**Fixed** (`utp_socket.go`). libutp's `send_rst`
(`utp_internal.cpp:846-863`) sets `ack_nr` to the sequence number of the
packet being rejected and `windowsize` to zero. We sent `ack_nr = 0`, naming
no packet, and advertised a 100000-byte receive window for a connection that
does not exist. Found by the unknown-connection-id case, which reached the
byte comparison only because both sides do answer such a packet.

## Deliberate divergences

Three, asserted explicitly in the corpus rather than absorbed into a tolerance:

**libutp acks twice on reaching a FIN.** Once immediately
(`utp_internal.cpp:2370`, *"if the other end wants to close, ack"*) and once
from the deferred-ack list it also schedules at `:2404`. We send one. The
second carries no information the first did not; emitting a gratuitous
duplicate would cost a packet and change nothing a peer depends on. The corpus
asserts libutp emits exactly one extra packet and that it is byte-identical
to the one before it — if it ever carried new information, the case fails.

**We reject a selective ack whose length is not a multiple of 4; libutp
accepts it.** libutp's extension loop performs no length validation on a
selective ack at all — `case 1: selack_ptr = data; break;`
(`utp_internal.cpp:1844`) — beyond the generic check that the length fits
inside the datagram. It will therefore read a 1, 3, 5 or 7-byte bitfield and
act on it. We drop the packet.

This is the stricter direction, so it is an interoperability risk rather than
an attack surface, and the risk is empty in practice: libutp always emits
exactly four bytes (`utp_internal.cpp:806-818`), so no libutp peer can produce
a packet we would reject here. A truncated bitfield has no defined meaning —
acting on one means guessing which packets the peer meant to ack.
`TestMalformedSelectiveAckLength` asserts the divergence in both directions:
that we stay silent, *and* that libutp still answers. If libutp ever tightens
this, the case fails rather than quietly agreeing.

**We have no half-close; libutp does.** On reaching the peer's FIN we tear the
connection down, where libutp keeps the socket in `CS_GOT_FIN` until the local
application closes it too. Data arriving after that point draws a RESET from
us and nothing from libutp.

This is the one M4 finding recorded rather than fixed: supporting a half-close
means a new connection state and a write path that survives the peer's FIN.
`TestConformanceDataAfterReachedFin` pins the current behaviour — it asserts
that we emit exactly one RESET and that libutp emits nothing, so it fails both
if libutp changes and if half-close lands here.

## Limits

Stated plainly, because a conformance harness that overclaims is worse than
none.

- **Only the responder role is covered.** Every case drives both
  implementations as the side accepting a connection. The initiator role —
  where we send the SYN and libutp answers — is not in the corpus.
- **Timing is not compared.** The brief asks for retransmit schedules and ack
  timing within a stated tolerance. libutp's side is fully deterministic here,
  but ours runs on the real clock with real goroutines, so the runner can only
  wait for our side to go quiet. Comparing schedules would need an injectable
  clock in our connection. Nothing in this corpus asserts *when* a packet was
  sent, only what it contained and in what order.
- **State is compared only through the wire.** Terminal outcomes are inferred
  from emitted packets, not read out of either implementation. Notably, a
  packet with an invalid `ack_nr` produces silence from both — but ours resets
  the connection internally where libutp merely ignores the packet, and this
  corpus cannot see that difference.
- **Malformed headers are barely covered.** Truncated packets, bad version
  nibbles, unknown extension types and malformed extension chains are not in
  the corpus. That is the gap most likely to hide another finding, and it is
  where M8's fuzzing should start.
- The corpus is nine cases. It proves what it covers and nothing else.
