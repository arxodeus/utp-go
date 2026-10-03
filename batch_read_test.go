//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package utp_go

import (
	"bytes"
	"fmt"
	"net"
	"testing"
)

// batchPair is a UdpConn and a socket sending to it.
func batchPair(t *testing.T, network string, ip net.IP) (*UdpConn, *net.UDPConn) {
	t.Helper()
	base, err := net.ListenUDP(network, &net.UDPAddr{IP: ip})
	if err != nil {
		t.Skipf("no %s loopback: %v", network, err)
	}
	_ = base.SetReadBuffer(8 << 20)
	c := &UdpConn{base: base}
	t.Cleanup(func() { _ = c.Close() })
	src, err := net.DialUDP(network, nil, base.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return c, src
}

// readBatch hands over every datagram already queued, in order, each whole,
// however many system calls that takes.
func TestReadBatchTakesEverythingQueued(t *testing.T) {
	for _, k := range []int{1, 15, 16, 17, 100} {
		t.Run(fmt.Sprintf("queued=%d", k), func(t *testing.T) {
			c, src := batchPair(t, "udp4", net.IPv4(127, 0, 0, 1))
			want := make([][]byte, k)
			for i := range want {
				want[i] = bytes.Repeat([]byte{byte(i)}, 100+i)
				if _, err := src.Write(want[i]); err != nil {
					t.Fatal(err)
				}
			}
			dgs, err := c.readBatch()
			if err != nil {
				t.Fatal(err)
			}
			if len(dgs) != k {
				t.Fatalf("read %d datagrams of %d queued", len(dgs), k)
			}
			for i, d := range dgs {
				if !bytes.Equal(d.payload, want[i]) {
					t.Fatalf("datagram %d: %d bytes, want %d bytes of %d", i, len(d.payload), len(want[i]), i)
				}
				if got, want := d.peer.Hash(), src.LocalAddr().String(); got != want {
					t.Fatalf("datagram %d came from %s, want %s", i, got, want)
				}
			}
		})
	}
}

// A datagram as large as loopback carries arrives whole: MaxPacketSize may be
// set that high, and a truncated packet would be lost.
func TestReadBatchTakesLargeDatagramWhole(t *testing.T) {
	c, src := batchPair(t, "udp4", net.IPv4(127, 0, 0, 1))
	big := bytes.Repeat([]byte{0xab, 0xcd}, 30000)
	small := []byte("after")
	for _, b := range [][]byte{big, small, big} {
		if _, err := src.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	dgs, err := c.readBatch()
	if err != nil {
		t.Fatal(err)
	}
	if len(dgs) != 3 || !bytes.Equal(dgs[0].payload, big) || !bytes.Equal(dgs[1].payload, small) ||
		!bytes.Equal(dgs[2].payload, big) {
		var lens []int
		for _, d := range dgs {
			lens = append(lens, len(d.payload))
		}
		t.Fatalf("read datagrams of %v bytes, want [60000 5 60000]", lens)
	}
}

// A peer read in a batch is the one net.UDPConn.ReadFrom names: connections
// are keyed by the peer's text, so any difference would misroute a packet.
func TestReadBatchPeerMatchesReadFrom(t *testing.T) {
	for _, tc := range []struct {
		network string
		ip      net.IP
	}{{"udp4", net.IPv4(127, 0, 0, 1)}, {"udp6", net.IPv6loopback}} {
		t.Run(tc.network, func(t *testing.T) {
			c, src := batchPair(t, tc.network, tc.ip)
			if _, err := src.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
			dgs, err := c.readBatch()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := src.Write([]byte("y")); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 16)
			_, from, err := c.base.ReadFrom(buf)
			if err != nil {
				t.Fatal(err)
			}
			if len(dgs) != 1 || dgs[0].peer.Hash() != NewUdpPeer(from.(*net.UDPAddr)).Hash() {
				t.Fatalf("batch read the peer as %v, ReadFrom as %s", dgs, from)
			}
		})
	}
}
