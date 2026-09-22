//go:build cgo

package utp_go

import "testing"

// What out-of-order data costs the advertised receive window.
//
// This is the confirmation of a hypothesis raised by a stall that a network
// test reached and could not explain: that this library's receive window
// shrinks by data it is holding out of order and cannot yet deliver, where
// libutp's does not.
//
// Both accountings are visible in the source:
//
//	receiveBuffer.Available()  (recv_buffer.go)
//	    available := len(rb.buf) - rb.offset
//	    rb.pending.Ascend(... available -= len(item.data) ...)
//
//	UTPSocket::get_rcv_window  (utp_internal.cpp:590-596)
//	    const size_t numbuf = utp_call_get_read_buffer_size(this->ctx, this);
//	    return opt_rcvbuf > numbuf ? opt_rcvbuf - numbuf : 0;
//
// libutp's window is its embedder's unread bytes. A packet held out of order
// sits in conn->inbuf and is never handed to utp_call_on_read until the gap
// before it is filled, so it never reaches that count. Ours is subtracted the
// moment it arrives.
//
// Why it matters beyond a number on the wire: the window is what permits the
// peer to send, and the packet that would fill the gap is one of the things it
// permits. Enough out-of-order data and the sender is told to stop -- including
// stopping from sending the one packet that would let everything be delivered
// and the buffer freed. The zero-window probe is then the only way out, and it
// arms on a window of exactly zero (conn.go), so a window left small rather
// than closed does not even get that.
//
// The reorder window bounds how much can accumulate -- outsideReorderWindow
// drops anything past 1024 packets ahead, as libutp does -- so this is bounded,
// not unbounded. It is still 1024 packets' worth of window that libutp keeps
// and this does not.
func TestReorderedDataShrinksOnlyOurWindow(t *testing.T) {
	// A gap at corpusSynSeq+1, then packets after it, none of which can be
	// delivered until the gap is filled.
	const held = 40
	const bodyLen = 1000

	body := make([]byte, bodyLen)
	for i := range body {
		body[i] = byte(i)
	}

	var raws [][]byte
	for i := 0; i < held; i++ {
		// +2 onwards: +1 is the hole and never arrives.
		seq := corpusSynSeq + 2 + uint16(i)
		raws = append(raws, NewPacketBuilder(st_data, corpusSynConnID+1,
			200000+uint32(i), corpusWindow, seq).
			WithAckNum(corpusPinnedSeq-1).WithPayload(body).Build().Encode())
	}

	ours, libutpOut := runDivergenceSteps(t, raws)

	lastWindow := func(tag string, raw [][]byte) uint32 {
		t.Helper()
		if len(raw) == 0 {
			t.Fatalf("%s emitted nothing; with a gap open every packet should draw an "+
				"acknowledgement", tag)
		}
		pkt, err := DecodePacket(raw[len(raw)-1])
		if err != nil {
			t.Fatalf("%s: could not decode its last packet: %v", tag, err)
		}
		return pkt.Header.WndSize
	}

	ourWindow := lastWindow("ours", ours)
	libutpWindow := lastWindow("libutp", libutpOut)
	heldBytes := uint32(held * bodyLen)

	t.Logf("after %d packets (%d bytes) held behind a gap: we advertise %d, libutp %d",
		held, heldBytes, ourWindow, libutpWindow)

	// libutp's window is untouched by data it cannot deliver.
	if libutpWindow < heldBytes {
		t.Errorf("libutp advertised %d after holding %d bytes out of order, which is "+
			"less than the data it is holding -- this case's reading of get_rcv_window "+
			"is wrong and the comparison below means nothing",
			libutpWindow, heldBytes)
	}

	// Ours is reduced by every byte of it.
	if ourWindow > libutpWindow-heldBytes+bodyLen {
		t.Errorf("we advertised %d against libutp's %d after holding %d bytes out of "+
			"order. This case exists because we shrink by them and libutp does not; "+
			"if that is no longer true it should be deleted, not adjusted",
			ourWindow, libutpWindow, heldBytes)
	}

	// The point, stated as the gap it is: libutp keeps this window open and
	// we do not.
	if gap := libutpWindow - ourWindow; gap < heldBytes {
		t.Errorf("the two windows differ by %d after %d bytes held out of order; "+
			"expected the whole of it", gap, heldBytes)
	}
}

