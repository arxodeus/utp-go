//go:build cgo

package libutp_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
	"github.com/zen-eth/utp-go/native/libutp"
)

// Interop with more than one libutp peer at a time.
//
// The M7 gate proves a single connection completes in each direction. That is
// not the shape a BitTorrent client runs: it holds tens of connections on one
// UDP port, all live at once, and every packet arriving on that port has to be
// routed to the right one by connection id alone. A demultiplexing bug, an
// accept that starves under inbound load, or state shared between connections
// that should not be, shows up here and nowhere in the single-connection gate.
//
// Each peer gets a payload stamped with its own index, so a byte delivered to
// the wrong connection is a failure with a name rather than a length mismatch.

const (
	concurrentPeers       = 16
	concurrentPayloadSize = 256 * 1024
)

// stampedPayload builds a payload no other index could produce: every 8-byte
// word carries the index, so a fragment delivered to the wrong stream names
// the stream it came from.
func stampedPayload(index int, size int) []byte {
	b := make([]byte, size)
	for off := 0; off+8 <= size; off += 8 {
		binary.BigEndian.PutUint64(b[off:], uint64(index)<<32|uint64(off))
	}
	return b
}

// payloadIndex recovers the index a payload was stamped with.
func payloadIndex(b []byte) (int, error) {
	if len(b) < 8 {
		return 0, fmt.Errorf("payload is %d bytes, too short to carry a stamp", len(b))
	}
	return int(binary.BigEndian.Uint64(b[:8]) >> 32), nil
}

// libutp initiating, all at once, into one socket of ours.
//
// This is the direction a seeding client sees: many peers arrive on one port
// and every one of them has to be accepted and kept apart.
func TestInteropConcurrentLibutpInitiators(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	sock, goPort := bindGoSocket(t, ctx)
	defer sock.Close()

	t.Logf("%d libutp initiators into our socket on :%d", concurrentPeers, goPort)

	type received struct {
		index int
		size  int
		err   error
	}
	results := make(chan received, concurrentPeers)

	// Accept loop: it cannot know which peer it is answering, so it decides
	// from the payload.
	var accepting sync.WaitGroup
	for i := 0; i < concurrentPeers; i++ {
		accepting.Add(1)
		go func() {
			defer accepting.Done()
			stream, err := sock.Accept(ctx, utp.NewConnectionConfig())
			if err != nil {
				results <- received{err: fmt.Errorf("accept: %w", err)}
				return
			}
			defer stream.Close()
			buf := make([]byte, 0, concurrentPayloadSize)
			n, err := stream.ReadToEOF(ctx, &buf)
			if err != nil && err != io.EOF {
				results <- received{err: fmt.Errorf("read: %w", err)}
				return
			}
			got := buf[:min(n, len(buf))]
			index, err := payloadIndex(got)
			if err != nil {
				results <- received{err: err}
				return
			}
			if want := stampedPayload(index, len(got)); !bytes.Equal(got, want) {
				results <- received{err: fmt.Errorf(
					"stream carrying peer %d's stamp does not match peer %d's payload "+
						"(%d bytes): %s", index, index, len(got), describeStamps(got))}
				return
			}
			results <- received{index: index, size: len(got)}
		}()
	}

	// Let the accepts register before any SYN arrives; an accept that has not
	// been posted yet is a different case, covered elsewhere.
	time.Sleep(200 * time.Millisecond)

	peers := make([]*libutp.Peer, 0, concurrentPeers)
	defer func() {
		for _, p := range peers {
			p.Shutdown()
		}
	}()
	for i := 0; i < concurrentPeers; i++ {
		p, err := libutp.NewPeer(0)
		if err != nil {
			t.Fatalf("creating libutp peer %d: %v", i, err)
		}
		peers = append(peers, p)
	}

	start := time.Now()
	var sending sync.WaitGroup
	sendErrs := make(chan error, concurrentPeers)
	for i, p := range peers {
		sending.Add(1)
		go func(index int, p *libutp.Peer) {
			defer sending.Done()
			if err := p.Connect(goPort); err != nil {
				sendErrs <- fmt.Errorf("peer %d connect: %w", index, err)
				return
			}
			if _, err := p.WaitState(60*time.Second, libutp.StateConnected); err != nil {
				sendErrs <- fmt.Errorf("peer %d never connected: %w", index, err)
				return
			}
			if _, err := p.Write(stampedPayload(index, concurrentPayloadSize)); err != nil {
				sendErrs <- fmt.Errorf("peer %d write: %w", index, err)
				return
			}
			if err := p.WaitDrained(120 * time.Second); err != nil {
				sendErrs <- fmt.Errorf("peer %d could not hand off its payload: %w", index, err)
				return
			}
			p.CloseStream()
		}(i, p)
	}
	sending.Wait()
	close(sendErrs)
	for err := range sendErrs {
		t.Errorf("%v", err)
	}

	seen := make(map[int]int, concurrentPeers)
	for i := 0; i < concurrentPeers; i++ {
		select {
		case res := <-results:
			if res.err != nil {
				t.Errorf("connection %d of %d: %v", i+1, concurrentPeers, res.err)
				continue
			}
			if res.size != concurrentPayloadSize {
				t.Errorf("peer %d's stream delivered %d of %d bytes",
					res.index, res.size, concurrentPayloadSize)
			}
			if prev, dup := seen[res.index]; dup {
				t.Errorf("peer %d's payload arrived twice (already seen as connection %d); "+
					"two connections are reading the same stream", res.index, prev)
			}
			seen[res.index] = i
		case <-time.After(120 * time.Second):
			t.Fatalf("only %d of %d concurrent connections completed", i, concurrentPeers)
		}
	}
	for i := 0; i < concurrentPeers; i++ {
		if _, ok := seen[i]; !ok {
			t.Errorf("peer %d's payload never arrived on any connection", i)
		}
	}

	elapsed := time.Since(start)
	total := concurrentPeers * concurrentPayloadSize
	t.Logf("%d concurrent libutp initiators, %d bytes each, all verified in %v (%.1f Mbps aggregate)",
		concurrentPeers, concurrentPayloadSize, elapsed.Round(time.Millisecond),
		float64(total)*8/elapsed.Seconds()/1e6)

	accepting.Wait()
}

