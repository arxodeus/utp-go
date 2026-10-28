package utp

import (
	"bytes"
	"testing"
)

// Fuzzing the wire decoder.
//
// The properties a decoder owes its caller whatever the input:
//
//   - It never panics; anyone who can send a UDP datagram to the port could
//     trigger one.
//   - It never reports success having read past its buffer, or claims a
//     length the input cannot support.
//   - Anything it accepts re-encodes to the same bytes. A decoder that
//     accepts a packet it cannot reproduce has silently reinterpreted it.
//
// Run longer with:
//
//	go test -run xxx -fuzz FuzzDecodePacket -fuzztime 5m

func FuzzDecodePacketHeader(f *testing.F) {
	// Seeds: a valid header, and the shapes of the malformed-packet
	// conformance cases.
	f.Add(newPacketBuilder(stData, 1234, 200000, 1048576, 42).Build().Encode())
	f.Add(newPacketBuilder(stSyn, 1, 0, 0, 0).Build().Encode())
	f.Add(make([]byte, 20))
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0xFF}, 20))

	f.Fuzz(func(t *testing.T, data []byte) {
		header, err := decodePacketHeader(data)
		if err != nil {
			if header != nil {
				t.Fatalf("decoder returned both a header and an error %v", err)
			}
			return
		}
		if header == nil {
			t.Fatal("decoder reported success with a nil header")
		}

		// Accepting means the input was at least a full header.
		if len(data) < minimalHeaderSize {
			t.Fatalf("accepted %d bytes as a header; the minimum is %d", len(data), minimalHeaderSize)
		}

		// Only version 1 exists, and only known extensions may lead.
		if header.Version != protocolVersionOne {
			t.Errorf("accepted version %d", header.Version)
		}
		if header.Extension > maxKnownExtension {
			t.Errorf("accepted leading extension %d", header.Extension)
		}
		if err := header.PacketType.Check(); err != nil {
			t.Errorf("accepted packet type %d: %v", header.PacketType, err)
		}

		// Whatever it accepted, it must be able to write back unchanged.
		reencoded := header.EncodeToBytes()
		if !bytes.Equal(reencoded, data[:minimalHeaderSize]) {
			t.Errorf("header does not round trip:\n in %x\nout %x", data[:minimalHeaderSize], reencoded)
		}
	})
}

func FuzzDecodePacket(f *testing.F) {
	f.Add(newPacketBuilder(stData, 1234, 200000, 1048576, 42).WithPayload([]byte("hello")).Build().Encode())
	f.Add(newPacketBuilder(stState, 7, 1, 2, 3).
		WithSelectiveAck(newSelectiveAck([]bool{true, false, true, false})).Build().Encode())
	f.Add(newPacketBuilder(stFin, 9, 0, 0, 1).Build().Encode())
	f.Add(make([]byte, 22))
	f.Add([]byte{1, 1, 0, 0})
	// Extensions the decoder does not interpret, which it must still keep:
	// extension 2 (eight bytes) leading a chain to an unknown type 99, and a
	// selective ack followed by an unknown type.
	withChain := func(first byte, chain ...byte) []byte {
		p := newPacketBuilder(stData, 5, 1, 2, 3).WithPayload([]byte("body")).Build().Encode()
		p[1] = first
		out := append([]byte{}, p[:minimalHeaderSize]...)
		out = append(out, chain...)
		return append(out, p[minimalHeaderSize:]...)
	}
	f.Add(withChain(2, 99, 8, 1, 2, 3, 4, 5, 6, 7, 8, 0, 3, 0xaa, 0xbb, 0xcc))
	f.Add(withChain(1, 99, 4, 0x05, 0, 0, 0, 0, 2, 0xde, 0xad))

	f.Fuzz(func(t *testing.T, data []byte) {
		pkt, err := decodePacket(data, false)
		if err != nil {
			if pkt != nil {
				t.Fatalf("decoder returned both a packet and an error %v", err)
			}
			return
		}
		if pkt == nil {
			t.Fatal("decoder reported success with a nil packet")
		}
		if pkt.Header == nil {
			t.Fatal("decoded packet has no header")
		}

		// The decoder must not manufacture payload the input did not contain.
		if len(pkt.Body) > len(data) {
			t.Fatalf("decoded a %d-byte body from a %d-byte packet", len(pkt.Body), len(data))
		}

		// The claimed length must match what was produced, or a caller sizing
		// a buffer from EncodedLen writes out of bounds.
		if got, want := pkt.EncodedLen(), len(pkt.Encode()); got != want {
			t.Errorf("EncodedLen says %d, Encode produced %d", got, want)
		}

		reencoded := pkt.Encode()

		// Everything this encoder produces, this decoder must accept. The
		// header's extension byte must agree with the extensions written.
		again, err := decodePacket(reencoded, false)
		if err != nil {
			t.Fatalf("this encoder produced a packet this decoder rejects (%v):\n"+
				"  in %x\n out %x", err, data, reencoded)
		}

		// And encoding is a fixed point from there on.
		if !bytes.Equal(again.Encode(), reencoded) {
			t.Errorf("encoding is not idempotent:\n first %x\nsecond %x", reencoded, again.Encode())
		}

		// Byte-exact round trip for every packet the decoder accepts: the
		// whole extension chain is kept in order (packet.wireExtensions).
		// The selective-ack bitfield's own round trip, where a bit-order bug
		// would hide, is covered by FuzzDecodeSelectiveAck.
		if !bytes.Equal(reencoded, data) {
			t.Errorf("an accepted packet did not round trip:\n in %x\nout %x", data, reencoded)
		}
	})
}

func FuzzDecodeSelectiveAck(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	f.Add(bytes.Repeat([]byte{0xAA}, 32))
	f.Add([]byte{1})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		ack, err := decodeSelectiveAck(data)
		if err != nil {
			if ack != nil {
				t.Fatalf("decoder returned both an ack and an error %v", err)
			}
			return
		}
		if ack == nil {
			t.Fatal("decoder reported success with a nil selective ack")
		}

		// A selective ack is a whole number of 4-byte words, and reports one
		// entry per bit.
		if len(data)%4 != 0 {
			t.Fatalf("accepted a %d-byte bitfield; must be a multiple of 4", len(data))
		}
		if got, want := len(ack.Acked()), len(data)*8; got != want {
			t.Errorf("a %d-byte bitfield decoded to %d entries, want %d", len(data), got, want)
		}
		if !bytes.Equal(ack.Encode(), data) {
			t.Errorf("selective ack does not round trip:\n in %x\nout %x", data, ack.Encode())
		}
	})
}
