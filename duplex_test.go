package utp_go

import (
	"bytes"
	"context"
	"crypto/rand"
	"net"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/log"
)

func duplexQuietLog() log.Logger {
	return log.NewLogger(log.NewTerminalHandlerWithLevel(os.Stderr, log.LevelCrit, false))
}

// Reading and writing on the same stream at the same time, which is what
// BitTorrent does on every peer connection and what nothing in this
// repository tested until the anacrolix integration needed it.
//
// It failed: the send buffer retained the caller's slice, so an echo loop --
// read into a buffer, write it back, reuse the buffer -- transmitted whatever
// the buffer held by the time the connection got round to sending. Every
// unidirectional test passed throughout, because each writes one buffer once.
func TestFullDuplexEcho(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	lg := duplexQuietLog()
	sa, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	sb, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close()

	aAddr := sa.LocalAddr().(*net.UDPAddr)
	bAddr := sb.LocalAddr().(*net.UDPAddr)
	t.Logf("a=%v b=%v", aAddr, bAddr)

	const size = 16 * 1024
	payload := make([]byte, size)
	rand.Read(payload)

	cidA := NewConnectionId(NewUdpPeer(bAddr), 1000, 1001)
	cidB := NewConnectionId(NewUdpPeer(aAddr), 1001, 1000)

	srvDone := make(chan error, 1)
	go func() {
		stream, err := sb.AcceptWithCid(ctx, cidB, NewConnectionConfig())
		if err != nil {
			srvDone <- err
			return
		}
		// Echo: read a chunk, write it back.
		total := 0
		buf := make([]byte, 4096)
		for total < size {
			n, err := stream.Read(ctx, buf)
			if err != nil {
				srvDone <- err
				return
			}
			if _, err := stream.Write(ctx, buf[:n]); err != nil {
				srvDone <- err
				return
			}
			total += n
		}
		srvDone <- nil
	}()

	time.Sleep(100 * time.Millisecond)
	stream, err := sa.ConnectWithCid(ctx, cidA, NewConnectionConfig())
	if err != nil {
		t.Fatal(err)
	}

	go func() { stream.Write(ctx, payload) }()

	got := make([]byte, 0, size)
	buf := make([]byte, 4096)
	for len(got) < size {
		n, err := stream.Read(ctx, buf)
		if err != nil {
			t.Fatalf("read after %d bytes: %v", len(got), err)
		}
		got = append(got, buf[:n]...)
	}
	if !bytes.Equal(got, payload) {
		for i := range got {
			if got[i] != payload[i] {
				t.Fatalf("echo differs at byte %d of %d", i, size)
			}
		}
	}
	if err := <-srvDone; err != nil {
		t.Errorf("server: %v", err)
	}
}
