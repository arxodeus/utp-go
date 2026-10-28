package utp

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"
)

// TestTheSecondFinIsAcknowledgedPromptly closes connections as a transfer
// does, the sender writing and closing and the receiver reading to the end
// and closing after, and requires the receiver's connection, which closed
// second, to end within a fraction of a retransmission timeout. Its FIN may
// reach a sender that has closed, is tearing down, or is gone, and must be
// acknowledged in each case.
func TestTheSecondFinIsAcknowledgedPromptly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	loopback := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
	server, err := Bind(ctx, "udp", loopback, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := Bind(ctx, "udp", loopback, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	sa := server.LocalAddr().(*net.UDPAddr)
	ca := client.LocalAddr().(*net.UDPAddr)
	payload := bytes.Repeat([]byte{1}, 64*1024)
	slow := 0
	const cycles = 60
	for i := 0; i < cycles; i++ {
		recv := uint16(2000 + i*2)
		done := make(chan *Stream, 1)
		go func() {
			s, err := server.AcceptWithCID(ctx, NewConnectionID(NewUDPPeer(ca), recv+1, recv), nil)
			if err != nil {
				done <- nil
				return
			}
			var b []byte
			_, _ = s.ReadToEOF(ctx, &b)
			s.Close()
			done <- s
		}()
		cs, err := client.ConnectWithCID(ctx, NewConnectionID(NewUDPPeer(sa), recv, recv+1), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cs.Write(ctx, payload); err != nil {
			t.Fatal(err)
		}
		cs.Close()
		ss := <-done
		if ss == nil {
			t.Fatal("accept failed")
		}
		select {
		case <-ss.Done():
		case <-time.After(300 * time.Millisecond):
			slow++
			select {
			case <-ss.Done():
			case <-time.After(10 * time.Second):
				t.Fatalf("cycle %d: the receiving connection never ended: its FIN was never acknowledged", i)
			}
		}
	}
	if slow > 0 {
		t.Fatalf("%d of %d receiving connections were still open 300 ms after closing: their FIN went unacknowledged until retransmitted", slow, cycles)
	}
}
