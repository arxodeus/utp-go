package utp_go

import (
	"bytes"
	"math/rand"
	"testing"
)

// The receive buffer allocates as data arrives, not its whole capacity up
// front: a connection that has received a kilobyte holds a few, not the
// megabyte it may be advertised.
func TestReceiveBufferAllocatesAsDataArrives(t *testing.T) {
	const capacity = 1 << 20
	rb := newReceiveBuffer(capacity, 99)
	if len(rb.buf) != 0 {
		t.Fatalf("a new receive buffer holds %d bytes before any data", len(rb.buf))
	}
	if rb.Window() != capacity || rb.Available() != capacity {
		t.Fatalf("window %d, available %d; want the full capacity %d advertised", rb.Window(), rb.Available(), capacity)
	}
	if err := rb.Write(make([]byte, 1024), 100); err != nil {
		t.Fatal(err)
	}
	if len(rb.buf) > minRecvBufAlloc {
		t.Fatalf("1 KB received holds %d bytes, want at most %d", len(rb.buf), minRecvBufAlloc)
	}
	if rb.Window() != capacity-1024 {
		t.Fatalf("window %d after 1 KB, want %d", rb.Window(), capacity-1024)
	}
}

// Against a model: packets arrive in order, out of order and repeated, reads
// take random amounts, and what is read is exactly the stream, in order. The
// allocation never passes the capacity, and the window and admission figures
// are those of the fixed-size buffer this replaced.
func TestReceiveBufferMatchesModel(t *testing.T) {
	for seed := int64(1); seed <= 50; seed++ {
		rng := rand.New(rand.NewSource(seed))
		capacity := 2048 + rng.Intn(64*1024)
		const first = uint16(65000) // wraps during the run
		rb := newReceiveBuffer(capacity, first-1)

		var stream []byte // every byte sent, in sequence order
		var payloads [][]byte
		var got []byte
		next := 0 // index of the next payload not yet admitted in order
		held := map[int]bool{}

		for step := 0; step < 3000; step++ {
			switch op := rng.Intn(10); {
			case op < 6: // a packet: the next in order, or one a little ahead
				i := next + rng.Intn(4)
				for len(payloads) <= i {
					p := make([]byte, 1+rng.Intn(1400))
					rng.Read(p)
					payloads = append(payloads, p)
					stream = append(stream, p...)
				}
				seq := first + uint16(i)
				before := rb.Available()
				err := rb.Write(payloads[i], seq)
				if err != nil {
					continue // refused for want of room: the peer would resend
				}
				if i < next || held[i] {
					if rb.Available() != before {
						t.Fatalf("seed %d: a repeated packet changed the room", seed)
					}
					continue
				}
				held[i] = true
				for held[next] {
					delete(held, next)
					next++
				}
			default: // a read of any size
				buf := make([]byte, 1+rng.Intn(5000))
				n := rb.Read(buf)
				got = append(got, buf[:n]...)
			}
			if len(rb.buf) > capacity {
				t.Fatalf("seed %d: %d bytes allocated for a capacity of %d", seed, len(rb.buf), capacity)
			}
			pending := 0
			for i := range held {
				pending += len(payloads[i])
			}
			inOrder := 0
			for i := 0; i < next; i++ {
				inOrder += len(payloads[i])
			}
			if unread := inOrder - len(got); rb.Window() != capacity-unread ||
				rb.Available() != capacity-unread-pending {
				t.Fatalf("seed %d step %d: window %d available %d; want %d and %d",
					seed, step, rb.Window(), rb.Available(), capacity-unread, capacity-unread-pending)
			}
		}
		for {
			buf := make([]byte, 4096)
			n := rb.Read(buf)
			if n == 0 {
				break
			}
			got = append(got, buf[:n]...)
		}
		inOrder := 0
		for i := 0; i < next; i++ {
			inOrder += len(payloads[i])
		}
		if !bytes.Equal(got, stream[:inOrder]) {
			t.Fatalf("seed %d: read %d bytes that differ from the %d delivered in order", seed, len(got), inOrder)
		}
	}
}
