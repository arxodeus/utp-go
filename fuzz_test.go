package utp_go

import (
	"bytes"
	"testing"
)

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

func FuzzDecodePacketHeader(f *testing.F) {
	// Seeds: a valid header, and the shapes the M2 corpus found interesting.
	f.Add(NewPacketBuilder(st_data, 1234, 200000, 1048576, 42).Build().Encode())
	f.Add(NewPacketBuilder(st_syn, 1, 0, 0, 0).Build().Encode())
	f.Add(make([]byte, 20))
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0xFF}, 20))

	f.Fuzz(func(t *testing.T, data []byte) {
		header, err := DecodePacketHeader(data)
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
		if len(data) < MINIMAL_HEADER_SIZE {
			t.Fatalf("accepted %d bytes as a header; the minimum is %d", len(data), MINIMAL_HEADER_SIZE)
		}

		// Only version 1 exists, and only known extensions may lead.
		if header.Version != PROTOCOL_VERSION_ONE {
			t.Errorf("accepted version %d", header.Version)
		}
		if header.Extension > MAX_KNOWN_EXTENSION {
			t.Errorf("accepted leading extension %d", header.Extension)
		}
		if err := header.PacketType.Check(); err != nil {
			t.Errorf("accepted packet type %d: %v", header.PacketType, err)
		}

		// Whatever it accepted, it must be able to write back unchanged.
		reencoded := header.EncodeToBytes()
		if !bytes.Equal(reencoded, data[:MINIMAL_HEADER_SIZE]) {
			t.Errorf("header does not round trip:\n in %x\nout %x", data[:MINIMAL_HEADER_SIZE], reencoded)
		}
	})
}

func FuzzDecodePacket(f *testing.F) {
	f.Add(NewPacketBuilder(st_data, 1234, 200000, 1048576, 42).WithPayload([]byte("hello")).Build().Encode())
	f.Add(NewPacketBuilder(st_state, 7, 1, 2, 3).
		WithSelectiveAck(NewSelectiveAck([]bool{true, false, true, false})).Build().Encode())
	f.Add(NewPacketBuilder(st_fin, 9, 0, 0, 1).Build().Encode())
	f.Add(make([]byte, 22))
	f.Add([]byte{1, 1, 0, 0})

	f.Fuzz(func(t *testing.T, data []byte) {
		pkt, err := DecodePacket(data)
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

		// The claimed length has to match what it produced, or a caller
		// sizing a buffer from EncodedLen writes out of bounds.
		if got, want := pkt.EncodedLen(), len(pkt.Encode()); got != want {
			t.Errorf("EncodedLen says %d, Encode produced %d", got, want)
		}

		reencoded := pkt.Encode()

		// Everything this encoder produces, this decoder must accept.
		//
		// This is the invariant, and it is the one that broke: Encode took
		// the extension byte from the header rather than from what it was
		// writing, so a decoded packet carrying an extension we do not retain
		// re-encoded to a header claiming an extension with no extension
		// bytes after it -- which this decoder rejects.
		again, err := DecodePacket(reencoded)
		if err != nil {
			t.Fatalf("this encoder produced a packet this decoder rejects (%v):\n"+
				"  in %x\n out %x", err, data, reencoded)
		}

		// And encoding is a fixed point from there on.
		if !bytes.Equal(again.Encode(), reencoded) {
			t.Errorf("encoding is not idempotent:\n first %x\nsecond %x", reencoded, again.Encode())
		}

		// Byte-exact round trip, where it is owed.
		//
		// It is owed for a packet that carries nothing this implementation
		// drops: no extensions at all, or exactly one selective ack with the
		// chain terminating after it. Those are the only two shapes this
		// library ever emits, and the only two it can reproduce.
		//
		// It is not owed otherwise. This decoder keeps the selective ack and
		// discards everything else in an extension chain -- extension 2,
		// unknown types further along, a zero-length selective ack -- because
		// nothing here reads them. Re-encoding then legitimately produces a
		// shorter packet, and demanding equality would be demanding that we
		// reproduce data we never stored. What is still owed in that case is
		// that the packet did not somehow grow.
		//
		// The selective-ack bitfield's own round trip, which is where a
		// bit-order bug would hide, is covered directly by
		// FuzzDecodeSelectiveAck rather than through this.
		chainEndsAfterFirst := len(data) > MINIMAL_HEADER_SIZE && data[MINIMAL_HEADER_SIZE] == 0
		reproducible := data[1] == 0 || (data[1] == 1 && chainEndsAfterFirst && pkt.Eack != nil)

		if reproducible {
			if !bytes.Equal(reencoded, data) {
				t.Errorf("a packet carrying only what we retain did not round trip:\n in %x\nout %x",
					data, reencoded)
			}
		} else if len(reencoded) > len(data) {
			t.Errorf("re-encoding grew a packet whose extensions were dropped:\n in %x (%d)\nout %x (%d)",
				data, len(data), reencoded, len(reencoded))
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
		ack, err := DecodeSelectiveAck(data)
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
