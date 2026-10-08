package utp_go

import (
	"bytes"
	"testing"
)

// The socket decodes each datagram from its read buffer and reuses the buffer
// for the next read, so a decoded packet must not refer to it: the body is
// copied out, and the rest of the packet is decoded into values. Here the
// buffer is overwritten after decoding, as the next read would, and the
// packet must be unchanged.
func TestDecodedPacketOutlivesItsBuffer(t *testing.T) {
	sack := NewSelectiveAck([]bool{true, false, true, false, false, true})
	body := []byte("payload bytes")
	wire := NewPacketBuilder(st_data, 7, 1234, 5678, 99).WithAckNum(42).
		WithSelectiveAck(sack).WithPayload(body).Build().Encode()

	buf := append([]byte(nil), wire...)
	pkt, err := decodePacket(buf, true)
	if err != nil {
		t.Fatal(err)
	}
	for i := range buf {
		buf[i] = 0xAA
	}
	if !bytes.Equal(pkt.Body, body) {
		t.Fatalf("body %q after the buffer was reused, expected %q", pkt.Body, body)
	}
	if !bytes.Equal(pkt.Encode(), wire) {
		t.Fatalf("the packet re-encodes differently after the buffer was reused")
	}
}
