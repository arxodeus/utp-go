package utp

import (
	"context"
	"sync"
	"testing"
	"time"
)

// holdingConn is a scriptedConn that counts the peers it is told to hold.
type holdingConn struct {
	*scriptedConn
	mu   sync.Mutex
	held map[string]int
}

func (c *holdingConn) HoldPeer(peer ConnectionPeer) {
	c.mu.Lock()
	c.held[peer.Hash()]++
	c.mu.Unlock()
}

func (c *holdingConn) ReleasePeer(peer ConnectionPeer) {
	c.mu.Lock()
	c.held[peer.Hash()]--
	c.mu.Unlock()
}

func (c *holdingConn) holds() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, k := range c.held {
		n += k
	}
	return n
}

// A transport that keeps something per peer is told a dial's peer is in use
// from its first SYN, and let go of it when the dial is abandoned. See Conn.
func TestADialHoldsItsPeerUntilTheSocketForgetsIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn := &holdingConn{scriptedConn: newScriptedConn(), held: map[string]int{}}
	sock := WithSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	dial, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := sock.Connect(dial, conn.peer, NewConnectionConfig())
		done <- err
	}()
	awaitSyn(t, conn.scriptedConn)
	if n := conn.holds(); n != 1 {
		t.Fatalf("%d holds while the dial waits for an answer, want 1", n)
	}
	stop()
	<-done
	deadline := time.Now().Add(5 * time.Second)
	for conn.holds() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d holds left after the dial was abandoned", conn.holds())
		}
		time.Sleep(time.Millisecond)
	}
}
