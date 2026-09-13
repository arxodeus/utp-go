package utp_go

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOnTransmit(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())
	initialTimeout := ctrl.Timeout()

	// Register the initial transmission of a packet with sequence number 1
	seqNum := uint16(1)
	packetOneSizeBytes := uint32(32)

	err := ctrl.OnTransmit(seqNum, Initial, packetOneSizeBytes)
	require.NoError(t, err, "transmission registration failed")

	transmissionRecord, exists := ctrl.transmissions[seqNum]
	require.True(t, exists, "transmission not recorded")
	require.Equal(t, packetOneSizeBytes, transmissionRecord.SizeBytes,
		"expected size %d, got %d", packetOneSizeBytes, transmissionRecord.SizeBytes)
	require.Equal(t, uint32(1), transmissionRecord.NumTransmissions,
		"expected 1 transmission, got %d", transmissionRecord.NumTransmissions)

	require.Equal(t, packetOneSizeBytes, ctrl.windowSizeBytes,
		"expected window size %d, got %d", packetOneSizeBytes, ctrl.windowSizeBytes)

	// Register the initial transmission of a packet with sequence number 2
	seqNum = 2
	packetTwoSizeBytes := uint32(128)

	err = ctrl.OnTransmit(seqNum, Initial, packetTwoSizeBytes)
	require.NoError(t, err, "transmission registration failed")

	transmissionRecord, exists = ctrl.transmissions[seqNum]
	require.True(t, exists, "transmission not recorded")
	require.Equal(t, packetTwoSizeBytes, transmissionRecord.SizeBytes,
		"expected size %d, got %d", packetOneSizeBytes, transmissionRecord.SizeBytes)
	require.Equal(t, uint32(1), transmissionRecord.NumTransmissions,
		"expected 1 transmission, got %d", transmissionRecord.NumTransmissions)

	require.Equal(t, packetOneSizeBytes+packetTwoSizeBytes, ctrl.windowSizeBytes,
		"expected window size %d, got %d", packetOneSizeBytes+packetTwoSizeBytes, ctrl.windowSizeBytes)

	// Register the retransmission of the packet with sequence number 2
	err = ctrl.OnTransmit(seqNum, Retransmission, 0)
	require.NoError(t, err, "transmission registration failed")

	transmissionRecord, exists = ctrl.transmissions[seqNum]
	require.True(t, exists, "transmission not recorded")
	require.Equal(t, packetTwoSizeBytes, transmissionRecord.SizeBytes,
		"expected size %d, got %d", packetOneSizeBytes, transmissionRecord.SizeBytes)
	require.Equal(t, uint32(2), transmissionRecord.NumTransmissions,
		"expected 1 transmission, got %d", transmissionRecord.NumTransmissions)

	require.Equal(t, packetOneSizeBytes+packetTwoSizeBytes, ctrl.windowSizeBytes,
		"expected window size %d, got %d", packetOneSizeBytes+packetTwoSizeBytes, ctrl.windowSizeBytes)

	if ctrl.Timeout() != initialTimeout {
		t.Fatalf("expected timeout %v, got %v", initialTimeout, ctrl.Timeout())
	}
}

func TestOnTransmitDuplicateTransmission(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())

	// Register the initial transmission of a packet with sequence number 1
	seqNum := uint16(1)
	bytes := uint32(32)

	err := ctrl.OnTransmit(seqNum, Initial, bytes)
	require.NoError(t, err, "transmission registration failed")
	require.Equal(t, bytes, ctrl.windowSizeBytes,
		"expected window size %d, got %d", bytes, ctrl.windowSizeBytes)

	// Register the initial transmission of the SAME packet
	err = ctrl.OnTransmit(seqNum, Initial, bytes)
	require.ErrorIs(t, err, ErrDuplicateTransmission, "expected ErrDuplicateTransmission, got %v", err)
	require.Equal(t, bytes, ctrl.windowSizeBytes,
		"expected window size %d, got %d", bytes, ctrl.windowSizeBytes)
}

