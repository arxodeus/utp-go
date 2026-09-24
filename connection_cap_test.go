package utp_go

import (
	"context"
	"testing"
	"time"
)

// The incoming-connection cap: libutp refuses a SYN when its context already
// holds more than 3000 sockets (utp_internal.cpp:2967-2974). See
// WithMaxConnections.

// capTestSocket is a socket on a scripted transport with the given options,
// and a function that sends it a SYN for a new connection id.
func capTestSocket(t *testing.T, opts ...SocketOption) (*UtpSocket, *scriptedConn, func(id uint16)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	conn := newScriptedConn()
	sock := WithSocket(ctx, conn, conformanceLogger(), opts...)
	t.Cleanup(sock.Close)
	syn := func(id uint16) {
		conn.inject(NewPacketBuilder(st_syn, id, 100000, 1<<20, 900).Build().Encode())
	}
	return sock, conn, syn
}

// settled waits until the socket has dealt with every SYN sent so far, which
// is when each has either been parked or refused.
func settled(t *testing.T, sock *UtpSocket, sent int) {
	t.Helper()
	waitFor(t, 2*time.Second, func() bool {
		return sock.incomingConns.len()+int(sock.ConnectionsRefusedAtCapacity()) >= sent
	})
}

// Past the cap a SYN is refused, silently, and nothing is created for it. The
// comparison is libutp's: refused while the socket holds more than the cap,
// so the cap itself admits one more.
func TestConnectionCapRefusesPastTheCap(t *testing.T) {
	sock, conn, syn := capTestSocket(t, WithMaxConnections(2))

	for i, id := range []uint16{100, 200, 300} {
		syn(id)
		settled(t, sock, i+1)
	}
	if n := sock.ConnectionsRefusedAtCapacity(); n != 0 {
		t.Fatalf("refused %d of three SYNs under a cap of 2; libutp refuses only while "+
			"it holds *more than* the cap (`GetCount() > 3000`)", n)
	}
	if n := sock.incomingConns.len(); n != 3 {
		t.Fatalf("%d SYNs parked, want 3", n)
	}

	syn(400)
	settled(t, sock, 4)
	if n := sock.ConnectionsRefusedAtCapacity(); n != 1 {
		t.Fatalf("the fourth SYN, with three held against a cap of 2: refused %d, want 1", n)
	}
	if n := sock.incomingConns.len(); n != 3 {
		t.Errorf("a refused SYN left state behind: %d parked, want 3", n)
	}
	if emitted := conn.takeEmitted(); len(emitted) != 0 {
		t.Errorf("answered at capacity with %s; libutp refuses with a bare `return 1`",
			describePackets(emitted))
	}
}

// A SYN for a connection already parked is the peer retransmitting it, not a
// new connection, and is not refused however full the socket is.
func TestConnectionCapIgnoresARetransmittedSyn(t *testing.T) {
	sock, _, syn := capTestSocket(t, WithMaxConnections(1))

	syn(100)
	syn(200)
	settled(t, sock, 2)
	syn(300)
	settled(t, sock, 3)
	if n := sock.ConnectionsRefusedAtCapacity(); n != 1 {
		t.Fatalf("setup: refused %d, want 1", n)
	}

	syn(100)
	time.Sleep(50 * time.Millisecond)
	if n := sock.ConnectionsRefusedAtCapacity(); n != 1 {
		t.Errorf("a retransmitted SYN for a parked connection was refused as a new one")
	}
}

// Capacity comes back when a connection goes, because the count is of what
// the socket holds now.
func TestConnectionCapFreesAsConnectionsGo(t *testing.T) {
	sock, _, syn := capTestSocket(t, WithMaxConnections(1))

	syn(100)
	syn(200)
	settled(t, sock, 2)
	syn(300)
	settled(t, sock, 3)
	if n := sock.ConnectionsRefusedAtCapacity(); n != 1 {
		t.Fatalf("setup: refused %d, want 1", n)
	}

	var key string
	sock.incomingConns.Range(func(k any, _ *IncomingPacket) bool {
		key = k.(string)
		return false
	})
	sock.removeIncomingConn(key)

	syn(400)
	waitFor(t, 2*time.Second, func() bool { return sock.incomingConns.len() == 2 })
	if n := sock.ConnectionsRefusedAtCapacity(); n != 1 {
		t.Errorf("with a slot freed, a new SYN was still refused (%d refusals)", n)
	}
}

// Connections this socket dialled count, as every socket in a libutp context
// does.
func TestConnectionCapCountsDialledConnections(t *testing.T) {
	sock, conn, syn := capTestSocket(t, WithMaxConnections(1))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = sock.ConnectWithCid(ctx, NewConnectionId(conn.peer, 5000, 5001), NewConnectionConfig())
	}()
	waitFor(t, 2*time.Second, func() bool { return sock.NumConnections() == 1 })

	syn(100)
	settled(t, sock, 1)
	syn(200)
	settled(t, sock, 2)
	if n := sock.ConnectionsRefusedAtCapacity(); n != 1 {
		t.Errorf("one dialled and one parked against a cap of 1: refused %d, want 1 -- "+
			"a dialled connection is not being counted", n)
	}
}

// A negative cap is no cap.
func TestConnectionCapCanBeRemoved(t *testing.T) {
	sock, _, syn := capTestSocket(t, WithMaxConnections(-1))
	for i := 0; i < 20; i++ {
		syn(uint16(100 + 10*i))
	}
	waitFor(t, 2*time.Second, func() bool { return sock.incomingConns.len() == 20 })
	if n := sock.ConnectionsRefusedAtCapacity(); n != 0 {
		t.Errorf("refused %d with the cap removed", n)
	}
}

// Without the option, and with it set to zero, the cap is libutp's.
func TestConnectionCapDefaultsToLibutps(t *testing.T) {
	for name, opts := range map[string][]SocketOption{
		"no option": nil,
		"zero":      {WithMaxConnections(0)},
	} {
		sock, _, _ := capTestSocket(t, opts...)
		if sock.maxConns != 3000 {
			t.Errorf("%s: cap %d, want libutp's 3000 (utp_internal.cpp:2967)", name, sock.maxConns)
		}
	}
}
