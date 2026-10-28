//go:build unix

package utpnet

import (
	"context"
	"net"
	"testing"
	"time"
)

// The first attempt to send a UDP datagram does not wait for another write
// on the port, such as the uTP sender's or one blocked on a full send
// buffer, since the deadline could not end that wait.
func TestUDPWriteToDoesNotWaitForAnotherWrite(t *testing.T) {
	s, err := Listen(context.Background(), "udp", "127.0.0.1:0", &Options{Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.raw == nil {
		t.Skip("no direct send on this platform")
	}
	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()

	// Another write holds the descriptor's write lock until release.
	held, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = s.raw.rc.Write(func(uintptr) bool {
			close(held)
			<-release
			return true
		})
	}()
	<-held
	defer close(release)

	s.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
	done := make(chan error, 1)
	go func() {
		_, err := s.WriteTo([]byte("x"), peer.LocalAddr())
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WriteTo returned %v while another write held the port", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WriteTo waited for another write on the port, past its deadline")
	}
}
