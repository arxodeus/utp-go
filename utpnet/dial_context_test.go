package utpnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// Cancelling the dial context must not close the connection the dial produced.
//
// This is what net.Dialer promises -- "Canceling ctx does not affect the
// connection after it is established" (net.Dialer.DialContext) -- and every Go
// caller is written against that promise. The standard shape is
//
//	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
//	defer cancel()
//	conn, err := dialer.DialContext(ctx, ...)
//
// where the cancel fires as soon as the dialling function returns, with the
// connection still in the caller's hands.
//
// This library gave the connection that context as its lifetime, so the
// connection died the moment the dial returned. It was invisible to every test
// here because they all dial with a context they keep alive for the duration.
// anacrolix/torrent does not: its outgoing connections cancel their dial
// context on the way out of establishOutgoingConn. What that produced was a
// connection that completed the whole BitTorrent handshake and then stopped
// mid-stream -- the peer read 796 bytes of a 1007-byte exchange and saw EOF,
// and the torrent never moved a piece.
func TestDialContextCancelDoesNotCloseTheConnection(t *testing.T) {
	server, client := listenPair(t)

	accepted := make(chan struct {
		data []byte
		err  error
	}, 1)
	go func() {
		conn, err := server.Accept()
		if err != nil {
			accepted <- struct {
				data []byte
				err  error
			}{err: err}
			return
		}
		defer conn.Close()
		// Answer, so the exchange is full duplex the way a handshake is.
		if _, err := conn.Write([]byte("server hello")); err != nil {
			accepted <- struct {
				data []byte
				err  error
			}{err: err}
			return
		}
		got, err := io.ReadAll(conn)
		accepted <- struct {
			data []byte
			err  error
		}{data: got, err: err}
	}()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 30*time.Second)
	conn, err := client.DialContext(dialCtx, "udp", server.Addr().String())
	if err != nil {
		cancelDial()
		t.Fatalf("dialling: %v", err)
	}
	// Exactly what a `defer cancel()` does, at exactly the moment it does it:
	// the dial is over, the connection is not.
	cancelDial()
	defer conn.Close()

	// Give the cancellation every chance to take the connection with it.
	time.Sleep(200 * time.Millisecond)

	greeting := make([]byte, len("server hello"))
	if err := conn.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, greeting); err != nil {
		t.Fatalf("reading after the dial context was cancelled: %v; the connection did not "+
			"survive its own dial", err)
	}
	if string(greeting) != "server hello" {
		t.Fatalf("read %q from the server, want %q", greeting, "server hello")
	}

	payload := bytes.Repeat([]byte("still here. "), 4096)
	if err := conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("writing after the dial context was cancelled: %v", err)
	}
	conn.Close()

	select {
	case res := <-accepted:
		if res.err != nil && !errors.Is(res.err, io.EOF) {
			t.Fatalf("the server's read failed: %v", res.err)
		}
		if !bytes.Equal(res.data, payload) {
			t.Fatalf("the server received %d bytes, sent %d; a connection that outlived its "+
				"dial context still lost data", len(res.data), len(payload))
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the server never finished reading")
	}
}
