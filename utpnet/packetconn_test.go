package utpnet

import (
	"io"
	"net"
	"runtime"
	"testing"
	"time"
)

// The net.PacketConn side of a Socket, held to its contract. A BitTorrent
// client sends every DHT query and reply on its uTP port through ReadFrom
// and WriteTo.

// goroutinesSettle returns the goroutine count once it has stopped moving, so
// a count is not taken while an unrelated goroutine is still winding down.
func goroutinesSettle() int {
	prev := -1
	for i := 0; i < 50; i++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		n := runtime.NumGoroutine()
		if n == prev {
			return n
		}
		prev = n
	}
	return runtime.NumGoroutine()
}

// Sending and receiving on the shared port does not accumulate goroutines.
// With no deadline set, the normal case for a DHT, a per-call goroutine
// serving the deadline would park forever.
//
// Two details of the measurement matter. Sending in bulk and draining
// afterwards loses datagrams, since the passthrough queue is bounded, so
// sends and reads are interleaved. And no deadline is set anywhere, since
// SetReadDeadline releases goroutines parked on the old deadline and would
// hide the leak being counted.
func TestSharedPortDoesNotLeakGoroutines(t *testing.T) {
	server, client := listenPair(t)
	dst := server.LocalAddr()
	buf := make([]byte, 2048)

	// Warm up: the first calls create the socket's own goroutines, which
	// are not being counted.
	for i := 0; i < 10; i++ {
		if _, err := client.WriteTo([]byte("warmup"), dst); err != nil {
			t.Fatal(err)
		}
		if _, _, err := server.ReadFrom(buf); err != nil {
			t.Fatal(err)
		}
	}

	before := goroutinesSettle()

	const messages = 500
	for i := 0; i < messages; i++ {
		if _, err := client.WriteTo([]byte("dht query"), dst); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		if _, _, err := server.ReadFrom(buf); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}

	after := goroutinesSettle()

	// A handful either way is scheduling noise; hundreds would be a leak.
	if grew := after - before; grew > 20 {
		t.Errorf("%d datagrams each way left %d extra goroutines (%d -> %d); the shared port "+
			"leaks one per call, and a DHT would run the process out of them",
			messages, grew, before, after)
	}
	t.Logf("%d WriteTo and %d ReadFrom calls: %d goroutines before, %d after",
		messages, messages, before, after)
}

// A deadline already in the past must fail both directions immediately, and
// must keep failing until it is moved.
func TestPacketConnPastDeadline(t *testing.T) {
	server, client := listenPair(t)

	past := time.Now().Add(-time.Hour)
	if err := client.SetDeadline(past); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 64)
	for i := 0; i < 3; i++ {
		if _, _, err := client.ReadFrom(buf); !isTimeout(err) {
			t.Fatalf("ReadFrom past a deadline returned %v, want a timeout", err)
		}
		if _, err := client.WriteTo([]byte("x"), server.LocalAddr()); !isTimeout(err) {
			t.Fatalf("WriteTo past a deadline returned %v, want a timeout", err)
		}
	}

	// Clearing the deadline puts both back in service.
	if err := client.SetDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.WriteTo([]byte("after"), server.LocalAddr()); err != nil {
		t.Fatalf("WriteTo after the deadline was cleared: %v", err)
	}
	if _, _, err := server.ReadFrom(buf); err != nil {
		t.Fatalf("the datagram sent after the deadline was cleared never arrived: %v", err)
	}
}

// A deadline moved while ReadFrom is blocked must take effect, in both
// directions: bringing it forward times the call out, and pushing it back
// keeps the call waiting.
func TestPacketConnDeadlineMovesUnderABlockedRead(t *testing.T) {
	_, client := listenPair(t)

	if err := client.SetReadDeadline(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 64)
		_, _, err := client.ReadFrom(buf)
		done <- err
	}()

	// Let the read block on the hour-long deadline.
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("ReadFrom returned %v while its deadline was an hour away", err)
	default:
	}

	// Bring it into the past; the blocked read must notice.
	if err := client.SetReadDeadline(time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !isTimeout(err) {
			t.Fatalf("ReadFrom returned %v after its deadline was moved into the past, want a timeout", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("moving the deadline into the past did not unblock ReadFrom")
	}
}

// Close must unblock a ReadFrom that is already waiting, and every call after
// it must report the socket is closed rather than block or panic.
func TestPacketConnCloseUnblocksAndStaysClosed(t *testing.T) {
	_, client := listenPair(t)

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 64)
		_, _, err := client.ReadFrom(buf)
		done <- err
	}()
	time.Sleep(200 * time.Millisecond)

	client.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ReadFrom returned no error after the socket was closed")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Close did not unblock a waiting ReadFrom")
	}

	buf := make([]byte, 64)
	if _, _, err := client.ReadFrom(buf); err == nil {
		t.Error("ReadFrom on a closed socket returned no error")
	}
	if _, err := client.WriteTo([]byte("x"), client.LocalAddr()); err == nil {
		t.Error("WriteTo on a closed socket returned no error")
	}
	if _, err := client.Accept(); err == nil {
		t.Error("Accept on a closed socket returned no error")
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

// The connection side's deadline path does not leak goroutines either.
// Conn.Read and Conn.Write use withDeadline and deadline.context, which also
// start a goroutine per call.
func TestConnIODoesNotLeakGoroutines(t *testing.T) {
	server, client := listenPair(t)

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := server.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	conn, err := client.Dial("udp", server.Addr().String())
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	defer conn.Close()

	var peer net.Conn
	select {
	case peer = <-accepted:
	case <-time.After(30 * time.Second):
		t.Fatal("no connection was accepted")
	}
	defer peer.Close()

	msg := []byte("ping")
	buf := make([]byte, len(msg))

	// Warm up, then measure. No deadline is set, for the same reason as above.
	for i := 0; i < 10; i++ {
		if _, err := conn.Write(msg); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(peer, buf); err != nil {
			t.Fatal(err)
		}
	}

	before := goroutinesSettle()

	const messages = 500
	for i := 0; i < messages; i++ {
		if _, err := conn.Write(msg); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		if _, err := io.ReadFull(peer, buf); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}

	after := goroutinesSettle()

	if grew := after - before; grew > 20 {
		t.Errorf("%d writes and reads left %d extra goroutines (%d -> %d)",
			messages, grew, before, after)
	}
	t.Logf("%d Write and %d Read calls: %d goroutines before, %d after",
		messages, messages, before, after)
}
