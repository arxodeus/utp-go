package utp

import "testing"

// The packet that fills a gap is taken even when it is larger than any packet
// received so far. The reserve held for it was the largest packet received,
// and the missing one, never having arrived, can be larger: twelve 300-byte
// packets behind a gap left 496 bytes of a 4,096-byte buffer, a 1,400-byte
// retransmission never fit, nothing was readable, and the connection stalled.
// From the release review of 9c44c4f (R5).
func TestGapLargerThanAnyPacketReceivedIsFilled(t *testing.T) {
	rb := newReceiveBuffer(4096, 0)
	lastAccepted := uint16(1)
	for seq := uint16(2); seq <= 13; seq++ {
		if err := rb.Write(make([]byte, 300), seq); err != nil {
			break // refusing out-of-order data to keep room for the gap is fine
		}
		lastAccepted = seq
	}
	if err := rb.Write(make([]byte, 1400), 1); err != nil {
		t.Fatalf("gap-filling retransmission refused: free %d, readable %d: %v",
			rb.Available(), rb.Readable(), err)
	}
	if rb.AckNum() != lastAccepted {
		t.Fatalf("ack %d, want %d", rb.AckNum(), lastAccepted)
	}
	if rb.Window() != 0 {
		t.Fatalf("window %d over a buffer past its capacity, want 0", rb.Window())
	}
}

// A buffer smaller than one packet takes the packet when it holds nothing
// readable, rather than refusing it for ever.
func TestPacketLargerThanTheWholeBufferIsTakenWhenEmpty(t *testing.T) {
	rb := newReceiveBuffer(1000, 0)
	if err := rb.Write(make([]byte, 1400), 1); err != nil {
		t.Fatalf("refused: %v", err)
	}
	if rb.Readable() != 1400 {
		t.Fatalf("readable %d, want 1400", rb.Readable())
	}
}

// The exception is for a buffer with nothing to read. One with readable data
// still refuses what does not fit: the reader will free room, and the peer
// must wait for it.
func TestInOrderPacketStillWaitsForTheReader(t *testing.T) {
	rb := newReceiveBuffer(4096, 0)
	if err := rb.Write(make([]byte, 4000), 1); err != nil {
		t.Fatal(err)
	}
	if err := rb.Write(make([]byte, 1400), 2); err == nil {
		t.Fatal("took a packet past the capacity with readable data held")
	}
	buf := make([]byte, 4000)
	rb.Read(buf)
	if err := rb.Write(make([]byte, 1400), 2); err != nil {
		t.Fatalf("refused after the reader made room: %v", err)
	}
}
