# conformance_malformed_test.go

Every comment in `conformance_malformed_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build cgo
```

## Line 11

```
// Malformed and hostile input, compared against libutp.
//
// The rule from the brief: every validation that causes libutp to drop or
// reset matters, because if this implementation accepts a packet libutp
// rejects, that is both an incompatibility and an attack surface.
//
// Each case injects raw bytes into an established connection on both sides
// and requires them to agree on whether anything comes back.
```

## Line 20 — func wireHeader

```
// wireHeader builds a well-formed 20-byte header, which each case then
// corrupts in one specific way.
```

## Line 27 — func wireHeader

```
// timestamp
```

## Line 28 — func wireHeader

```
// timestamp diff
```

## Line 29 — func wireHeader

```
// window
```

## Line 35 — func dataHeader

```
// dataHeader is a valid ST_DATA header for the established corpus connection.
```

## Line 37 — func dataHeader

```
/*ST_DATA*/
```

## Line 64 — func TestMalformedWrongVersion

```
// uTP version 1 is the only version either implementation speaks.
```

## Line 75 — func TestMalformedUnknownPacketType

```
// Types beyond ST_SYN (4) are undefined.
```

## Line 86 — func TestMalformedExtensionPromisedButAbsent

```
// The extension byte says an extension follows, but the packet ends.
```

## Line 89 — func TestMalformedExtensionPromisedButAbsent

```
// selective ack follows
```

## Line 93 — func TestMalformedExtensionLengthOverrunsPacket

```
// An extension header whose length runs off the end of the packet.
```

## Line 95 — func TestMalformedExtensionLengthOverrunsPacket

```
/*next*/
```

## Line 95 — func TestMalformedExtensionLengthOverrunsPacket

```
/*len*/
```

## Line 100 — func TestMalformedSelectiveAckLength

```
// A selective ack whose length is not a whole number of 4-byte words.
//
// This is a deliberate divergence, and the only one in this file. libutp
// performs no length validation on a selective-ack extension at all -- its
// parser records the pointer and moves on (utp_internal.cpp:1844, `case 1`),
// with only the generic "does the length fit in the packet" check applied. It
// therefore accepts a 1, 3, 5 or 7-byte bitfield and interprets it.
//
// We reject it. A bitfield that is not a whole number of words is truncated,
// and interpreting it means guessing which acks the peer meant. libutp never
// emits such an extension -- it always writes exactly 4 bytes
// (utp_internal.cpp:815-818) -- so nothing in the wild depends on the
// leniency, and being stricter here cannot make us unroutable.
//
// The direction matters: being stricter than the reference is an
// interoperability risk, being more permissive is an attack surface. This
// test pins the direction, so a future change that makes us accept these
// would fail.
```

## Line 138 — func TestMalformedZeroLengthSelectiveAck

```
// A zero-length selective ack, where both agree.
```

## Line 148 — func TestMalformedExtensionChainLoops

```
// A chain of extensions that never terminates within the packet.
```

## Line 150 — func TestMalformedExtensionChainLoops

```
// Each extension's "next" points at another extension, forever.
```

## Line 154 — func TestMalformedExtensionChainLoops

```
/*next: another extension*/
```

## Line 159 — func unknownExtensionPacket

```
// unknownExtensionPacket is a valid ST_DATA whose *first* extension is of an
// unknown type, well formed and with a payload behind it.
```

## Line 163 — func unknownExtensionPacket

```
// unknown extension type
```

## Line 164 — func unknownExtensionPacket

```
/*next: none*/
```

## Line 168 — func TestMalformedUnknownExtensionType

```
// An unknown first extension type. BEP 29 says unknown extensions are skipped
// using their length, so a well-formed one carrying a payload ought to be
// accepted and acked. Neither implementation does that: both drop the packet.
//
// libutp does not drop it in its extension loop, which has no default case and
// would skip the type quite happily (utp_internal.cpp:1844-1866). It drops it
// two steps earlier, in UTP_Version (utp_internal.cpp:2480):
//
//	return (pf->type() < ST_NUM_STATES && pf->ext < 3 ? pf->version() : 0);
//
// A first extension byte of 3 or more makes the packet report version 0, and a
// version that is not 1 is not uTP. The packet never reaches a socket.
//
// So the two implementations agree, and agree in departing from the
// specification. Recorded in DEVIATIONS.md.
//
// The second step is the assertion and the third is what makes it mean
// anything: the identical packet without the extension is acked, so the
// silence above is the extension being rejected and not some unrelated reason
// the connection had stopped answering. This case asserted nothing at all
// until that control was added -- it expected an emission, got none from
// either side, and passed on the match.
```

## Line 207 — func TestMalformedUnknownExtensionTypeInChain

```
// The same unknown type, but second in the chain rather than first.
//
// UTP_Version inspects only `pf->ext`, the first extension byte. An unknown
// type reached through a chain never passes through that gate, and libutp's
// extension loop skips it by its length as BEP 29 says it should. So the
// packet libutp rejects and the packet it accepts differ only in the order of
// two extensions.
//
// This is the case that separates "rejects unknown extensions" from "rejects
// an unknown extension byte in the header", and the two implementations have
// to agree on which of those they do.
```

## Line 220 — func TestMalformedUnknownExtensionTypeInChain

```
// first extension: selective ack, which is known
```

## Line 221 — func TestMalformedUnknownExtensionTypeInChain

```
/*next: the unknown type*/
```

## Line 222 — func TestMalformedUnknownExtensionTypeInChain

```
/*next: none*/
```

## Line 230 — func TestMalformedUnknownConnectionId

```
// A packet for a connection id neither side knows. Both answer with a RESET,
// so this compares rather than expecting silence.
```

## Line 239 — func runDivergenceCase

```
// runDivergenceCase drives both implementations to an established connection,
// injects one raw packet, and returns what each emitted, without asserting
// that they agree. It is for cases where they deliberately do not.
```

## Line 247 — func runDivergenceSteps

```
// runDivergenceSteps drives both implementations through a handshake and then
// the given raw packets, and returns what each emitted in response to the
// last one, without asserting that they agree. It is for cases where they
// deliberately do not.
```

## Line 256 — func runDivergenceStepsAnswered

```
// runDivergenceStepsAnswered is runDivergenceSteps for a caller that expects
// an answer to the last packet: it waits for one rather than for quiet.
```

## Line 293 — func runDivergence

```
// settle calls a connection done after 60 ms with nothing new, which on a
	// loaded machine can come before its answer to the last packet: under the
	// full suite TestReorderedDataDoesNotBlockOurWindow read "ours emitted
	// nothing" from a connection that answers every packet when run alone. A
	// caller that expects an answer waits for one.
```
