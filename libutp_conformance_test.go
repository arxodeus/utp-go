package utp_go

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Behaviours matched to libutp, with the reference line numbers in the
// assertions so a future change can be checked against the same source.

// --- Zero-length ST_DATA ----------------------------------------------------
//
// libutp guards only the delivery to the application with `count > 0` and
// advances ack_nr unconditionally (utp_internal.cpp:2342-2355). It therefore
// acks a zero-length ST_DATA like any other packet.
//
// This implementation used to reject such a packet in the decoder, so it was
// dropped before the connection saw it, never acked, and the peer would
// retransmit it forever.

func TestDecoderAcceptsZeroLengthData(t *testing.T) {
	pkt := NewPacketBuilder(st_data, 4242, 12345, 100_000, 7).
		WithAckNum(3).
		Build()

	encoded := pkt.Encode()
	decoded, err := DecodePacket(encoded)
	require.NoError(t, err, "a zero-length ST_DATA must decode; libutp accepts one")
	require.Equal(t, st_data, decoded.Header.PacketType)
	require.Equal(t, uint16(7), decoded.Header.SeqNum)
	require.Empty(t, decoded.Body, "payload should be empty")
}

func TestZeroLengthDataAdvancesTheAckNumber(t *testing.T) {
	syn := uint16(100)
	synAck := uint16(101)
	conn := CreateTestConnection(Endpoint{Type: Acceptor, SynNum: syn, SynAck: synAck})

	congestionCtrl := newDefaultController(fromConnConfig(conn.config))
	conn.state = &ConnState{
		stateType:   ConnConnected,
		SendBuf:     newSendBuffer(TEST_BUFFER_SIZE),
		SentPackets: newSentPacketsWithoutLogger(synAck, congestionCtrl),
		RecvBuf:     newReceiveBuffer(TEST_BUFFER_SIZE, syn),
	}

	before := conn.state.RecvBuf.AckNum()
	require.Equal(t, syn, before)

	// The next in-order packet, carrying nothing.
	require.NoError(t, conn.onData(syn+1, []byte{}))

	require.Equal(t, ConnConnected, conn.state.stateType,
		"a zero-length ST_DATA must not close the connection")
	require.Equal(t, before+1, conn.state.RecvBuf.AckNum(),
		"a zero-length ST_DATA must still advance the ack number, as libutp does at utp_internal.cpp:2354")

	// And the sequence space must keep moving afterwards, so a following
	// packet with real data is delivered rather than treated as a gap.
	require.NoError(t, conn.onData(syn+2, []byte("hello")))
	require.Equal(t, before+2, conn.state.RecvBuf.AckNum())

	buf := make([]byte, 32)
	n := conn.state.RecvBuf.Read(buf)
	require.Equal(t, "hello", string(buf[:n]),
		"data after an empty packet should still be readable")
}

// --- SYN retransmission backoff ---------------------------------------------
//
// libutp doubles retransmit_timeout on every retransmission timeout
// (utp_internal.cpp:1179, applied at :1203), starting from 3000ms
// (utp_internal.cpp:2762). This implementation previously computed
// InitialTimeout * 1.5^attempts, which grows from the initial value rather
// than the current one, with a different factor.

func TestSynRetransmissionBackoffDoubles(t *testing.T) {
	conn := newRecordingConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := WithSocket(ctx, conn, testSocketLogger())
	defer sock.Close()

	cfg := NewConnectionConfig()
	// Scaled down so the test is quick; the shape is what matters.
	cfg.InitialTimeout = 200 * time.Millisecond
	cfg.MaxConnAttempts = 4

	// Connect to a peer that never answers, so every SYN times out.
	cid := NewConnectionId(&testPeer{name: "silent"}, 200, 201)
	connectDone := make(chan struct{})
	go func() {
		defer close(connectDone)
		_, _ = sock.ConnectWithCid(ctx, cid, cfg)
	}()

	// Four attempts at 200/400/800ms is 1.4s of backoff; allow generously.
	waitFor(t, 8*time.Second, func() bool { return conn.countType(st_syn) >= cfg.MaxConnAttempts })

	stamps := conn.stampsOfType(st_syn)
	t.Logf("SYN transmissions at %v", relativeStamps(stamps))
	require.GreaterOrEqual(t, len(stamps), 4,
		"expected MaxConnAttempts SYN transmissions")

	// Gaps between successive SYNs must roughly double.
	gaps := make([]time.Duration, 0, len(stamps)-1)
	for i := 1; i < len(stamps); i++ {
		gaps = append(gaps, stamps[i].Sub(stamps[i-1]))
	}
	t.Logf("gaps between SYNs: %v", gaps)

	for i := 1; i < len(gaps); i++ {
		ratio := float64(gaps[i]) / float64(gaps[i-1])
		// Doubling is 2.0; 1.5^n growth would give about 1.5. The timer wheel
		// has 25ms resolution and the scheduler adds noise, so the band is
		// wide enough to separate the two hypotheses without flaking.
		if ratio < 1.7 || ratio > 2.4 {
			t.Errorf("gap %d/%d ratio %.2f (%v then %v); libutp doubles, so expected about 2.0",
				i, i-1, ratio, gaps[i-1], gaps[i])
		}
	}

	cancel()
	<-connectDone
}

func TestConnectGivesUpAfterMaxAttempts(t *testing.T) {
	conn := newRecordingConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := WithSocket(ctx, conn, testSocketLogger())
	defer sock.Close()

	cfg := NewConnectionConfig()
	cfg.InitialTimeout = 100 * time.Millisecond

	// libutp gives up on a connection attempt once retransmit_count reaches 2
	// in CS_SYN_SENT, which is three transmissions of the SYN in total
	// (utp_internal.cpp:1191).
	require.Equal(t, 3, cfg.MaxConnAttempts,
		"default should match libutp's three SYN transmissions")

	cid := NewConnectionId(&testPeer{name: "silent"}, 300, 301)
	errCh := make(chan error, 1)
	go func() {
		_, err := sock.ConnectWithCid(ctx, cid, cfg)
		errCh <- err
	}()

	select {
	case err := <-errCh:
		require.Error(t, err, "connecting to a silent peer must fail")
		t.Logf("connect failed with: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("connect never gave up on a silent peer")
	}

	require.LessOrEqual(t, conn.countType(st_syn), cfg.MaxConnAttempts,
		"sent more SYNs than MaxConnAttempts allows")
}

func relativeStamps(stamps []time.Time) []time.Duration {
	if len(stamps) == 0 {
		return nil
	}
	out := make([]time.Duration, len(stamps))
	for i, s := range stamps {
		out[i] = s.Sub(stamps[0]).Round(time.Millisecond)
	}
	return out
}
