package utp

import (
	"testing"
	"time"
)

// The controller holds records only for the packets the sender holds. Here
// 200,000 packets are sent and acknowledged a few at a time, three times
// round the sequence space, and the controller must never hold more records
// than packets in flight.
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
			sent.OnTransmit(seq, stData, make([]byte, size), size, now)
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
