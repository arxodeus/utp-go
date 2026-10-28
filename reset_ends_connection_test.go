package utp

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A RESET ends the connection whatever acknowledgement number it carries,
// and a blocked reader is told.
//
// A peer's socket answers a packet for a connection it no longer has with a
// RESET acknowledging that packet's sequence number. If the packet was one
// of our acknowledgements, that is our next, unsent number, which must not
// cause the RESET to be dropped as acknowledging a packet never sent. Here
// the RESET answers our SYN-ACK; the reader must return at once.
func TestResetAnsweringOurAckEndsTheConnection(t *testing.T) {
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		conn := newScriptedConn()
		sock := NewSocket(ctx, conn, conformanceLogger())
		peerID := uint16(6000 + 2*i)
		cid := NewConnectionID(conn.peer, peerID+1, peerID)
		accepted := make(chan *Stream, 1)
		go func() {
			s, _ := sock.AcceptWithCID(ctx, cid, NewConnectionConfig())
			accepted <- s
		}()
		time.Sleep(time.Millisecond)
		conn.inject(newPacketBuilder(stSyn, peerID, uint32(time.Now().UnixMicro()), 1<<20, 900).Build().Encode())
		s := <-accepted
		if s == nil {
			t.Fatal("no accept")
		}
		var ourSeq uint16
		for ourSeq == 0 {
			for _, raw := range conn.takeEmitted() {
				if p, err := decodePacket(raw, false); err == nil && p.Header.PacketType == stState {
					ourSeq = p.Header.SeqNum
				}
			}
			time.Sleep(time.Millisecond)
		}
		conn.inject(newPacketBuilder(stData, peerID+1, uint32(time.Now().UnixMicro()), 1<<20, 901).
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
		conn.inject(newPacketBuilder(stReset, peerID+1, 0, 0, 902).WithAckNum(ourSeq).Build().Encode())
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
