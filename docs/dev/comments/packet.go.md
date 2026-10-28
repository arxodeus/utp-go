# packet.go

Every comment in `packet.go` at e56c9aa, verbatim.

## Line 13 — const minimalHeaderSize, minimalHeaderSizeWithSelectiveAck, protocolVersionOn...

```
// maxKnownExtension is the highest extension type either implementation
	// recognises: 0 none, 1 selective ack, 2 extension bits.
```

## Line 89 — func (*packetHeaderV1).appendTo

```
// appendTo appends the header's 20 bytes to b.
```

## Line 91 — func (*packetHeaderV1).appendTo

```
// 2
```

## Line 92 — func (*packetHeaderV1).appendTo

```
// 2
```

## Line 93 — func (*packetHeaderV1).appendTo

```
// 4
```

## Line 94 — func (*packetHeaderV1).appendTo

```
// 4
```

## Line 95 — func (*packetHeaderV1).appendTo

```
// 4
```

## Line 96 — func (*packetHeaderV1).appendTo

```
// 2
```

## Line 97 — func (*packetHeaderV1).appendTo

```
// 2
```

## Line 101 — func IsPacket

```
// IsPacket reports whether b is a uTP packet for this implementation, as far
// as its header can say: the length, the packet type, the version nibble and
// the first extension byte, the same four things libutp checks before it will
// look at a packet at all (utp_internal.cpp:2481, :2834). It is the decoder's
// own validation, so a datagram this accepts is one the socket can parse, and
// one it refuses is one the socket would drop. utpnet uses it to tell uTP from
// another protocol sharing the port.
```

## Line 124 — func decodePacketHeaderInto

```
// decodePacketHeaderInto is decodePacketHeader into a header the caller
// provides.
```

## Line 136 — func decodePacketHeaderInto

```
// Version 1 is the only version either this implementation or libutp
	// speaks. libutp drops anything else outright (utp_internal.cpp:2834),
	// and accepting what the reference rejects is both an incompatibility and
	// an attack surface: it lets a peer reach the rest of this decoder with a
	// packet no real implementation would have sent.
```

## Line 147 — func decodePacketHeaderInto

```
// The first extension byte must name a known extension: 0 (none),
	// 1 (selective ack) or 2 (extension bits).
	//
	// libutp folds this into its version check. UTP_Version
	// (utp_internal.cpp:2481) returns the version only if
	// `pf->type() < ST_NUM_STATES && pf->ext < 3`, and returns 0 otherwise,
	// which the caller then rejects as an unsupported version
	// (utp_internal.cpp:2834). So a packet whose *first* extension is type 3
	// or above is dropped outright.
	//
	// Only the first one: an unknown extension further along a chain is
	// skipped by its length, which is what BEP 29 describes. Verified
	// empirically against libutp -- a chain of selective-ack followed by
	// extension 99 is accepted, while extension 99 alone is not.
	//
	// Accepting what the reference rejects is both an incompatibility and an
	// attack surface, so this rejects too.
```

## Line 190 — type packetWithHeader

```
// packetWithHeader is a packet and its header in one allocation, for the
// decoder and the builder, which make one of each for every packet.
```

## Line 197 — type selectiveAck

```
// selectiveAck represents a selective acknowledgment
```

## Line 202 — func newSelectiveAck

```
// newSelectiveAck creates a new SelectiveAck from a slice of booleans
```

## Line 235 — func (*selectiveAck).Encode

```
// Encode renders the selective-ack bitmask.
//
// Bit order is least-significant-first within each byte: the LSB of the first
// byte represents ack_nr+2, the next bit ack_nr+3, and so on. That is what
// BEP 29 specifies and what libutp emits -- it builds the mask with
// `m |= 1 << i` for the i'th packet past ack_nr+2 and writes the low byte
// first (utp_internal.cpp:806-818). A reversed order would agree with a
// decoder reversed the same way, so only a comparison against libutp checks
// it. History: HISTORY.md, "selectiveAck.Encode".
```

## Line 260 — func decodeSelectiveAck

```
// decodeSelectiveAck decodes a byte slice into a SelectiveAck
```

## Line 275 — func decodeSelectiveAck

```
// Least-significant bit first, matching Encode and BEP 29.
```

## Line 289 — type packet

```
// chain is the extension chain as it arrived, in order, for a decoded
	// packet, and nil for one built here. See wireExtensions.
```

## Line 292 — type packet

```
// borrowed says Body is a slice of the socket reader's buffer, which the
	// next read reuses. Whatever keeps the packet beyond the call that
	// received it takes the body first. See own.
```

