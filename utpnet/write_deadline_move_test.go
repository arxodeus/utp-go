package utpnet

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	utp "github.com/arxodeus/utp"
)

// Moving the write deadline while a Write waits for room neither loses nor
// repeats bytes. Write started over from the beginning whenever the stream
// came back with nothing reported, and the stream reported nothing even
// for bytes it had taken, so the peer got the start of the payload twice.
// From the release review of 9c44c4f (R1).
func TestMovingTheWriteDeadlineDoesNotRepeatBytes(t *testing.T) {
	cfg := utp.NewConnectionConfig()
	cfg.BufferSize = 64 * 1024
	opts := &Options{Logger: quiet(), ConnectionConfig: cfg}
	server, err := Listen(context.Background(), "udp", "127.0.0.1:0", opts)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := Listen(context.Background(), "udp", "127.0.0.1:0", opts)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := server.Accept()
		if err != nil {
			t.Error(err)
		}
		accepted <- c
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := client.DialContext(ctx, "udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	peer := <-accepted
	if peer == nil {
		t.FailNow()
	}

	payload := make([]byte, 1<<20)
	for i := range payload {
		payload[i] = byte(i * 13)
	}
	conn.SetWriteDeadline(time.Now().Add(time.Minute))
	wrote := make(chan error, 1)
	go func() {
		n, err := conn.Write(payload)
		if err == nil && n != len(payload) {
			err = io.ErrShortWrite
		}
		wrote <- err
		conn.Close()
	}()
	// The peer is not reading, so the write is waiting; move its deadline
	// a few times, then let the peer read.
	for i := 0; i < 3; i++ {
		time.Sleep(200 * time.Millisecond)
		conn.SetWriteDeadline(time.Now().Add(time.Minute))
	}
	got, err := io.ReadAll(peer)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-wrote; err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("peer received %d bytes for a %d-byte write", len(got), len(payload))
	}
}
