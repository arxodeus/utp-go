package utp

import (
	"context"
	"sync"
	"testing"

	"github.com/bradfitz/iter"
	qt "github.com/frankban/quicktest"
)

// TestSendToRaceErrorAddr checks that an error returned by PacketConn.WriteTo
// does not keep a reference to an address buffer that later sends reuse.
func TestSendToRaceErrorAddr(t *testing.T) {
	c := qt.New(t)
	s, err := NewSocket("udp", "localhost:0")
	c.Assert(err, qt.IsNil)
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var wg sync.WaitGroup
	for range iter.N(2) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.DialContext(ctx, "udp", "1.1.1.1:1")
			c.Log(err.Error())
			c.Assert(err, qt.Not(qt.IsNil))
		}()
	}
	wg.Wait()
}
