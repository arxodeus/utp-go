package utp

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// udpPair is two sockets on loopback, b accepting.
func udpPair(t *testing.T) (a, b *Socket) {
	t.Helper()
	var err error
	a, err = NewSocket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { a.Close() })
	b, err = NewSocket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })
	return a, b
}

func dialAccept(t *testing.T, a, b *Socket) (dialed, accepted *Conn) {
	t.Helper()
	acc := make(chan net.Conn, 1)
	go func() {
		c, err := b.Accept()
		if err != nil {
			acc <- nil
			return
		}
		acc <- c
	}()
	d, err := a.DialTimeout(b.Addr().String(), 10*time.Second)
	require.NoError(t, err)
	c := <-acc
	require.NotNil(t, c)
	t.Cleanup(func() { d.Close(); c.Close() })
	return d.(*Conn), c.(*Conn)
}

// The synchronous firewall callback is asked about each connection request,
// with the requester's UDP address, and a refusal gets no answer: the dial
// runs into its own deadline.
func TestSyncFirewallRefusesWithoutAnswer(t *testing.T) {
	a, b := udpPair(t)
	var asked atomic.Value
	b.SetSyncFirewallCallback(func(addr net.Addr) bool {
		asked.Store(addr.String())
		_, ok := addr.(*net.UDPAddr)
		return ok
	})
	_, err := a.DialTimeout(b.Addr().String(), time.Second)
	require.Equal(t, context.DeadlineExceeded, err)
	require.Equal(t, a.Addr().String(), asked.Load())

	b.SetSyncFirewallCallback(nil)
	dialAccept(t, a, b)
}

// The asynchronous callback is used when there is no synchronous one.
func TestAsyncFirewallRefuses(t *testing.T) {
	a, b := udpPair(t)
	b.SetFirewallCallback(func(net.Addr) bool { return true })
	_, err := a.DialTimeout(b.Addr().String(), time.Second)
	require.Equal(t, context.DeadlineExceeded, err)
}

// Context options can be set and read back, invalid ones are refused, and
// they apply to connections created afterwards, including one accepted by an
// Accept that was already waiting when the option changed.
func TestOptionsReachNewConnections(t *testing.T) {
	a, b := udpPair(t)
	require.Equal(t, 1<<20, b.WriteBufferLen())
	require.Equal(t, 1<<20, b.ReadBufferLen())
	// One connection first, so that b's accept loop is waiting in a fresh
	// Accept, called with the defaults, when the options change.
	_, first := dialAccept(t, a, b)
	require.Equal(t, 1<<20, first.WriteBufferLen())
	time.Sleep(50 * time.Millisecond)

	require.Equal(t, 0, b.SetOption(SendBuffer, 50_000))
	require.Equal(t, 0, b.SetOption(RecvBuffer, 300_000))
	require.Equal(t, 0, b.SetOption(TargetDelay, 25_000))
	require.Equal(t, -1, b.SetOption(SendBuffer, 0))
	require.Equal(t, -1, b.SetOption(Option(99), 1))
	require.Equal(t, 50_000, b.WriteBufferLen())
	require.Equal(t, 300_000, b.ReadBufferLen())
	require.Panics(t, func() { b.SetWriteBufferLen(0) })

	cfg := b.us.ConnectionConfig()
	require.Equal(t, uint32(50_000), cfg.WindowSize)
	require.Equal(t, 300_000, cfg.BufferSize)
	require.Equal(t, 25*time.Millisecond, cfg.TargetDelay)
	require.Equal(t, 0, b.SetOption(TargetDelay, 0))
	require.Equal(t, 100*time.Millisecond, b.us.ConnectionConfig().TargetDelay,
		"a target of zero is apply_ccontrol's 100 ms")

	_, accepted := dialAccept(t, a, b)
	require.Equal(t, 50_000, accepted.WriteBufferLen())

	dialed, _ := dialAccept(t, b, a)
	require.Equal(t, 50_000, dialed.WriteBufferLen(), "a connection b dials takes b's options")
}

// SetWriteBufferLen changes a live connection's window ceiling.
func TestConnSetWriteBufferLen(t *testing.T) {
	a, b := udpPair(t)
	d, _ := dialAccept(t, a, b)
	require.Equal(t, 1<<20, d.WriteBufferLen())
	d.SetWriteBufferLen(64 * 1024)
	require.Equal(t, 64*1024, d.WriteBufferLen())
	require.Panics(t, func() { d.SetWriteBufferLen(0) })
}

