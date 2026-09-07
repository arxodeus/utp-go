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
- **No differential fuzzing against libutp.** The M2 harness can compare the
  two implementations on a given input, and the fuzzer can generate inputs,
  but they are not wired together. That is the obvious next step and it is not
  done: it would turn every divergence in behaviour into a fuzz failure rather
  than only crashes and hangs.
- **No long-running soak.** The longest run here is a few hundred connection
  cycles over seconds. Nothing has been run for hours, which is where a slow
  leak — a few bytes per connection — would show and these would not.
- **No memory limit under adversarial input.** The flood test checks that
  RESET answers are rate limited, but nothing bounds what a peer can make the
  socket allocate by, say, opening many half-connections.
