package utp_go

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A RESET ends the connection whatever acknowledgement number it carries, and
// a reader blocked on it is told.
//
// A peer's socket answers a packet for a connection it no longer has with a
// RESET acknowledging that packet's sequence number. When the packet was one
// of our acknowledgements or keep-alives, that is our next, unsent number,
// which the acknowledgement check took for acknowledging a packet never sent:
// the RESET was dropped, and the connection, its peer gone, ran on with its
// reader waiting. libutp acts on a RESET before checking anything else
// (utp_internal.cpp:2850-2873). The long soak left server connections
// running this way for clients that had given up. Here the RESET answers our
// SYN-ACK, a STATE; the reader must return at once.
func TestResetAnsweringOurAckEndsTheConnection(t *testing.T) {
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		conn := newScriptedConn()
		sock := WithSocket(ctx, conn, conformanceLogger())
		peerID := uint16(6000 + 2*i)
		cid := NewConnectionId(conn.peer, peerID+1, peerID)
		accepted := make(chan *UtpStream, 1)
		go func() {
			s, _ := sock.AcceptWithCid(ctx, cid, NewConnectionConfig())
			accepted <- s
		}()
		time.Sleep(time.Millisecond)
		conn.inject(NewPacketBuilder(st_syn, peerID, uint32(time.Now().UnixMicro()), 1<<20, 900).Build().Encode())
		s := <-accepted
		if s == nil {
			t.Fatal("no accept")
		}
		var ourSeq uint16
		for ourSeq == 0 {
			for _, raw := range conn.takeEmitted() {
				if p, err := DecodePacket(raw); err == nil && p.Header.PacketType == st_state {
					ourSeq = p.Header.SeqNum
				}
			}
			time.Sleep(time.Millisecond)
		}
		conn.inject(NewPacketBuilder(st_data, peerID+1, uint32(time.Now().UnixMicro()), 1<<20, 901).
			WithAckNum(ourSeq - 1).WithPayload([]byte("hi")).Build().Encode())
		got := make(chan error, 1)
		go func() {
			buf := make([]byte, 16)
			if _, err := s.Read(ctx, buf); err != nil {
				got <- err
				return
			}
			_, err := s.Read(ctx, buf)
			got <- err
		}()
		time.Sleep(time.Duration(i%7) * time.Millisecond)
		conn.inject(NewPacketBuilder(st_reset, peerID+1, 0, 0, 902).WithAckNum(ourSeq).Build().Encode())
		select {
		case err := <-got:
			if !errors.Is(err, ErrReset) {
				t.Fatalf("iteration %d: read returned %v after the peer's RESET, expected ErrReset", i, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("iteration %d: a reader blocked on a connection the peer reset was not woken; state %v", i, s.conn.state.stateType)
		}
		sock.Close()
		cancel()
	}
}
