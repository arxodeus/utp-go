package utp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Protocol behaviors checked against the reference implementation's rules.

// --- Zero-length ST_DATA ----------------------------------------------------
//
// A zero-length ST_DATA is acknowledged like any other packet and advances
// the acknowledgement number; only delivery is skipped.

func TestDecoderAcceptsZeroLengthData(t *testing.T) {
	pkt := newPacketBuilder(st_data, 4242, 12345, 100_000, 7).
		WithAckNum(3).
		Build()

	encoded := pkt.Encode()
	decoded, err := decodePacket(encoded, false)
	require.NoError(t, err, "a zero-length ST_DATA must decode; libutp accepts one")
	require.Equal(t, st_data, decoded.Header.PacketType)
	require.Equal(t, uint16(7), decoded.Header.SeqNum)
	require.Empty(t, decoded.Body, "payload should be empty")
}

func TestZeroLengthDataAdvancesTheAckNumber(t *testing.T) {
	syn := uint16(100)
	synAck := uint16(101)
	conn := CreateTestConnection(endpointInfo{Type: acceptor, SynNum: syn, SynAck: synAck})

	congestionCtrl := newDefaultController(fromConnConfig(conn.config))
	conn.state = &connState{
		stateType:   connConnected,
		SendBuf:     newSendBuffer(TEST_BUFFER_SIZE),
		SentPackets: newSentPacketsWithoutLogger(synAck, congestionCtrl),
		RecvBuf:     newReceiveBuffer(TEST_BUFFER_SIZE, syn),
	}

	before := conn.state.RecvBuf.AckNum()
	require.Equal(t, syn, before)

	// The next in-order packet, carrying nothing.
	require.NoError(t, conn.onData(syn+1, []byte{}))

	require.Equal(t, connConnected, conn.state.stateType,
		"a zero-length ST_DATA must not close the connection")
	require.Equal(t, before+1, conn.state.RecvBuf.AckNum(),
		"a zero-length ST_DATA must still advance the ack number, as libutp does at utp_internal.cpp:2354")

	// The sequence space keeps moving afterwards, so a following packet with
	// real data is delivered rather than treated as a gap.
	require.NoError(t, conn.onData(syn+2, []byte("hello")))
	require.Equal(t, before+2, conn.state.RecvBuf.AckNum())

	buf := make([]byte, 32)
	n := conn.state.RecvBuf.Read(buf)
	require.Equal(t, "hello", string(buf[:n]),
		"data after an empty packet should still be readable")
}

// --- SYN retransmission backoff ---------------------------------------------
//
// The SYN's retransmission timeout doubles on every expiry, starting from
// 3000ms.

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
		// and the scheduler add noise, so the band is wide enough to separate
		// the two without flaking.
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

	// The attempt is abandoned after the SYN has been sent three times.
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
// For a packet sent once:
//
//	ertt = (now_micros - time_sent) / 1000   // milliseconds
//	if rtt == 0 { rtt = ertt; rtt_var = ertt / 2 }
//	else {
//	    delta   = rtt - ertt
//	    rtt_var = rtt_var + (abs(delta) - rtt_var) / 4
//	    rtt     = rtt - rtt/8 + ertt/8
//	}
//	rto = max(rtt + rtt_var*4, 1000)

func ackAfter(seqNum uint16, rtt time.Duration) ackSample {
	return ackSample{Delay: 10 * time.Millisecond, RTT: rtt, ReceivedAt: time.Now()}
}

func TestFirstRTTSampleIsAdoptedDirectly(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())

	const ertt = 400 * time.Millisecond
	require.NoError(t, ctrl.OnTransmit(1, transmitInitial, 500))
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
	require.NoError(t, ctrl.OnTransmit(1, transmitInitial, 500))
	require.NoError(t, ctrl.OnAck(1, ackAfter(1, ertt)))

	st := ctrl.Stats()
	require.Equal(t, ertt, st.RTT)
	require.Equal(t, time.Second, st.Timeout,
		"libutp floors the rto at 1000ms (utp_internal.cpp:1380)")
}

func TestSubsequentRTTSamplesFollowLibutp(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())

	const first = 400 * time.Millisecond
	require.NoError(t, ctrl.OnTransmit(1, transmitInitial, 500))
	require.NoError(t, ctrl.OnAck(1, ackAfter(1, first)))

	// rtt = 400ms, rtt_var = 200ms.
	const second = 480 * time.Millisecond
	require.NoError(t, ctrl.OnTransmit(2, transmitInitial, 500))
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

// The estimator works in whole milliseconds: the sample is truncated and rtt
// and rtt_var are integer millisecond counts, so fractions are dropped, not
// carried.
func TestRTTSamplesAreWholeMilliseconds(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())

	require.NoError(t, ctrl.OnTransmit(1, transmitInitial, 500))
	require.NoError(t, ctrl.OnAck(1, ackAfter(1, 400*time.Millisecond+900*time.Microsecond)))
	require.NoError(t, ctrl.OnTransmit(2, transmitInitial, 500))
	require.NoError(t, ctrl.OnAck(2, ackAfter(2, 480*time.Millisecond+700*time.Microsecond)))

	// As TestSubsequentRTTSamplesFollowLibutp: ertt 400 then 480 give rtt
	// 410, rtt_var 170, rto 1090. Carried in microseconds they would give
	// 410.875, 170.288 and 1092.
	st := ctrl.Stats()
	require.Equal(t, 410*time.Millisecond, st.RTT)
	require.Equal(t, int64(170000), st.RTTVarianceMicros)
	require.Equal(t, 1090*time.Millisecond, st.Timeout)

	// On a path faster than a millisecond the estimate stays 0, and every
	// sample is taken as the first. The loss probe keeps its own microsecond
	// estimate.
	fast := newDefaultController(defaultCtrlConfig())
	require.NoError(t, fast.OnTransmit(1, transmitInitial, 500))
	require.NoError(t, fast.OnAck(1, ackAfter(1, 300*time.Microsecond)))
	require.Equal(t, time.Duration(0), fast.Stats().RTT)
	require.Equal(t, 300*time.Microsecond, fast.Stats().FineRTT)
}

// A retransmitted packet's ack cannot be attributed to one transmission, so
// it does not move the estimate (Karn's algorithm).
func TestRetransmittedPacketDoesNotUpdateRTT(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())

	const ertt = 400 * time.Millisecond
	require.NoError(t, ctrl.OnTransmit(1, transmitInitial, 500))
	require.NoError(t, ctrl.OnAck(1, ackAfter(1, ertt)))
	before := ctrl.Stats()

	// A packet sent twice, then acked with a wildly different measurement.
	require.NoError(t, ctrl.OnTransmit(2, transmitInitial, 500))
	require.NoError(t, ctrl.OnTransmit(2, transmitRetransmission, 500))
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
