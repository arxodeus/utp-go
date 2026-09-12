//go:build cgo

package netem

import (
	"context"
	"sync"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
	"github.com/zen-eth/utp-go/native/libutp"
)

// What a peer's half-close actually costs, measured rather than assumed.
//
// DEVIATIONS.md records that this library has no half-close: on reaching the
// peer's FIN it tears the connection down, where libutp keeps its socket in
// CS_GOT_FIN until its own application closes too. That entry had no
// measurement behind it, and "libutp has a feature we lack" is not by itself a
// reason to build the feature.
//
// This drives the case from the only side that can start it. Our API has no
// CloseWrite -- Close means done entirely -- so an application using this
// library cannot half-close. Only the peer can, and only after we have sent
// our FIN. So: we write, we Close, and libutp then writes back, which is
// exactly what its half-close is for.
//
// It did cost compatibility, and this is what found it: before the socket
// learned to stay silent for data past a closed connection's FIN, this failed
// three times out of three with libutp reporting UTP_ECONNRESET and one RESET
// sent from here. A peer doing something entirely legitimate was told its
// connection had broken. Both assertions below are for that, and they are the
// reason this test is worth its runtime.
//
// What is left is the half-close itself. libutp writes and we discard, because
// our application said it was finished and there is nobody to give it to. That
// costs an API capability on our side, not compatibility on the wire.
// Implementing it means a new public method, a connection that keeps sending
// after the peer's FIN, and a reader that sees end of stream while the writer
// carries on -- a feature, not a conformance fix. It stays unbuilt, and this
// is the evidence for that being a reasonable choice rather than an oversight.
func TestPeerMayWriteAfterOurFin(t *testing.T) {
	n := NewNetwork(101)
	defer n.Close()
	ours := n.MustAddEndpoint("a")
	theirs := n.MustAddEndpoint("b")
	n.Connect(ours, theirs, Config{
		Delay: 10 * time.Millisecond, BandwidthBps: 20_000_000, QueueBytes: 64 * 1024,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const connSeed = 7900
	reply := make([]byte, 4096)
	for i := range reply {
		reply[i] = byte(i * 7)
	}

	drv, err := libutp.NewDriver(uint64(time.Now().UnixMicro()))
	if err != nil {
		t.Fatal(err)
	}
	drv.PushRandom(connSeed)
	drv.Listen()

	// The driver is a C object freed by Close, and the pump goroutine below is
	// the only thing that touches it. It has to be stopped and joined first --
	// a deferred drv.Close() alone runs while the pump is still ticking, and
	// the next SetTime dereferences a freed handle. (It does: this test
	// segfaulted in 5 of 12 runs before the goroutine was joined.) ctx is not
	// enough either, because its cancel is deferred after this and so runs
	// later.
	stopPump := make(chan struct{})
	var pump sync.WaitGroup
	defer func() {
		close(stopPump)
		pump.Wait()
		drv.Close()
	}()

	inbox := make(chan []byte, 4096)
	go func() {
		buf := make([]byte, 65536)
		for {
			nb, _, err := theirs.ReadFrom(buf)
			if err != nil {
				return
			}
			p := make([]byte, nb)
			copy(p, buf[:nb])
			select {
			case inbox <- p:
			case <-ctx.Done():
				return
			}
		}
	}()

	var (
		mu         sync.Mutex
		libutpRead int
		wroteBack  bool
		finalErr   int
	)

	pump.Add(1)
	go func() {
		defer pump.Done()
		tick := time.NewTicker(200 * time.Microsecond)
		defer tick.Stop()
		readBuf := make([]byte, 64*1024)
		for {
			select {
			case <-stopPump:
				return
			case <-ctx.Done():
				return
			case pkt := <-inbox:
				drv.SetTime(uint64(time.Now().UnixMicro()))
				drv.Inject(pkt)
				drv.IssueAcks()
			case <-tick.C:
				drv.SetTime(uint64(time.Now().UnixMicro()))
				drv.CheckTimeouts()
			}
			for {
				nr := drv.Read(readBuf)
				if nr == 0 {
					break
				}
				mu.Lock()
				libutpRead += nr
				mu.Unlock()
			}
			mu.Lock()
			// StateEOF means libutp has taken our FIN. Its application is
			// still entitled to write, which is the half-close.
			if !wroteBack && drv.State() == libutp.StateEOF {
				wroteBack = true
				_, _ = drv.Write(reply)
			}
			finalErr = drv.Err()
			mu.Unlock()
			for _, out := range drv.Emitted() {
				_, _ = theirs.WriteTo(out, ours.Addr())
			}
			drv.ClearEmitted()
		}
	}()

	sock := utp.WithSocket(ctx, ours, quiet())
	defer sock.Close()
	cid := utp.NewConnectionId(theirs.Addr(), connSeed, connSeed+1)
	stream, err := sock.ConnectWithCid(ctx, cid, utp.NewConnectionConfig())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	const greeting = "hello from our side"
	if _, err := stream.Write(ctx, []byte(greeting)); err != nil {
		t.Fatalf("write: %v", err)
	}
	stream.Close()

	// Long enough for libutp to notice the FIN, write back, and retransmit if
	// it thought that had failed.
	time.Sleep(3 * time.Second)

	mu.Lock()
	read, wrote, code := libutpRead, wroteBack, finalErr
	mu.Unlock()

	if read != len(greeting) {
		t.Errorf("libutp read %d bytes of our %d; the transfer before the close did not complete",
			read, len(greeting))
	}
	if !wrote {
		t.Fatal("libutp never reached end of stream on its read side, so the half-close this " +
			"test exists to exercise never happened")
	}
	// The point of the measurement: the peer writing after our FIN is not an
	// error for it, and does not draw a reset from us.
	if code != 0 {
		t.Errorf("libutp reported error code %d after writing past our FIN; writing after a "+
			"peer's FIN is what its half-close is for and must not fail", code)
	}
	if resets := sock.PacketsResetSent(); resets != 0 {
		t.Errorf("this socket sent %d RESETs to a peer exercising its half-close; discarding "+
			"what we have nobody to give to is one thing, telling the peer its connection "+
			"broke is another", resets)
	}

	t.Logf("libutp read %d bytes, then wrote past our FIN: no error (code %d), no RESET from us, "+
		"and nothing delivered here because Close() had already said we were finished",
		read, code)
}
