package utp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A Socket.Close that lands between a connection's publication and the end
// of newConn must not leave newConn reading the stream Close took away: it
// read c.us outside the lock, after Close had set it to nil, and panicked.
func TestCloseWhileAConnIsBeingPublished(t *testing.T) {
	a, b := udpPair(t)
	testHookConnPublished = func(s *Socket) {
		if s == a {
			s.Close()
		}
	}
	defer func() { testHookConnPublished = nil }()

	go func() {
		if c, err := b.Accept(); err == nil {
			c.Close()
		}
	}()
	c, err := a.Dial(b.Addr().String())
	require.NoError(t, err)
	c.Close()
}
