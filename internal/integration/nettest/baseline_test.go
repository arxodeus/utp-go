package nettest

import (
	"net"
	"testing"

	"golang.org/x/net/nettest"
)

// A control, run in the same process and the same container as the suite
// above: the same eleven subtests against kernel TCP over loopback.
//
// It exists because "the suite passes" is not the only thing worth knowing
// from it. The subtest timings say what the deadline paths cost, and a timing
// is meaningless without something to compare it to -- a machine that is slow
// at all of this would otherwise read as a library that is slow at it.
func TestTCPBaseline(t *testing.T) {
	nettest.TestConn(t, makeTCPPipe)
}

func makeTCPPipe() (c1, c2 net.Conn, stop func(), err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, nil, err
	}
	type res struct {
		c   net.Conn
		err error
	}
	accepted := make(chan res, 1)
	go func() {
		c, err := ln.Accept()
		accepted <- res{c, err}
	}()
	dialed, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		ln.Close()
		return nil, nil, nil, err
	}
	r := <-accepted
	if r.err != nil {
		dialed.Close()
		ln.Close()
		return nil, nil, nil, r.err
	}
	return dialed, r.c, func() {
		dialed.Close()
		r.c.Close()
		ln.Close()
	}, nil
}
