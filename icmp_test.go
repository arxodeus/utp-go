package utp

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

func icmpQuietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 4}))
}

// quotedDatagram builds the bytes an ICMP message would quote: a uTP datagram
// this side sent, carrying connId. Routers quote the start of the offending
// datagram, so 20 bytes of header is all a caller can rely on.
func quotedDatagram(t *testing.T, pktType packetType, connId uint16) []byte {
	t.Helper()
	p := newPacketBuilder(pktType, connId, uint32(time.Now().UnixMicro()), 1024*1024, 1).Build()
	encoded := p.Encode()
	if len(encoded) < minimalHeaderSize {
		t.Fatalf("encoded packet is %d bytes, shorter than a header", len(encoded))
	}
	// Quote only the header, which is the least a router will send.
	return encoded[:minimalHeaderSize]
}

// icmpPair brings up a connected pair over loopback UDP and returns the
// initiator's socket, its stream, the acceptor's stream, and the initiator's
// connection id.
type icmpPair struct {
	sa, sb   *UtpSocket
	client   *UtpStream
	server   *UtpStream
	cidA     *ConnectionId
	peerB    ConnectionPeer
	metrics  chan ConnectionMetrics
	shutdown func()
}

func newICMPPair(t *testing.T, ctx context.Context) *icmpPair {
	t.Helper()
	lg := icmpQuietLog()
	sa, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		sa.Close()
		t.Fatal(err)
	}
	aAddr := sa.LocalAddr().(*net.UDPAddr)
	bAddr := sb.LocalAddr().(*net.UDPAddr)

	cidA := NewConnectionId(NewUdpPeer(bAddr), 2000, 2001)
	cidB := NewConnectionId(NewUdpPeer(aAddr), 2001, 2000)

	// The observer runs on the connection's goroutine, so it must not block;
	// a buffered channel that drops the oldest sample keeps the latest.
	samples := make(chan ConnectionMetrics, 256)
	cfg := NewConnectionConfig()
	cfg.Metrics = func(m ConnectionMetrics) {
		select {
		case samples <- m:
		default:
			select {
			case <-samples:
			default:
			}
			select {
			case samples <- m:
			default:
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	var server *UtpStream
	var serverErr error
	go func() {
		defer wg.Done()
		server, serverErr = sb.AcceptWithCid(ctx, cidB, NewConnectionConfig())
	}()

	client, err := sa.ConnectWithCid(ctx, cidA, cfg)
	if err != nil {
		sa.Close()
		sb.Close()
		t.Fatalf("connect: %v", err)
	}
	wg.Wait()
	if serverErr != nil {
		sa.Close()
		sb.Close()
		t.Fatalf("accept: %v", serverErr)
	}

	return &icmpPair{
		sa: sa, sb: sb,
		client: client, server: server,
		cidA:    cidA,
		peerB:   NewUdpPeer(bAddr),
		metrics: samples,
		shutdown: func() {
			sa.Close()
			sb.Close()
		},
	}
}

// latestCeiling waits for a metrics sample whose MTU ceiling satisfies want,
// and reports the last ceiling it saw either way.
func latestCeiling(t *testing.T, samples <-chan ConnectionMetrics, want func(uint32) bool, timeout time.Duration) (uint32, bool) {
	t.Helper()
	deadline := time.After(timeout)
	var last uint32
	for {
		select {
		case m := <-samples:
			last = m.MtuCeiling
			if want(m.MtuCeiling) {
				return m.MtuCeiling, true
			}
		case <-deadline:
			return last, false
		}
	}
}

// A router's fragmentation-needed report reaches the connection that sent the
// offending datagram and lowers its path-MTU ceiling.
func TestProcessICMPFragmentationLowersTheCeiling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p := newICMPPair(t, ctx)
	defer p.shutdown()

	// Record the ceiling before the report, so the test cannot pass on a
	// ceiling that was already low.
	before, ok := latestCeiling(t, p.metrics, func(c uint32) bool { return c > 0 }, 5*time.Second)
	if !ok {
		t.Fatalf("no metrics sample with an MTU ceiling in 5s")
	}
	const linkMTU = 1300
	const wantCeiling = linkMTU - ipv4HeaderAndUDPOverhead
	if before <= wantCeiling {
		t.Fatalf("ceiling was already %d, at or below the %d this test lowers it to; the test would prove nothing",
			before, wantCeiling)
	}

	quoted := quotedDatagram(t, st_data, p.cidA.Send)
	if !p.sa.ProcessICMPFragmentation(quoted, p.peerB, linkMTU) {
		t.Fatal("ProcessICMPFragmentation did not recognise a datagram this socket's own connection sent")
	}

	// Keep the connection producing samples: metrics are sampled by the
	// event loop, which needs something to do.
	go func() {
		buf := make([]byte, 4096)
		for ctx.Err() == nil {
			if _, err := p.client.Write(ctx, buf); err != nil {
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 4096)
		for ctx.Err() == nil {
			if _, err := p.server.Read(ctx, buf); err != nil {
				return
			}
		}
	}()

	got, ok := latestCeiling(t, p.metrics, func(c uint32) bool { return c <= wantCeiling }, 10*time.Second)
	if !ok {
		t.Fatalf("ceiling still %d after the ICMP report; want at most %d (a %d-byte link less the IPv4 and UDP headers), was %d before",
			got, wantCeiling, linkMTU, before)
	}
	t.Logf("ceiling %d -> %d after ICMP fragmentation-needed for a %d-byte link", before, got, linkMTU)
}

// An ICMP error for an established connection ends it, and the application
// learns of it.
func TestProcessICMPErrorResetsAnEstablishedConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p := newICMPPair(t, ctx)
	defer p.shutdown()

	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 1024)
		_, err := p.client.Read(ctx, buf)
		readErr <- err
	}()

	quoted := quotedDatagram(t, st_data, p.cidA.Send)
	if !p.sa.ProcessICMPError(quoted, p.peerB) {
		t.Fatal("ProcessICMPError did not recognise a datagram this socket's own connection sent")
	}

	select {
	case err := <-readErr:
		if !errors.Is(err, ErrReset) {
			t.Errorf("read returned %v, want %v", err, ErrReset)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the reader did not learn of the ICMP error in 10s")
	}
}

