package utp

// Adapted from go-libutp's accept_write_test.go (v1.5.1). There, an accepted
// connection cannot write until the initiator's first data packet arrives,
// and the test checks that the write waits and then completes. Here an
// accepted connection is established by the handshake, so the write
// completes at once; this checks that the bytes arrive before the dialer has
// sent anything.

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// gatedPacketConn lets packets be received only when a token is available,
// so a test controls exactly when the wrapped Socket processes what it has
// been sent. Closing tokens lets everything through.
type gatedPacketConn struct {
	net.PacketConn
	tokens chan struct{}
}

func (me gatedPacketConn) ReadFrom(b []byte) (n int, addr net.Addr, err error) {
	<-me.tokens
	n, addr, err = me.PacketConn.ReadFrom(b)
	return
}

func TestWriteOnAcceptedConnBeforeEstablished(t *testing.T) {
	dialerPc, err := listenPacket("inproc", "")
	require.NoError(t, err)
	acceptorPc, err := listenPacket("inproc", "")
	require.NoError(t, err)
	// Only the initiating SYN gets through until we release the gate.
	tokens := make(chan struct{}, 1)
	tokens <- struct{}{}

	dialer, err := NewSocketFromPacketConn(dialerPc)
	require.NoError(t, err)
	defer dialer.Close()
	acceptor, err := NewSocketFromPacketConn(gatedPacketConn{acceptorPc, tokens})
	require.NoError(t, err)
	defer acceptor.Close()

	dialed := make(chan net.Conn, 1)
	go func() {
		c, err := dialer.Dial(acceptor.Addr().String())
		if err != nil {
			t.Errorf("dialing: %v", err)
			close(dialed)
			return
		}
		dialed <- c
	}()

	accepted, err := acceptor.Accept()
	require.NoError(t, err)
	defer accepted.Close()

	// The acceptor has seen nothing after the SYN, and the write completes
	// anyway.
	written := make(chan error, 1)
	go func() {
		_, err := accepted.Write([]byte("hello"))
		written <- err
	}()
	select {
	case err := <-written:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("write on an accepted connection did not complete")
	}

	c := <-dialed
	require.NotNil(t, c)
	defer c.Close()
	require.NoError(t, c.SetReadDeadline(time.Now().Add(10*time.Second)))
	got := make([]byte, 5)
	_, err = io.ReadFull(c, got)
	require.NoError(t, err)
	require.Equal(t, "hello", string(got))
	close(tokens)
}
