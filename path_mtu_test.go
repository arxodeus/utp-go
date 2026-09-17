package utp_go

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// fakePathMTU is a Conn that answers PathMTU, so the composition rule can be
// tested without depending on whatever interfaces this machine happens to
// have.
type fakePathMTU struct {
	Conn
	mtu   int
	ok    bool
	local net.Addr
}

func (f *fakePathMTU) PathMTU(ConnectionPeer) (int, bool) { return f.mtu, f.ok }

// LocalAddr is forwarded explicitly: UtpSocket.LocalAddr duck-types for it,
// and embedding the Conn interface promotes only the methods Conn declares.
func (f *fakePathMTU) LocalAddr() net.Addr { return f.local }

// A Conn with no PathMTU method at all: the ordinary case, and the one that
// must be completely unaffected.
type plainConn struct{ Conn }

func pathMTUTestPeer(t *testing.T) ConnectionPeer {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", "203.0.113.7:6881")
	if err != nil {
		t.Fatal(err)
	}
	return NewUdpPeer(addr)
}

// The composition rule, which is the whole of the design decision: a
// discovered path MTU may lower the configured ceiling and may never raise
// it.
//
// libutp takes get_udp_mtu as the ceiling outright. Loopback reports 65536
// and a jumbo-frame link 9000, and adopting either would put this library on
// a probe schedule nothing has measured it at. Raising the ceiling is a
// separate decision from fixing the case where 1400 is too big.
func TestPathMTUCeilingOnlyLowers(t *testing.T) {
	peer := pathMTUTestPeer(t)
	const configured = uint16(1400)

	cases := []struct {
		name       string
		socket     Conn
		want       uint16
		wantReason string
	}{
		{"no PathMTU method", &plainConn{}, configured,
			"a Conn that cannot answer must be left exactly as it was"},
		{"provider declines", &fakePathMTU{ok: false, mtu: 1232}, configured,
			"a false second return means no answer, whatever the first says"},
		{"narrower path", &fakePathMTU{ok: true, mtu: 1232}, 1232,
			"an IPv6 minimum link is the case this exists for"},
		{"wireguard default", &fakePathMTU{ok: true, mtu: 1420 - 28}, 1392,
			"a tunnel a little under the configured ceiling"},
		{"wider path", &fakePathMTU{ok: true, mtu: 8972}, configured,
			"a jumbo-frame link must not raise the ceiling"},
		{"loopback", &fakePathMTU{ok: true, mtu: 65488}, configured,
			"nothing here wants a 65-kilobyte datagram"},
		{"exactly equal", &fakePathMTU{ok: true, mtu: int(configured)}, configured, ""},
		{"zero", &fakePathMTU{ok: true, mtu: 0}, configured,
			"a zero is not an answer"},
		{"negative", &fakePathMTU{ok: true, mtu: -1}, configured, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pathMTUCeiling(c.socket, peer, configured)
			if got != c.want {
				t.Errorf("ceiling %d, want %d: %s", got, c.want, c.wantReason)
			}
		})
	}

	// A nil peer is not a reason to guess.
	if got := pathMTUCeiling(&fakePathMTU{ok: true, mtu: 900}, nil, configured); got != configured {
		t.Errorf("ceiling %d for a nil peer, want %d", got, configured)
	}
}

// The header arithmetic, stated once rather than inferred from behaviour.
func TestDatagramSizeForInterfaceMTU(t *testing.T) {
	v4 := net.ParseIP("192.0.2.1")
	v6 := net.ParseIP("2001:db8::1")

	cases := []struct {
		mtu   int
		ip    net.IP
		want  int
		wantK bool
	}{
		{1500, v4, 1472, true},   // Ethernet
		{1420, v4, 1392, true},   // WireGuard's default
		{1280, v6, 1232, true},   // IPv6's floor
		{1500, v6, 1452, true},   // IPv6 over Ethernet: a bigger header
		{65536, v4, 65508, true}, // loopback
		{604, v4, 576, true},     // exactly the absolute floor
		{603, v4, 0, false},      // one below it
		{576, v4, 0, false},      // a link that cannot carry 576 of payload
		{0, v4, 0, false},
	}
	for _, c := range cases {
		got, ok := datagramSizeForInterfaceMTU(c.mtu, c.ip)
		if got != c.want || ok != c.wantK {
			t.Errorf("datagramSizeForInterfaceMTU(%d, %v) = (%d, %v), want (%d, %v)",
				c.mtu, c.ip, got, ok, c.want, c.wantK)
		}
	}
}

