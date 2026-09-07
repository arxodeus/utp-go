package utp_go

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// Soak tests: the defects that only appear after something has been running
// for a while.
//
// Everything else in this repository measures a transfer or a packet. These
// measure what is left behind afterwards -- goroutines, map entries, heap --
// because a library that leaks a goroutine per connection works perfectly in
// every test and dies in production after a day.
//
// Longer runs:
//
//	UTP_SOAK_CYCLES=2000 go test -run TestSoak -timeout 30m

func soakCycles(def int) int {
	if v := os.Getenv("UTP_SOAK_CYCLES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// settleGoroutines waits for the goroutine count to stop falling, so a
// measurement is not taken while teardown is still in flight.
func settleGoroutines(d time.Duration) int {
	deadline := time.Now().Add(d)
	last := runtime.NumGoroutine()
	stable := 0
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		runtime.GC()
		n := runtime.NumGoroutine()
		if n >= last {
			stable++
			if stable >= 4 {
				return n
			}
		} else {
			stable = 0
		}
		last = n
	}
	return runtime.NumGoroutine()
}

// A connection opened and closed leaves nothing behind. Repeatedly.
//
// This is the shape of leak that matters most for the target use: a
// BitTorrent client opens and drops peer connections continuously for days.
func TestSoakConnectionChurn(t *testing.T) {
	if testing.Short() {
		t.Skip("soak tests are not -short tests")
	}
	cycles := soakCycles(60)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	lg := duplexQuietLog()

	server, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	serverAddr := server.LocalAddr().(*net.UDPAddr)
	clientAddr := client.LocalAddr().(*net.UDPAddr)

	payload := make([]byte, 4096)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	// A few cycles first, so one-off allocations (buffers, timer wheels) are
	// not counted as growth.
	const warmup = 5
	runCycle := func(i int) error {
		recv := uint16(2000 + i*2)
		send := recv + 1
		cidClient := NewConnectionId(NewUdpPeer(serverAddr), recv, send)
		cidServer := NewConnectionId(NewUdpPeer(clientAddr), send, recv)

		done := make(chan error, 1)
		go func() {
			stream, err := server.AcceptWithCid(ctx, cidServer, NewConnectionConfig())
			if err != nil {
				done <- err
				return
			}
			var buf []byte
			readCtx, readCancel := context.WithTimeout(ctx, 20*time.Second)
			defer readCancel()
			if _, err := stream.ReadToEOF(readCtx, &buf); err != nil {
				done <- err
				return
			}
			stream.Close()
			if !bytes.Equal(buf, payload) {
				done <- fmt.Errorf("cycle %d: payload differs (%d bytes)", i, len(buf))
				return
			}
			done <- nil
		}()

		time.Sleep(5 * time.Millisecond)
		stream, err := client.ConnectWithCid(ctx, cidClient, NewConnectionConfig())
		if err != nil {
			return fmt.Errorf("cycle %d connect: %w", i, err)
		}
		writeCtx, writeCancel := context.WithTimeout(ctx, 20*time.Second)
		defer writeCancel()
		if _, err := stream.Write(writeCtx, payload); err != nil {
			return fmt.Errorf("cycle %d write: %w", i, err)
		}
		stream.Close()

		select {
		case err := <-done:
			return err
		case <-time.After(30 * time.Second):
			return fmt.Errorf("cycle %d never completed", i)
		}
	}

	for i := 0; i < warmup; i++ {
		if err := runCycle(i); err != nil {
			t.Fatalf("warmup: %v", err)
		}
	}

	baseGoroutines := settleGoroutines(5 * time.Second)
	var baseMem runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&baseMem)
	baseConns := server.NumConnections() + client.NumConnections()

	for i := warmup; i < warmup+cycles; i++ {
		if err := runCycle(i); err != nil {
			t.Fatalf("%v", err)
		}
	}

	endGoroutines := settleGoroutines(15 * time.Second)
	var endMem runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&endMem)
	endConns := server.NumConnections() + client.NumConnections()

	t.Logf("%d connection cycles: goroutines %d -> %d, live heap %d KiB -> %d KiB, tracked connections %d -> %d",
		cycles, baseGoroutines, endGoroutines,
		baseMem.HeapAlloc/1024, endMem.HeapAlloc/1024, baseConns, endConns)

	// A goroutine per connection would show as `cycles` extra goroutines.
	// The allowance is for the runtime's own workers and the sockets' timer
	// wheels, not for anything that scales with the number of connections.
	if grew := endGoroutines - baseGoroutines; grew > 20 {
		t.Errorf("goroutines grew by %d over %d connection cycles; that scales with connections, "+
			"which is a leak", grew, cycles)
	}

	if endConns > baseConns {
		t.Errorf("the sockets still track %d connections after %d closed cycles (%d at the start); "+
			"closed connections are not being removed", endConns, cycles, baseConns)
	}
}

