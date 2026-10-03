package utp_go

import (
	"context"
	"testing"
	"time"
)

// By default a connection is never closed for silence, as in libutp.
//
// libutp has no idle timeout: a connection with nothing in flight ends only by
// its application, a reset, or the retransmission timeout giving up, and that
// counts only while something is outstanding (utp_internal.cpp:1239-1240).
// Measured against its driver, a connection whose peer went quiet was still
// open after 600 seconds, having sent 20 keep-alives. This library closed one
// after 60, which ended a connection that an outage of a minute or more had
// interrupted and that libutp would have resumed. Here the peer is silent for
// ten minutes and then speaks: the connection must have kept sending its
// keep-alives, and must take the data.
func TestSilentPeerDoesNotCloseAnIdleConnectionByDefault(t *testing.T) {
	const (
		ourSeq   = 0x4321
		peerID   = 6000
		peerSeq  = 900
		interval = defaultKeepAliveInterval
		silence  = 10 * time.Minute
	)

	clk := newVirtualClock(time.Unix(0, 0).Add(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer pinRandom(ourSeq)()

	conn := newScriptedConn()
	sock := WithSocket(ctx, conn, conformanceLogger(), WithClock(clk))
	defer sock.Close()

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
	conn.takeEmitted()

	// The acknowledgement of the SYN was the last packet; from here the peer
	// says nothing, and the connection keeps its keep-alives going.
	keepAlives := 0
	for elapsed := time.Duration(0); elapsed+interval <= silence; elapsed += interval {
		clk.Advance(interval)
		for _, raw := range conn.takeEmitted() {
			pkt, err := DecodePacket(raw)
			if err != nil {
				t.Fatalf("after %v of silence: undecodable packet: %v", elapsed+interval, err)
			}
			if pkt.Header.PacketType != st_state {
				t.Fatalf("after %v of silence the connection sent %s, not a keep-alive",
					elapsed+interval, describePackets([][]byte{raw}))
			}
			keepAlives++
		}
	}
	clk.Advance(silence % interval)
	conn.takeEmitted()
	if want := int(silence / interval); keepAlives != want {
		t.Fatalf("%d keep-alives in %v of silence, expected %d: the connection "+
			"stopped sending, so it was closed", keepAlives, silence, want)
	}

	// The peer comes back.
	clk.AwaitReactionTo(func() {
		conn.inject(NewPacketBuilder(st_data, peerID+1, 200000, 1<<20, peerSeq+1).
			WithAckNum(ourSeq - 1).WithPayload([]byte("back")).Build().Encode())
	})
	emitted := conn.takeEmitted()
	if len(emitted) != 1 {
		t.Fatalf("the peer's data after %v drew %d packets, expected one acknowledgement",
			silence, len(emitted))
	}
	if pkt, err := DecodePacket(emitted[0]); err != nil || pkt.Header.PacketType != st_state ||
		pkt.Header.AckNum != peerSeq+1 {
		t.Fatalf("the peer's data after %v drew %s, expected a STATE acknowledging %d",
			silence, describePackets(emitted), peerSeq+1)
	}
	readCtx, readCancel := context.WithTimeout(ctx, 10*time.Second)
	defer readCancel()
	buf := make([]byte, 16)
	n, err := stream.Read(readCtx, buf)
	if err != nil || string(buf[:n]) != "back" {
		t.Fatalf("read after %v of silence: %q, %v", silence, buf[:n], err)
	}
}
