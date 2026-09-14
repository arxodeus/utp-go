package utp_go

import (
	"bytes"
	"context"
	"crypto/rand"
	"net"
	"testing"
	"time"
)

// writevPair brings up a connected pair over loopback and returns both ends.
func writevPair(t *testing.T, ctx context.Context, cid uint16) (client, server *UtpStream) {
	t.Helper()
	lg := duplexQuietLog()
	sa, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sa.Close)
	sb, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sb.Close)

	cidA := NewConnectionId(NewUdpPeer(sb.LocalAddr().(*net.UDPAddr)), cid, cid+1)
	cidB := NewConnectionId(NewUdpPeer(sa.LocalAddr().(*net.UDPAddr)), cid+1, cid)

	accepted := make(chan *UtpStream, 1)
	go func() {
		s, err := sb.AcceptWithCid(ctx, cidB, NewConnectionConfig())
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- s
	}()
	client, err = sa.ConnectWithCid(ctx, cidA, NewConnectionConfig())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	server = <-accepted
	if server == nil {
		t.Fatal("accept failed")
	}
	return client, server
}

// A vectored write, libutp's utp_writev: several buffers written as one stream
// write.
//
// What must be true is that the peer sees exactly the concatenation, with
// nothing lost between the pieces and no framing implied by the split -- uTP
// is a byte stream, and a caller that split its data into a header and a body
// must not be able to tell afterwards how it split it.
func TestWriteVDeliversTheConcatenation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, server := writevPair(t, ctx, 4100)

	// Sizes chosen to straddle a packet boundary, so the pieces cannot line
	// up with packets: a 1400-byte path carries about 1380 bytes of payload.
	pieces := [][]byte{
		make([]byte, 17),
		make([]byte, 1400),
		nil,
		make([]byte, 0),
		make([]byte, 64*1024),
		make([]byte, 3),
	}
	var want []byte
	for _, p := range pieces {
		if _, err := rand.Read(p); err != nil {
			t.Fatal(err)
		}
		want = append(want, p...)
	}

	writeErr := make(chan error, 1)
	go func() {
		n, err := client.WriteV(ctx, pieces)
		if err == nil && n != len(want) {
			t.Errorf("WriteV wrote %d bytes, want %d", n, len(want))
		}
		client.Close()
		writeErr <- err
	}()

	got := make([]byte, 0, len(want))
	if _, err := server.ReadToEOF(ctx, &got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("WriteV: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("received %d bytes, want %d", len(got), len(want))
	}
	if !bytes.Equal(got, want) {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("first difference at byte %d of %d", i, len(want))
			}
		}
	}
}

// A vectored write is indistinguishable on the wire from the same bytes
// written whole. Asserted because the saving it exists for is a copy, not a
// change in what is sent, and a framing difference would be a bug rather than
// an optimisation.
func TestWriteVMatchesASingleWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	payload := make([]byte, 40*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	// An awkward split: not on any packet boundary.
	pieces := [][]byte{payload[:7], payload[7:9000], payload[9000:]}

	read := func(write func(s *UtpStream) error, cid uint16) []byte {
		client, server := writevPair(t, ctx, cid)
		done := make(chan error, 1)
		go func() {
			err := write(client)
			client.Close()
			done <- err
		}()
		got := make([]byte, 0, len(payload))
		if _, err := server.ReadToEOF(ctx, &got); err != nil {
			t.Fatalf("read: %v", err)
		}
		if err := <-done; err != nil {
			t.Fatalf("write: %v", err)
		}
		return got
	}

	whole := read(func(s *UtpStream) error {
		_, err := s.Write(ctx, payload)
		return err
	}, 4200)
	vectored := read(func(s *UtpStream) error {
		_, err := s.WriteV(ctx, pieces)
		return err
	}, 4300)

	if !bytes.Equal(whole, payload) {
		t.Fatal("the single write did not arrive intact; the comparison would prove nothing")
	}
	if !bytes.Equal(vectored, whole) {
		t.Error("the vectored write delivered different bytes from the single write")
	}
}

