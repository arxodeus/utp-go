package utpnet

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// The other half-close: this end has finished reading, the peer has not
// finished sending.
//
// libutp's utp_shutdown(s, SHUT_RD) sets read_shutdown, and what that flag
// does is narrower than it looks: `conn->ack_nr++` runs whether or not the
// bytes are delivered (utp_internal.cpp:2344-2355, and again for the reorder
// buffer at :2392-2395). Incoming data is acknowledged and dropped, not
// refused.
//
// The distinction is the whole test. The obvious implementation -- stop
// delivering and let the receive buffer fill -- closes the advertised window,
// and a peer that cannot finish sending cannot finish closing either. So this
// sends far more than the receive buffer holds and requires it to go through.
func TestCloseReadKeepsThePeerSending(t *testing.T) {
	server, client := listenPair(t)

	// Four times the default receive buffer. A connection that merely stopped
	// delivering would stall a quarter of the way in.
	const total = 4 * 1024 * 1024

	served := make(chan error, 1)
	go func() {
		conn, err := server.Accept()
		if err != nil {
			served <- err
			return
		}
		defer conn.Close()

		if err := conn.SetWriteDeadline(time.Now().Add(60 * time.Second)); err != nil {
			served <- err
			return
		}
		payload := make([]byte, 64*1024)
		for sent := 0; sent < total; sent += len(payload) {
			if _, err := conn.Write(payload); err != nil {
				served <- err
				return
			}
		}

		// And the peer's own data still arrives, in the other direction.
		if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
			served <- err
			return
		}
		buf := make([]byte, len("from the reader that stopped reading"))
		if _, err := io.ReadFull(conn, buf); err != nil {
			served <- err
			return
		}
		if string(buf) != "from the reader that stopped reading" {
			served <- errors.New("the reply did not arrive intact")
			return
		}
		served <- nil
	}()

	conn, err := client.Dial("utp", server.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	cr, ok := conn.(interface{ CloseRead() error })
	if !ok {
		t.Fatalf("Dial returned %T, which does not offer CloseRead; callers type-assert for it", conn)
	}
	if err := cr.CloseRead(); err != nil {
		t.Fatalf("CloseRead: %v", err)
	}

	// Reading is over, and says so rather than blocking or reporting the end
	// of a stream that has not ended.
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n, err := conn.Read(make([]byte, 1024))
	if !errors.Is(err, utp.ErrReadClosed) {
		t.Errorf("Read returned (%d, %v), want (0, %v)", n, err, utp.ErrReadClosed)
	}

	// Sending is untouched.
	if err := conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("from the reader that stopped reading")); err != nil {
		t.Fatalf("write after CloseRead: %v", err)
	}

	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("the peer could not finish: %v", err)
		}
	case <-time.After(90 * time.Second):
		t.Fatalf("the peer never finished sending %d bytes; a shut-down read side "+
			"must keep acknowledging, or its window closes and the peer stalls", total)
	}
}

// CloseRead does not end the connection, and Close after it still does.
func TestCloseReadIsNotAClose(t *testing.T) {
	server, client := listenPair(t)

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := server.Accept()
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- conn
	}()

	conn, err := client.Dial("utp", server.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	srvConn := <-accepted
	if srvConn == nil {
		t.Fatal("accept failed")
	}
	defer srvConn.Close()

	if err := conn.(interface{ CloseRead() error }).CloseRead(); err != nil {
		t.Fatalf("CloseRead: %v", err)
	}
	// Twice is not an error: net.TCPConn tolerates it and so must this.
	if err := conn.(interface{ CloseRead() error }).CloseRead(); err != nil {
		t.Fatalf("second CloseRead: %v", err)
	}

	if err := conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("alive")); err != nil {
		t.Fatalf("the connection did not survive CloseRead: %v", err)
	}
	if err := srvConn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(srvConn, buf); err != nil {
		t.Fatalf("read after the peer's CloseRead: %v", err)
	}

	// Now really close, and the peer reaches end of stream.
	if err := conn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := srvConn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(srvConn); err != nil {
		t.Errorf("reading to end of stream after the peer closed: %v", err)
	}
}
