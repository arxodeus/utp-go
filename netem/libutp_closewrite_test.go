//go:build cgo

package netem

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
	"github.com/zen-eth/utp-go/native/libutp"
)

// The half-close against real libutp, from the side that initiates it.
//
// TestPeerMayWriteAfterOurFin covers the same exchange with Close(), where the
// application has said it is finished entirely and what libutp sends back is
// discarded -- correctly, because there is nobody left to give it to. This is
// the same exchange with CloseWrite(), where there is: we finish sending, and
// libutp's reply has to arrive.
//
// That is what libutp's own applications have always been able to do
// (`utp_shutdown(s, SHUT_WR)`, utp.h:176) and what this library could not.

func TestCloseWriteDeliversWhatThePeerSendsAfterIt(t *testing.T) {
	n := NewNetwork(102)
	defer n.Close()
	ours := n.MustAddEndpoint("a")
	theirs := n.MustAddEndpoint("b")
	n.Connect(ours, theirs, Config{
		Delay: 10 * time.Millisecond, BandwidthBps: 20_000_000, QueueBytes: 64 * 1024,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const connSeed = 7950
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
	if err := stream.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}

	// Read the reply libutp sent after our FIN. This is the whole point: the
	// connection is still ours to read from.
	got := make([]byte, 0, len(reply))
	readCtx, readCancel := context.WithTimeout(ctx, 20*time.Second)
	defer readCancel()
	buf := make([]byte, 8192)
	for len(got) < len(reply) {
		nr, err := stream.Read(readCtx, buf)
		if err != nil {
			t.Fatalf("reading after CloseWrite: %v (%d of %d bytes so far); the peer's reply "+
				"was sent after our FIN, which is exactly what a half-close is for",
				err, len(got), len(reply))
		}
		got = append(got, buf[:nr]...)
	}
	stream.Close()

	mu.Lock()
	read, wrote, code := libutpRead, wroteBack, finalErr
	mu.Unlock()

	if read != len(greeting) {
		t.Errorf("libutp read %d bytes of our %d before we half-closed", read, len(greeting))
	}
	if !wrote {
		t.Fatal("libutp never reached end of stream on its read side, so it never sent the " +
			"reply this test exists to receive")
	}
	if !bytes.Equal(got, reply) {
		t.Fatalf("received %d bytes after CloseWrite but they do not match what libutp sent",
			len(got))
	}
	if code != 0 {
		t.Errorf("libutp reported error code %d after writing past our FIN", code)
	}
	if resets := sock.PacketsResetSent(); resets != 0 {
		t.Errorf("this socket sent %d RESETs to a peer writing after our half-close", resets)
	}

	t.Logf("half-close against real libutp: it read our %d bytes, saw end of stream, and sent "+
		"%d bytes back which we read in full -- the case that used to be discarded",
		read, len(got))
}
