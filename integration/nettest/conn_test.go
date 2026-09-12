package nettest

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/zen-eth/utp-go/utpnet"
	"golang.org/x/net/nettest"
)

// The standard library's own net.Conn conformance suite, run against ours.
//
// utpnet.Conn claims to be a net.Conn, and everything that embeds this library
// -- a BitTorrent client above all -- programs against that claim rather than
// against uTP. Until now nothing checked it. The claim was already false in a
// way no test here could see: cancelling the context a dial was made with
// closed the connection the dial had just returned, which net.Dialer documents
// must not happen, and which took a real torrent to find.
//
// nettest.TestConn is the suite the standard library holds its own
// implementations to. It covers the things a hand-written test does not think
// to: reads racing writes, a deadline set in the past, a deadline moved while
// a read is blocked on it, Close while another goroutine is inside Read, and
// every method called concurrently with every other. It is written to be run
// under the race detector, and its own documentation warns that some issues
// surface only across repeated runs -- so this is worth running more than once
// (`go test -race -count=5 ./...`).
func TestUtpConnSatisfiesNetConn(t *testing.T) {
	nettest.TestConn(t, makeUtpPipe)
}

// makeUtpPipe gives nettest a connected pair, one end from Dial and one from
// Accept, on two separate sockets -- which is the shape a real peer connection
// has, rather than two ends of one socket talking to themselves.
func makeUtpPipe() (c1, c2 net.Conn, stop func(), err error) {
	opts := &utpnet.Options{Logger: quiet()}

	server, err := utpnet.Listen(context.Background(), "udp", "127.0.0.1:0", opts)
	if err != nil {
		return nil, nil, nil, err
	}
	client, err := utpnet.Listen(context.Background(), "udp", "127.0.0.1:0", opts)
	if err != nil {
		server.Close()
		return nil, nil, nil, err
	}

	type acceptResult struct {
		conn net.Conn
		err  error
	}
	accepted := make(chan acceptResult, 1)
	go func() {
		conn, err := server.Accept()
		accepted <- acceptResult{conn: conn, err: err}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dialed, err := client.DialContext(ctx, "udp", server.Addr().String())
	if err != nil {
		server.Close()
		client.Close()
		return nil, nil, nil, err
	}

	var res acceptResult
	select {
	case res = <-accepted:
	case <-time.After(30 * time.Second):
		dialed.Close()
		server.Close()
		client.Close()
		return nil, nil, nil, errAcceptTimeout
	}
	if res.err != nil {
		dialed.Close()
		server.Close()
		client.Close()
		return nil, nil, nil, res.err
	}

	stop = func() {
		t0 := time.Now()
		dialed.Close()
		t1 := time.Now()
		res.conn.Close()
		t2 := time.Now()
		client.Close()
		server.Close()
		if os.Getenv("UTP_TIME_STOP") != "" {
			fmt.Fprintf(os.Stderr, "STOP dialed.Close=%v accepted.Close=%v sockets=%v\n",
				t1.Sub(t0).Round(time.Millisecond), t2.Sub(t1).Round(time.Millisecond),
				time.Since(t2).Round(time.Millisecond))
		}
	}
	return dialed, res.conn, stop, nil
}

type errString string

func (e errString) Error() string { return string(e) }

const errAcceptTimeout = errString("utpnet: no connection was accepted")

func quiet() log.Logger {
	return log.NewLogger(log.NewTerminalHandlerWithLevel(os.Stderr, log.LevelCrit, false))
}
