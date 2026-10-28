package utp

import (
	"context"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Concurrent teardown, every way a connection can end, all at once.
//
// Each cycle runs a batch of connections whose ends are deliberately mixed:
// closed, abandoned, silent until the idle timeout, canceled mid-transfer,
// and the sockets closed underneath them. The socket's connection table, the
// shared retransmission wheel and the per-connection event loops are torn
// down from several directions at once. It is most useful under the race
// detector.
//
// Longer runs:
//
//	UTP_SOAK_CYCLES=200 go test -race -run TestTeardownRace -timeout 60m
func TestTeardownRace(t *testing.T) {
	if testing.Short() {
		t.Skip("soak tests are not -short tests")
	}
	cycles := soakCycles(8)

	// Per cycle: enough that teardowns overlap rather than queue. It can be
	// overridden, since varying the concurrency explores different
	// interleavings.
	conns := 24
	if v := os.Getenv("UTP_SOAK_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			conns = n
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	var completed, idled, abandoned, cancelled atomic.Uint64

	for cycle := 0; cycle < cycles; cycle++ {
		// A fresh socket pair per cycle, so socket teardown is exercised too:
		// the sockets are closed below with connections still in flight.
		lg := duplexQuietLog()
		server, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
		if err != nil {
			t.Fatal(err)
		}
		client, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		serverAddr := server.LocalAddr().(*net.UDPAddr)
		clientAddr := client.LocalAddr().(*net.UDPAddr)

		cfg := func() *ConnectionConfig {
			c := NewConnectionConfig()
			// A short idle timeout, so that path is reached in bulk.
			c.MaxIdleTimeout = 400 * time.Millisecond
			c.InitialTimeout = 100 * time.Millisecond
			c.MinTimeout = 100 * time.Millisecond
			return c
		}

		payload := make([]byte, 8*1024)
		for i := range payload {
			payload[i] = byte(i)
		}

		var wg sync.WaitGroup
		cycleCtx, cycleCancel := context.WithCancel(ctx)

		for i := 0; i < conns; i++ {
			mode := i % 4
			recv := uint16(3000 + cycle*conns*2 + i*2)
			send := recv + 1
			cidClient := NewConnectionId(NewUdpPeer(serverAddr), recv, send)
			cidServer := NewConnectionId(NewUdpPeer(clientAddr), send, recv)

			// Each connection gets its own context so one can be canceled
			// mid-flight without disturbing the others.
			connCtx, connCancel := context.WithCancel(cycleCtx)

			wg.Add(2)
			go func(mode int) {
				defer wg.Done()
				stream, err := server.AcceptWithCid(connCtx, cidServer, cfg())
				if err != nil {
					return
				}
				switch mode {
				case 2:
					// Abandoned: accepted, never read, then closed.
					time.Sleep(time.Duration(30+mode*10) * time.Millisecond)
					stream.Close()
					abandoned.Add(1)
				default:
					var buf []byte
					readCtx, readCancel := context.WithTimeout(connCtx, 5*time.Second)
					defer readCancel()
					_, _ = stream.ReadToEOF(readCtx, &buf)
					stream.Close()
					if len(buf) == len(payload) {
						completed.Add(1)
					}
				}
			}(mode)

			go func(mode int, cancelConn context.CancelFunc) {
				defer wg.Done()
				defer cancelConn()
				stream, err := client.ConnectWithCid(connCtx, cidClient, cfg())
				if err != nil {
					return
				}
				switch mode {
				case 1:
					// Silent peer: write nothing and never close, so the
					// connection ends on its idle timeout rather than a FIN.
					time.Sleep(600 * time.Millisecond)
					idled.Add(1)
					stream.Close()
				case 3:
					// Canceled mid-transfer.
					go func() {
						time.Sleep(time.Duration(20+mode*5) * time.Millisecond)
						cancelConn()
						cancelled.Add(1)
					}()
					_, _ = stream.Write(connCtx, payload)
					stream.Close()
				default:
					writeCtx, writeCancel := context.WithTimeout(connCtx, 5*time.Second)
					defer writeCancel()
					_, _ = stream.Write(writeCtx, payload)
					stream.Close()
				}
			}(mode, connCancel)
		}

		// Close the sockets while connections are still winding down, so the
		// socket's table and the shared timer wheel go away underneath
		// live event loops.
		go func() {
			time.Sleep(500 * time.Millisecond)
			client.Close()
			server.Close()
		}()

		wg.Wait()
		cycleCancel()
		client.Close()
		server.Close()
	}

	t.Logf("%d cycles x %d connections: %d completed, %d idled out, %d abandoned, %d cancelled",
		cycles, conns, completed.Load(), idled.Load(), abandoned.Load(), cancelled.Load())

	// Every mode must have been exercised, or the test proves nothing.
	if idled.Load() == 0 {
		t.Error("no connection reached its idle timeout; the path this test exists for was never taken")
	}
	if abandoned.Load() == 0 {
		t.Error("no connection was abandoned without reading")
	}
	if cancelled.Load() == 0 {
		t.Error("no connection was cancelled mid-transfer")
	}
}
