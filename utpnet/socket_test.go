package utpnet

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/log"
	utp "github.com/zen-eth/utp-go"
)

func quiet() log.Logger {
	return log.NewLogger(log.NewTerminalHandlerWithLevel(os.Stderr, log.LevelCrit, false))
}

func listenPair(t *testing.T) (server, client *Socket) {
	t.Helper()
	opts := &Options{Logger: quiet()}
	server, err := Listen(context.Background(), "udp", "127.0.0.1:0", opts)
	if err != nil {
		t.Fatalf("listening server: %v", err)
	}
	t.Cleanup(func() { server.Close() })

	client, err = Listen(context.Background(), "udp", "127.0.0.1:0", opts)
	if err != nil {
		t.Fatalf("listening client: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return server, client
}

// The whole point of the package: a uTP connection that behaves like any
// other net.Conn, over a real UDP socket.
func TestDialAcceptTransfer(t *testing.T) {
	server, client := listenPair(t)

	payload := make([]byte, 512*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	received := make(chan []byte, 1)
	errs := make(chan error, 2)

	go func() {
		conn, err := server.Accept()
		if err != nil {
			errs <- err
			return
		}
		defer conn.Close()
		got, err := io.ReadAll(conn)
		if err != nil {
			errs <- err
			return
		}
		received <- got
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := client.DialContext(ctx, "udp", server.Addr().String())
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}

	go func() {
		if _, err := conn.Write(payload); err != nil {
			errs <- err
			return
		}
		conn.Close()
	}()

	select {
	case got := <-received:
		if !bytes.Equal(got, payload) {
			t.Fatalf("payload corrupted: got %d bytes, sent %d", len(got), len(payload))
		}
	case err := <-errs:
		t.Fatalf("transfer failed: %v", err)
	case <-time.After(90 * time.Second):
		t.Fatal("transfer did not complete")
	}
}

// The addresses a caller sees have to be the real ones, or a BitTorrent
// client cannot tell its peers apart.
func TestConnAddresses(t *testing.T) {
	server, client := listenPair(t)

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := server.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := client.DialContext(ctx, "udp", server.Addr().String())
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	defer conn.Close()

	if got := conn.RemoteAddr().String(); got != server.Addr().String() {
		t.Errorf("RemoteAddr is %q, want the server's address %q", got, server.Addr())
	}
	if got := conn.LocalAddr().String(); got != client.Addr().String() {
		t.Errorf("LocalAddr is %q, want the client's address %q", got, client.Addr())
	}

	select {
	case in := <-accepted:
		defer in.Close()
		if got := in.RemoteAddr().String(); got != client.Addr().String() {
			t.Errorf("accepted RemoteAddr is %q, want the dialler's address %q", got, client.Addr())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("no connection was accepted")
	}
}

// A BitTorrent client runs the DHT on the same UDP port as uTP. Datagrams
// that are not uTP have to come back out of ReadFrom rather than being
// swallowed or answered.
func TestNonUtpDatagramsPassThrough(t *testing.T) {
	sock, _ := listenPair(t)

	raw, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()

	// Shaped like a DHT query, which is what would really be sharing the port.
	msg := []byte("d1:ad2:id20:abcdefghij0123456789e1:q4:ping1:t2:aa1:y1:qe")
	if _, err := raw.WriteToUDP(msg, sock.Addr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}

	if err := sock.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	n, from, err := sock.ReadFrom(buf)
	if err != nil {
		t.Fatalf("reading the passed-through datagram: %v", err)
	}
	if !bytes.Equal(buf[:n], msg) {
		t.Errorf("passed-through datagram was altered:\n got %q\nwant %q", buf[:n], msg)
	}
	if from.String() != raw.LocalAddr().String() {
		t.Errorf("datagram came from %q, want %q", from, raw.LocalAddr())
	}

	// And the reverse direction, which is how the DHT would reply.
	reply := []byte("d1:rd2:id20:mnopqrstuvwxyz123456e1:t2:aa1:y1:re")
	if _, err := sock.WriteTo(reply, raw.LocalAddr()); err != nil {
		t.Fatalf("writing a non-uTP datagram: %v", err)
	}
	_ = raw.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, _, err = raw.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("reading the reply: %v", err)
	}
	if !bytes.Equal(buf[:n], reply) {
		t.Errorf("reply was altered:\n got %q\nwant %q", buf[:n], reply)
	}
}

// uTP traffic must not leak out of ReadFrom. If it did, a caller sharing the
// port would try to parse uTP packets as its own protocol.
func TestUtpPacketsDoNotLeakToReadFrom(t *testing.T) {
	server, client := listenPair(t)

	go func() {
		conn, err := server.Accept()
		if err == nil {
			_, _ = io.Copy(io.Discard, conn)
			conn.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := client.DialContext(ctx, "udp", server.Addr().String())
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	if _, err := conn.Write(bytes.Repeat([]byte{7}, 64*1024)); err != nil {
		t.Fatalf("writing: %v", err)
	}
	conn.Close()

	if err := server.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	n, _, err := server.ReadFrom(buf)
	if err == nil {
		t.Fatalf("ReadFrom returned %d bytes of uTP traffic: %x", n, buf[:min(n, 32)])
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("expected a timeout once the uTP traffic was filtered out, got %v", err)
	}
}

// The sniff decides what the uTP machinery ever sees, so it is tested on its
// own rather than only through a transfer.
func TestIsUtpPacket(t *testing.T) {
	valid := utp.NewPacketBuilder(0, 1234, 200000, 1048576, 42).Build().Encode()

	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"a real uTP packet", valid, true},
		{"empty", nil, false},
		{"one byte short of a header", valid[:len(valid)-1][:19], false},
		{"a DHT query", []byte("d1:ad2:id20:abcdefghij0123456789e1:q4:ping1:t2:aa1:y1:qe"), false},
		{"text", []byte("this is not a uTP packet at all, not even close"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUtpPacket(tc.data); got != tc.want {
				t.Errorf("isUtpPacket(%q) = %v, want %v", tc.data, got, tc.want)
			}
		})
	}

	// A version other than 1 is not ours, whatever else it looks like.
	t.Run("wrong version", func(t *testing.T) {
		wrong := append([]byte(nil), valid...)
		wrong[0] = (wrong[0] & 0xF0) | 2
		if isUtpPacket(wrong) {
			t.Error("a version-2 header was taken for uTP")
		}
	})
}

// Deadlines have to work the way net.Conn's do, including being movable from
// another goroutine while a call is blocked. Callers rely on that to unblock
// readers.
func TestReadDeadline(t *testing.T) {
	server, client := listenPair(t)

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := server.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := client.DialContext(ctx, "udp", server.Addr().String())
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	defer conn.Close()

	var in net.Conn
	select {
	case in = <-accepted:
		defer in.Close()
	case <-time.After(30 * time.Second):
		t.Fatal("no connection was accepted")
	}

	// Nothing has been sent, so this must time out rather than hang.
	if err := in.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	buf := make([]byte, 64)
	_, err = in.Read(buf)
	elapsed := time.Since(start)

	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("Read returned %v, want a net.Error with Timeout() true -- callers switch on that", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Read took %v to hit a 300ms deadline", elapsed)
	}

	// A deadline set from another goroutine unblocks a Read already in
	// progress: the standard library behaves this way and callers use it.
	if err := in.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	unblocked := make(chan error, 1)
	go func() {
		defer wg.Done()
		_, err := in.Read(buf)
		unblocked <- err
	}()
	time.Sleep(200 * time.Millisecond)
	if err := in.SetReadDeadline(time.Now()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-unblocked:
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Errorf("the blocked Read returned %v, want a timeout", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("setting a deadline did not unblock a Read already in progress")
	}
	wg.Wait()
}

// Close has to be safe to call twice and from several goroutines: net.Conn
// callers do both.
func TestCloseIsIdempotent(t *testing.T) {
	server, _ := listenPair(t)
	if err := server.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, err := server.Accept(); err == nil {
		t.Error("Accept succeeded on a closed socket")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Many simultaneous connections on one port, each exchanging data in both
// directions: the traffic shape a BitTorrent client produces.
//
// This is what found the send buffer retaining the caller's slice. A
// unidirectional transfer never touches its buffer again after writing, so
// every test in this repository passed while any echo loop corrupted its own
// data.
func TestConcurrentFullDuplexConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("not a -short test")
	}

	const peers = 16
	const messageSize = 64 * 1024

	server, client := listenPair(t)

	go func() {
		for i := 0; i < peers; i++ {
			conn, err := server.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, io.LimitReader(c, messageSize))
			}(conn)
		}
	}()

	payload := make([]byte, messageSize)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, peers)
	for i := 0; i < peers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			conn, err := client.DialContext(ctx, "udp", server.Addr().String())
			if err != nil {
				errs <- fmt.Errorf("peer %d dial: %w", n, err)
				return
			}
			defer conn.Close()

			written := make(chan error, 1)
			go func() {
				_, err := conn.Write(payload)
				written <- err
			}()

			got := make([]byte, messageSize)
			if _, err := io.ReadFull(conn, got); err != nil {
				errs <- fmt.Errorf("peer %d read back: %w", n, err)
				return
			}
			if err := <-written; err != nil {
				errs <- fmt.Errorf("peer %d write: %w", n, err)
				return
			}
			if !bytes.Equal(got, payload) {
				for j := range got {
					if got[j] != payload[j] {
						errs <- fmt.Errorf("peer %d: echoed data differs at byte %d of %d", n, j, messageSize)
						return
					}
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
