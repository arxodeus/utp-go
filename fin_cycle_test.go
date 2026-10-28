package utp

import (
	"testing"
)

// A FIN that overtakes the last data must not strand it, however long the
// connection has run. With the first sequence number 1000 and the
// acknowledgement at 998 after a full cycle, DATA 999 and 1000 must still be
// accepted. The fixture sets consumed directly rather than sending 65,534
// packets.
func TestFinOvertakingDataAfterAFullSequenceCycle(t *testing.T) {
	rb := newReceiveBuffer(4096, 1000)
	rb.consumed = 65534
	if rb.AckNum() != 998 {
		t.Fatal("invalid fixture")
	}
	c := &connection{
		logger: newLogger(nil),
		cid:    &ConnectionId{},
		state:  &connState{stateType: connConnected, RecvBuf: rb},
	}
	if err := c.onFin(1001, nil); err != nil {
		t.Fatal(err)
	}
	for _, seq := range []uint16{999, 1000} {
		if err := c.onData(seq, []byte{1}); err != nil {
			t.Fatal(err)
		}
	}
	if rb.AckNum() != 1001 {
		t.Fatalf("data before the FIN was discarded: ack %d, want 1001", rb.AckNum())
	}
}

// Data past the FIN is still refused, at any point in the cycle.
func TestDataPastTheFinIsStillDropped(t *testing.T) {
	for _, init := range []uint16{0, 1000, 65530} {
		rb := newReceiveBuffer(4096, init)
		rb.consumed = 65534
		c := &connection{
			logger: newLogger(nil),
			cid:    &ConnectionId{},
			state:  &connState{stateType: connConnected, RecvBuf: rb},
		}
		fin := rb.AckNum() + 3
		if err := c.onFin(fin, nil); err != nil {
			t.Fatal(err)
		}
		if err := c.onData(fin+1, []byte{1}); err != nil {
			t.Fatal(err)
		}
		if !c.dropUnacked || rb.HoldsOutOfOrder(fin+1) {
			t.Fatalf("init %d: data numbered past the FIN was taken", init)
		}
	}
}