// A send the operating system says can never succeed fails a dial at once,
// rather than after 21 seconds of retransmitted SYNs.
func TestUnsendableDialFailsAtOnce(t *testing.T) {
	s, err := NewSocket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer s.Close()
	start := time.Now()
	_, err = s.DialContext(context.Background(), "", "127.0.0.1:0")
	require.Error(t, err)
	require.Less(t, time.Since(start), 2*time.Second, "failed after %v: %v", time.Since(start), err)
	t.Logf("dial to port 0 failed after %v: %v", time.Since(start), err)
}

// The Socket is a PacketConn for everything that is not uTP, alongside its
// connections, with working deadlines.
func TestNonUTPDatagramsShareThePort(t *testing.T) {
	s, err := NewSocket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer s.Close()
	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	defer peer.Close()

	msg := []byte("d1:ad2:id20:abcdefghij0123456789e1:q4:ping1:t2:aa1:y1:qe")
	_, err = peer.WriteTo(msg, s.Addr())
	require.NoError(t, err)
	require.NoError(t, s.SetReadDeadline(time.Now().Add(5*time.Second)))
	buf := make([]byte, 1500)
	n, from, err := s.ReadFrom(buf)
	require.NoError(t, err)
	require.Equal(t, msg, buf[:n])
	require.Equal(t, peer.LocalAddr().String(), from.String())

	_, err = s.WriteTo([]byte("reply"), from)
	require.NoError(t, err)
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(5*time.Second)))
	n, _, err = peer.ReadFrom(buf)
	require.NoError(t, err)
	require.Equal(t, "reply", string(buf[:n]))

	require.NoError(t, s.SetReadDeadline(time.Now().Add(50*time.Millisecond)))
	_, _, err = s.ReadFrom(buf)
	var ne net.Error
	require.ErrorAs(t, err, &ne)
	require.True(t, ne.Timeout())
}

// Read and Write after Close return ErrConnClosed, as go-libutp's do.
func TestConnClosedErrors(t *testing.T) {
	a, b := udpPair(t)
	d, _ := dialAccept(t, a, b)
	require.NoError(t, d.Close())
	_, err := d.Write([]byte("x"))
	require.Equal(t, ErrConnClosed, err)
	_, err = d.Read(make([]byte, 1))
	require.Equal(t, ErrConnClosed, err)
}

// A connection's data goes both ways, and the connection leaves the socket's
// books once both ends have closed.
func TestTransferAndTeardown(t *testing.T) {
	a, b := udpPair(t)
	d, c := dialAccept(t, a, b)
	payload := bytes.Repeat([]byte("0123456789abcdef"), 64*1024)
	go func() {
		_, _ = d.Write(payload)
		d.Close()
	}()
	var got bytes.Buffer
	_, err := got.ReadFrom(c)
	require.NoError(t, err)
	require.True(t, bytes.Equal(payload, got.Bytes()))
	c.Close()
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(a.conns) == 0 && len(b.conns) == 0
	}, 10*time.Second, 10*time.Millisecond)
}

// WriteStatus lists each open socket in go-libutp's format.
func TestWriteStatus(t *testing.T) {
	a, b := udpPair(t)
	dialAccept(t, a, b)
	var out strings.Builder
	WriteStatus(&out)
	assert.Contains(t, out.String(), "listening at "+b.Addr().String()+"\nhas 1 conns\nbacklog: 0\n")
}

// Log options enable more verbose logging.
func TestLogOptions(t *testing.T) {
	s, err := NewSocket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer s.Close()
	h := &logHandler{s: s}
	require.False(t, h.Enabled(context.Background(), -4)) // debug
	require.Equal(t, 0, s.SetOption(LogMtu, 1))
	require.True(t, h.Enabled(context.Background(), -4))
	require.Equal(t, 0, s.SetOption(LogMtu, 0))
	require.False(t, h.Enabled(context.Background(), -4))
}

// A dial completes whether or not anything is waiting in Accept, and Accept
// then returns the connection: the engine answers a SYN as it arrives on a
// socket with its own configuration (utp.WithAcceptConfig).
func TestDialCompletesBeforeAccept(t *testing.T) {
	a, b := udpPair(t)
	d, err := a.DialTimeout(b.Addr().String(), 5*time.Second)
	require.NoError(t, err)
	defer d.Close()
	c, err := b.Accept()
	require.NoError(t, err)
	defer c.Close()
	require.Equal(t, d.LocalAddr().String(), c.RemoteAddr().String())
}