func TestOnTransmitUnknownSeqNum(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())

	// Register the retransmission of the packet with sequence number 1
	seqNum := uint16(1)
	err := ctrl.OnTransmit(seqNum, Retransmission, 0)
	require.ErrorIs(t, err, ErrUnknownSeqNum, "expected ErrUnknownSeqNum, got %v", err)
	require.Equal(t, uint32(0), ctrl.windowSizeBytes, "expected window size 0, got %d", ctrl.windowSizeBytes)
}

func TestOnTransmitInsufficientWindowSize(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())

	// Register the transmission of a packet with sequence number 1
	seqNum := uint16(1)
	bytes := ctrl.maxWindowSizeBytes + 1

	err := ctrl.OnTransmit(seqNum, Initial, bytes)
	require.ErrorIs(t, err, ErrInsufficientWindowSize,
		"expected ErrInsufficientWindowSize, got %v", err)

	require.Equal(t, uint32(0), ctrl.windowSizeBytes,
		"expected window size 0, got %d", ctrl.windowSizeBytes)
}

func TestOnAck(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())

	// Register the initial transmission
	seqNum := uint16(1)
	bytes := uint32(32)

	err := ctrl.OnTransmit(seqNum, Initial, bytes)
	require.NoError(t, err, "transmission registration failed")

	// Register the acknowledgement
	ackDelay := 150 * time.Millisecond
	ackRTT := 300 * time.Millisecond
	ackReceivedAt := time.Now()
	ack := Ack{
		Delay:      ackDelay,
		RTT:        ackRTT,
		ReceivedAt: ackReceivedAt,
	}

	err = ctrl.OnAck(seqNum, ack)
	require.NoError(t, err, "ack registration failed")

	// Check base delay
	baseDelay := ctrl.delayAcc.BaseDelay()
	require.Equal(t, ackDelay, baseDelay, "expected base delay %v, got %v", ackDelay, baseDelay)
	// Check window size
	require.Equal(t, uint32(0), ctrl.windowSizeBytes, "expected window size 0, got %d", ctrl.windowSizeBytes)

	// Check timeout
	require.True(t, ctrl.minTimeout >= ctrl.Timeout(), "expected timeout %v, got %v", ctrl.minTimeout, ctrl.Timeout())
}

func TestOnAckUnknownSeqNum(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())

	// Register the acknowledgement for packet with sequence number 1
	seqNum := uint16(1)
	ack := Ack{
		Delay:      150 * time.Millisecond,
		RTT:        300 * time.Millisecond,
		ReceivedAt: time.Now(),
	}

	err := ctrl.OnAck(seqNum, ack)
	require.ErrorIs(t, err, ErrUnknownSeqNum, "expected ErrUnknownSeqNum, got %v", err)
}

func TestOnLostPacketRetransmitting(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())

	initialMaxWindowSizeBytes := ctrl.minWindowSizeBytes * 10
	ctrl.maxWindowSizeBytes = initialMaxWindowSizeBytes

	// Register initial transmission
	seqNum := uint16(1)
	bytes := uint32(32)

	err := ctrl.OnTransmit(seqNum, Initial, bytes)
	require.NoError(t, err, "transmission registration failed")
	require.Equal(t, bytes, ctrl.windowSizeBytes,
		"expected window size %d, got %d", bytes, ctrl.windowSizeBytes)

	// Register packet loss with retransmission
	err = ctrl.OnLostPacket(seqNum, true, time.Now())
	require.NoError(t, err, "lost packet registration failed")

	require.Equal(t, bytes, ctrl.windowSizeBytes,
		"expected window size %d, got %d", bytes, ctrl.windowSizeBytes)

	require.True(t, ctrl.maxWindowSizeBytes >= ctrl.minWindowSizeBytes,
		"max window size less than min window size")

	expectedMaxWindow := initialMaxWindowSizeBytes / 2
	require.Equal(t, expectedMaxWindow, ctrl.maxWindowSizeBytes,
		"expected max window size %d, got %d", expectedMaxWindow, ctrl.maxWindowSizeBytes)
}

