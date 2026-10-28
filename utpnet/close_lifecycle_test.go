package utpnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// Closing a connection and then its socket right after a Write delivers
// everything the Write accepted: the socket keeps reading acknowledgements
// while the connection delivers.
func TestSocketCloseAfterWriteDeliversEverything(t *testing.T) {
	server, client := listenPair(t)

	payload := make([]byte, 4<<20)
	for i := range payload {
		payload[i] = byte(i * 31)
	}
	received := make(chan []byte, 1)
	go func() {
		c, err := server.Accept()
		if err != nil {
			t.Error(err)
			received <- nil
			return
		}
		got, err := io.ReadAll(c)
		if err != nil {
			t.Error(err)
		}
		received <- got
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := client.DialContext(ctx, "udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	client.Close()

	select {
	case got := <-received:
		if !bytes.Equal(got, payload) {
			t.Fatalf("peer received %d of %d bytes", len(got), len(payload))
		}
	case <-time.After(60 * time.Second):
		t.Fatal("transfer never finished")
	}
}

// failingPacketConn is a real UDP socket whose reads fail with err once told
// to.
type failingPacketConn struct {
	*net.UDPConn
	fail chan struct{}
	err  error
}

func (c *failingPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	select {
	case <-c.fail:
		return 0, nil, c.err
	default:
	}
	// Short reads, so the failure is seen soon after it is asked for.
	c.UDPConn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	n, addr, err := c.UDPConn.ReadFrom(b)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		select {
		case <-c.fail:
			return 0, nil, c.err
		default:
		}
	}
	return n, addr, err
}

// A persistent read error closes the socket: Accept and ReadFrom, already
// waiting, return at once with it.
func TestTerminalReadErrorClosesTheSocket(t *testing.T) {
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	pc := &failingPacketConn{UDPConn: udp, fail: make(chan struct{}), err: errors.New("interface gone")}
	s, err := NewSocketFromPacketConn(context.Background(), pc, &Options{Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	errs := make(chan error, 2)
	go func() {
		_, err := s.Accept()
		errs <- err
	}()
	go func() {
		_, _, err := s.ReadFrom(make([]byte, 64))
		errs <- err
	}()
	time.Sleep(100 * time.Millisecond)
	close(pc.fail)
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			if !errors.Is(err, ErrSocketClosed) || !errors.Is(err, pc.err) {
				t.Fatalf("got %v, want ErrSocketClosed wrapping %v", err, pc.err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a call waiting on the socket outlived its read error")
		}
	}
}
