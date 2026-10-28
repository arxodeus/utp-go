//go:build cgo

package utp

import "testing"

// Edge cases of incoming packet processing.

// An out-of-order packet that has already arrived is discarded without an
// acknowledgement.
func TestConformanceDuplicateOutOfOrderData(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "out-of-order data is selectively acknowledged",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("second")).Build(),
			wantAck: ackNum(corpusSynSeq),
		},
		{
			name: "the same packet again draws nothing",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("second")).Build(),
			wantNoEmission: true,
		},
		{
			name: "control: the missing packet is acknowledged with both",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 220000, corpusWindow, corpusSynSeq+1).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("first")).Build(),
			wantAck: ackNum(corpusSynSeq + 2),
		},
	})
}

// establishedSteps completes the reference implementation's side of the
// connection. Its acceptor waits for the first ST_DATA before it is
// established and drops anything else before it, which these cases are not
// about.
func establishedSteps() []step {
	return []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "first data completes the connection",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 190000, corpusWindow, corpusSynSeq+1).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("first")).Build(),
			wantAck: ackNum(corpusSynSeq + 1),
		},
	}
}

// Data numbered past the peer's FIN, arriving out of order, is dropped and
// the connection continues.
func TestConformanceDataPastTheFinIsDropped(t *testing.T) {
	runResponderCorpus(t, append(establishedSteps(), []step{
		{
			name: "a FIN ahead of a gap",
			inject: newPacketBuilder(stFin, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+4).
				WithAckNum(corpusPinnedSeq - 1).Build(),
			wantAck: ackNum(corpusSynSeq + 1),
		},
		{
			name: "data numbered past the FIN draws nothing",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+6).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("too late")).Build(),
			wantNoEmission: true,
		},
		{
			name: "control: data inside the stream is still acknowledged",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 220000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("in time")).Build(),
			wantAck: ackNum(corpusSynSeq + 2),
		},
	}...))
}

// A second FIN with another sequence number does not move the end of the
// stream or end the connection; it is treated like data.
func TestConformanceSecondFinDoesNotMoveTheEnd(t *testing.T) {
	runResponderCorpus(t, append(establishedSteps(), []step{
		{
			name: "a FIN ahead of a gap",
			inject: newPacketBuilder(stFin, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+4).
				WithAckNum(corpusPinnedSeq - 1).Build(),
			wantAck: ackNum(corpusSynSeq + 1),
		},
		{
			name: "a second FIN, earlier",
			inject: newPacketBuilder(stFin, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+3).
				WithAckNum(corpusPinnedSeq - 1).Build(),
			wantAck: ackNum(corpusSynSeq + 1),
		},
		{
			name: "the gap fills, and the stream ends at the first FIN",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 220000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("in time")).Build(),
			wantAck:               ackNum(corpusSynSeq + 4),
			libutpExtraDuplicates: 1,
			divergenceReason: "on reaching a FIN libutp acks twice, once immediately " +
				"and once from the deferred list; the second is a duplicate",
		},
	}...))
}

// A SYN for a connection that already exists draws nothing from the
// reference implementation, while this library sends its SYN-ACK again, so
// an acceptor whose SYN-ACK was lost still answers the initiator's retries.
func TestConformanceDuplicateSynBeforeData(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name:      "the same SYN again",
			inject:    synPacketFor(corpusSynConnID, corpusSynSeq),
			oursExtra: 1,
			divergenceReason: "libutp ignores a SYN for a connection it already has; we " +
				"repeat the SYN-ACK so a lost one does not end the handshake",
		},
	})
}

// withExtensionBits returns the encoded packet with an "extension bits"
// extension (type 2) of n bytes ahead of its payload.
func withExtensionBits(p *packet, n int) []byte {
	raw := p.Encode()
	if raw[1] != 0 {
		panic("withExtensionBits expects a packet with no extensions")
	}
	out := append([]byte(nil), raw[:20]...)
	out[1] = 2
	out = append(out, 0, byte(n))
	out = append(out, make([]byte, n)...)
	return append(out, raw[20:]...)
}

// An extension-bits extension must be 8 bytes long, or the packet is
// dropped.
func TestConformanceExtensionBitsMustBeEightBytes(t *testing.T) {
	runResponderCorpus(t, append(establishedSteps(), []step{
		{
			name: "data with 4 bytes of extension bits draws nothing",
			injectRaw: withExtensionBits(newPacketBuilder(stData, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq-1).WithPayload([]byte("short bits")).Build(), 4),
			wantNoEmission: true,
		},
		{
			name: "the same with 8 bytes is acknowledged",
			injectRaw: withExtensionBits(newPacketBuilder(stData, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq-1).WithPayload([]byte("full bits")).Build(), 8),
			wantAck: ackNum(corpusSynSeq + 2),
		},
	}...))
}
