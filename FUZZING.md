# Fuzzing and soak (M8)

Two kinds of test that nothing else in this repository does: one that does not
need someone to think of the input, and one that measures what is left behind
after a lot of work rather than whether the work succeeded.

## Running them

The seed corpora run as ordinary tests, so `go test ./...` covers them. To
actually fuzz:

```
go test -run xxx -fuzz '^FuzzDecodePacketHeader$'    -fuzztime 5m
go test -run xxx -fuzz '^FuzzDecodePacket$'          -fuzztime 5m
go test -run xxx -fuzz '^FuzzDecodeSelectiveAck$'    -fuzztime 5m
go test -run xxx -fuzz '^FuzzResponderPacketSequence$' -fuzztime 5m
go test -run xxx -fuzz '^FuzzDifferentialResponder$'   -fuzztime 10m
```

Soak:

```
go test -run TestSoak -v
UTP_SOAK_CYCLES=2000 go test -run TestSoak -v -timeout 30m
```

Failing inputs are written to `testdata/fuzz/` and then run as ordinary
regression cases forever after. Two are already there; both are described
below.

## The targets, and what each asserts

### `FuzzDecodePacketHeader`, `FuzzDecodePacket`, `FuzzDecodeSelectiveAck`

The wire decoder. M2's malformed corpus compared this implementation against
libutp on hand-written hostile input and found two real divergences that way;
these pick up where that stops, because a corpus can only contain the
malformed packets someone thought of.

The properties are the ones a decoder owes its caller whatever it is given:

- **It never panics.** A panic here is remotely triggerable by anyone who can
  send a UDP datagram to the port.
- **It never reports success and a nil result**, or a body longer than the
  input, or an `EncodedLen` that disagrees with `Encode`.
- **Everything it encodes, it can decode.** This is the invariant that broke.
- **Byte-exact round trip, where that is owed** — for a packet carrying
  nothing this implementation drops. It is not owed for a packet carrying an
  extension chain we discard, and the test says which is which rather than
  weakening the property for everything.

### `FuzzResponderPacketSequence`

A live connection rather than a single packet. The defects that survive a
decoder fuzzer are the ones that need a *sequence*: a state machine accepting
a packet it should not have in the state it is in, a counter that wraps, a
buffer indexed from a sequence number the peer chose.

The property is deliberately weak, because a strong one would be wrong — an
arbitrary byte sequence is not a valid uTP conversation, so almost any
behaviour is permitted. What is not permitted is panicking, hanging, or dying:
after whatever the fuzzer sent, a fresh well-formed connection must still be
answered. That last one is the one worth having. It says a peer cannot use
malformed traffic to wedge a socket against everyone else on the port.

It runs at roughly 25–100 executions per second, against ~50,000/sec for the
decoder targets, because each execution builds and tears down a socket. Run it
for longer rather than expecting the same coverage.

### `FuzzDifferentialResponder`

The fuzzer generating input and **libutp deciding whether the answer was
right**.

The two halves existed separately and neither could do this alone. M2's
harness drives both implementations through the same packets and compares
what each emits, field by field — but only on cases someone wrote down. The
fuzzer above generates sequences nobody wrote down — but could only tell that
this implementation had crashed, hung, or died, because it had nothing to
compare against. A *behavioural* divergence on input nobody thought of is
invisible to either.

The property is not "the two agree". They deliberately do not, in ways
CONFORMANCE.md and DEVIATIONS.md set out, and a fuzzer that failed on those
would be useless. It is the direction rule those documents already use:

> Being stricter than the reference is an interoperability risk.
> Being more permissive is an attack surface.

So: **we must never answer more than libutp does** — asserted strictly, because
emitting a packet where the reference stays silent is the amplification
surface and is what a malformed-input attack looks for. **Where both answer,
the significant fields must agree**, with timestamps and the advertised window
exempt for the reasons in `allowedToDiffer`. **Answering less is permitted**,
and is where the documented divergences live; being quieter cannot be
exploited, only cost interoperability, which the M2 corpus pins case by case.

Two details make the comparison mean what it says:

- **Packets are matched per connection id, not by position.** Order between
  packets for different connections is not observable by any peer, and ours
  falls out differently because a RESET for a connection we do not have is
  answered on the socket's goroutine while a connection's own STATE goes
  through that connection's. Comparing by position reported that as a
  five-field divergence when the two had emitted the same two packets. Order
  *within* one connection is observable, and is compared.
- **Both sides are primed with one in-order data packet** after the handshake.
  libutp completes an incoming connection only on an `ST_DATA`
  (`utp_internal.cpp:2158-2161`) and sits in `CS_SYN_RECV` dropping everything
  until one arrives; we treat it as established once the handshake completes.
  Without priming, every generated sequence starting with anything but data
  reported that one known divergence instead of finding a new one. The
  divergence itself is pinned by `TestConformanceFinBeforeAnyData`, so
  removing it here does not hide it.

It runs at roughly 10–20 executions per second, because each one builds two
implementations and waits for ours to go quiet. That is the price of an
oracle.

### The soak tests

- `TestSoakConnectionChurn` — connections opened, used and closed in a loop.
  This is the shape that matters most for the target use: a BitTorrent client
  opens and drops peer connections continuously for days. 400 cycles:
  goroutines 16 → 16, tracked connections 0 → 0, live heap flat.
