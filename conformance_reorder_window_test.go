//go:build cgo

package utp_go

import "testing"

// Out-of-order data costs us no more receive window than it costs libutp.
//
// It used to cost all of it. `receiveBuffer.Available()` subtracted every byte
// held behind a gap from the window this library advertised, where libutp's
// `get_rcv_window` (`utp_internal.cpp:590-596`) counts only what its embedder
// has not yet read -- and a packet held out of order sits in `conn->inbuf`,
// never reaching `utp_call_on_read` until the gap before it is filled, so it
// never enters that figure.
//
// Measured before the split into Window and Available:
//
//	40,000 bytes held       we advertised 1,008,576   libutp 1,048,576
//	1,120,000 bytes held    we advertised     1,376   libutp 1,048,576
//
// The second row is the one that mattered. 1,376 bytes is under the 1,400 a
// packet needs, so the peer could send nothing -- including the retransmission
// that would have filled the gap and let the whole buffer be delivered. And it
// is above zero, so the peer's zero-window probe, which tests for exactly
// zero, never armed. Both ends went silent. That was the cause of a stall a
// network case reached in 4 runs out of 20.
//
// These cases now pin the agreement rather than the divergence. If they start
// failing because our window falls again, the stall is back.
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
		raws = append(raws, NewPacketBuilder(st_data, corpusSynConnID+1,
			200000+uint32(i), corpusWindow, seq).
			WithAckNum(corpusPinnedSeq-1).WithPayload(body).Build().Encode())
	}

	ourWindow, libutpWindow := windowsAfter(t, raws)
	heldBytes := uint32(held * bodyLen)

	t.Logf("after %d packets (%d bytes) held behind a gap: we advertise %d, libutp %d",
		held, heldBytes, ourWindow, libutpWindow)

	// The control: libutp has to be holding the data and still advertising
	// room, or there is no agreement worth checking.
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

// The same, driven to where it used to block the peer entirely.
//
// 800 packets behind a gap is more than a megabyte -- enough to have taken the
// old accounting under the size of a single packet. The reorder window bounds
// it there: outsideReorderWindow drops anything more than 1024 packets past
// the gap, as libutp does.
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
		raws = append(raws, NewPacketBuilder(st_data, corpusSynConnID+1,
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
	ourRaw, libutpRaw := runDivergenceSteps(t, raws)

	last := func(tag string, raw [][]byte) uint32 {
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
	return last("ours", ourRaw), last("libutp", libutpRaw)
}