// The same accounting, driven to the point where it stops the peer.
//
// A smaller window is a difference. A window smaller than a packet is a stall:
// the peer may send nothing, including the packet that would fill the gap and
// let the whole buffer be delivered and freed. Nothing else can fill it -- the
// gap is the peer's to retransmit, and the window forbids it.
//
// **And it does not stop at zero, which is worse than if it did.** Measured
// below: 800 packets held behind a gap leave the window at 1376 bytes, under
// the 1400 a packet needs but above the zero that would arm the peer's
// zero-window probe (conn.go tests peerRecvWindow == 0 exactly). So the one
// mechanism that would eventually break the deadlock never fires. This is the
// state the network traces kept showing -- windows of 861 and 7 bytes, both
// ends silent for seconds at a time -- and it is why those runs stalled.
//
// The reorder window bounds how far it can go: outsideReorderWindow drops
// anything more than 1024 packets past the gap, as libutp does. 1024 packets
// is more than a megabyte, so a megabyte receive buffer can be driven under a
// packet by data the peer was entitled to send.
func TestReorderedDataCanBlockOurWindowEntirely(t *testing.T) {
	if testing.Short() {
		t.Skip("drives 800 reordered packets through both implementations")
	}

	const held = 800
	const bodyLen = 1400

	body := make([]byte, bodyLen)
	var raws [][]byte
	for i := 0; i < held; i++ {
		seq := corpusSynSeq + 2 + uint16(i)
		raws = append(raws, NewPacketBuilder(st_data, corpusSynConnID+1,
			200000+uint32(i), corpusWindow, seq).
			WithAckNum(corpusPinnedSeq-1).WithPayload(body).Build().Encode())
	}

	ours, libutpOut := runDivergenceSteps(t, raws)
	if len(ours) == 0 || len(libutpOut) == 0 {
		t.Fatalf("emissions: ours %d, libutp %d", len(ours), len(libutpOut))
	}
	ourPkt, err := DecodePacket(ours[len(ours)-1])
	if err != nil {
		t.Fatalf("decoding our last packet: %v", err)
	}
	libutpPkt, err := DecodePacket(libutpOut[len(libutpOut)-1])
	if err != nil {
		t.Fatalf("decoding libutp's last packet: %v", err)
	}

	t.Logf("after %d packets (%d bytes) held behind a gap: we advertise %d, libutp %d",
		held, held*bodyLen, ourPkt.Header.WndSize, libutpPkt.Header.WndSize)

	if libutpPkt.Header.WndSize < uint32(bodyLen) {
		t.Fatalf("libutp's window fell to %d as well, so this is not a divergence and "+
			"this case should be deleted rather than adjusted", libutpPkt.Header.WndSize)
	}
	if ourPkt.Header.WndSize >= uint32(bodyLen) {
		t.Errorf("our window is %d, still enough to carry a %d-byte packet. This case "+
			"exists to pin the stall: if reordered data no longer blocks the window, "+
			"the limitation it records has been fixed and this should be rewritten to "+
			"assert that instead", ourPkt.Header.WndSize, bodyLen)
	}
	// Stated separately because it is the part that makes it a deadlock rather
	// than a pause.
	if ourPkt.Header.WndSize == 0 {
		t.Logf("note: the window reached exactly zero on this run, which would at " +
			"least arm the peer's zero-window probe. The stall this case is about is " +
			"the non-zero case, which does not.")
	}
}
