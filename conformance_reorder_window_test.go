//go:build cgo

package utp

import "testing"

// Out-of-order data costs no more advertised receive window than it does in
// the reference implementation, which counts only data delivered and not yet
// read. If the window fell with out-of-order data, a large enough reorder
// backlog would leave less than a packet of window, too little for the
// retransmission that fills the gap and more than zero, so no zero-window
// probe either: both ends would go silent.
func TestReorderedDataCostsUsNoMoreWindowThanLibutp(t *testing.T) {
	const held = 40
	const bodyLen = 1000

	body := make([]byte, bodyLen)
	for i := range body {
		body[i] = byte(i)
	}

	var raws [][]byte
	for i := 0; i < held; i++ {
		// +2 onwards: +1 is the gap, and it never arrives.
		seq := corpusSynSeq + 2 + uint16(i)
		raws = append(raws, newPacketBuilder(stData, corpusSynConnID+1,
			200000+uint32(i), corpusWindow, seq).
			WithAckNum(corpusPinnedSeq-1).WithPayload(body).Build().Encode())
	}

	ourWindow, libutpWindow := windowsAfter(t, raws)
	heldBytes := uint32(held * bodyLen)

	t.Logf("after %d packets (%d bytes) held behind a gap: we advertise %d, libutp %d",
		held, heldBytes, ourWindow, libutpWindow)

	// The control: the reference implementation must be holding the data and
	// still advertising room, or there is no agreement to check.
	if libutpWindow < heldBytes {
		t.Fatalf("libutp advertised %d after holding %d bytes out of order, less than "+
			"the data it holds; this case's reading of get_rcv_window is wrong and "+
			"the comparison means nothing", libutpWindow, heldBytes)
	}

	if ourWindow != libutpWindow {
		t.Errorf("we advertise %d against libutp's %d after holding %d bytes out of "+
			"order. The two should agree: data held behind a gap is data the peer was "+
			"entitled to send, and charging the window for it stalls a peer that has "+
			"done nothing wrong.", ourWindow, libutpWindow, heldBytes)
	}
}

// The same, driven to a backlog of 800 packets, more than a megabyte. The
// reorder window bounds it: outsideReorderWindow drops anything more than
// 1024 packets past the gap.
func TestReorderedDataDoesNotBlockOurWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("drives 800 reordered packets through both implementations")
	}

	const held = 800
	const bodyLen = 1400

	body := make([]byte, bodyLen)
	var raws [][]byte
	for i := 0; i < held; i++ {
		seq := corpusSynSeq + 2 + uint16(i)
		raws = append(raws, newPacketBuilder(stData, corpusSynConnID+1,
			200000+uint32(i), corpusWindow, seq).
			WithAckNum(corpusPinnedSeq-1).WithPayload(body).Build().Encode())
	}

	ourWindow, libutpWindow := windowsAfter(t, raws)

	t.Logf("after %d packets (%d bytes) held behind a gap: we advertise %d, libutp %d",
		held, held*bodyLen, ourWindow, libutpWindow)

	if libutpWindow < uint32(bodyLen) {
		t.Fatalf("libutp's window fell to %d as well, so there is no agreement to "+
			"check here", libutpWindow)
	}
	if ourWindow < uint32(bodyLen) {
		t.Errorf("our window is %d, too small to carry a %d-byte packet, so the peer "+
			"cannot retransmit the packet that would fill the gap. This is the stall "+
			"the split into Window and Available was made to remove.",
			ourWindow, bodyLen)
	}
	if ourWindow != libutpWindow {
		t.Errorf("we advertise %d against libutp's %d", ourWindow, libutpWindow)
	}
}

// windowsAfter drives both implementations through the same packets and
// returns the receive window each ends up advertising.
func windowsAfter(t *testing.T, raws [][]byte) (ours, libutpOut uint32) {
	t.Helper()
	ourRaw, libutpRaw := runDivergenceStepsAnswered(t, raws)

	last := func(tag string, raw [][]byte) uint32 {
		t.Helper()
		if len(raw) == 0 {
			t.Fatalf("%s emitted nothing; with a gap open every packet should draw an "+
				"acknowledgement", tag)
		}
		pkt, err := decodePacket(raw[len(raw)-1], false)
		if err != nil {
			t.Fatalf("%s: could not decode its last packet: %v", tag, err)
		}
		return pkt.Header.WndSize
	}
	return last("ours", ourRaw), last("libutp", libutpRaw)
}