// Discovery against this machine, whatever it has. The value cannot be
// asserted -- it depends on the host -- but its relationship to the interface
// it came from can.
func TestPathMTUDiscoveryAgreesWithTheInterface(t *testing.T) {
	loopback, err := net.ResolveUDPAddr("udp", "127.0.0.1:9")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := pathMTUFor(loopback)
	if !ok {
		t.Skip("no route to loopback reported an interface MTU on this host")
	}

	// Find the loopback interface and check the arithmetic end to end.
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	var want int
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipNet, ok := a.(*net.IPNet); ok && ipNet.IP.Equal(net.IPv4(127, 0, 0, 1)) {
				want = iface.MTU - int(ipv4HeaderAndUDPOverhead)
			}
		}
	}
	if want == 0 {
		t.Skip("127.0.0.1 is not on any interface this host reports")
	}
	if got != want {
		t.Errorf("discovered %d for loopback, want %d (interface MTU less the "+
			"IPv4 and UDP headers)", got, want)
	}
	t.Logf("loopback carries %d-byte datagrams", got)
}

// End to end: a socket whose Conn reports a narrow path must build its
// connections with a correspondingly narrow ceiling, and must still work.
//
// The ceiling is read back through ConnectionMetrics, which is the only view
// of the search from outside.
func TestNarrowPathLowersTheConnectionCeiling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	lg := duplexQuietLog()

	// The narrow-path answer is stood in front of a real UDP socket before
	// the uTP socket is built over it. Wrapping s.socket afterwards would be
	// a write racing the read loop that is already reading it -- which the
	// race detector duly reported when this test did that.
	const narrow = 1232 // an IPv6 minimum link
	base, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	narrowConn := &fakePathMTU{
		Conn:  &UdpConn{base: base},
		mtu:   narrow,
		ok:    true,
		local: base.LocalAddr(),
	}
	sa := WithSocket(ctx, narrowConn, lg)
	defer sa.Close()

	sb, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close()

	aAddr := sa.LocalAddr().(*net.UDPAddr)
	bAddr := sb.LocalAddr().(*net.UDPAddr)

	cidA := NewConnectionId(NewUdpPeer(bAddr), 5000, 5001)
	cidB := NewConnectionId(NewUdpPeer(aAddr), 5001, 5000)

	// Collected under a lock rather than through a channel: the observer runs
	// on the connection's goroutine for as long as the connection lives, so
	// there is no moment at which the test can safely close a channel it is
	// still writing to.
	var (
		mu       sync.Mutex
		ceilings []uint32
	)
	cfg := NewConnectionConfig()
	configured := cfg.MaxPacketSize
	cfg.MetricsInterval = 5 * time.Millisecond
	cfg.Metrics = func(m ConnectionMetrics) {
		mu.Lock()
		ceilings = append(ceilings, m.MtuCeiling)
		mu.Unlock()
	}

	accepted := make(chan *UtpStream, 1)
	go func() {
		s, err := sb.AcceptWithCid(ctx, cidB, NewConnectionConfig())
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- s
	}()
	client, err := sa.ConnectWithCid(ctx, cidA, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	server := <-accepted
	if server == nil {
		t.Fatal("accept failed")
	}
	defer server.Close()
	defer client.Close()

	// The caller's config must come back untouched: it is theirs, and may be
	// reused for the next connection.
	if cfg.MaxPacketSize != configured {
		t.Errorf("the caller's config was modified: MaxPacketSize is now %d, was %d",
			cfg.MaxPacketSize, configured)
	}

	payload := make([]byte, 256*1024)
	writeDone := make(chan error, 1)
	go func() {
		_, err := client.Write(ctx, payload)
		writeDone <- err
	}()
	got := make([]byte, 0, len(payload))
	readCtx, readCancel := context.WithTimeout(ctx, 20*time.Second)
	defer readCancel()
	for len(got) < len(payload) {
		buf := make([]byte, 64*1024)
		n, err := server.Read(readCtx, buf)
		if err != nil {
			t.Fatalf("read after %d bytes: %v", len(got), err)
		}
		got = append(got, buf[:n]...)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write: %v", err)
	}

	mu.Lock()
	observed := append([]uint32(nil), ceilings...)
	mu.Unlock()

	sawNarrow := false
	var highest uint32
	for _, c := range observed {
		if c > highest {
			highest = c
		}
		if c == narrow {
			sawNarrow = true
		}
	}
	t.Logf("configured ceiling %d, discovered %d, highest observed %d",
		configured, narrow, highest)
	if !sawNarrow {
		t.Errorf("no sample reported the discovered ceiling of %d; highest was %d",
			narrow, highest)
	}
	if highest > narrow {
		t.Errorf("the ceiling reached %d, above the %d the path was said to carry",
			highest, narrow)
	}
}
