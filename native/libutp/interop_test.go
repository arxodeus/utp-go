//go:build cgo

package libutp_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/log"
	utp "github.com/zen-eth/utp-go"
	"github.com/zen-eth/utp-go/native/libutp"
)

// The M7 interoperability gate.
//
// Everything else in this repository is conformance by citation: code read
// against utp_internal.cpp and matched by hand. These tests are the only
// evidence that a real libutp peer -- the thing every BitTorrent client in the
// wild actually runs -- will complete a transfer with this implementation.
//
// Both directions matter. A bug in connection-id allocation, for instance,
// can leave one role working and the other unroutable.

func quietLogger() log.Logger {
	return log.NewLogger(log.NewTerminalHandlerWithLevel(os.Stderr, log.LevelCrit, false))
}

func makePayload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + i/251)
	}
	return b
}

func loopback(port uint16) *net.UDPAddr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(port)}
}

// bindGoSocket binds one of our sockets to an arbitrary free port and reports
// the port it got.
func bindGoSocket(t *testing.T, ctx context.Context) (*utp.UtpSocket, uint16) {
	t.Helper()
	// Find a free port by binding a throwaway UDP socket.
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(probe.LocalAddr().(*net.UDPAddr).Port)
	_ = probe.Close()

	sock, err := utp.Bind(ctx, "udp4", loopback(port), quietLogger())
	if err != nil {
		t.Fatalf("binding our socket to :%d: %v", port, err)
	}
	return sock, port
}

const interopPayloadSize = 512 * 1024

// --- our implementation as the initiator ------------------------------------

func TestInteropGoInitiatorLibutpResponder(t *testing.T) {
	peer, err := libutp.NewPeer(0)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Shutdown()
	peer.Listen()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	sock, goPort := bindGoSocket(t, ctx)
	defer sock.Close()

	t.Logf("go implementation on :%d (initiator), libutp on :%d (responder)", goPort, peer.Port())

	// Our initiator sends the SYN carrying cid.Recv; libutp derives its own
	// ids from it. Getting this relationship wrong is what makes an
	// implementation unroutable to half its peers, so it is the first thing
	// this test exercises.
	const initiatorCid, responderCid = 4000, 4001
	cid := utp.NewConnectionId(utp.NewUdpPeer(loopback(peer.Port())), initiatorCid, responderCid)

	payload := makePayload(interopPayloadSize)

	start := time.Now()
	stream, err := sock.ConnectWithCid(ctx, cid, utp.NewConnectionConfig())
	if err != nil {
		t.Fatalf("our implementation could not connect to libutp: %v", err)
	}

	if _, err := peer.WaitState(30*time.Second, libutp.StateConnected); err != nil {
		t.Fatalf("libutp never accepted our connection: %v", err)
	}
	t.Logf("handshake completed in %v", time.Since(start).Round(time.Millisecond))

	written, err := stream.Write(ctx, payload)
	if err != nil {
		t.Fatalf("write to libutp: %v", err)
	}
	if written != len(payload) {
		t.Fatalf("wrote %d of %d bytes", written, len(payload))
	}

	got, err := peer.ReadFull(len(payload), 90*time.Second)
	if err != nil {
		t.Fatalf("libutp read %d of %d bytes: %v", len(got), len(payload), err)
	}
	elapsed := time.Since(start)

	if !bytes.Equal(got, payload) {
		t.Fatalf("libutp received %d bytes but they do not match what we sent", len(got))
	}
	t.Logf("go -> libutp: %d bytes verified in %v (%.1f Mbps)",
		len(got), elapsed.Round(time.Millisecond),
		float64(len(got))*8/elapsed.Seconds()/1e6)

	closeStart := time.Now()
	stream.Close()
	t.Logf("stream.Close() took %v", time.Since(closeStart).Round(time.Millisecond))
}

// --- our implementation as the responder ------------------------------------

func TestInteropLibutpInitiatorGoResponder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	sock, goPort := bindGoSocket(t, ctx)
	defer sock.Close()

	peer, err := libutp.NewPeer(0)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Shutdown()

	t.Logf("libutp on :%d (initiator), go implementation on :%d (responder)", peer.Port(), goPort)

	payload := makePayload(interopPayloadSize)

	// libutp chooses its own random connection id, so the responder cannot
	// know it in advance: this is the path that accepts whatever SYN arrives.
	type acceptResult struct {
		data []byte
		err  error
	}
	results := make(chan acceptResult, 1)
	go func() {
		stream, err := sock.Accept(ctx, utp.NewConnectionConfig())
		if err != nil {
			results <- acceptResult{err: err}
			return
		}
		defer stream.Close()
		buf := make([]byte, 0, len(payload))
		n, err := stream.ReadToEOF(ctx, &buf)
		if err != nil && err != io.EOF {
			results <- acceptResult{err: err}
			return
		}
		results <- acceptResult{data: buf[:min(n, len(buf))]}
	}()

	// Give the accept a moment to register before the SYN arrives.
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	if err := peer.Connect(goPort); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.WaitState(30*time.Second, libutp.StateConnected); err != nil {
		t.Fatalf("libutp could not connect to our implementation: %v", err)
	}
	t.Logf("handshake completed in %v", time.Since(start).Round(time.Millisecond))

	if _, err := peer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := peer.WaitDrained(60 * time.Second); err != nil {
		t.Fatalf("libutp could not hand off its payload: %v", err)
	}
	peer.CloseStream()

	select {
	case res := <-results:
		if res.err != nil {
			t.Fatalf("our implementation failed to receive from libutp: %v", res.err)
		}
		elapsed := time.Since(start)
		if len(res.data) != len(payload) {
			t.Fatalf("received %d of %d bytes from libutp", len(res.data), len(payload))
		}
		if !bytes.Equal(res.data, payload) {
			t.Fatal("received the right number of bytes from libutp, but they do not match")
		}
		t.Logf("libutp -> go: %d bytes verified in %v (%.1f Mbps)",
			len(res.data), elapsed.Round(time.Millisecond),
			float64(len(res.data))*8/elapsed.Seconds()/1e6)
	case <-time.After(90 * time.Second):
		t.Fatal("our implementation never finished reading from libutp")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
