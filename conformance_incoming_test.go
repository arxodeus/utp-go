//go:build cgo

package utp_go

import "testing"

// Cases from reading utp_process_incoming line by line (LIBUTP-AUDIT.md).

// An out-of-order packet that has already arrived is discarded with no
// acknowledgement: libutp returns before schedule_ack (utp_internal.cpp:
// 2425-2431).
func TestConformanceDuplicateOutOfOrderData(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "out-of-order data is selectively acknowledged",
			inject: NewPacketBuilder(st_data, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("second")).Build(),
			wantAck: ackNum(corpusSynSeq),
		},
		{
			name: "the same packet again draws nothing",
			inject: NewPacketBuilder(st_data, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("second")).Build(),
			wantNoEmission: true,
		},
		{
			name: "control: the missing packet is acknowledged with both",
			inject: NewPacketBuilder(st_data, corpusSynConnID+1, 220000, corpusWindow, corpusSynSeq+1).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("first")).Build(),
			wantAck: ackNum(corpusSynSeq + 2),
		},
	})
}

// establishedSteps completes libutp's side of the connection. Its acceptor
// stays in CS_SYN_RECV until the first ST_DATA (utp_internal.cpp:2157) and
// drops anything else that arrives before it (:2311), which is a recorded
// deviation ("Completing an incoming connection") and not what these cases
// are about.
func establishedSteps() []step {
	return []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "first data completes the connection",
			inject: NewPacketBuilder(st_data, corpusSynConnID+1, 190000, corpusWindow, corpusSynSeq+1).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("first")).Build(),
			wantAck: ackNum(corpusSynSeq + 1),
		},
	}
}

// Data numbered past the peer's FIN, arriving out of order, is dropped and the
// connection carries on: `if (conn->got_fin && pk_seq_nr > conn->eof_pkt)
// return 0;` (utp_internal.cpp:2381-2386).
func TestConformanceDataPastTheFinIsDropped(t *testing.T) {
	runResponderCorpus(t, append(establishedSteps(), []step{
		{
			name: "a FIN ahead of a gap",
			inject: NewPacketBuilder(st_fin, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+4).
				WithAckNum(corpusPinnedSeq - 1).Build(),
			wantAck: ackNum(corpusSynSeq + 1),
		},
		{
			name: "data numbered past the FIN draws nothing",
			inject: NewPacketBuilder(st_data, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+6).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("too late")).Build(),
			wantNoEmission: true,
		},
		{
			name: "control: data inside the stream is still acknowledged",
			inject: NewPacketBuilder(st_data, corpusSynConnID+1, 220000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("in time")).Build(),
			wantAck: ackNum(corpusSynSeq + 2),
		},
	}...))
}

// A second FIN with another sequence number does not move the end of the
// stream, and does not end the connection: libutp records eof_pkt only
// `if (pk_flags == ST_FIN && !conn->got_fin)` (utp_internal.cpp:2316) and
// otherwise treats the packet like data.
func TestConformanceSecondFinDoesNotMoveTheEnd(t *testing.T) {
	runResponderCorpus(t, append(establishedSteps(), []step{
		{
			name: "a FIN ahead of a gap",
			inject: NewPacketBuilder(st_fin, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+4).
				WithAckNum(corpusPinnedSeq - 1).Build(),
			wantAck: ackNum(corpusSynSeq + 1),
		},
		{
			name: "a second FIN, earlier",
			inject: NewPacketBuilder(st_fin, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+3).
				WithAckNum(corpusPinnedSeq - 1).Build(),
			wantAck: ackNum(corpusSynSeq + 1),
		},
		{
			name: "the gap fills, and the stream ends at the first FIN",
			inject: NewPacketBuilder(st_data, corpusSynConnID+1, 220000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("in time")).Build(),
			wantAck:               ackNum(corpusSynSeq + 4),
			libutpExtraDuplicates: 1,
			divergenceReason: "on reaching a FIN libutp acks twice, once immediately " +
				"and once from the deferred list; the second is a duplicate",
		},
	}...))
}
