package utp

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnMethodsAfterClose(t *testing.T) {
	s, err := NewSocket("udp", "localhost:0")
	require.NoError(t, err)
	defer s.Close()
	d, a := connPairSocket(s)
	// Closing the parent Socket is the fastest way to end the Conns.
	assert.NoError(t, s.Close())
	for _, c := range []net.Conn{d, a} {
		// Test a Conn whose connection no longer exists. Under mu: the
		// goroutine that waits for the stream to end clears it too.
		mu.Lock()
		us := c.(*Conn).us
		mu.Unlock()
		assert.Nil(t, us)
		// These must not panic; their results do not matter.
		assert.NotPanics(t, func() { c.RemoteAddr() })
		assert.NotPanics(t, func() { c.LocalAddr() })
	}
}