## Line 298 — func (*packet).own

```
// own gives the packet a body of its own, if it was borrowed.
//
// The socket decodes each datagram from its read buffer and processes most
// of them before it reads again -- an established connection's, inline -- so
// most need no copy: in-order data goes straight from the read buffer into
// the receive buffer, as libutp hands its application a pointer into the
// packet it received (utp_call_on_read, utp_internal.cpp:2351). Only a packet
// kept for later -- queued for a connection's loop, or a SYN waiting for an
// Accept -- takes its body, here. The receive buffer copies what it holds out
// of order (receiveBuffer.Write).
```

## Line 326 — const extensionBits

```
// extensionBits is the "extension bits" extension's type (BEP 29).
```

## Line 329 — func (*packet).malformedExtensionBits

```
// malformedExtensionBits reports an extension-bits extension of any length
// but 8, which libutp refuses (utp_internal.cpp:1850-1856).
```

## Line 340 — func (*packet).wireExtensions

```
// wireExtensions is the extension chain Encode writes, in order.
//
// A packet built here carries at most a selective ack. A decoded packet
// carries every extension it arrived with, in the order it arrived, so that
// decoding and encoding again gives back the same bytes: unknown types
// further along the chain, which both this decoder and libutp step over
// (utp_internal.cpp:1844-1866), and extension 2, whose contents nothing here
// reads.
//
// The selective ack keeps its place in the chain, and its bytes come from
// Eack, so a caller that changes Eack changes what is written. Its place is
// the last type-1 extension, because that is the one the decoder reads, as
// libutp's loop does. If Eack is nil that entry is dropped -- unless it was
// empty, in which case it never became an Eack and is kept as it came.
//
// An unknown type is never written first. A packet whose first extension is
// unknown is rejected by this decoder and by libutp's version check
// (utp_internal.cpp:2480), so leading with one would make the packet
// unreadable. It can only happen if a caller cleared the selective ack that
// came first, and then the unknown extensions ahead of the first known one
// are dropped. History: HISTORY.md, "packet.wireExtensions".
```

## Line 395 — func (*packet).Encode

```
// Encode writes the packet to the wire.
//
// The header's extension byte is derived from what is actually being written
// rather than taken from p.Header, so that everything this produces can be
// decoded again: a decoded packet keeps whatever extension byte the peer
// sent, while Eack is nil unless a selective ack was retained.
// FuzzDecodePacket holds it to that; history: HISTORY.md,
// "packet.Encode".
```

## Line 407 — func (*packet).appendEncoded

```
// appendEncoded appends the packet's wire form to bytes, so that the send
// path can encode into a buffer it reuses.
```

## Line 419 — func (*packet).appendEncoded

```
// Each extension is introduced by the one before it: the header names
	// the first, and each carries the type of the next.
```

## Line 430 — func (*packet).appendEncoded

```
// Append payload
```

## Line 436 — func decodePacket

```
// decodePacket decodes one datagram. If borrowed is set, b is the reader's
// buffer, reused once the datagram has been dispatched: the packet's body
// still refers to it, and is taken by own when the packet is kept. Nothing
// else in the packet refers to b: the header is decoded into fields and each
// extension copied.
```

## Line 479 — func decodePacket

```
// A zero-length ST_DATA is accepted, not rejected.
	//
	// libutp guards only the delivery to the application with `count > 0`
	// and advances ack_nr unconditionally (utp_internal.cpp:2342-2355), so it
	// acks such a packet like any other. Rejecting it here dropped the packet
	// before the connection ever saw it, which meant it was never acked and
	// the peer retransmitted it forever -- a silent stall against any peer
	// that sends one.
	// Every extension is kept, in order (see wireExtensions), so the header's
	// extension byte stays as it arrived, and it agrees with what Encode will
	// write.
```

## Line 509 — func decodeRawExtensions

```
// Check if enough data for header (2 bytes)
```

## Line 514 — func decodeRawExtensions

```
// Read next extension type
```

## Line 517 — func decodeRawExtensions

```
// Read extension length
```

## Line 520 — func decodeRawExtensions

```
// Calculate start of extension data
```

## Line 523 — func decodeRawExtensions

```
// Check if enough data for extension content
```

## Line 528 — func decodeRawExtensions

```
// Copy extension data
```

## Line 532 — func decodeRawExtensions

```
// Add to extensions list
```

## Line 538 — func decodeRawExtensions

```
// Move to next extension
```
