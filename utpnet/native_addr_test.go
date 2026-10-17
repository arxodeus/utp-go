package utpnet

import (
	"fmt"
	"testing"
)

type otherAddr string

func (a otherAddr) Network() string { return "other" }
func (a otherAddr) String() string  { return string(a) }

// The map from UDP form back to a PacketConn's own address type is bounded,
// however many addresses datagrams arrive from, and keeps the most recent.
// It forgot nothing, so a PacketConn carrying a DHT grew it for as long as
// it ran. From the release review of 9c44c4f (R10).
func TestNativeAddressesAreBounded(t *testing.T) {
	p := &packetConn{}
	var last otherAddr
	for i := 0; i < 3*nativeMax; i++ {
		last = otherAddr(fmt.Sprintf("10.%d.%d.%d:6881", byte(i>>16), byte(i>>8), byte(i)))
		if _, ok := p.udpForm(last); !ok {
			t.Fatalf("%v has no UDP form", last)
		}
	}
	if n := len(p.native); n > nativeMax {
		t.Fatalf("%d addresses remembered after %d, want at most %d", n, 3*nativeMax, nativeMax)
	}
	udp, _ := p.udpForm(last)
	if got := p.nativeOf(udp); got != last {
		t.Fatalf("the latest address maps back to %v, want %v", got, last)
	}
}
