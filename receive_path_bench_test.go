package utp_go

import (
	"context"
	"encoding/binary"
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

// BenchmarkReceivePath is the cost of one in-order data packet on an
// established connection: routing it, handling it, handing its bytes to the
// application and acknowledging it -- what the socket's reader does for each
// datagram, measured warm and without the wake-up that precedes it.
func BenchmarkReceivePath(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := newBenchConn()
	sock := WithSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	cid := NewConnectionId(conn.peer, benchConnID+1, benchConnID)
	accepted := make(chan *UtpStream, 1)
	go func() {
		s, err := sock.AcceptWithCid(ctx, cid, NewConnectionConfig())
		if err != nil {
			b.Error(err)
		}
		accepted <- s
	}()
	time.Sleep(20 * time.Millisecond)
	defer pinRandom(benchOurSeq)()
	conn.in <- NewPacketBuilder(st_syn, benchConnID, 100000, benchWindow, benchPeerSeq).Build().Encode()
	stream := <-accepted
	if stream == nil {
		b.FailNow()
	}

	// One datagram, its timestamp and sequence number rewritten for each
	// packet: each is handled before the next is written, and the receive
	// buffer copies the payload, so nothing holds on to it. Building b.N of
	// them up front kept hundreds of megabytes alive and timed the garbage
	// collector instead.
	const payloadSize = 1000
	template := NewPacketBuilder(st_data, benchConnID+1, 200000, benchWindow, benchPeerSeq+1).
		WithAckNum(benchOurSeq - 1).WithPayload(make([]byte, payloadSize)).Build().Encode()

	// The reader's own two steps, called here so nothing but the work is
	// timed: no channel between the datagram and the socket, no wake-up. The
	// socket's read loop sits idle meanwhile, waiting on conn.in. What the
	// connection hands up is taken at once, as by an application that never
	// falls behind; one that did would let the receive buffer fill, and the
	// packets after it -- never resent here -- would stall the connection.
	touched := make(map[*connection]struct{})
	read := 0
	b.SetBytes(payloadSize)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		binary.BigEndian.PutUint32(template[4:], 200000+uint32(i))
		binary.BigEndian.PutUint16(template[16:], benchPeerSeq+1+uint16(i))
		sock.dispatch(&IncomingPacketRaw{peer: conn.peer, payload: template}, touched)
		sock.endBatch(touched)
		for len(stream.reads) > 0 {
			read += (<-stream.reads).Len
		}
	}
	b.StopTimer()
	if read != b.N*payloadSize {
		b.Fatalf("read %d bytes of %d", read, b.N*payloadSize)
	}
}