// The same for connections that are abandoned rather than closed: the peer
// vanishes and nothing is ever read or written again. A client dealing with
// real peers gets these constantly.
func TestSoakAbandonedConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("soak tests are not -short tests")
	}
	cycles := soakCycles(40)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	sock, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, duplexQuietLog())
	if err != nil {
		t.Fatal(err)
	}
	defer sock.Close()

	base := settleGoroutines(3 * time.Second)

	// Every one of these is a connection attempt to a port with nothing on
	// it. Each should give up and clean up on its own.
	for i := 0; i < cycles; i++ {
		attemptCtx, attemptCancel := context.WithTimeout(ctx, 200*time.Millisecond)
		dead := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9 + i}
		cid := NewConnectionId(NewUdpPeer(dead), uint16(5000+i*2), uint16(5001+i*2))
		_, _ = sock.ConnectWithCid(attemptCtx, cid, NewConnectionConfig())
		attemptCancel()
	}

	end := settleGoroutines(20 * time.Second)
	t.Logf("%d abandoned connection attempts: goroutines %d -> %d, tracked connections %d",
		cycles, base, end, sock.NumConnections())

	if grew := end - base; grew > 20 {
		t.Errorf("goroutines grew by %d over %d abandoned attempts; abandoned connections are "+
			"not cleaning themselves up", grew, cycles)
	}
	if n := sock.NumConnections(); n > 5 {
		t.Errorf("the socket still tracks %d connections after %d abandoned attempts", n, cycles)
	}
}

// A socket answering packets for connections it does not have must not
// accumulate state for them. This is the amplification surface: a peer that
// sends to torn-down connections should cost the socket a bounded amount.
func TestSoakUnknownConnectionFlood(t *testing.T) {
	if testing.Short() {
		t.Skip("soak tests are not -short tests")
	}
	packets := soakCycles(20000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := newFuzzConn()
	defer conn.Close()
	sock := WithSocket(ctx, conn, fuzzLogger())
	defer sock.Close()

	base := settleGoroutines(2 * time.Second)
	var baseMem runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&baseMem)

	for i := 0; i < packets; i++ {
		pkt := NewPacketBuilder(st_data, uint16(i), 1000, 1024, uint16(i)).
			WithAckNum(uint16(i)).WithPayload([]byte("x")).Build()
		conn.inject(pkt.Encode())
	}
	time.Sleep(2 * time.Second)

	end := settleGoroutines(5 * time.Second)
	var endMem runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&endMem)

	t.Logf("%d packets for unknown connections: goroutines %d -> %d, live heap %d KiB -> %d KiB, "+
		"emitted %d packets", packets, base, end,
		baseMem.HeapAlloc/1024, endMem.HeapAlloc/1024, conn.emittedCount())

	if grew := end - base; grew > 20 {
		t.Errorf("goroutines grew by %d while answering packets for unknown connections", grew)
	}

	// The RESET rate limiting is what should hold this down: libutp remembers
	// what it has already answered and stops entirely past a limit
	// (utp_internal.cpp:2907-2945). Without it a peer draws one RESET per
	// packet, which is both an amplification vector and unbounded state.
	if emitted := conn.emittedCount(); emitted > packets/2 {
		t.Errorf("answered %d of %d packets for unknown connections; the RESET rate limit is not holding",
			emitted, packets)
	}
}
