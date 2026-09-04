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

// --- RTT estimator and RTO ---------------------------------------------------
//
// libutp, at utp_internal.cpp:1362-1380:
//
//	if (pkt->transmissions == 1) {
//	    ertt = (now_micros - pkt->time_sent) / 1000;   // milliseconds
//	    if (rtt == 0) { rtt = ertt; rtt_var = ertt / 2; }
//	    else {
//	        delta   = rtt - ertt;
//	        rtt_var = rtt_var + (abs(delta) - rtt_var) / 4;
//	        rtt     = rtt - rtt/8 + ertt/8;
//	    }
//	    rto = max(rtt + rtt_var * 4, 1000);
//	}
//
// This implementation keeps the same quantities in microseconds.

func ackAfter(seqNum uint16, rtt time.Duration) Ack {
	return Ack{Delay: 10 * time.Millisecond, RTT: rtt, ReceivedAt: time.Now()}
}

func TestFirstRTTSampleIsAdoptedDirectly(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())

	const ertt = 400 * time.Millisecond
	require.NoError(t, ctrl.OnTransmit(1, Initial, 500))
	require.NoError(t, ctrl.OnAck(1, ackAfter(1, ertt)))

	st := ctrl.Stats()
	require.Equal(t, ertt, st.RTT,
		"the first sample should be adopted outright, not eased up from zero (utp_internal.cpp:1365)")
	require.Equal(t, ertt.Microseconds()/2, st.RTTVarianceMicros,
		"first-sample variance should be ertt/2 (utp_internal.cpp:1366)")

	// rto = max(400ms + 4*200ms, 1000ms) = 1200ms
	require.Equal(t, 1200*time.Millisecond, st.Timeout,
		"rto should be rtt + rtt_var*4 when that exceeds the floor")
}

func TestRTOFloorIsOneSecond(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())

	// A fast path: rtt + rtt_var*4 = 20ms + 40ms = 60ms, well under the floor.
	const ertt = 20 * time.Millisecond
	require.NoError(t, ctrl.OnTransmit(1, Initial, 500))
	require.NoError(t, ctrl.OnAck(1, ackAfter(1, ertt)))

	st := ctrl.Stats()
	require.Equal(t, ertt, st.RTT)
	require.Equal(t, time.Second, st.Timeout,
		"libutp floors the rto at 1000ms (utp_internal.cpp:1380)")
}

func TestSubsequentRTTSamplesFollowLibutp(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())

	const first = 400 * time.Millisecond
	require.NoError(t, ctrl.OnTransmit(1, Initial, 500))
	require.NoError(t, ctrl.OnAck(1, ackAfter(1, first)))

	// rtt = 400ms, rtt_var = 200ms.
	const second = 480 * time.Millisecond
	require.NoError(t, ctrl.OnTransmit(2, Initial, 500))
	require.NoError(t, ctrl.OnAck(2, ackAfter(2, second)))

	// delta   = 400 - 480 = -80ms
	// rtt_var = 200 + (80 - 200)/4 = 200 - 30 = 170ms
	// rtt     = 400 - 50 + 60 = 410ms
	st := ctrl.Stats()
	require.Equal(t, 410*time.Millisecond, st.RTT)
	require.Equal(t, (170 * time.Millisecond).Microseconds(), st.RTTVarianceMicros)

	// rto = max(410 + 4*170, 1000) = max(1090, 1000) = 1090ms
	require.Equal(t, 1090*time.Millisecond, st.Timeout)
}

// A retransmitted packet's ack cannot be attributed to a particular
// transmission, so it must not move the estimate. libutp guards on
// `pkt->transmissions == 1` (utp_internal.cpp:1362); this is Karn's algorithm.
func TestRetransmittedPacketDoesNotUpdateRTT(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())

	const ertt = 400 * time.Millisecond
	require.NoError(t, ctrl.OnTransmit(1, Initial, 500))
	require.NoError(t, ctrl.OnAck(1, ackAfter(1, ertt)))
	before := ctrl.Stats()

	// A packet sent twice, then acked with a wildly different measurement.
	require.NoError(t, ctrl.OnTransmit(2, Initial, 500))
	require.NoError(t, ctrl.OnTransmit(2, Retransmission, 500))
	require.NoError(t, ctrl.OnAck(2, ackAfter(2, 5*time.Second)))

	after := ctrl.Stats()
	require.Equal(t, before.RTT, after.RTT,
		"an ack for a retransmitted packet must not move the RTT estimate")
	require.Equal(t, before.RTTVarianceMicros, after.RTTVarianceMicros)
	require.Equal(t, before.Timeout, after.Timeout)
}

func TestInitialRTTVarianceMatchesLibutp(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())
	st := ctrl.Stats()
	require.Equal(t, time.Duration(0), st.RTT)
	require.Equal(t, (800 * time.Millisecond).Microseconds(), st.RTTVarianceMicros,
		"libutp starts rtt_var at 800ms (utp_internal.cpp:2610)")
	require.Equal(t, 3*time.Second, st.Timeout,
		"the initial timeout should be libutp's 3000ms (utp_internal.cpp:2762)")
}
