package netem

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	utp "github.com/arxodeus/utp"
)

// A connection whose peer has closed only its sending side, and is still
// reading, delivers everything written to it before Close, on a link that
// loses packets. Once both ends had sent a FIN and the application had
// closed, the connection ended at once -- with whatever of its data was still
// unacknowledged, which a lost packet then never delivered. libutp ends a
// closed socket only once its FIN is acknowledged (utp_internal.cpp:2174-2181),
// a RESET arrives, or its retransmissions give up.
func TestCloseAfterThePeersFinDeliversWhatWasWritten(t *testing.T) {
	for seed := int64(1); seed <= 8; seed++ {
		n := NewNetwork(seed)
		a := n.MustAddEndpoint("a")
		b := n.MustAddEndpoint("b")
		n.Connect(a, b, Config{Delay: 10 * time.Millisecond, BandwidthBps: 10_000_000, QueueBytes: 256 * 1024, LossRate: 0.05})

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		sa := utp.NewSocket(ctx, a, quiet())
		sb := utp.NewSocket(ctx, b, quiet())

		var wg sync.WaitGroup
		var got []byte
		var readErr error
		accepted := make(chan *utp.Stream, 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := sb.AcceptWithCID(ctx, utp.NewConnectionID(a.Addr(), 501, 500), nil)
			if err != nil {
				readErr = err
				accepted <- nil
				return
			}
			accepted <- s
			// The peer closes its sending side and goes on reading.
			s.CloseWrite()
			readErr = func() error { _, err := s.ReadToEOF(ctx, &got); return err }()
			s.Close()
		}()
		client, err := sa.ConnectWithCID(ctx, utp.NewConnectionID(b.Addr(), 500, 501), nil)
		if err != nil {
			t.Fatal(err)
		}
		if <-accepted == nil {
			t.Fatal(readErr)
		}
		// Wait for the peer's FIN, then write and close.
		var sink []byte
		if _, err := client.ReadToEOF(ctx, &sink); err != nil {
			t.Fatal(err)
		}
		payload := bytes.Repeat([]byte{byte(seed)}, 256*1024)
		if _, err := client.Write(ctx, payload); err != nil {
			t.Fatal(err)
		}
		client.Close()
		wg.Wait()
		sa.Close()
		sb.Close()
		cancel()
		n.Close()
		if readErr != nil || !bytes.Equal(got, payload) {
			t.Fatalf("seed %d: peer read %d of %d bytes (%v)", seed, len(got), len(payload), readErr)
		}
	}
}
