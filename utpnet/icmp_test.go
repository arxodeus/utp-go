package utpnet

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	utp "github.com/arxodeus/utp"
)

// quotedHeader builds the twenty bytes a router quotes back: the start of a
// uTP datagram this side sent, carrying connId. It is written out by hand,
// since a test that encoded it with the code that decodes it would agree
// with itself whatever the layout.
//
//	0        type<<4 | version
//	1        first extension
//	2-3      connection id
//	4-7      timestamp, microseconds
//	8-11     timestamp difference
//	12-15    window size
//	16-17    sequence number
//	18-19    acknowledgement number
func quotedHeader(t *testing.T, connID uint16) []byte {
	t.Helper()
	b := make([]byte, 20)
	b[0] = 0<<4 | 1 // ST_DATA, version 1
	b[1] = 0        // no extensions
	binary.BigEndian.PutUint16(b[2:4], connID)
	binary.BigEndian.PutUint32(b[4:8], uint32(time.Now().UnixMicro()))
	binary.BigEndian.PutUint32(b[8:12], 0)
	binary.BigEndian.PutUint32(b[12:16], 1<<20)
	binary.BigEndian.PutUint16(b[16:18], 1)
	binary.BigEndian.PutUint16(b[18:20], 0)
	return b
}

// The ICMP entry points reach the connection through this package. A caller
// reading IP_RECVERR off its UDP socket has a net.Addr and a quoted payload,
// and hands them in here. The connection ids are taken from the socket.
func TestSocketProcessICMPError(t *testing.T) {
	server, client := listenPair(t)

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := server.Accept()
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- conn
	}()

	conn, err := client.Dial("utp", server.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	srvConn := <-accepted
	if srvConn == nil {
		t.Fatal("accept failed")
	}
	defer srvConn.Close()

	// The dialed connection's send id is what its data packets carry.
	uc, ok := conn.(*Conn)
	if !ok {
		t.Fatalf("Dial returned %T, want *Conn", conn)
	}
	cid := uc.stream.CID()

	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 1024)
		_, err := conn.Read(buf)
		readErr <- err
	}()

	serverAddr := server.Addr().(*net.UDPAddr)
	quoted := quotedHeader(t, cid.Send)
	if !client.ProcessICMPError(quoted, serverAddr) {
		t.Fatal("ProcessICMPError did not match the connection that sent the quoted datagram")
	}

	select {
	case err := <-readErr:
		if err == nil || errors.Is(err, io.EOF) {
			t.Fatalf("read returned %v; the connection should have failed", err)
		}
		if !errors.Is(err, utp.ErrReset) {
			t.Errorf("read returned %v, want %v", err, utp.ErrReset)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the reader did not learn of the ICMP error in 10s")
	}
}

// The fragmentation entry point, and what it refuses. A nil address must not
// become a peer that matches something.
func TestSocketProcessICMPFragmentation(t *testing.T) {
	server, client := listenPair(t)

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := server.Accept()
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- conn
	}()

	conn, err := client.Dial("utp", server.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	srvConn := <-accepted
	if srvConn == nil {
		t.Fatal("accept failed")
	}
	defer srvConn.Close()

	cid := conn.(*Conn).stream.CID()
	serverAddr := server.Addr().(*net.UDPAddr)
	quoted := quotedHeader(t, cid.Send)

	if !client.ProcessICMPFragmentation(quoted, serverAddr, 1300) {
		t.Fatal("ProcessICMPFragmentation did not match the connection that sent the quoted datagram")
	}
	if client.ProcessICMPFragmentation(quoted, nil, 1300) {
		t.Error("a nil address matched a connection")
	}
	if client.ProcessICMPError(quoted, nil) {
		t.Error("a nil address matched a connection")
	}
	if client.ProcessICMPFragmentation(quoted[:10], serverAddr, 1300) {
		t.Error("a runt quote matched a connection")
	}

	// The connection is still usable: a fragmentation report changes the
	// size of what is sent, not whether anything is sent.
	payload := []byte("after the report")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write after the report: %v", err)
	}
	if err := srvConn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(srvConn, buf); err != nil {
		t.Fatalf("read after the report: %v", err)
	}
	if string(buf) != string(payload) {
		t.Errorf("got %q, want %q", buf, payload)
	}
}
