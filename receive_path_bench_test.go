package utp

import (
	"context"
	"encoding/binary"
	"slices"
	"testing"
	"time"
)

// The connection the benchmark drives: the peer's ids and sequence numbers,
// and ours, pinned.
const (
	benchConnID  = 6000
	benchPeerSeq = 900
	benchOurSeq  = 0x4321
	benchWindow  = 1 << 20
)

// benchConn feeds a socket datagrams from a channel and discards what it
// writes, so the receive path can be measured without a kernel in the way.
type benchConn struct {
	peer   *scriptedPeer
	in     chan []byte
	writes chan struct{}
	closed chan struct{}
	// wroteAt is when WriteTo last ran: on the benchmark's goroutine, since
	// the reader sends acknowledgements itself.
	wroteAt time.Time
}

func newBenchConn() *benchConn {
	return &benchConn{
		peer:   &scriptedPeer{name: "bench-peer"},
		in:     make(chan []byte, 1<<16),
		writes: make(chan struct{}, 1<<16),
		closed: make(chan struct{}),
	}
}

func (c *benchConn) ReadFrom(b []byte) (int, ConnectionPeer, error) {
	select {
	case p := <-c.in:
		return copy(b, p), c.peer, nil
	case <-c.closed:
		return 0, nil, context.Canceled
	}
}

func (c *benchConn) WriteTo(b []byte, _ ConnectionPeer) (int, error) {
	c.wroteAt = time.Now()
	select {
	case c.writes <- struct{}{}:
	default:
	}
	return len(b), nil
}

func (c *benchConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

// benchPayload is the size of each data packet's payload.
const benchPayload = 1000

// benchReceiver returns an established connection for the receive-path
// benchmarks, accepted from benchConn's peer, and the data packet they feed
// it. The one datagram is rewritten for each packet; each is handled before
// the next is written, and the receive buffer copies the payload.
func benchReceiver(b *testing.B) (*benchConn, *Socket, *Stream, []byte) {
	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)
	conn := newBenchConn()
	sock := NewSocket(ctx, conn, conformanceLogger())
	b.Cleanup(func() { sock.Close() })

	cid := NewConnectionID(conn.peer, benchConnID+1, benchConnID)
	accepted := make(chan *Stream, 1)
	go func() {
		s, err := sock.AcceptWithCID(ctx, cid, NewConnectionConfig())
		if err != nil {
			b.Error(err)
		}
		accepted <- s
	}()
	time.Sleep(20 * time.Millisecond)
	b.Cleanup(pinRandom(benchOurSeq))
	conn.in <- newPacketBuilder(stSyn, benchConnID, 100000, benchWindow, benchPeerSeq).Build().Encode()
	stream := <-accepted
	if stream == nil {
		b.FailNow()
	}
	template := newPacketBuilder(stData, benchConnID+1, 200000, benchWindow, benchPeerSeq+1).
		WithAckNum(benchOurSeq - 1).WithPayload(make([]byte, benchPayload)).Build().Encode()
	return conn, sock, stream, template
}

// BenchmarkReceivePath measures one in-order data packet on an established
// connection: routing, handling, delivering its bytes and acknowledging, as
// the socket's reader does for each datagram, warm and without the wake-up
// that precedes it.
func BenchmarkReceivePath(b *testing.B) {
	conn, sock, stream, template := benchReceiver(b)

	// Call the reader's two steps directly, so only the work is timed. The
	// socket's read loop sits idle on conn.in. Delivered data is taken at
	// once, as by an application that never falls behind; otherwise the
	// receive buffer would fill and the connection stall.
	touched := &readBatch{}
	read := 0
	b.SetBytes(benchPayload)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		binary.BigEndian.PutUint32(template[4:], 200000+uint32(i))
		binary.BigEndian.PutUint16(template[16:], benchPeerSeq+1+uint16(i))
		sock.dispatch(&incomingPacketRaw{peer: conn.peer, payload: template}, touched)
		sock.endBatch(touched)
		for len(stream.reads) > 0 {
			read += (<-stream.reads).Len
		}
	}
	b.StopTimer()
	if read != b.N*benchPayload {
		b.Fatalf("read %d bytes of %d", read, b.N*benchPayload)
	}
}

// BenchmarkReceivePathCold is BenchmarkReceivePath with the caches evicted
// before each packet, as on a reader that wakes once a millisecond with the
// rest of the process running in between, and packets spaced as at 10 Mb/s.
// Each packet is timed and the median reported (cold-ns/op), with the median
// time to the acknowledgement's write (cold-ack-ns/op); the benchmark's own
// ns/op includes the eviction and is not meaningful.
func BenchmarkReceivePathCold(b *testing.B) {
	conn, sock, stream, template := benchReceiver(b)

	// Roughly what other threads evict between two packets a millisecond
	// apart.
	evict := make([]byte, 4<<20)
	touched := &readBatch{}
	took := make([]time.Duration, 0, b.N)
	toAck := make([]time.Duration, 0, b.N)
	var last time.Time
	for i := 0; i < b.N; i++ {
		for j := 0; j < len(evict); j += 64 {
			evict[j]++
		}
		// Packets 1.2 ms apart, as at 10 Mb/s, so the receiver acknowledges
		// each; closer together it would hold acknowledgements back.
		for time.Since(last) < 1200*time.Microsecond {
		}
		last = time.Now()
		binary.BigEndian.PutUint32(template[4:], 200000+uint32(i))
		binary.BigEndian.PutUint16(template[16:], benchPeerSeq+1+uint16(i))
		start := time.Now()
		sock.dispatch(&incomingPacketRaw{peer: conn.peer, payload: template}, touched)
		sock.endBatch(touched)
		took = append(took, time.Since(start))
		if conn.wroteAt.After(start) {
			toAck = append(toAck, conn.wroteAt.Sub(start))
		}
		for len(stream.reads) > 0 {
			<-stream.reads
		}
	}
	slices.Sort(took)
	b.ReportMetric(float64(took[len(took)/2].Nanoseconds()), "cold-ns/op")
	if len(toAck) < b.N/2 {
		b.Fatalf("%d of %d packets acknowledged at once; the pacing is not 10 Mb/s's", len(toAck), b.N)
	}
	slices.Sort(toAck)
	b.ReportMetric(float64(toAck[len(toAck)/2].Nanoseconds()), "cold-ack-ns/op")
}
