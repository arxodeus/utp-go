package utp_go

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The dead-connection rules -- a write to a connection that has ended fails at
// once with the reason, and a read reports the reason rather than a clean end
// -- were tested only for a connection the idle timeout had closed. These are
// the other two ways a connection ends without its application: the peer
// resets it, and the sender gives up after consecutive retransmission
// timeouts (utp_internal.cpp:1191).

// deadAccepted returns an accepted stream that has read one packet of data,
// after kill has ended its connection.
func deadAccepted(t *testing.T, kill func(conn *scriptedConn, clk *virtualClock, stream *UtpStream)) (*UtpStream, func()) {
	t.Helper()
	const (
		ourSeq  = 0x4321
		peerID  = 6000
		peerSeq = 900
	)
	clk := newVirtualClock(time.Unix(0, 0).Add(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	restore := pinRandom(ourSeq)

	conn := newScriptedConn()
	sock := WithSocket(ctx, conn, conformanceLogger(), WithClock(clk))

	cfg := NewConnectionConfig()
	cfg.Clock = clk
	cfg.NowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }

	cid := NewConnectionId(conn.peer, peerID+1, peerID)
	accepted := make(chan *UtpStream, 1)
	go func() {
		s, err := sock.AcceptWithCid(ctx, cid, cfg)
		if err != nil {
			t.Errorf("accept: %v", err)
		}
		accepted <- s
	}()
	clk.AwaitParticipants(3)
	clk.AwaitQuiet()
	clk.AwaitReactionTo(func() {
		conn.inject(NewPacketBuilder(st_syn, peerID, 100000, 1<<20, peerSeq).Build().Encode())
	})
	stream := <-accepted
	if stream == nil {
		t.FailNow()
	}
	clk.AwaitParticipants(4)
	clk.AwaitQuiet()
	clk.AwaitReactionTo(func() {
		conn.inject(NewPacketBuilder(st_data, peerID+1, 110000, 1<<20, peerSeq+1).
			WithAckNum(ourSeq - 1).WithPayload([]byte("hello")).Build().Encode())
	})
	buf := make([]byte, 16)
	if n, err := stream.Read(ctx, buf); err != nil || string(buf[:n]) != "hello" {
		t.Fatalf("first read: %q, %v", buf[:n], err)
	}
	kill(conn, clk, stream)
	select {
	case <-stream.ended:
	default:
		t.Fatal("the connection had not ended")
	}
	return stream, func() {
		sock.Close()
		cancel()
		restore()
	}
}

func deadByReset(t *testing.T) (*UtpStream, func()) {
	return deadAccepted(t, func(conn *scriptedConn, clk *virtualClock, _ *UtpStream) {
		clk.AwaitReactionTo(func() {
			conn.inject(NewPacketBuilder(st_reset, 6000+1, 120000, 0, 902).
				WithAckNum(0x4321 - 1).Build().Encode())
		})
		clk.AwaitQuiet()
	})
}

func deadByGivingUp(t *testing.T) (*UtpStream, func()) {
	return deadAccepted(t, func(conn *scriptedConn, clk *virtualClock, stream *UtpStream) {
		// Data the peer never acknowledges: the sender retransmits, backs
		// off, and gives up.
		wctx, wcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer wcancel()
		if _, err := stream.Write(wctx, []byte("unanswered")); err != nil {
			t.Fatalf("write before giving up: %v", err)
		}
		for elapsed := time.Duration(0); elapsed < 10*time.Minute; elapsed += 500 * time.Millisecond {
			clk.Advance(500 * time.Millisecond)
			select {
			case <-stream.ended:
				clk.AwaitQuiet()
				return
			default:
			}
		}
	})
}

func TestDeadConnectionWritesFailWithTheReason(t *testing.T) {
	for _, c := range []struct {
		name string
		dead func(*testing.T) (*UtpStream, func())
		want error
	}{
		{"reset", deadByReset, ErrReset},
		{"gave up", deadByGivingUp, ErrTimedOut},
	} {
		t.Run(c.name, func(t *testing.T) {
			stream, done := c.dead(t)
			defer done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			start := time.Now()
			_, err := stream.Write(ctx, []byte("after"))
			if !errors.Is(err, c.want) {
				t.Fatalf("write returned %v after %v; expected %v at once", err, time.Since(start), c.want)
			}
		})
	}
}

func TestDeadConnectionReadsReportTheReason(t *testing.T) {
	for _, c := range []struct {
		name string
		dead func(*testing.T) (*UtpStream, func())
		want error
	}{
		{"reset", deadByReset, ErrReset},
		{"gave up", deadByGivingUp, ErrTimedOut},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Twice per run, with the socket's context cancelled first, so
			// that Read has the cancelled context and the closed queue to
			// choose between.
			for i := 0; i < 5; i++ {
				stream, done := c.dead(t)
				done()
				<-stream.streamCtx.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				buf := make([]byte, 16)
				_, err := stream.Read(ctx, buf)
				var all []byte
				_, toEOF := stream.ReadToEOF(ctx, &all)
				cancel()
				if !errors.Is(err, c.want) || !errors.Is(toEOF, c.want) {
					t.Fatalf("run %d: Read returned %v, ReadToEOF %v; expected %v from both", i, err, toEOF, c.want)
				}
			}
		})
	}
}
