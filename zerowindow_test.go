package utp

import (
	"context"
	"testing"
	"time"
)

// A peer that advertises a zero receive window stops this sender, and
// something must start it again. The peer's window update is a single
// packet; if it is lost, the sender has nothing outstanding and no reason to
// send. When an acknowledgement reports a zero window a timer is armed, and
// when it expires one packet is let through; the peer's acknowledgement
// carries a fresh window.
func TestZeroWindowProbe(t *testing.T) {
	cfg := NewConnectionConfig()
	// The default interval is 15 seconds; a shorter one keeps the test
	// short.
	cfg.ZeroWindowProbeInterval = 150 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	restore := pinRandom(4000)
	defer restore()

	conn := newScriptedConn()
	defer conn.Close()
	sock := WithSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	cid := NewConnectionId(conn.peer, 4000, 4001)
	streamCh := make(chan *UtpStream, 1)
	go func() {
		stream, err := sock.ConnectWithCid(ctx, cid, cfg)
		if err == nil {
			streamCh <- stream
		}
	}()

	waitFor(t, 2*time.Second, func() bool { return conn.emittedCount() > 0 })

	// Answer the SYN, advertising a full window so the connection completes.
	conn.inject(newPacketBuilder(st_state, 4000, 150000, 1024*1024, 900).
		WithAckNum(4000).Build().Encode())

	var stream *UtpStream
	select {
	case stream = <-streamCh:
	case <-time.After(5 * time.Second):
		t.Fatal("connect never completed")
	}

	// Now tell it the window is closed.
	conn.inject(newPacketBuilder(st_state, 4000, 160000, 0, 900).
		WithAckNum(4000).Build().Encode())
	time.Sleep(50 * time.Millisecond)
	conn.takeEmitted()

	// Queue data. Nothing may go out while the window is closed.
	go func() {
		writeCtx, writeCancel := context.WithTimeout(ctx, 20*time.Second)
		defer writeCancel()
		_, _ = stream.Write(writeCtx, []byte("data the peer has no room for"))
	}()

	time.Sleep(cfg.ZeroWindowProbeInterval / 2)
	if n := conn.emittedCount(); n > 0 {
		t.Fatalf("sent %d packet(s) into a zero window before the probe was due: %s",
			n, describePackets(conn.takeEmitted()))
	}

	// Once the probe interval has passed, exactly one packet must go out.
	deadline := time.Now().Add(4 * cfg.ZeroWindowProbeInterval)
	for time.Now().Before(deadline) {
		if conn.emittedCount() > 0 {
			emitted := conn.takeEmitted()
			pkt, err := decodePacket(emitted[0], false)
			if err != nil {
				t.Fatalf("probe does not decode: %v", err)
			}
			if pkt.Header.PacketType != st_data {
				t.Errorf("probe was %s, expected a data packet carrying the queued bytes",
					pkt.Header.PacketType.String())
			}
			if len(pkt.Body) == 0 {
				t.Error("probe carried no payload; it exists to make the peer answer with a window")
			}
			t.Logf("probe after %v: %s", cfg.ZeroWindowProbeInterval, describePackets(emitted[:1]))
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no probe after %v with a closed window and data queued; the connection is stuck "+
		"until its idle timeout", 4*cfg.ZeroWindowProbeInterval)
}

// An established connection that goes quiet sends a keep-alive after 29
// seconds, below the 30-second UDP mapping timeout common in NATs, so its
// mapping survives and a peer with a shorter idle timeout does not tear it
// down.
func TestKeepAlive(t *testing.T) {
	cfg := NewConnectionConfig()
	// The default interval is 29 seconds; a shorter one keeps the test
	// short.
	cfg.KeepAliveInterval = 120 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	restore := pinRandom(4100)
	defer restore()

	conn := newScriptedConn()
	defer conn.Close()
	sock := WithSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	cid := NewConnectionId(conn.peer, 4100, 4101)
	streamCh := make(chan *UtpStream, 1)
	go func() {
		stream, err := sock.ConnectWithCid(ctx, cid, cfg)
		if err == nil {
			streamCh <- stream
		}
	}()

	waitFor(t, 2*time.Second, func() bool { return conn.emittedCount() > 0 })
	conn.inject(newPacketBuilder(st_state, 4100, 150000, 1024*1024, 900).
		WithAckNum(4100).Build().Encode())

	select {
	case <-streamCh:
	case <-time.After(5 * time.Second):
		t.Fatal("connect never completed")
	}
	time.Sleep(30 * time.Millisecond)
	conn.takeEmitted()

	// Now say nothing to it, and wait.
	deadline := time.Now().Add(6 * cfg.KeepAliveInterval)
	for time.Now().Before(deadline) {
		if conn.emittedCount() > 0 {
			emitted := conn.takeEmitted()
			pkt, err := decodePacket(emitted[0], false)
			if err != nil {
				t.Fatalf("keep-alive does not decode: %v", err)
			}
			if pkt.Header.PacketType != st_state {
				t.Errorf("keep-alive was %s, libutp sends a STATE", pkt.Header.PacketType.String())
			}
			// The keep-alive acknowledges one behind, so it reads as a stale
			// acknowledgement and the peer answers it.
			if want := uint16(899 - 1); pkt.Header.AckNum != want {
				t.Errorf("keep-alive acked %d, want %d -- one behind, so the peer answers it",
					pkt.Header.AckNum, want)
			}
			t.Logf("keep-alive after %v of silence: %s",
				cfg.KeepAliveInterval, describePackets(emitted[:1]))
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no keep-alive after %v of silence; a NAT mapping lasts about 30 seconds and "+
		"libutp's interval is 29", 6*cfg.KeepAliveInterval)
}