// The edges: nothing to write, and a write after the sending side is closed.
func TestWriteVEdges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, server := writevPair(t, ctx, 4400)
	defer server.Close()

	for _, empty := range [][][]byte{nil, {}, {nil}, {{}, nil, {}}} {
		n, err := client.WriteV(ctx, empty)
		if err != nil || n != 0 {
			t.Errorf("WriteV(%v) = (%d, %v), want (0, nil)", empty, n, err)
		}
	}

	if err := client.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.WriteV(ctx, [][]byte{[]byte("too late")}); err == nil {
		t.Error("WriteV succeeded after CloseWrite")
	}
}

// WriteV must not retain the caller's buffers, for the same reason Write must
// not: a write that hit its deadline leaves its entry in the connection's
// pending list, and a caller is free to reuse its buffers the moment the call
// returns. Run this under -race.
func TestWriteVDoesNotRetainCallerBuffers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, server := writevPair(t, ctx, 4500)
	defer server.Close()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			if _, err := server.Read(ctx, buf); err != nil {
				return
			}
		}
	}()

	pieces := [][]byte{make([]byte, 512), make([]byte, 4096)}
	for i := 0; i < 64; i++ {
		if _, err := client.WriteV(ctx, pieces); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		// Reuse immediately, as a real caller would.
		for _, p := range pieces {
			for j := range p {
				p[j] = byte(i)
			}
		}
	}
	client.Close()
	<-readDone
}

// What WriteV is actually worth, measured rather than asserted.
//
// The claim is one copy rather than two: a caller with several buffers either
// joins them (allocate the total, copy into it) and calls Write (which
// allocates the total again and copies again), or calls WriteV (allocate
// once, copy once). Allocated bytes per operation is the figure that shows
// it, and it is far more robust to a noisy loopback than nanoseconds are.
//
// Run both together -- `go test -bench 'WriteV|WriteJoined' -benchmem` -- and
// compare B/op. Cross-run comparisons in this harness are not worth having;
// see BENCHMARKS.md.
func benchmarkVectored(b *testing.B, vectored bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	lg := duplexQuietLog()
	sa, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		b.Fatal(err)
	}
	defer sa.Close()
	sb, err := Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, lg)
	if err != nil {
		b.Fatal(err)
	}
	defer sb.Close()

	cidA := NewConnectionId(NewUdpPeer(sb.LocalAddr().(*net.UDPAddr)), 4600, 4601)
	cidB := NewConnectionId(NewUdpPeer(sa.LocalAddr().(*net.UDPAddr)), 4601, 4600)
	accepted := make(chan *UtpStream, 1)
	go func() {
		s, err := sb.AcceptWithCid(ctx, cidB, NewConnectionConfig())
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- s
	}()
	client, err := sa.ConnectWithCid(ctx, cidA, NewConnectionConfig())
	if err != nil {
		b.Fatal(err)
	}
	server := <-accepted
	if server == nil {
		b.Fatal("accept failed")
	}
	defer server.Close()

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		buf := make([]byte, 64*1024)
		for {
			if _, err := server.Read(ctx, buf); err != nil {
				return
			}
		}
	}()

	pieces := [][]byte{make([]byte, 64), make([]byte, 16*1024), make([]byte, 128)}
	total := 0
	for _, p := range pieces {
		total += len(p)
	}

	b.ReportAllocs()
	b.SetBytes(int64(total))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if vectored {
			if _, err := client.WriteV(ctx, pieces); err != nil {
				b.Fatal(err)
			}
			continue
		}
		joined := make([]byte, 0, total)
		for _, p := range pieces {
			joined = append(joined, p...)
		}
		if _, err := client.Write(ctx, joined); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	client.Close()
	<-drained
}

func BenchmarkWriteV(b *testing.B)      { benchmarkVectored(b, true) }
func BenchmarkWriteJoined(b *testing.B) { benchmarkVectored(b, false) }
