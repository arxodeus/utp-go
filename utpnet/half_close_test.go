package utpnet

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// A half-close: one end finishes sending, both keep going.
//
// libutp has had this all along -- `utp_shutdown(s, SHUT_WR)`, with the socket
// held in CS_GOT_FIN while its application keeps reading. This library did
// not: on reaching the peer's FIN the connection tore itself down, so a peer
// that closed its sending side took the whole connection with it, along with
// whatever it had not finished sending back. That was the one place the
// reference could do something an application here could not.
//
// The shape below is the one that matters in practice, and the one that used
// to fail: a client sends a request, says it has nothing more to send, and
// then reads a reply that is larger than everything before it.
func TestCloseWriteKeepsTheReadSideOpen(t *testing.T) {
	server, client := listenPair(t)

	reply := make([]byte, 512*1024)
	if _, err := rand.Read(reply); err != nil {
		t.Fatal(err)
	}
	const request = "everything I have to say"

	served := make(chan error, 1)
	go func() {
		conn, err := server.Accept()
		if err != nil {
			served <- err
			return
		}
		defer conn.Close()

		// Read to end of stream: the peer has closed its sending side, which
		// must not close ours.
		got, err := io.ReadAll(conn)
		if err != nil {
			served <- err
			return
		}
		if string(got) != request {
			served <- errors.New("the request did not arrive intact")
			return
		}
		// And now answer, long after the peer said it was finished.
		if _, err := conn.Write(reply); err != nil {
			served <- err
			return
		}
		served <- nil
	}()

	conn := dialTo(t, client, server.Addr())
	defer conn.Close()

	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("writing the request: %v", err)
	}

	half, ok := conn.(interface{ CloseWrite() error })
	if !ok {
		t.Fatal("utpnet.Conn does not implement CloseWrite; a caller holding a net.Conn " +
			"type-asserts for exactly this interface")
	}
	if err := half.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}

	// Writing after CloseWrite is an error, as it is on a TCP connection.
	if _, err := conn.Write([]byte("more")); err == nil {
		t.Error("writing after CloseWrite succeeded; the sending side is supposed to be closed")
	}

	if err := conn.SetReadDeadline(time.Now().Add(120 * time.Second)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(reply))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("reading the reply after CloseWrite: %v; the read side did not survive the "+
			"half-close", err)
	}
	if !bytes.Equal(got, reply) {
		t.Fatal("the reply arrived but does not match what was sent")
	}

	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("the far end failed: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the far end never finished")
	}
}

// The far end must see end of stream, not an error, when its peer half-closes.
//
// A reader that cannot tell "the peer has finished sending" from "the
// connection broke" cannot use a half-close for anything: the whole point is
// that the reply is still worth sending.
func TestCloseWriteLooksLikeEndOfStreamNotFailure(t *testing.T) {
	server, client := listenPair(t)

	result := make(chan error, 1)
	go func() {
		conn, err := server.Accept()
		if err != nil {
			result <- err
			return
		}
		defer conn.Close()
		// io.ReadAll reports io.EOF as success; anything else is a failure.
		_, err = io.ReadAll(conn)
		result <- err
	}()

	conn := dialTo(t, client, server.Addr())
	defer conn.Close()
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := conn.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("the reader saw %v where it should have seen a clean end of stream", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the reader never reached end of stream after the peer half-closed")
	}
}

// dialTo opens a connection from one socket to another's address.
func dialTo(t *testing.T, from *Socket, addr net.Addr) net.Conn {
	t.Helper()
	conn, err := from.Dial("udp", addr.String())
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	return conn
}