func TestOnLostPacketUnknownSeqNum(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())

	// Set initial max window size
	initialMaxWindowSizeBytes := ctrl.minWindowSizeBytes * 10
	ctrl.maxWindowSizeBytes = initialMaxWindowSizeBytes

	// Try to register loss for unknown sequence number
	seqNum := uint16(1)
	err := ctrl.OnLostPacket(seqNum, false, time.Now())
	// Check error
	require.ErrorIs(t, err, ErrUnknownSeqNum, "lost packet registration failed")

	// Verify window sizes unchanged
	require.Equal(t, uint32(0), ctrl.windowSizeBytes, "expected window size 0, got %d", ctrl.windowSizeBytes)
	require.Equal(t, initialMaxWindowSizeBytes, ctrl.maxWindowSizeBytes,
		"expected max window size %d, got %d", initialMaxWindowSizeBytes, ctrl.maxWindowSizeBytes)
}

// A timeout with packets in flight resets the window to one packet and
// re-enters slow start. libutp: utp_internal.cpp:1223-1228.
func TestOnTimeoutWithPacketsInFlight(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())
	ctrl.slowStart = false

	initialMaxWindowSizeBytes := ctrl.minWindowSizeBytes * 10
	ctrl.maxWindowSizeBytes = initialMaxWindowSizeBytes
	initialTimeout := ctrl.Timeout()

	ctrl.OnTimeout(true)

	require.Equal(t, ctrl.minWindowSizeBytes, ctrl.maxWindowSizeBytes,
		"expected max window size %d, got %d", ctrl.minWindowSizeBytes, ctrl.maxWindowSizeBytes)
	require.True(t, ctrl.slowStart, "a timeout with packets in flight must re-enter slow start")

	expectedTimeout := initialTimeout * 2
	require.Equal(t, expectedTimeout, ctrl.Timeout(),
		"expected timeout %v, got %v", expectedTimeout, ctrl.Timeout())
}

// A timeout on an idle connection -- nothing in flight, so nothing was
// actually lost -- decays the window by a third instead of collapsing it.
//
// libutp: "No need to be aggressive about resetting the congestion window.
// Just let it decay by a 3:rd" (utp_internal.cpp:1216-1222). This fork
// collapsed the window to its floor in both cases, so an application that
// paused long enough to hit an RTO restarted from two packets.
func TestOnTimeoutWhileIdleDecaysGently(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())
	ctrl.slowStart = false

	initial := ctrl.minWindowSizeBytes * 30
	ctrl.maxWindowSizeBytes = initial

	ctrl.OnTimeout(false)

	want := initial * 2 / 3
	require.Equal(t, want, ctrl.maxWindowSizeBytes,
		"an idle timeout should decay the window to two thirds (%d), got %d", want, ctrl.maxWindowSizeBytes)
	require.False(t, ctrl.slowStart, "an idle timeout is not a loss signal and must not re-enter slow start")
	require.Greater(t, ctrl.maxWindowSizeBytes, ctrl.minWindowSizeBytes,
		"an idle timeout collapsed the window to its floor")
}

func TestOnTimeoutNotExceedMax(t *testing.T) {
	config := defaultCtrlConfig()
	config.InitialTimeout = 2 * time.Second
	config.MaxTimeout = 3 * time.Second

	ctrl := newDefaultController(config)

	// Register timeout
	ctrl.OnTimeout(true)

	// Verify timeout is capped at max
	require.Equal(t, config.MaxTimeout, ctrl.Timeout(),
		"expected timeout %v, got %v", config.MaxTimeout, ctrl.Timeout())
}

func TestPush(t *testing.T) {
	window := 100 * time.Millisecond
	acc := newDelayAccumulator(window)

	delay := 50 * time.Millisecond
	delayReceivedAt := time.Now()
	acc.Push(delay, delayReceivedAt)

	require.Equal(t, 1, acc.delays.Len(), "delay not pushed onto accumulator")

	item := (*acc.delays)[0]
	require.Equal(t, delay, item.Value,
		"expected delay %v, got %v", delay, item.Value)
	expectedDeadline := delayReceivedAt.Add(window)
	require.Equal(t, expectedDeadline, item.Deadline,
		"expected delay %v, got %v", expectedDeadline, item.Deadline)
}

