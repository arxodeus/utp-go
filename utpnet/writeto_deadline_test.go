package utpnet

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// blockingPacketConn is a real UDP socket whose WriteTo waits until
// released.
type blockingPacketConn struct {
	*net.UDPConn
	release chan struct{}
}

func (c *blockingPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	<-c.release
	return c.UDPConn.WriteTo(b, addr)
}

// A WriteTo that blocks ends at its deadline, and at a deadline moved while
// it waits.
func TestWriteToObservesItsDeadlineWhileBlocked(t *testing.T) {
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	pc := &blockingPacketConn{UDPConn: udp, release: make(chan struct{})}
	s, err := NewSocketFromPacketConn(context.Background(), pc, &Options{Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defer close(pc.release)
	to := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}

	var ne net.Error
	errs := make(chan error, 1)

	// The deadline passing.
	s.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
	go func() {
		_, err := s.WriteTo([]byte("first"), to)
		errs <- err
	}()
	select {
	case err := <-errs:
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("blocked WriteTo returned %v, want a timeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a blocked WriteTo outlived its 200ms deadline")
	}

	// The deadline moved, from none to soon, while the write waits.
	s.SetWriteDeadline(time.Time{})
	go func() {
		_, err := s.WriteTo([]byte("second"), to)
		errs <- err
	}()
	time.Sleep(100 * time.Millisecond)
	s.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	select {
	case err := <-errs:
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("WriteTo returned %v, want a timeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WriteTo ignored a deadline set while it was blocked")
	}
}

// blockingUDP is a UDP socket whose WriteToUDP waits until released.
type blockingUDP struct {
	udpPacketConn
	release chan struct{}
}

func (b *blockingUDP) WriteToUDP(p []byte, addr *net.UDPAddr) (int, error) {
	<-b.release
	return b.udpPacketConn.WriteToUDP(p, addr)
}

// On a UDP socket too, a blocked WriteTo ends at a deadline set after it
// began. The kernel's send buffer cannot be made to block on demand, so the
// socket's write is made to block, and the non-blocking first attempt is
// made to find it busy.
func TestUDPWriteToObservesADeadlineSetWhileBlocked(t *testing.T) {
	s, err := Listen(context.Background(), "udp", "127.0.0.1:0", &Options{Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	release := make(chan struct{})
	defer close(release)
	s.passUDP = &blockingUDP{udpPacketConn: s.passUDP, release: release}
	s.raw = nil // as if the kernel could not take it at once

	errs := make(chan error, 1)
	go func() {
		_, err := s.WriteTo([]byte("x"), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9})
		errs <- err
	}()
	time.Sleep(100 * time.Millisecond)
	s.SetWriteDeadline(time.Now())
	select {
	case err := <-errs:
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("WriteTo returned %v, want a timeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a blocked UDP WriteTo ignored a deadline set after it began")
	}
}
