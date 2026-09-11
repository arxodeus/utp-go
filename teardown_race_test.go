package utp_go

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
// This exists for one recorded observation: a data race seen once, years of
// runs ago in this repository's terms, in an early -race pass of
// TestManyConcurrentTransfers, "under conditions where many connections were
// hitting the 60s idle timeout". The report was never captured and it has not
// reproduced since, so there is nothing to fix -- only a hypothesis to press
// on until it either produces a race or stops being worth suspecting.
//
// What that observation implies, and what this concentrates:
//
//   - many connections ending at once, not one at a time
//   - the *idle timeout* path specifically, which no other test drives in
//     bulk: MaxIdleTimeout is 60s by default, so reaching it requires either
//     a minute of waiting or the short timeout set here
//   - teardown overlapping teardown -- a close racing an idle expiry racing a
//     cancelled context racing the socket going away underneath all of them
//
// Each cycle runs a batch of connections whose ends are deliberately mixed, so
// the socket's connection table, the shared retransmission wheel and the
// per-connection event loops are all being torn down from several directions
// at the same moment.
//
// Longer runs, which is the point of it:
//
//	UTP_SOAK_CYCLES=200 go test -race -run TestTeardownRace -timeout 60m
func TestTeardownRace(t *testing.T) {
	if testing.Short() {
		t.Skip("soak tests are not -short tests")
	}
	cycles := soakCycles(8)

	// Per cycle. Enough that teardowns genuinely overlap rather than queue.
	//
	// Overridable because a race is a timing accident: running the same shape
	// repeatedly explores one interleaving landscape, and varying the
	// concurrency between runs explores several.
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
			// The condition the original observation named. At the default of
			// 60s no test reaches this path in bulk.
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

			// Each connection gets its own context so mode 3 can cancel one
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
					// Abandoned: accepted, never read, then closed. This is
					// the path that used to deadlock Close against its own
					// event loop.
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
					// Cancelled mid-transfer.
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

		// Close the sockets while connections are still winding down. This is
		// the overlap the observation pointed at: the socket's table and the
		// shared timer wheel going away underneath live event loops.
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

	// Every mode must actually have been exercised. A soak test that silently
	// stopped reaching the idle-timeout path would look like evidence and be
	// worth nothing.
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