func TestBaseDelay(t *testing.T) {
	window := 100 * time.Millisecond
	acc := newDelayAccumulator(window)

	// Push delays in descending order
	delaySmall := 50 * time.Millisecond
	delaySmallReceivedAt := time.Now()
	acc.Push(delaySmall, delaySmallReceivedAt)

	delaySmaller := 25 * time.Millisecond
	delaySmallerReceivedAt := time.Now()
	acc.Push(delaySmaller, delaySmallerReceivedAt)

	delaySmallest := 5 * time.Millisecond
	delaySmallestReceivedAt := time.Now()
	acc.Push(delaySmallest, delaySmallestReceivedAt)

	delayExpired := 1 * time.Millisecond
	delayExpiredReceivedAt := time.Now().Add(-window)
	acc.Push(delayExpired, delayExpiredReceivedAt)

	// Check that all delays are present
	require.Equal(t, 4, acc.delays.Len(), "expected 4 delays, got %d", acc.delays.Len())

	// Get base delay
	baseDelay := acc.BaseDelay()
	require.Equal(t, delaySmallest, baseDelay, "expected base delay %v, got %v", delaySmallest, baseDelay)
	// Check that expired delay was removed
	require.Equal(t, 3, acc.delays.Len(), "expected 3 delays, got %d", acc.delays.Len())
}

func TestBaseDelayEmpty(t *testing.T) {
	window := time.Millisecond * 100
	acc := newDelayAccumulator(window)
	baseDelay := acc.BaseDelay()
	require.Equal(t, time.Duration(0), baseDelay, "expected base delay 0, got %v", baseDelay)
}

// The congestion window is halved at most once per maxWindowDecayInterval,
// however many packets are declared lost in that span.
//
// libutp: MAX_WINDOW_DECAY, guarded by can_decay_win
// (utp_internal.cpp:51, :602-605). Without the guard a burst of four losses
// -- one queue overflow -- took the window to a sixteenth in a single event.
func TestWindowDecaysAtMostOncePerInterval(t *testing.T) {
	ctrl := newDefaultController(defaultCtrlConfig())
	initial := ctrl.minWindowSizeBytes * 64
	ctrl.maxWindowSizeBytes = initial

	now := time.Now()
	for seqNum := uint16(1); seqNum <= 4; seqNum++ {
		require.NoError(t, ctrl.OnTransmit(seqNum, Initial, 32))
	}

	// Four losses in the same instant.
	for seqNum := uint16(1); seqNum <= 4; seqNum++ {
		require.NoError(t, ctrl.OnLostPacket(seqNum, true, now))
	}
	require.Equal(t, initial/2, ctrl.maxWindowSizeBytes,
		"four losses in one instant halved the window %d times, not once",
		func() int {
			n := 0
			for w := initial; w > ctrl.maxWindowSizeBytes; w /= 2 {
				n++
			}
			return n
		}())

	// Still inside the interval: no further decay.
	require.NoError(t, ctrl.OnTransmit(uint16(5), Initial, 32))
	require.NoError(t, ctrl.OnLostPacket(uint16(5), true, now.Add(maxWindowDecayInterval-time.Millisecond)))
	require.Equal(t, initial/2, ctrl.maxWindowSizeBytes,
		"a loss inside the decay interval halved the window again")

	// Past the interval: decay again.
	require.NoError(t, ctrl.OnTransmit(uint16(6), Initial, 32))
	require.NoError(t, ctrl.OnLostPacket(uint16(6), true, now.Add(maxWindowDecayInterval)))
	require.Equal(t, initial/4, ctrl.maxWindowSizeBytes,
		"a loss past the decay interval did not halve the window")
}

