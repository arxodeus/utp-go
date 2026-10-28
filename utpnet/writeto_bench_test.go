package utpnet

import (
	"context"
	"net"
	"testing"
)

// BenchmarkWriteTo measures a passthrough datagram on a UDP socket with no
// write deadline, the path every send takes for a DHT sharing the port.
func BenchmarkWriteTo(b *testing.B) {
	s, err := Listen(context.Background(), "udp", "127.0.0.1:0", &Options{Logger: quiet()})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	sink, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer sink.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			if _, _, err := sink.ReadFrom(buf); err != nil {
				return
			}
		}
	}()
	p := make([]byte, 200)
	to := sink.LocalAddr()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.WriteTo(p, to)
	}
}
