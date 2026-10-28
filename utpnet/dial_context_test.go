package utpnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// Canceling the dial context does not close the connection the dial
// returned, as net.Dialer documents: "Canceling ctx does not affect the
// connection after it is established". Callers commonly write
//
//	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
//	defer cancel()
//	conn, err := dialer.DialContext(ctx, ...)
//
// and the cancel runs as soon as the dialing function returns.
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
	// What defer cancel() does, when it does it: the dial is over, the
	// connection is not.
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