// A peer's reported delay cannot drive the window below what the round trip
// says is possible.
//
// libutp clamps the delay it feeds the controller to the minimum round-trip
// time of the packets the acknowledgement covers:
//
//	// the delay can never be greater than the rtt. The min_rtt
//	// variable is the RTT in microseconds
//	int32 our_delay = min<uint32>(our_hist.get_value(), uint32(min_rtt));
//	                                        (utp_internal.cpp:1615-1621)
//
// The delay it is clamping is not measured locally. It arrives in the
// timestamp-difference field of every incoming packet and is passed to the
// controller unaltered (conn.go: `delay := time.Duration(
// packet.Header.TimestampDiff) * time.Microsecond`), so it is a 32-bit number
// under the remote peer's control. Nothing else between the wire and the
// congestion window checks it.
//
// Without the clamp one packet ends the connection's usefulness. The gain is
// `MAX_CWND_INCREASE_BYTES_PER_RTT * window_factor * (target - our_delay) /
// target`, so a reported delay of 30 seconds against a 100 ms target makes the
// second factor -299 and takes the window to its floor on a single
// acknowledgement -- by malice, or by a peer whose clock stepped, or by a
// timestamp that wrapped.
func TestDelayClampedToRTT(t *testing.T) {
	const (
		rtt        = 20 * time.Millisecond
		honest     = 5 * time.Millisecond
		absurd     = 30 * time.Second
		packetSize = 1400
	)

	// grow drives one honest transmit-and-acknowledge cycle.
	grow := func(ctrl *defaultController, seq uint16, delay time.Duration, at time.Time) {
		if err := ctrl.OnTransmit(seq, Initial, packetSize); err != nil {
			t.Fatalf("transmit %d: %v", seq, err)
		}
		if err := ctrl.OnAck(seq, Ack{Delay: delay, RTT: rtt, ReceivedAt: at}); err != nil {
			t.Fatalf("ack %d: %v", seq, err)
		}
	}

	ctrl := newDefaultController(defaultCtrlConfig())
	ctrl.slowStart = false
	ctrl.maxWindowSizeBytes = 60 * packetSize

	now := time.Now()
	var seq uint16
	for i := 0; i < 40; i++ {
		seq++
		grow(ctrl, seq, honest, now.Add(time.Duration(i)*rtt))
	}
	before := ctrl.maxWindowSizeBytes

	// A peer that lies once costs 17% of the window and no more -- the gain is
	// scaled by this packet's share of it, so a single acknowledgement can
	// only move it so far. A peer that lies does not lie once. Twenty
	// acknowledgements is what a second of a hostile or broken peer looks
	// like, and is what separates the two behaviours: clamped, the delay reads
	// as 20ms against a 100ms target and the window is fine; unclamped, each
	// one takes about 14.6 KB and the window reaches its floor.
	//
	// The first version of this test injected exactly one and passed without
	// the clamp.
	for i := 0; i < 20; i++ {
		seq++
		grow(ctrl, seq, absurd, now.Add(time.Duration(41+i)*rtt))
	}
	after := ctrl.maxWindowSizeBytes

	if before <= ctrl.minWindowSizeBytes*4 {
		t.Fatalf("the window was only %d bytes (floor %d) before the poisoned ack; there is no "+
			"room for a collapse to be visible", before, ctrl.minWindowSizeBytes)
	}
	// Clamped, the delay reads as 20ms against a 100ms target, which is under
	// target, so the window holds or grows. Unclamped it reaches the floor.
	// Half is far from both.
	if after < before/2 {
		t.Errorf("20 acknowledgements reporting %v of delay on a %v path took the window from "+
			"%d bytes to %d (floor %d). libutp clamps the reported delay to the round trip "+
			"(utp_internal.cpp:1615-1621) precisely so that a peer cannot do this",
			absurd, rtt, before, after, ctrl.minWindowSizeBytes)
	}
	t.Logf("window %d -> %d bytes across 20 acknowledgements each claiming %v of delay on a "+
		"%v path", before, after, absurd, rtt)
}
