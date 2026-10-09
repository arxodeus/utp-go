package utp_go

import (
	"bytes"
	"testing"
)

// The socket decodes each datagram from its read buffer and reuses the buffer
// for the next read. A packet processed before then needs nothing of its own;
// one that is kept takes its body (own), and nothing else in it refers to the
// buffer: the header is decoded into values and the extensions copied. Here
// the buffer is overwritten after own, as the next read would, and the packet
// must be unchanged.
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
	pkt.own()
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

// Data held out of order outlives the read that brought it, so the receive
// buffer keeps a copy: the body may be the socket reader's buffer.
func TestOutOfOrderDataOutlivesItsBuffer(t *testing.T) {
	rb := newReceiveBuffer(1<<16, 100)
	later := []byte("second")
	if err := rb.Write(later, 102); err != nil {
		t.Fatal(err)
	}
	for i := range later {
		later[i] = 0xAA
	}
	if err := rb.Write([]byte("first"), 101); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 32)
	n := rb.Read(got)
	if string(got[:n]) != "firstsecond" {
		t.Fatalf("read %q after the out-of-order packet's buffer was reused, expected %q", got[:n], "firstsecond")
	}
}
