//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package utp

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// batchPair is a udpConn and a socket sending to it.
func batchPair(t *testing.T, network string, ip net.IP) (*udpConn, *net.UDPConn) {
	t.Helper()
	base, err := net.ListenUDP(network, &net.UDPAddr{IP: ip})
	if err != nil {
		t.Skipf("no %s loopback: %v", network, err)
	}
	_ = base.SetReadBuffer(8 << 20)
	c := &udpConn{base: base}
	t.Cleanup(func() { _ = c.Close() })
	src, err := net.DialUDP(network, nil, base.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return c, src
}

// readQueued reads the k datagrams just sent to c, copied out of its arena.
// Linux delivers on loopback before the send returns, so one batch takes
// all k. Darwin may still be delivering, so there they are read until all
// have come, still in order and each whole.
func readQueued(t *testing.T, c *udpConn, k int) []datagram {
	t.Helper()
	var dgs []datagram
	deadline := time.Now().Add(2 * time.Second)
	for len(dgs) < k && time.Now().Before(deadline) {
		got, err := c.readBatch()
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range got {
			d.payload = append([]byte(nil), d.payload...)
			dgs = append(dgs, d)
		}
		if runtime.GOOS == "linux" {
			break
		}
	}
	return dgs
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
			dgs := readQueued(t, c, k)
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

// A datagram as large as loopback carries arrives whole: MaxPacketSize may
// be set that high. On Darwin the sender's limit (net.inet.udp.maxdgram) is
// raised first, and the size is the largest of a few the platform accepts.
func TestReadBatchTakesLargeDatagramWhole(t *testing.T) {
	c, src := batchPair(t, "udp4", net.IPv4(127, 0, 0, 1))
	_ = src.SetWriteBuffer(1 << 20)
	var big []byte
	for _, size := range []int{60000, 32768, 16384} {
		b := bytes.Repeat([]byte{0xab}, size)
		if _, err := src.Write(b); err == nil {
			big = b
			break
		} else if !errors.Is(err, syscall.EMSGSIZE) {
			t.Fatal(err)
		}
	}
	if big == nil {
		t.Skip("this platform sends no UDP datagram of 16 KB or more on loopback")
	}
	t.Logf("%d-byte datagrams", len(big))
	small := []byte("after")
	for _, b := range [][]byte{small, big} {
		if _, err := src.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	dgs := readQueued(t, c, 3)
	if len(dgs) != 3 || !bytes.Equal(dgs[0].payload, big) || !bytes.Equal(dgs[1].payload, small) ||
		!bytes.Equal(dgs[2].payload, big) {
		var lens []int
		for _, d := range dgs {
			lens = append(lens, len(d.payload))
		}
		t.Fatalf("read datagrams of %v bytes, want [%d 5 %d]", lens, len(big), len(big))
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
			if len(dgs) != 1 || dgs[0].peer.Hash() != NewUDPPeer(from.(*net.UDPAddr)).Hash() {
				t.Fatalf("batch read the peer as %v, ReadFrom as %s", dgs, from)
			}
		})
	}
}
