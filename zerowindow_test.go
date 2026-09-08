package utp_go

import (
	"context"
	"testing"
	"time"
)

// A peer that advertises a zero receive window stops this sender. Something
// has to start it again.
//
// The peer will send a window update when its application drains its buffer,
// but that update is a single packet on an unreliable path. If it is lost,
// the sender has nothing outstanding -- so no retransmission timer -- and no
// reason of its own to transmit. The connection stops dead until the idle
// timeout kills it, with data still queued and a peer waiting for it.
//
// libutp answers this with a probe: when an acknowledgement reports a zero
// window it arms a timer, and when that expires it forces the peer's window
// up to one packet (utp_internal.cpp:2145-2151 and :1142-1145). One packet
// goes out, the peer acknowledges it, and the acknowledgement carries a fresh
// window. Recovery costs one packet per interval and needs nothing from the
// peer beyond an ack.
func TestZeroWindowProbe(t *testing.T) {
	cfg := NewConnectionConfig()
	// libutp's interval is 15 seconds. Testing that honestly would mean a
	// 15-second test, so the interval is configurable -- as InitialTimeout,
	// MinTimeout and MaxTimeout already are -- and defaults to libutp's.
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
	conn.inject(NewPacketBuilder(st_state, 4000, 150000, 1024*1024, 900).
		WithAckNum(4000).Build().Encode())

	var stream *UtpStream
	select {
	case stream = <-streamCh:
	case <-time.After(5 * time.Second):
		t.Fatal("connect never completed")
	}

	// Now tell it the window is closed.
	conn.inject(NewPacketBuilder(st_state, 4000, 160000, 0, 900).
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
			pkt, err := DecodePacket(emitted[0])
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

// An established connection that goes quiet must say something occasionally.
//
// libutp sends a keep-alive after 29 seconds of silence
// (utp_internal.cpp:74, :1271-1274), chosen to sit under the 30-second UDP
// mapping timeout common in NATs. A connection quiet for longer than that
// loses its mapping and cannot be reached from outside again -- and a peer
// with an idle timeout shorter than ours eventually tears it down while we
// still believe it is alive.
//
// This library sent nothing at all. It relied entirely on the peer speaking
// first, which against another copy of this library means neither side ever
// does.
func TestKeepAlive(t *testing.T) {
	cfg := NewConnectionConfig()
	// libutp's interval is 29 seconds; configurable so this is not a
	// half-minute test.
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
	conn.inject(NewPacketBuilder(st_state, 4100, 150000, 1024*1024, 900).
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
			pkt, err := DecodePacket(emitted[0])
			if err != nil {
				t.Fatalf("keep-alive does not decode: %v", err)
			}
			if pkt.Header.PacketType != st_state {
				t.Errorf("keep-alive was %s, libutp sends a STATE", pkt.Header.PacketType.String())
			}
			// libutp acks one behind, so the packet reads as a stale
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
