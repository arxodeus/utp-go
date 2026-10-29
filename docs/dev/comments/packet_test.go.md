# packet_test.go

Every comment in `packet_test.go` at e56c9aa, verbatim.

## Line 16 — func (*packetHeaderV1).Generate

```
// The extension byte must name a known extension: the decoder rejects
	// anything above maxKnownExtension, matching libutp
	// (utp_internal.cpp:2481). This generator previously produced arbitrary
	// bytes and asserted they survived a round trip, which only held while
	// the decoder validated nothing.
```

## Line 42 — func (*selectiveAck).Generate

```
//if len(acked) == 0 {
	//	var empty [32]bool
	//	acked = empty[:]
	//}
```

## Line 108 — func TestPacket

```
// Helper function to check property
```

## Line 110 — func TestPacket

```
// Check empty payload
```

## Line 115 — func TestPacket

```
// Handle selective ack
```

## Line 123 — func TestPacket

```
// Create packet
```

## Line 130 — func TestPacket

```
// Get encoded length
```

## Line 133 — func TestPacket

```
// Encode packet
```

## Line 136 — func TestPacket

```
// Check length
```

## Line 142 — func TestPacket

```
// Decode packet
```

## Line 150 — func TestPacket

```
// Compare packets. A decoded packet also carries the raw extension
		// chain it arrived with, which a built one does not, so compare what
		// both hold, and that the decoded one writes back the same bytes.
```

## Line 164 — func TestDecodedPacketKeepsExtensionsThroughEdits

```
// A decoded packet keeps its whole extension chain, and a caller that edits
// its selective ack gets that edit written in the ack's place. The fuzzer
// checks the unedited round trip; these are the edits it cannot make.
```

## Line 170 — func TestDecodedPacketKeepsExtensionsThroughEdits

```
// Chain an unknown extension, type 99, after the selective ack.
```