- `TestSoakAbandonedConnections` — connection attempts to a port with nothing
  on it, abandoned by context cancellation. A client dialling real peers gets
  these constantly.
- `TestSoakUnknownConnectionFlood` — 20,000 packets for connections the socket
  does not have. Checks that the RESET rate limiting holds: 1,001 answers to
  20,000 packets, and flat memory.

## What they found

### `Encode` could produce a packet this decoder rejects

**Fixed.** `packet.Encode` took the header's extension byte from `p.Header`,
which is only right for packets built by `PacketBuilder`. A *decoded* packet
keeps whatever extension byte the peer sent, while `Eack` is nil unless a
selective ack was retained — so a packet that arrived carrying a zero-length
selective ack, or the extension-bits extension, or a chain continuing past the
selective ack, re-encoded to a header claiming an extension with no extension
bytes after it. This library's own decoder rejects that.

`Encode` now derives the byte from what it is writing, and `DecodePacket`
normalises the header to describe what was actually retained, so the struct no
longer claims to hold something it discarded.

Nothing on the live path re-encodes a decoded packet, so this was latent. But
`DecodePacket` is exported and its result has exported methods, so an external
caller can reach it, and a proxy or relay built on this library would have
emitted unparseable packets.

Both minimised inputs are in `testdata/fuzz/FuzzDecodePacket/`.

### No bound on how far ahead a peer could push us

**Fixed.** libutp drops any packet whose sequence number is more than 1024
past the next expected one, without buffering it, acking it, or letting it
touch congestion control (`REORDER_BUFFER_MAX_SIZE`, `utp_internal.cpp:54`,
applied at `:1890`). A packet that is *old* — within 1024 behind — is also
dropped, but re-acked first, because the peer evidently missed the ack already
sent.

This fork had no such bound. A peer could name any sequence number in the
16-bit space and we would buffer the packet as out-of-order data and answer it
with a STATE. Three costs: unbounded pending state driven by a remote party,
one emitted packet per junk packet where the reference emits none, and — since
the sequence number is far outside the 30-entry selective-ack window — a
selective ack naming nothing at all.

A single `ST_FIN` with a sequence number 11,435 past the expected one drew a
STATE from us and silence from libutp.

### Packets acking data we never sent were processed

**Fixed, and it corrects an earlier claim.** libutp validates the
acknowledgement number before anything else, and is unusually explicit about
why (`utp_internal.cpp:1794-1807`):

> ignore packets whose ack_nr is invalid. This would imply a spoofed address or
> a malicious attempt to attach the uTP implementation. acking a packet that
> hasn't been sent yet!

The acceptable range is a short window ending at the last sequence number
actually sent. We had no such rule. CONFORMANCE.md previously recorded that we
"already match" here, on the strength of two corpus cases that happened to
produce the same silence for other reasons — the behaviour was incidental, not
implemented, and the fuzzer showed the difference with a single `ST_DATA`
whose `ack_nr` named a packet we had never sent.

Order matters between these two: a packet rejected for its ack number must not
first draw a re-ack from the reorder-window check.

### Our re-ack consumed a sequence number

**Fixed**, and it was introduced by the reorder-window fix above — caught two
fuzz runs later. The re-ack went through `transmit()`, which registers the
packet with `SentPackets` and arms a retransmission timer. A STATE is neither
retransmitted nor numbered: libutp's `send_ack` writes the current `seq_nr`
without consuming it (`utp_internal.cpp:781`, with `:1088-1089` showing where
one *is* consumed). Every re-ack advanced our sequence number, so the next
STATE carried a number libutp's never would.

### Failed connection attempts leaked socket state

**Fixed.** The socket was told to forget a connection from exactly one place:
the branch of the connection's event loop where the state machine reached
`ConnClosed`. A connection torn down by a cancelled context — which is what an
abandoned or failed connection attempt *is* — exited by a different path and
left its entry in the socket's connection table forever.

Measured before the fix: 40 attempts to a dead port left 40 tracked
connections behind, permanently. A client dialling unreachable peers, which is
most of them, accumulates one per attempt.

The goroutines were fine throughout, which is why nothing that counted
goroutines had noticed. It is the map entry and its channel that leaked. The
event loop now reports shutdown on every exit path, with a bounded wait so a
connection outliving its socket cannot block on the report.

## What this does not cover

- **No fuzzing of the initiator role.** `FuzzResponderPacketSequence` drives
  the accepting side only, as the M2 corpus does. A malicious *server* is not
  modelled.
- **Differential fuzzing covers the responder role only**, as the M2 corpus
  does. A malicious *server* — one that answers our SYN — is not modelled by
  either.
- **The differential comparison is coarse in time.** It compares what each
  side emitted after the whole sequence, not after each packet, because
  settling ours between every packet costs more than the coverage is worth.
  A divergence in *when* a packet is sent, rather than whether or what, would
  not be caught.
- **No long-running soak.** The longest run here is a few hundred connection
  cycles over seconds. Nothing has been run for hours, which is where a slow
  leak — a few bytes per connection — would show and these would not.
- **No memory limit under adversarial input.** The flood test checks that
  RESET answers are rate limited, but nothing bounds what a peer can make the
  socket allocate by, say, opening many half-connections.
