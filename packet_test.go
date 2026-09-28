package utp_go

import (
	"bytes"
	"errors"
	"math"
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"
	"time"
)

func (p *PacketHeaderV1) Generate(rand *rand.Rand, size int) reflect.Value {
	packetType := PacketType(rand.Intn(5))
	// The extension byte must name a known extension: the decoder rejects
	// anything above MAX_KNOWN_EXTENSION, matching libutp
	// (utp_internal.cpp:2481). This generator previously produced arbitrary
	// bytes and asserted they survived a round trip, which only held while
	// the decoder validated nothing.
	extension := byte(rand.Intn(MAX_KNOWN_EXTENSION + 1))
	header := &PacketHeaderV1{
		PacketType:    packetType,
		Version:       PROTOCOL_VERSION_ONE,
		Extension:     extension,
		ConnectionId:  uint16(rand.Intn(math.MaxUint16)),
		Timestamp:     int64(rand.Intn(math.MaxUint32)),
		TimestampDiff: uint32(rand.Intn(math.MaxUint32)),
		WndSize:       uint32(rand.Intn(math.MaxUint32)),
		SeqNum:        uint16(rand.Intn(math.MaxUint16)),
		AckNum:        uint16(rand.Intn(math.MaxUint16)),
	}
	return reflect.ValueOf(header)
}

func (a *SelectiveAck) Generate(rand *rand.Rand, size int) reflect.Value {
	bits := rand.Intn(size)
	acked := make([]bool, bits)
	for i := 0; i < bits; i++ {
		acked[i] = rand.Intn(2) == 1
	}
	//if len(acked) == 0 {
	//	var empty [32]bool
	//	acked = empty[:]
	//}
	return reflect.ValueOf(NewSelectiveAck(acked))
}

func TestPacketHeaderEncodeDecode(t *testing.T) {
	config := &quick.Config{
		MaxCount: 1000,
		Rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	checkPacketHeader := func(header *PacketHeaderV1) bool {
		encoded := header.EncodeToBytes()
		if len(encoded) != 20 {
			return false
		}
		headerFromDecode, err := DecodePacketHeader(encoded)
		if err != nil {
			return false
		}
		return reflect.DeepEqual(header, headerFromDecode)
	}
	if err := quick.Check(checkPacketHeader, config); err != nil {
		t.Error(err)
	}
}

func TestSelectiveAckEncodeDecode(t *testing.T) {
	config := &quick.Config{
		MaxCount: 1000,
		Rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	checkSelective := func(ack *SelectiveAck) bool {
		encodedLen := ack.EncodedLen()
		encoded := ack.Encode()
		if len(encoded)%(SELECTIVE_ACK_BITS/8) != 0 {
			return false
		}
		if len(encoded) != encodedLen {
			return false
		}
		ackFromDecode, err := DecodeSelectiveAck(encoded)
		if err != nil {
			if encodedLen < 4 && errors.Is(err, ErrInsufficientSelectiveAckLen) {
				return true
			}
			t.Logf("expected err to be nil, got %v", err)
			return false
		}
		res := reflect.DeepEqual(ack, ackFromDecode)
		if !res {
			t.Logf("expected %v, got %v", ack, ackFromDecode)
		}
		return res
	}
	if err := quick.Check(checkSelective, config); err != nil {
		t.Error(err)
	}
}

func TestPacket(t *testing.T) {
	config := &quick.Config{
		MaxCount: 1000,
		Rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	// Helper function to check property
	checkPacket := func(header *PacketHeaderV1, selectiveAck *SelectiveAck, payload []byte) bool {
		// Check empty payload
		if len(payload) == 0 {
			return true
		}

		// Handle selective ack
		if len(selectiveAck.acked) > 0 {
			header.Extension = 1
		} else {
			selectiveAck = nil
			header.Extension = 0
		}

		// Create packet
		packetInst := &packet{
			Header: header,
			Eack:   selectiveAck,
			Body:   payload,
		}

		// Get encoded length
		encodedLen := packetInst.EncodedLen()

		// Encode packet
		encoded := packetInst.Encode()

		// Check length
		if len(encoded) != encodedLen {
			t.Errorf("encoded length mismatch: got %d, want %d", len(encoded), encodedLen)
			return false
		}

		// Decode packet

		decoded, err := DecodePacket(encoded)
		if err != nil {
			t.Errorf("failed to decode packet: %v", err)
			return false
		}

		// Compare packets. A decoded packet also carries the raw extension
		// chain it arrived with, which a built one does not, so compare what
		// both hold, and that the decoded one writes back the same bytes.
		return reflect.DeepEqual(decoded.Header, packetInst.Header) &&
			reflect.DeepEqual(decoded.Eack, packetInst.Eack) &&
			reflect.DeepEqual(decoded.Body, packetInst.Body) &&
			bytes.Equal(decoded.Encode(), encoded)
	}

	if err := quick.Check(checkPacket, config); err != nil {
		t.Error(err)
	}
}

// A decoded packet keeps its whole extension chain, and a caller that edits
// its selective ack gets that edit written in the ack's place. The fuzzer
// checks the unedited round trip; these are the edits it cannot make.
func TestDecodedPacketKeepsExtensionsThroughEdits(t *testing.T) {
	base := NewPacketBuilder(st_state, 5, 1, 2, 3).
		WithSelectiveAck(NewSelectiveAck([]bool{true, false, false, false})).Build().Encode()
	// Chain an unknown extension, type 99, after the selective ack.
	sackEnd := MINIMAL_HEADER_SIZE + 2 + 4
	wire := append([]byte{}, base[:sackEnd]...)
	wire[MINIMAL_HEADER_SIZE] = 99
	wire = append(wire, 0, 3, 0xaa, 0xbb, 0xcc)
	wire = append(wire, base[sackEnd:]...)

	decoded, err := DecodePacket(wire)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if !bytes.Equal(decoded.Encode(), wire) {
		t.Fatalf("an unedited packet did not round trip:\n in %x\nout %x", wire, decoded.Encode())
	}

	t.Run("a replaced selective ack is written in its place", func(t *testing.T) {
		p, _ := DecodePacket(wire)
		p.Eack = NewSelectiveAck([]bool{false, true, false, false})
		again, err := DecodePacket(p.Encode())
		if err != nil {
			t.Fatalf("the edited packet does not decode: %v", err)
		}
		if again.Eack == nil || again.Eack.Encode()[0] != p.Eack.Encode()[0] {
			t.Errorf("the replaced selective ack did not survive")
		}
		if len(again.chain) != 2 || again.chain[1].extension != 99 {
			t.Errorf("the unknown extension after it was lost: %+v", again.chain)
		}
	})

	t.Run("a cleared selective ack never leaves an unknown extension first", func(t *testing.T) {
		p, _ := DecodePacket(wire)
		p.Eack = nil
		out := p.Encode()
		again, err := DecodePacket(out)
		if err != nil {
			t.Fatalf("clearing the selective ack produced an unreadable packet: %v\n%x", err, out)
		}
		if again.Eack != nil || again.Header.Extension != 0 {
			t.Errorf("want no extensions left, got header extension %d, Eack %v",
				again.Header.Extension, again.Eack)
		}
		if !bytes.Equal(again.Body, p.Body) {
			t.Errorf("the body changed")
		}
	})
}
