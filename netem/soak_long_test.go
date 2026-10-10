package netem

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	utp "github.com/zen-eth/utp-go"
)

// soakDuration is how long TestSoakLongRunning runs: UTP_SOAK_DURATION, or
// fifteen seconds, enough to exercise everything it checks.
func soakDuration(t *testing.T) time.Duration {
	if v := os.Getenv("UTP_SOAK_DURATION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("UTP_SOAK_DURATION: %v", err)
		}
		return d
	}
	return 15 * time.Second
}

// streamReader adapts a stream to io.Reader for io.ReadFull.
type streamReader struct {
	ctx context.Context
	s   *utp.UtpStream
}

func (r streamReader) Read(p []byte) (int, error) { return r.s.Read(r.ctx, p) }

// soakServe answers every message on a connection -- a 4-byte length, then
// that many bytes -- with the message's SHA-256, until the peer ends its
// side.
func soakServe(ctx context.Context, s *utp.UtpStream) error {
	defer s.Close()
	r := streamReader{ctx, s}
	var hdr [4]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("server read header: %w", err)
		}
		msg := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(r, msg); err != nil {
			return fmt.Errorf("server read %d bytes: %w", len(msg), err)
		}
		sum := sha256.Sum256(msg)
		if _, err := s.Write(ctx, sum[:]); err != nil {
			return fmt.Errorf("server write digest: %w", err)
		}
	}
}

// soakExchange sends one message of n bytes and checks the digest it gets
// back.
func soakExchange(ctx context.Context, s *utp.UtpStream, rng *rand.Rand, n int) error {
	msg := make([]byte, 4+n)
	binary.BigEndian.PutUint32(msg, uint32(n))
	rng.Read(msg[4:])
	if _, err := s.Write(ctx, msg); err != nil {
		return fmt.Errorf("client write %d bytes: %w", n, err)
	}
	var got [32]byte
	if _, err := io.ReadFull(streamReader{ctx, s}, got[:]); err != nil {
		return fmt.Errorf("client read digest: %w", err)
	}
	if want := sha256.Sum256(msg[4:]); !bytes.Equal(got[:], want[:]) {
		return fmt.Errorf("digest mismatch for %d bytes", n)
	}
	return nil
}

// soakSize draws a message size: mostly small, as a peer's requests and
// control messages are, sometimes a piece-sized block.
func soakSize(rng *rand.Rand) int {
	switch r := rng.Float64(); {
	case r < 0.6:
		return 1 + rng.Intn(4096)
	case r < 0.9:
		return 4096 + rng.Intn(60*1024)
	default:
		return 64*1024 + rng.Intn(448*1024)
	}
}

type soakSample struct {
	at         time.Duration
	heap       uint64
	goroutines int
	conns      int
	exchanges  int64
}

func soakMeasure(start time.Time, a, b *utp.UtpSocket, exchanges int64) soakSample {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return soakSample{
		at:         time.Since(start).Round(time.Second),
		heap:       m.HeapInuse,
		goroutines: runtime.NumGoroutine(),
		conns:      a.NumConnections() + b.NumConnections(),
		exchanges:  exchanges,
	}
}

