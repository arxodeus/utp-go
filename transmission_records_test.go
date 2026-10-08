package utp_go

import (
	"testing"
	"time"
)

// The controller holds a record only for the packets the sender holds.
//
// Records used to be replaced only when a sequence number came round again,
// so a connection that had sent 65,536 packets held one for each, about 5 MB,
// for the rest of its life. libutp frees a packet's record when it is
// acknowledged (ack_packet, utp_internal.cpp:1397). Here 200,000 packets go
// out and are acknowledged a few at a time, three times round the sequence
// space; the controller must never hold more records than packets in flight.
func TestControllerKeepsRecordsOnlyForTheWindow(t *testing.T) {
	const (
		packets  = 200_000
		inFlight = 8
		size     = 100
	)
	ctrl := newDefaultController(fromConnConfig(NewConnectionConfig()))
	sent := newSentPacketsWithoutLogger(1, ctrl)
	now := time.Unix(0, 0)
	seq := uint16(1)
	most := 0
	for i := 0; i < packets; i += inFlight {
		for j := 0; j < inFlight; j++ {
			sent.OnTransmit(seq, st_data, make([]byte, size), size, now)
			seq++
		}
		now = now.Add(time.Millisecond)
		if err := sent.OnAckNum(seq-1, nil, time.Millisecond, now); err != nil {
			t.Fatalf("ack of %d: %v", seq-1, err)
		}
		if n := len(ctrl.transmissions); n > most {
			most = n
		}
	}
	if most > inFlight {
		t.Fatalf("the controller held %d records with at most %d packets in flight", most, inFlight)
	}
}