// An ICMP error while only the SYN has been sent means nothing is listening,
// which is reported as a refusal rather than a reset, and arrives long before
// the connection attempts would have run out.
func TestProcessICMPErrorRefusesAPendingConnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	lg := icmpQuietLog()

	sa, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()

	// A bound, silent UDP socket: the SYN reaches it and nothing answers, as
	// with a connect to a closed port until ICMP arrives.
	silent, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	peer := NewUdpPeer(silent.LocalAddr().(*net.UDPAddr))

	cfg := NewConnectionConfig()
	cid := NewConnectionId(peer, 3000, 3001)

	connectDone := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := sa.ConnectWithCid(ctx, cid, cfg)
		connectDone <- err
	}()

	// A SYN carries the sender's own receive id, so that is what the router
	// would quote back.
	quoted := quotedDatagram(t, st_syn, cid.Recv)

	// Wait until the connection exists to be found, then report the error.
	// Retrying keeps the test from depending on how fast Connect gets going.
	deadline := time.After(5 * time.Second)
	for {
		if sa.ProcessICMPError(quoted, peer) {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no connection matched the quoted SYN within 5s")
		case <-time.After(5 * time.Millisecond):
		}
	}

	select {
	case err := <-connectDone:
		elapsed := time.Since(start)
		if !errors.Is(err, ErrConnRefused) {
			t.Fatalf("connect returned %v after %v, want %v", err, elapsed, ErrConnRefused)
		}
		// Three SYN attempts at the default timeout take far longer; if the
		// answer took that long the ICMP path did nothing.
		if elapsed > 2*time.Second {
			t.Errorf("connect took %v to be refused; the ICMP report should end it at once", elapsed)
		}
		t.Logf("connect refused after %v", elapsed)
	case <-time.After(20 * time.Second):
		t.Fatal("connect never returned")
	}
}

// What must be ignored. ICMP is trivially forged, so each of these would
// otherwise let an off-path sender tear down or shrink a connection.
func TestProcessICMPIgnoresWhatItCannotMatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p := newICMPPair(t, ctx)
	defer p.shutdown()

	good := quotedDatagram(t, st_data, p.cidA.Send)

	runt := good[:minimalHeaderSize-1]
	badVersion := append([]byte(nil), good...)
	badVersion[0] = (badVersion[0] & 0xF0) | 2
	badType := append([]byte(nil), good...)
	badType[0] = 0xF0 | (badType[0] & 0x0F)
	unknownId := quotedDatagram(t, st_data, p.cidA.Send+64)

	otherAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	otherPeer := NewUdpPeer(otherAddr)

	cases := []struct {
		name   string
		quoted []byte
		peer   ConnectionPeer
	}{
		{"runt", runt, p.peerB},
		{"empty", nil, p.peerB},
		{"wrong version", badVersion, p.peerB},
		{"unknown packet type", badType, p.peerB},
		{"unknown connection id", unknownId, p.peerB},
		{"right id, wrong peer", good, otherPeer},
		{"nil peer", good, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if p.sa.ProcessICMPError(c.quoted, c.peer) {
				t.Error("ProcessICMPError accepted it")
			}
			if p.sa.ProcessICMPFragmentation(c.quoted, c.peer, 1300) {
				t.Error("ProcessICMPFragmentation accepted it")
			}
		})
	}

	// The connection is still alive and carrying data, so "ignored" is
	// distinguishable from "acted on".
	payload := []byte("still here")
	if _, err := p.client.Write(ctx, payload); err != nil {
		t.Fatalf("write after the ignored reports: %v", err)
	}
	buf := make([]byte, len(payload))
	readCtx, readCancel := context.WithTimeout(ctx, 10*time.Second)
	defer readCancel()
	if _, err := p.server.Read(readCtx, buf); err != nil {
		t.Fatalf("read after the ignored reports: %v", err)
	}
}

// A datagram quoted from a connection this socket accepted matches too.
func TestProcessICMPMatchesAnAcceptedConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p := newICMPPair(t, ctx)
	defer p.shutdown()

	// sb accepted, so its send id is cidB.Send, which is cidA.Recv.
	quoted := quotedDatagram(t, st_data, p.cidA.Recv)

	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 1024)
		_, err := p.server.Read(ctx, buf)
		readErr <- err
	}()

	aAddr := p.sa.LocalAddr().(*net.UDPAddr)
	if !p.sb.ProcessICMPError(quoted, NewUdpPeer(aAddr)) {
		t.Fatal("the acceptor's socket did not match a datagram its own connection sent")
	}

	select {
	case err := <-readErr:
		if !errors.Is(err, ErrReset) {
			t.Errorf("read returned %v, want %v", err, ErrReset)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the acceptor did not learn of the ICMP error in 10s")
	}
}