// TestSoakLongRunning runs the traffic a BitTorrent client lives with, for
// as long as it is told to, over a link that loses and reorders packets, and
// checks that nothing accumulates.
//
// Sixteen workers open a connection, exchange one message of random size and
// close it, over and over; four connections stay open throughout, sending a
// message and then going quiet for up to twenty seconds. Every message is
// answered with its SHA-256, so every byte is checked. The live heap,
// goroutines and tracked connections are sampled as it runs. At the end, with
// everything closed, there must be no connections left on either socket and
// no more goroutines than at the start, and the heap over the last half of
// the run must not exceed the first half's by more than the noise between
// samples allows. A leak of a few bytes per connection, which the seconds-long
// soaks cannot see, shows here within the hour: at the rate this churns, a
// hundred bytes per connection is tens of megabytes an hour.
//
// It runs for fifteen seconds by default. For a real soak:
//
//	UTP_SOAK_DURATION=4h go test ./netem/ -run TestSoakLongRunning -count=1 -timeout 5h -v
func TestSoakLongRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("soak tests are not -short tests")
	}
	duration := soakDuration(t)
	// One sample a minute in a real soak, more often in a short one.
	interval := duration / 15
	if interval < time.Second {
		interval = time.Second
	} else if interval > time.Minute {
		interval = time.Minute
	}
	const (
		churners  = 16
		longLived = 4
	)

	n := NewNetwork(77)
	defer n.Close()
	a := n.MustAddEndpoint("client")
	b := n.MustAddEndpoint("server")
	n.Connect(a, b, Config{
		Delay: 5 * time.Millisecond, Jitter: time.Millisecond,
		LossRate: 0.005, ReorderRate: 0.01,
		BandwidthBps: 50_000_000, QueueBytes: 256 * 1024,
		// The link's per-packet queue samples are kept for its life, and
		// would be the soak's largest heap growth.
		NoQueueSamples: true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pair := NewUtpPair(ctx, n, a, b, quiet())

	var (
		failures  []error
		failMu    sync.Mutex
		exchanges atomic.Int64
		bytesSent atomic.Int64
		served    sync.WaitGroup
		// serverMetrics is each server connection's latest sample, for
		// reporting one that never ends.
		serverMetrics sync.Map
	)
	fail := func(err error) {
		failMu.Lock()
		failures = append(failures, err)
		failMu.Unlock()
	}

	// The server accepts and serves until the run is over.
	acceptCtx, stopAccepting := context.WithCancel(ctx)
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			cfg := utp.NewConnectionConfig()
			cfg.Metrics = func(m utp.ConnectionMetrics) {
				// Only while it is being served: an entry for every
				// connection ever made would be the soak's own leak.
				if _, live := serverMetrics.Load(m.Cid.Hash()); live {
					serverMetrics.Store(m.Cid.Hash(), m)
				}
			}
			s, err := pair.SockB.Accept(acceptCtx, cfg)
			if err != nil {
				if acceptCtx.Err() == nil {
					fail(fmt.Errorf("accept: %w", err))
				}
				return
			}
			served.Add(1)
			key := s.Cid().Hash()
			serverMetrics.Store(key, utp.ConnectionMetrics{Cid: s.Cid(), At: time.Now(), State: "accepted"})
			go func() {
				defer served.Done()
				defer serverMetrics.Delete(key)
				if err := soakServe(ctx, s); err != nil {
					fail(err)
				}
			}()
		}
	}()

	start := time.Now()
	baseline := soakMeasure(start, pair.SockA, pair.SockB, 0)
	deadline := start.Add(duration)
	var workers sync.WaitGroup
	opCtx := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, 2*time.Minute) }

	for w := 0; w < churners; w++ {
		workers.Add(1)
		go func(w int) {
			defer workers.Done()
			rng := rand.New(rand.NewSource(int64(1000 + w)))
			for time.Now().Before(deadline) {
				c, done := opCtx()
				s, err := pair.SockA.Connect(c, b.Addr(), utp.NewConnectionConfig())
				if err != nil {
					done()
					fail(fmt.Errorf("churner %d connect: %w", w, err))
					continue
				}
				size := soakSize(rng)
				if err := soakExchange(c, s, rng, size); err != nil {
					fail(fmt.Errorf("churner %d: %w", w, err))
				} else {
					exchanges.Add(1)
					bytesSent.Add(int64(size))
				}
				s.Close()
				done()
			}
		}(w)
	}
	for l := 0; l < longLived; l++ {
		workers.Add(1)
		go func(l int) {
			defer workers.Done()
			rng := rand.New(rand.NewSource(int64(2000 + l)))
			s, err := pair.SockA.Connect(ctx, b.Addr(), utp.NewConnectionConfig())
			if err != nil {
				fail(fmt.Errorf("long-lived %d connect: %w", l, err))
				return
			}
			defer s.Close()
			for time.Now().Before(deadline) {
				c, done := opCtx()
				size := soakSize(rng)
				if err := soakExchange(c, s, rng, size); err != nil {
					done()
					fail(fmt.Errorf("long-lived %d: %w", l, err))
					return
				}
				done()
				exchanges.Add(1)
				bytesSent.Add(int64(size))
				quiet := time.Duration(1+rng.Intn(20)) * time.Second
				if wait := time.Until(deadline); wait < quiet {
					quiet = wait
				}
				time.Sleep(quiet)
			}
		}(l)
	}

	var samples []soakSample
	for time.Now().Before(deadline) {
		if wait := time.Until(deadline); wait < interval {
			time.Sleep(max(wait, 0))
		} else {
			time.Sleep(interval)
		}
		s := soakMeasure(start, pair.SockA, pair.SockB, exchanges.Load())
		samples = append(samples, s)
		t.Logf("%v: heap %.1f MB, %d goroutines, %d connections, %d exchanges, %d MB sent",
			s.at, float64(s.heap)/(1<<20), s.goroutines, s.conns, s.exchanges, bytesSent.Load()>>20)
	}
	workers.Wait()
	stopAccepting()
	<-acceptDone
	// A server connection whose client has gone must still end: the
	// client's socket answers its keep-alive, every 29 seconds, with a RESET.
	servedDone := make(chan struct{})
	go func() { served.Wait(); close(servedDone) }()
	select {
	case <-servedDone:
	case <-time.After(time.Minute + 2*29*time.Second):
		t.Errorf("server connections still running %v after the clients finished", time.Minute+2*29*time.Second)
		serverMetrics.Range(func(_, v any) bool {
			m := v.(utp.ConnectionMetrics)
			t.Logf("still served: %+v", m)
			return true
		})
		if f := os.Getenv("UTP_SOAK_STACKS"); f != "" {
			if out, err := os.Create(f); err == nil {
				pprof.Lookup("goroutine").WriteTo(out, 2)
				out.Close()
				t.Logf("goroutine stacks written to %s", f)
			}
		}
		failMu.Lock()
		for _, err := range failures {
			t.Log(err)
		}
		failMu.Unlock()
		t.FailNow()
	}

	// Every connection gone from both sockets; the closing side lingers a
	// moment for its FIN to be acknowledged.
	settle := time.Now().Add(30 * time.Second)
	for pair.SockA.NumConnections()+pair.SockB.NumConnections() > 0 && time.Now().Before(settle) {
		time.Sleep(100 * time.Millisecond)
	}
	end := soakMeasure(start, pair.SockA, pair.SockB, exchanges.Load())
	for end.goroutines > baseline.goroutines && time.Now().Before(settle) {
		time.Sleep(100 * time.Millisecond)
		end = soakMeasure(start, pair.SockA, pair.SockB, exchanges.Load())
	}
	t.Logf("end: %d exchanges, %d MB sent, %d connections left, goroutines %d -> %d, heap %.1f -> %.1f MB",
		end.exchanges, bytesSent.Load()>>20, end.conns, baseline.goroutines, end.goroutines,
		float64(baseline.heap)/(1<<20), float64(end.heap)/(1<<20))

	failMu.Lock()
	defer failMu.Unlock()
	for i, err := range failures {
		if i == 10 {
			t.Errorf("... and %d more", len(failures)-10)
			break
		}
		t.Error(err)
	}
	if end.exchanges < churners {
		t.Errorf("only %d exchanges completed: the soak did not run", end.exchanges)
	}
	if end.conns != 0 {
		t.Errorf("%d connections still tracked after everything closed", end.conns)
	}
	if end.goroutines > baseline.goroutines {
		t.Errorf("goroutines %d at the start, %d once everything closed", baseline.goroutines, end.goroutines)
	}
	if len(samples) >= 4 {
		half := len(samples) / 2
		first, last := heapMedian(samples[1:half]), heapMedian(samples[half:])
		// Clean three-minute runs put the two medians within 1.6 MB of each
		// other, at 13-16 MB. What this resolves is growth of 4 MB plus a
		// quarter of the first half's median between the halves: over two
		// hours, about 880,000 connections, a leak of some 25 bytes each.
		// A run of seconds resolves nothing that small, and is not meant to.
		slack := uint64(4<<20) + first/4
		t.Logf("live heap median %.1f MB over the first half, %.1f MB over the second",
			float64(first)/(1<<20), float64(last)/(1<<20))
		if last > first+slack {
			t.Errorf("live heap grew from %.1f MB to %.1f MB between the halves of the run",
				float64(first)/(1<<20), float64(last)/(1<<20))
		}
	}
}

func heapMedian(s []soakSample) uint64 {
	if len(s) == 0 {
		return 0
	}
	h := make([]uint64, len(s))
	for i, x := range s {
		h[i] = x.heap
	}
	sort.Slice(h, func(i, j int) bool { return h[i] < h[j] })
	return h[len(h)/2]
}
