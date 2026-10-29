# fuzz_test.go

Every comment in `fuzz_test.go` at e56c9aa, verbatim.

## Line 8

```
// Fuzzing the wire decoder.
//
// M2's malformed corpus compared this implementation against libutp on
// hand-written hostile input, and found two real divergences that way. This
// is where that leaves off: the corpus can only test the malformed packets
// someone thought of, and a fuzzer does not need to think of them.
//
// The properties asserted are the ones a decoder owes its caller regardless
// of input:
//
//   - It never panics. A panic here is remotely triggerable by anyone who can
//     send a UDP datagram to the port.
//   - It never reports success while having read past the buffer it was
//     given, or claims a length the input cannot support.
//   - Anything it accepts, it re-encodes to the same bytes. A decoder that
//     accepts a packet it cannot reproduce has silently reinterpreted it,
//     which is how two implementations end up disagreeing about what a peer
//     said.
//
// Run longer with:
//
//	go test -run xxx -fuzz FuzzDecodePacket -fuzztime 5m
```

## Line 32 — func FuzzDecodePacketHeader

```
// Seeds: a valid header, and the shapes the M2 corpus found interesting.
```

## Line 51 — func FuzzDecodePacketHeader

```
// Accepting means the input was at least a full header.
```

## Line 56 — func FuzzDecodePacketHeader

```
// Only version 1 exists, and only known extensions may lead.
```

## Line 67 — func FuzzDecodePacketHeader

```
// Whatever it accepted, it must be able to write back unchanged.
```

## Line 82 — func FuzzDecodePacket

```
// Extensions the decoder does not interpret, which it must still keep:
	// extension 2 (eight bytes) leading a chain to an unknown type 99, and a
	// selective ack followed by an unknown type.
```

## Line 110 — func FuzzDecodePacket

```
// The decoder must not manufacture payload the input did not contain.
```

## Line 115 — func FuzzDecodePacket

```
// The claimed length has to match what it produced, or a caller
		// sizing a buffer from EncodedLen writes out of bounds.
```

## Line 123 — func FuzzDecodePacket

```
// Everything this encoder produces, this decoder must accept.
		//
		// This is the invariant, and it is the one that broke: Encode took
		// the extension byte from the header rather than from what it was
		// writing, so a decoded packet carrying an extension we do not retain
		// re-encoded to a header claiming an extension with no extension
		// bytes after it -- which this decoder rejects.
```

## Line 136 — func FuzzDecodePacket

```
// And encoding is a fixed point from there on.
```

## Line 141 — func FuzzDecodePacket

```
// Byte-exact round trip, for every packet the decoder accepts.
		//
		// This used to be owed only for packets carrying nothing the decoder
		// dropped -- no extensions, or exactly one selective ack -- because
		// it kept the selective ack and discarded everything else in the
		// chain: extension 2, unknown types further along, a zero-length
		// selective ack. It now keeps the whole chain in order
		// (packet.wireExtensions), so nothing is dropped and nothing is owed
		// less than the original bytes.
		//
		// The selective-ack bitfield's own round trip, which is where a
		// bit-order bug would hide, is also covered directly by
		// FuzzDecodeSelectiveAck.
```

## Line 179 — func FuzzDecodeSelectiveAck

```
// A selective ack is a whole number of 4-byte words, and reports one
		// entry per bit.
```
