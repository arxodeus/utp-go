package utp_go

import "testing"

// The packet that fills a gap is always admitted.
//
// Without room held back for it, a gap is a deadlock: data behind the gap is
// accepted until the buffer is full, and then the retransmission that would
// release all of it is refused for want of space -- every time the peer sends
// it, and the peer sends it forever. Nothing else can free the buffer, because
// nothing behind the gap can be delivered until the gap closes.
//
// This is that situation, built directly rather than waited for.
func TestGapFillingPacketIsAdmittedWhenTheBufferIsFull(t *testing.T) {
	const capacity = 32 * 1024
	const packet = 1000
	const firstSeq = uint16(101) // initSeqNum is 100, so 101 is the gap

	rb := newReceiveBuffer(capacity, 100)

	// Everything from firstSeq+1 onwards, leaving firstSeq missing. Stop when
	// the buffer refuses one, which is the state the deadlock needs.
	body := make([]byte, packet)
	accepted := 0
	for i := 1; i < capacity/packet+4; i++ {
		if err := rb.Write(append([]byte(nil), body...), firstSeq+uint16(i)); err != nil {
			break
		}
		accepted++
	}
	if accepted == 0 {
		t.Fatal("the buffer refused the first out-of-order packet; this case needs it to fill")
	}
	if rb.Readable() != 0 {
		t.Fatalf("%d bytes readable with the gap still open", rb.Readable())
	}
	t.Logf("buffer holds %d bytes behind the gap, %d readable, %d free",
		rb.Pending(), rb.Readable(), rb.Available())

	// The buffer must be full enough that an out-of-order packet no longer
	// fits, or the reserve is not being tested.
	if err := rb.Write(append([]byte(nil), body...), firstSeq+uint16(accepted+2)); err == nil {
		t.Fatalf("an out-of-order packet was still accepted after %d; the buffer is not "+
			"full and this case is not exercising the reserve", accepted)
	}

	// And now the one that matters: the gap.
	if err := rb.Write(append([]byte(nil), body...), firstSeq); err != nil {
		t.Fatalf("the packet filling the gap was refused: %v. Everything behind it is "+
			"then undeliverable forever, because nothing else can free the buffer.", err)
	}

	// Filling it releases everything behind it.
	if rb.Readable() == 0 {
		t.Error("the gap was filled and nothing became readable")
	}
	want := (accepted + 1) * packet
	if rb.Readable() != want {
		t.Errorf("%d bytes readable after filling the gap, expected %d -- the whole run",
			rb.Readable(), want)
	}
}

// The reserve costs one packet's worth of buffer and no more.
func TestGapReserveCostsOnePacket(t *testing.T) {
	const capacity = 32 * 1024
	const packet = 1000

	rb := newReceiveBuffer(capacity, 100)
	body := make([]byte, packet)

	accepted := 0
	for i := 1; i < capacity/packet+4; i++ {
		if err := rb.Write(append([]byte(nil), body...), uint16(101+i)); err != nil {
			break
		}
		accepted++
	}

	// Out-of-order data is held to the capacity less one packet's reserve, so
	// the free space left over is at least the reserve and less than the
	// reserve plus another packet.
	free := rb.Available()
	if free < packet {
		t.Errorf("%d bytes free after filling with out-of-order data; the reserve should "+
			"keep at least one packet (%d) back", free, packet)
	}
	if free >= 2*packet {
		t.Errorf("%d bytes free, which is more than the reserve needs; out-of-order data "+
			"is being held back by more than one packet's worth", free)
	}
	t.Logf("%d out-of-order packets accepted, %d bytes free, reserve %d",
		accepted, free, rb.gapReserve())
}

// In-order data is not subject to the reserve at all: a buffer filling up in
// order fills completely.
func TestInOrderDataIsNotHeldBackByTheReserve(t *testing.T) {
	const capacity = 8 * 1024
	const packet = 1000

	rb := newReceiveBuffer(capacity, 100)
	body := make([]byte, packet)

	accepted := 0
	for i := 1; i <= capacity/packet+2; i++ {
		if err := rb.Write(append([]byte(nil), body...), uint16(100+i)); err != nil {
			break
		}
		accepted++
	}

	if got, want := rb.Readable(), accepted*packet; got != want {
		t.Errorf("%d readable after %d in-order packets, expected %d", got, accepted, want)
	}
	if rb.Available() >= packet {
		t.Errorf("%d bytes still free after filling in order; in-order data should be "+
			"able to use the whole buffer", rb.Available())
	}
}
