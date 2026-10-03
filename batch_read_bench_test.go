//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package utp_go

import (
	"fmt"
	"net"
	"testing"
	"time"
)

// BenchmarkReadBatch is the cost of UdpConn.readBatch taking k datagrams the
// kernel already holds, per datagram (ns/datagram): k = 1 is a reader woken
// for each packet, as on a paced path, and larger k a reader that fell behind
// a burst. Only the read is timed; the datagrams are queued before it.
func BenchmarkReadBatch(b *testing.B) {
	for _, k := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("queued=%d", k), func(b *testing.B) {
			base, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				b.Fatal(err)
			}
			_ = base.SetReadBuffer(4 << 20)
			c := &UdpConn{base: base}
			defer c.Close()
			src, err := net.DialUDP("udp4", nil, base.LocalAddr().(*net.UDPAddr))
			if err != nil {
				b.Fatal(err)
			}
			defer src.Close()
			payload := make([]byte, 1400)
			var took time.Duration
			for i := 0; i < b.N; i++ {
				for j := 0; j < k; j++ {
					if _, err := src.Write(payload); err != nil {
						b.Fatal(err)
					}
				}
				start := time.Now()
				dgs, err := c.readBatch()
				took += time.Since(start)
				if err != nil {
					b.Fatal(err)
				}
				// Loopback delivers inside the sender's write, so all k are
				// queued by now and one read takes them.
				if len(dgs) != k {
					b.Fatalf("read %d datagrams of %d queued", len(dgs), k)
				}
			}
			b.ReportMetric(float64(took.Nanoseconds())/float64(b.N*k), "ns/datagram")
		})
	}
}