// The other direction: one socket of ours initiating to many libutp peers at
// once. This is the leeching side, and it exercises our connection-id
// allocation and send path under concurrency rather than our accept path.
func TestInteropConcurrentGoInitiators(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	sock, goPort := bindGoSocket(t, ctx)
	defer sock.Close()

	peers := make([]*libutp.Peer, 0, concurrentPeers)
	defer func() {
		for _, p := range peers {
			p.Shutdown()
		}
	}()
	for i := 0; i < concurrentPeers; i++ {
		p, err := libutp.NewPeer(0)
		if err != nil {
			t.Fatalf("creating libutp peer %d: %v", i, err)
		}
		p.Listen()
		peers = append(peers, p)
	}
	t.Logf("our socket on :%d initiating to %d libutp responders", goPort, concurrentPeers)

	start := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, concurrentPeers*2)
	for i, p := range peers {
		wg.Add(1)
		go func(index int, p *libutp.Peer) {
			defer wg.Done()
			// Distinct connection ids per peer: the pair is what routes a
			// packet back to its connection, and reusing one across peers is
			// exactly the allocation bug this looks for.
			base := uint16(5000 + index*2)
			cid := utp.NewConnectionId(utp.NewUdpPeer(loopback(p.Port())), base, base+1)
			stream, err := sock.ConnectWithCid(ctx, cid, utp.NewConnectionConfig())
			if err != nil {
				errs <- fmt.Errorf("peer %d: our connect failed: %w", index, err)
				return
			}
			if _, err := p.WaitState(60*time.Second, libutp.StateConnected); err != nil {
				errs <- fmt.Errorf("peer %d never accepted our connection: %w", index, err)
				return
			}
			payload := stampedPayload(index, concurrentPayloadSize)
			if _, err := stream.Write(ctx, payload); err != nil {
				errs <- fmt.Errorf("peer %d: write: %w", index, err)
				return
			}
			got, err := p.ReadFull(len(payload), 120*time.Second)
			if err != nil {
				errs <- fmt.Errorf("peer %d read %d of %d bytes: %w",
					index, len(got), len(payload), err)
				stream.Close()
				return
			}
			if !bytes.Equal(got, payload) {
				gotIndex, perr := payloadIndex(got)
				if perr != nil {
					errs <- fmt.Errorf("peer %d received %d bytes that match nothing: %v",
						index, len(got), perr)
				} else {
					errs <- fmt.Errorf("peer %d received peer %d's payload; packets are "+
						"crossing between connections", index, gotIndex)
				}
			}
			stream.Close()
		}(i, p)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("%v", err)
	}

	elapsed := time.Since(start)
	total := concurrentPeers * concurrentPayloadSize
	t.Logf("%d concurrent connections from one socket, %d bytes each, all verified in %v "+
		"(%.1f Mbps aggregate)", concurrentPeers, concurrentPayloadSize,
		elapsed.Round(time.Millisecond), float64(total)*8/elapsed.Seconds()/1e6)
}

// describeStamps reports what a payload actually contains, word by word, so a
// mismatch says which streams contributed and where.
func describeStamps(b []byte) string {
	type run struct {
		index, off int
		words      int
	}
	var runs []run
	for off := 0; off+8 <= len(b); off += 8 {
		w := binary.BigEndian.Uint64(b[off:])
		idx, stampOff := int(w>>32), int(uint32(w))
		if n := len(runs); n > 0 && runs[n-1].index == idx &&
			stampOff == runs[n-1].off+runs[n-1].words*8 {
			runs[n-1].words++
			continue
		}
		runs = append(runs, run{index: idx, off: stampOff, words: 1})
	}
	var sb strings.Builder
	for i, r := range runs {
		if i == 8 {
			fmt.Fprintf(&sb, " ... and %d more runs", len(runs)-8)
			break
		}
		fmt.Fprintf(&sb, "[peer %d from offset %d, %d words] ", r.index, r.off, r.words)
	}
	return sb.String()
}
