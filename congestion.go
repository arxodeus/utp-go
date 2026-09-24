package utp_go

import (
	"container/heap"
	"errors"
	"math"
	"sync"
	"time"
)

const (
	defaultTargetMicros = 100000 * time.Microsecond
	// defaultInitialTimeout matches libutp's initial rto and its connect
	// timer, both 3000ms (utp_internal.cpp:2609 and :2762).
	defaultInitialTimeout = 3 * time.Second
	// defaultMinTimeout is the RTO floor. libutp computes
	// rto = max(rtt + rtt_var * 4, 1000) in milliseconds
	// (utp_internal.cpp:1380), so the floor is one second.
	defaultMinTimeout = 1000 * time.Millisecond
	defaultMaxTimeout = 60 * time.Second
	// defaultMaxPacketSizeBytes is the largest datagram this library will
	// ever try -- the ceiling of the path-MTU search, not a size it sends
	// straight away.
	//
	// libutp derives its ceiling from the interface MTU (`get_udp_mtu`,
	// utp_internal.cpp:1316) and searches downward from there. 1400 is the
	// same idea with a fixed, conservative starting assumption: below a
	// 1500-byte Ethernet MTU with room for tunnelling overhead.
	//
	// This was a flat 1024 before path-MTU discovery existed, because without
	// discovery the only safe fixed size is a small one. It is safe to raise
	// now precisely because it is no longer what gets sent: the search starts
	// at the midpoint between 576 and this, and grows only once a probe of a
	// given size has been acknowledged. An untested path therefore still gets
	// a 988-byte packet, close to the old 1024, and reaches 1400 only after
	// proving it can. See mtu.go.
	defaultMaxPacketSizeBytes = 1400
	// defaultMaxWindowSizeIncBytes is the cap on how far the congestion
	// window may grow in one RTT. libutp:
	// `#define MAX_CWND_INCREASE_BYTES_PER_RTT 3000` (utp_internal.cpp:43).
	// This was 1024, one packet per RTT, which on a 100 ms path meant about
	// ten kilobytes of window per second -- a 20 Mbps link needs 500 KB.
	defaultMaxWindowSizeIncBytes = 3000
	defaultGain                  = 1.0
	defaultDelayWindow           = 120 * time.Second
)

const (
	Initial = iota
	Retransmission
)

type Transmit int

type packetRecord struct {
	SizeBytes        uint32
	NumTransmissions uint32
	Acked            bool
	// NeedResend is libutp's need_resend: the packet was given up as lost
	// and its bytes no longer count in flight. Resending it counts them
	// again (utp_internal.cpp:877-881); acknowledging it does not subtract
	// them a second time (:1390-1396).
	NeedResend bool
}

type Ack struct {
	Delay      time.Duration
	RTT        time.Duration
	ReceivedAt time.Time
}

var (
	ErrInsufficientWindowSize = errors.New("insufficient window size")
	ErrUnknownSeqNum          = errors.New("unknown sequence number")
	ErrDuplicateTransmission  = errors.New("duplicate transmission")
)

type ctrlConfig struct {
	TargetDelayMicros     uint32
	InitialTimeout        time.Duration
	MinTimeout            time.Duration
	MaxTimeout            time.Duration
	MaxPacketSizeBytes    uint32
	MaxWindowSizeIncBytes uint32
	Gain                  float32
	Algorithm             CongestionAlgorithm
	DelayWindow           time.Duration
	// Clock is where the controller reads time: the delay window's expiry
	// and the age of the application-limited mark. Defaults to RealClock.
	Clock      Clock
	WindowSize uint32
}

func defaultCtrlConfig() *ctrlConfig {
	return &ctrlConfig{
		TargetDelayMicros:     uint32(defaultTargetMicros.Microseconds()),
		InitialTimeout:        defaultInitialTimeout,
		MinTimeout:            defaultMinTimeout,
		MaxTimeout:            defaultMaxTimeout,
		MaxPacketSizeBytes:    defaultMaxPacketSizeBytes,
		MaxWindowSizeIncBytes: defaultMaxWindowSizeIncBytes,
		Gain:                  defaultGain,
		DelayWindow:           defaultDelayWindow,
		Clock:                 RealClock,
		// The window ceiling, libutp's opt_sndbuf, whose default is also
		// 1 MB (utp_api.cpp:91). This was absent, which was harmless while
		// nothing read the field and a zero ceiling the moment something
		// did.
		WindowSize: DefaultWindowSize,
	}
}

type Controller interface {
	OnTransmit(seqNum uint16, transmit Transmit, dataLen uint32) error
	OnAck(seqNum uint16, ack Ack) error
	OnLostPacket(seqNum uint16, retransmitting bool, now time.Time) error
	OnTimeout(hasPacketsInFlight bool)
	OnWindowFull(now time.Time)
	// OnTick lets a controller act on the passage of time when no acks are
	// arriving. Only LEDBAT++ needs it, for its slowdowns.
	OnTick(now time.Time)
	// OnPeerDelay reports the one-way delay this end measured on a packet
	// arriving from the peer -- libutp's `their_delay`, in raw wrapping
	// microseconds. It is not a congestion signal for this sender; it is how
	// clock drift between the two ends is detected. See
	// defaultController.OnPeerDelay.
	OnPeerDelay(sample uint32, now time.Time)
	Timeout() time.Duration
	BytesAvailableInWindow() uint32
	// BytesInFlight is libutp's cur_window: payload bytes sent and neither
	// acknowledged nor given up as lost.
	BytesInFlight() uint32
	// CongestionWindow is libutp's max_window.
	CongestionWindow() uint32
	// OnRTTSample feeds a round trip measured outside OnAck: the SYN's, on
	// the dialling side. libutp takes it through the same ack_packet as any
	// other (utp_internal.cpp:1362-1380).
	OnRTTSample(rtt time.Duration)
	// MarkForResend gives a packet up as lost: its bytes stop counting in
	// flight until it is sent again. libutp does this to every packet in
	// flight on a retransmission timeout (utp_internal.cpp:1230-1237). A
	// packet already acknowledged or already marked is left alone.
	MarkForResend(seqNum uint16)
	// Stats returns a snapshot of the controller's internal state.
	//
	// It exists so a test harness can plot the congestion window and RTT
	// estimate over time. A controller that can only be observed through its
	// effect on throughput cannot be told apart from one that is doing
	// nothing -- which is not hypothetical here; see KNOWN-LIMITATIONS.md.
	Stats() ControllerStats
}

// ControllerStats is a snapshot of a congestion controller's state.
type ControllerStats struct {
	// WindowSizeBytes is the volume currently considered in flight.
	WindowSizeBytes uint32
	// MaxWindowSizeBytes is the congestion window: what the controller
	// currently believes the path will carry.
	MaxWindowSizeBytes uint32
	// MinWindowSizeBytes is the floor the window will not drop below.
	MinWindowSizeBytes uint32
	// RTT is the smoothed round-trip time estimate.
	RTT time.Duration
	// RTTVarianceMicros is the RTT variance estimate, in microseconds.
	RTTVarianceMicros int64
	// Timeout is the current retransmission timeout.
	Timeout time.Duration
	// BaseDelay is the lowest one-way delay observed in the delay window --
	// LEDBAT's estimate of the path with no queue. Queueing delay is the
	// difference between CurrentDelay and this.
	//
	// The series both are drawn from is the peer's measurement of *our*
	// outgoing path: the timestamp_difference field it puts on every packet,
	// which is libutp's `our_hist` (utp_internal.cpp:2016-2021). That is the
	// direction LEDBAT controls, because it is the direction this sender's
	// packets travel.
	BaseDelay time.Duration
	// ClockSkewCorrection is how much has been added to the base delay to
	// cancel clock drift between the two ends. Zero on a pair of clocks
	// running at the same rate, which is every pair that has not been
	// deliberately skewed.
	//
	// Exposed because a correction that cannot be observed cannot be tested,
	// and this repository has already learned that the expensive way.
	ClockSkewCorrection time.Duration
	// ClockDrift and ClockDriftPenalty are libutp's clock_drift and the delay
	// penalty it earns. See ConnectionMetrics for what they mean.
	ClockDrift        int64
	ClockDriftPenalty time.Duration
	// CurrentDelay is the most recent sample of that same series.
	//
	// It is here because there was no way to compute a queueing delay
	// without it. ConnectionMetrics.QueueingDelay used to subtract BaseDelay
	// from the *other* direction's measurement, which is not a queue in
	// either direction. See the note there.
	CurrentDelay time.Duration
	// TargetDelayMicros is the standing queue the controller aims for.
	TargetDelayMicros uint32
	// SlowStart reports whether the controller is still in slow start.
	//
	// It is here because two very different behaviours are indistinguishable
	// without it. libutp's application-limited guard zeroes the LEDBAT gain
	// (utp_internal.cpp:1681-1686), but in slow start the window is
	// `max(ss_cwnd, ledbat_cwnd)` (:1699) and `ss_cwnd` is not gated -- so a
	// window growing while the application sends nothing is correct in slow
	// start and a defect after it. A test that cannot tell which phase it is
	// in cannot measure the guard at all; see
	// netem.TestApplicationLimitedWindowDoesNotGrow.
	SlowStart bool
	// AppLimitedSince is how long it has been since the sender last had data
	// to send and no window to send it in -- the age of libutp's
	// `last_maxed_out_window` (:945, :957, read at :1681). Growth is
	// suppressed once this exceeds one second. Zero means the window has
	// never been filled.
	AppLimitedSince time.Duration
}

type defaultController struct {
	targetDelayMicros     uint32
	timeout               time.Duration
	minTimeout            time.Duration
	maxTimeout            time.Duration
	windowSizeBytes       uint32
	maxWindowSizeBytes    uint32
	minWindowSizeBytes    uint32
	maxWindowSizeIncBytes uint32
	gain                  float32
	rtt                   time.Duration
	rttVarianceMicros     int64
	transmissions         map[uint16]*packetRecord
	delayAcc              *delayAccumulator
	// lastWindowDecay is when the congestion window was last halved for
	// loss. libutp's `last_rwin_decay` (utp_internal.cpp:461).
	lastWindowDecay time.Time
	// slowStart and ssthreshBytes are the slow-start phase and its exit
	// threshold. libutp starts every connection in slow start with
	// `ssthresh = opt_sndbuf` (utp_internal.cpp:2620-2621) and grows the
	// window by a packet per acked packet until either the threshold is
	// crossed or the delay approaches the target (:1691-1702).
	//
	// This fork had no slow start at all: every connection began at two
	// packets and crept up by the LEDBAT increment alone.
	slowStart     bool
	ssthreshBytes uint32
	// maxWindowUpperBytes is the ceiling on the congestion window --
	// libutp's `opt_sndbuf`, which it clamps against at
	// utp_internal.cpp:1710.
	maxWindowUpperBytes uint32
	// lastMaxedOutWindow is when the sender last had data to send and no
	// window to send it in. libutp's `last_maxed_out_window`, set by
	// `is_full` (utp_internal.cpp:945, :957) and read at :1681: a sender
	// that has not filled its window in the last second is limited by the
	// application rather than the path, and growing the window further would
	// be measuring nothing.
	lastMaxedOutWindow time.Time

	// peerDelayHist is the base delay of packets arriving *from* the peer,
	// libutp's `their_hist` (utp_internal.cpp:507). It is not a congestion
	// signal for this sender -- it describes the other direction -- and its
	// only use is detecting clock drift. See OnPeerDelay.
	peerDelayHist *peerDelayHist

	// drift estimates the long-run slope of the delay the peer reports for
	// our packets: the second of libutp's two clock-drift mechanisms. See
	// driftEstimator, and applyCongestionControl for what it is used for.
	drift *driftEstimator

	// clk is where this controller reads time. Never nil after
	// newDefaultController; read through now().
	clk Clock

	// currentDelay is the most recent delay sample pushed into delayAcc --
	// the newest value of the series BaseDelay is the minimum of. Reported,
	// not acted on: the control path already has the sample in hand when it
	// needs it.
	currentDelay time.Duration

	// algorithm selects the congestion controller. Everything above is
	// shared; the LEDBAT++ state below is used only when it is selected.
	algorithm CongestionAlgorithm
	// minRTT is the lowest round trip seen -- LEDBAT++'s `base`, which its
	// gain is computed from (draft-irtf-iccrg-ledbat-plus-plus-01 §4.2).
	minRTT time.Duration
	// ppPhase and the four fields after it drive the periodic slowdowns that
	// keep the base-delay estimate honest (§4.4).
	ppPhase             ledbatPPPhase
	ppSlowdownStartedAt time.Time
	ppFreezeUntil       time.Time
	ppNextSlowdownAt    time.Time
	ppSlowdownSsthresh  uint32

	mu sync.Mutex
}

func newDefaultController(config *ctrlConfig) *defaultController {
	clk := config.Clock
	if clk == nil {
		clk = RealClock
	}
	ctrl := &defaultController{
		targetDelayMicros:     config.TargetDelayMicros,
		timeout:               config.InitialTimeout,
		minTimeout:            config.MinTimeout,
		maxTimeout:            config.MaxTimeout,
		windowSizeBytes:       0,
		maxWindowSizeBytes:    2 * config.MaxPacketSizeBytes,
		minWindowSizeBytes:    2 * config.MaxPacketSizeBytes,
		maxWindowSizeIncBytes: config.MaxWindowSizeIncBytes,
		gain:                  config.Gain,
		rtt:                   0,
		// libutp starts rtt_var at 800 (utp_internal.cpp:2610). Its rtt and
		// rtt_var are milliseconds -- the RTT sample is computed as
		// microseconds/1000 at utp_internal.cpp:1364 -- so that is 800ms.
		// It is superseded by the first RTT sample below, and exists so a
		// timeout computed before any ack is conservative rather than zero.
		rttVarianceMicros: (800 * time.Millisecond).Microseconds(),
		transmissions:     make(map[uint16]*packetRecord),
		clk:               clk,
		delayAcc:          newDelayAccumulatorWithClock(config.DelayWindow, clk),
		peerDelayHist:     newPeerDelayHist(config.DelayWindow),
		// libutp starts the first averaging slot five seconds after the
		// socket is created, not after the first sample arrives
		// (utp_internal.cpp:2553).
		drift: newDriftEstimator(clk.Now()),
		// libutp starts every connection in slow start with
		// `ssthresh = opt_sndbuf` (utp_internal.cpp:2620-2621), and its
		// default opt_sndbuf is 1 MB (utp_api.cpp:91) -- the same value as
		// DefaultWindowSize here.
		slowStart:           true,
		ssthreshBytes:       config.WindowSize,
		maxWindowUpperBytes: config.WindowSize,
		algorithm:           config.Algorithm,
	}
	// A ceiling below the floor pins the window shut on the first ack, which
	// is what an unset WindowSize used to produce.
	if ctrl.maxWindowUpperBytes == 0 {
		ctrl.maxWindowUpperBytes = DefaultWindowSize
		ctrl.ssthreshBytes = DefaultWindowSize
	}
	if ctrl.maxWindowUpperBytes < ctrl.minWindowSizeBytes {
		// A caller asking for a window smaller than two packets gets two
		// packets: below that nothing can be sent at all. Their intent to be
		// small is respected rather than replaced with the default.
		ctrl.maxWindowUpperBytes = ctrl.minWindowSizeBytes
		if ctrl.ssthreshBytes < ctrl.minWindowSizeBytes {
			ctrl.ssthreshBytes = ctrl.minWindowSizeBytes
		}
	}
	if config.Algorithm == AlgorithmLEDBATPP {
		// draft §4.5: LEDBAT++ targets 60ms of queueing delay where RFC 6817
		// and libutp use 100ms.
		ctrl.targetDelayMicros = uint32(ledbatPPTargetDelay.Microseconds())
		// draft §4.1: "LEDBAT++ sender limits the initial window to 2
		// packets" -- which is already minWindowSizeBytes here.
		ctrl.maxWindowSizeBytes = ctrl.minWindowSizeBytes
		ctrl.ppPhase = ppSlowStart
	}
	return ctrl
}

// Stats returns a snapshot of this controller's state.
func (c *defaultController) Stats() ControllerStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	var appLimitedSince time.Duration
	if !c.lastMaxedOutWindow.IsZero() {
		appLimitedSince = c.now().Sub(c.lastMaxedOutWindow)
	}
	return ControllerStats{
		WindowSizeBytes:     c.windowSizeBytes,
		MaxWindowSizeBytes:  c.maxWindowSizeBytes,
		MinWindowSizeBytes:  c.minWindowSizeBytes,
		RTT:                 c.rtt,
		RTTVarianceMicros:   c.rttVarianceMicros,
		Timeout:             c.timeout,
		BaseDelay:           c.delayAcc.BaseDelay(),
		CurrentDelay:        c.currentDelay,
		ClockSkewCorrection: c.delayAcc.skew,
		ClockDrift:          c.drift.drift,
		ClockDriftPenalty:   time.Duration(c.drift.penaltyMicros()) * time.Microsecond,
		TargetDelayMicros:   c.targetDelayMicros,
		SlowStart:           c.slowStart,
		AppLimitedSince:     appLimitedSince,
	}
}

func (c *defaultController) Timeout() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.timeout
}

func (c *defaultController) BytesAvailableInWindow() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxWindowSizeBytes > c.windowSizeBytes {
		return c.maxWindowSizeBytes - c.windowSizeBytes
	}
	return 0
}

func (c *defaultController) BytesInFlight() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.windowSizeBytes
}

func (c *defaultController) CongestionWindow() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxWindowSizeBytes
}

func (c *defaultController) MarkForResend(seqNum uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()
	packetInst, exists := c.transmissions[seqNum]
	if !exists || packetInst.Acked || packetInst.NeedResend {
		return
	}
	packetInst.NeedResend = true
	c.windowSizeBytes -= packetInst.SizeBytes
}

func (c *defaultController) OnTransmit(seqNum uint16, transmission Transmit, dataLen uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var packetInst *packetRecord
	if transmission == Initial {
		if _, exists := c.transmissions[seqNum]; exists {
			return ErrDuplicateTransmission
		}
		packetInst = &packetRecord{
			SizeBytes:        dataLen,
			NumTransmissions: 1,
			Acked:            false,
		}
		c.transmissions[seqNum] = packetInst
	} else {
		var exists bool
		packetInst, exists = c.transmissions[seqNum]
		if !exists {
			return ErrUnknownSeqNum
		}
		packetInst.NumTransmissions++
		// A packet given up as lost counts in flight again once it is
		// resent: `if (pkt->transmissions == 0 || pkt->need_resend)
		// cur_window += pkt->payload` (utp_internal.cpp:877-881). Not
		// window-checked, as libutp's send_packet is not: the caller has
		// already decided it may go.
		if packetInst.NeedResend {
			packetInst.NeedResend = false
			c.windowSizeBytes += packetInst.SizeBytes
		}
	}
	if packetInst.NumTransmissions == 1 {
		if c.windowSizeBytes+packetInst.SizeBytes > c.maxWindowSizeBytes {
			return ErrInsufficientWindowSize
		}
		c.windowSizeBytes += packetInst.SizeBytes
	}

	return nil
}

func (c *defaultController) OnAck(seqNum uint16, ack Ack) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	packetInst, exists := c.transmissions[seqNum]
	if !exists {
		return ErrUnknownSeqNum
	}

	if packetInst.Acked {
		return nil
	}
	packetInst.Acked = true
	c.transmissions[seqNum] = packetInst

	c.delayAcc.Push(ack.Delay, ack.ReceivedAt)
	// The same sample libutp feeds to our_hist also drives its clock-drift
	// estimate (utp_internal.cpp:2025-2107), on the same condition: a zero
	// means the peer has no measurement yet, and driftEstimator.push drops it.
	c.drift.push(uint32(ack.Delay.Microseconds()), ack.ReceivedAt)
	c.currentDelay = ack.Delay

	baseDelayMicros := uint32(c.delayAcc.BaseDelay().Microseconds())
	packetDelayMicros := uint32(ack.Delay.Microseconds())
	if c.algorithm == AlgorithmLEDBATPP {
		c.applyLedbatPP(baseDelayMicros, packetDelayMicros, packetInst.SizeBytes, ack.RTT, ack.ReceivedAt)
	} else {
		c.applyCongestionControl(baseDelayMicros, packetDelayMicros, packetInst.SizeBytes, ack.RTT, ack.ReceivedAt)
	}

	// "if need_resend is set, this packet has already been considered
	// timed-out, and is not included in the cur_window anymore"
	// (utp_internal.cpp:1390-1396).
	if !packetInst.NeedResend {
		c.windowSizeBytes -= packetInst.SizeBytes
	}

	// Only unretransmitted packets update the RTT estimate: an ack for a
	// packet sent more than once cannot be attributed to a particular
	// transmission. libutp applies the same rule
	// (`if (pk->transmissions == 1)`, utp_internal.cpp:1362).
	if packetInst.NumTransmissions == 1 {
		c.updateRTT(ack.RTT.Microseconds())
	}

	return nil
}

func (c *defaultController) OnRTTSample(rtt time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updateRTT(rtt.Microseconds())
}

// updateRTT is libutp's RTT estimator and timeout (utp_internal.cpp:1362-1380),
// for one sample in microseconds. Called with the lock held.
func (c *defaultController) updateRTT(ertt int64) {
	if c.rtt == 0 {
		// First sample: adopt it outright rather than easing an average
		// up from zero, which would take about twenty samples to
		// converge and leave the RTO wrong for all of them
		// (utp_internal.cpp:1364-1367).
		c.rtt = time.Duration(ertt) * time.Microsecond
		c.rttVarianceMicros = ertt / 2
	} else {
		// utp_internal.cpp:1370-1372.
		rttMicros := c.rtt.Microseconds()
		delta := rttMicros - ertt
		c.rttVarianceMicros = maxInt64(0, c.rttVarianceMicros+(absInt64(delta)-c.rttVarianceMicros)/4)
		rttMicros = rttMicros - rttMicros/8 + ertt/8
		c.rtt = time.Duration(maxInt64(rttMicros, 0)) * time.Microsecond
	}

	c.applyTimeoutAdjustment()
}

// maxWindowDecayInterval is the shortest gap between two halvings of the
// congestion window.
//
// libutp: `MAX_WINDOW_DECAY 100 // ms` (utp_internal.cpp:51), enforced by
// `can_decay_win` (:602-605) and applied once per ack that resent anything,
// not once per packet resent (:1609-1610).
const maxWindowDecayInterval = 100 * time.Millisecond

func (c *defaultController) OnLostPacket(seqNum uint16, retransmitting bool, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	packetInst, exists := c.transmissions[seqNum]
	if !exists {
		return ErrUnknownSeqNum
	}

	// Halve the window at most once per maxWindowDecayInterval.
	//
	// This was halving once for every packet declared lost. A burst of four
	// losses -- one queue overflow -- took the window to a sixteenth in a
	// single event, and with the window already at its floor the connection
	// then crawled. libutp decays once per ack that resent anything, and not
	// again for 100 ms however many acks arrive in between.
	if c.lastWindowDecay.IsZero() || now.Sub(c.lastWindowDecay) >= maxWindowDecayInterval {
		c.maxWindowSizeBytes = uint32(math.Max(float64(c.maxWindowSizeBytes/2), float64(c.minWindowSizeBytes)))
		c.lastWindowDecay = now
		// libutp leaves slow start on any decay and sets the threshold to
		// where the window ended up (utp_internal.cpp:616-617). LEDBAT++
		// leaves its own slow start on the same signal: a loss is congestion
		// however the delay looked.
		c.slowStart = false
		c.ssthreshBytes = c.maxWindowSizeBytes
		if c.algorithm == AlgorithmLEDBATPP &&
			(c.ppPhase == ppSlowStart || c.ppPhase == ppSlowdownRamp) {
			c.exitLedbatPPSlowStart(now)
		}
	}

	if !retransmitting && !packetInst.NeedResend {
		packetInst.NeedResend = true
		c.windowSizeBytes -= packetInst.SizeBytes
	}

	return nil
}

// OnTimeout is libutp's timeout branch (utp_internal.cpp:1206-1228).
//
// hasPacketsInFlight distinguishes the two cases libutp treats differently.
// This fork collapsed the window to its floor in both, so an application that
// paused long enough to hit an RTO -- routine -- restarted from two packets,
// and with no slow start took hundreds of round trips to recover on a
// high-bandwidth path.
func (c *defaultController) OnTimeout(hasPacketsInFlight bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	packetSize := c.minWindowSizeBytes / 2
	if !hasPacketsInFlight && c.maxWindowSizeBytes > packetSize {
		// "we don't have any packets in-flight, even though we could. This
		// implies that the connection is just idling. No need to be
		// aggressive about resetting the congestion window. Just let it decay
		// by a 3:rd." (utp_internal.cpp:1216-1222)
		c.maxWindowSizeBytes = maxUint32(c.maxWindowSizeBytes*2/3, packetSize)
	} else {
		// "our delay was so high that our congestion window was shrunk below
		// one packet ... reset the congestion window to fit one packet, to
		// start over again" (:1223-1228). libutp re-enters slow start here.
		c.maxWindowSizeBytes = packetSize
		c.slowStart = true
		if c.algorithm == AlgorithmLEDBATPP {
			// Back to the start of the cycle: ramp up again, and let the
			// slowdown schedule be set when that ramp ends.
			c.ppPhase = ppSlowStart
			c.ppNextSlowdownAt = time.Time{}
		}
	}
	if c.maxWindowSizeBytes < c.minWindowSizeBytes {
		c.maxWindowSizeBytes = c.minWindowSizeBytes
	}
	c.timeout = time.Duration(math.Min(float64(c.timeout*2), float64(c.maxTimeout)))
}

// applyMaxWindowSizeAdjustment adjusts the maximum window size based on the given adjustment.
// applyCongestionControl is libutp's apply_ccontrol (utp_internal.cpp:1615-1712),
// run once per acked packet rather than once per ack packet. The two are
// equivalent: the window factor scales each step by that packet's share of
// the window, so a full window of acks sums to one RTT's worth of increase
// either way.
//
// It is called with the controller's lock held.
func (c *defaultController) applyCongestionControl(
	baseDelayMicros uint32,
	packetDelayMicros uint32,
	bytesAcked uint32,
	rtt time.Duration,
	now time.Time,
) {
	// The queueing delay this ack reports: how far above the lowest delay
	// seen on this path the packet ran.
	ourDelayMicros := int64(packetDelayMicros) - int64(baseDelayMicros)
	if ourDelayMicros < 0 {
		ourDelayMicros = 0
	}

	// "the delay can never be greater than the rtt" (utp_internal.cpp:1617-1621):
	// libutp clamps our_delay to the minimum RTT of the packets this ack
	// covers. Without the clamp a peer that reports a wild timestamp -- by
	// malice, by a clock step, or by a timestamp wrap -- drives off_target
	// arbitrarily negative and collapses the window in one ack. We clamp to
	// this packet's own RTT, which is the closest thing available where the
	// controller sees one packet at a time.
	if rttMicros := rtt.Microseconds(); rttMicros > 0 && ourDelayMicros > rttMicros {
		ourDelayMicros = rttMicros
	}

	// A clock drifting fast enough to be dishonest gets a penalty added to
	// the delay it measures.
	//
	// libutp (utp_internal.cpp:1646-1650), applied after the RTT clamp above
	// and deliberately so: the penalty is allowed to push the delay past the
	// round trip, because its purpose is to make the flow yield rather than
	// to describe the path.
	//
	// This is the second of libutp's two clock-drift mechanisms and does a
	// different job from the first. delayAccumulator's shift corrects the
	// measurement for ordinary drift between honest clocks, bounded and aged
	// out. This one does not correct anything: past 40,000 ppm, which is
	// three orders of magnitude beyond crystal drift, it inflates the delay
	// so that a peer running its clock slow gains nothing by it.
	if penalty := c.drift.penaltyMicros(); penalty > 0 {
		ourDelayMicros += penalty
	}

	target := int64(c.targetDelayMicros)
	if target <= 0 {
		// utp_internal.cpp:1635-1636.
		target = 100000
	}

	offTarget := target - ourDelayMicros
	delayFactor := float64(offTarget) / float64(target)

	// libutp: min(bytes_acked, max_window) / max(max_window, bytes_acked)
	// (utp_internal.cpp:1668). Two things this fork had wrong. It divided by
	// the bytes currently *in flight* rather than by the congestion window,
	// which is a smaller denominator and so a larger factor -- with one
	// packet outstanding the factor was 1, and a single ack claimed a whole
	// RTT's worth of increase. And it had no min/max, so the factor could
	// exceed 1 outright.
	maxWindow := float64(c.maxWindowSizeBytes)
	acked := float64(bytesAcked)
	windowFactor := math.Min(acked, maxWindow) / math.Max(maxWindow, acked)

	scaledGain := float64(c.gain) * float64(c.maxWindowSizeIncBytes) * windowFactor * delayFactor

	// A sender that has not filled its window in the last second is limited
	// by the application, not by the path, so the delay it measures says
	// nothing about how much more the path would carry. libutp refuses to
	// grow the window in that case (utp_internal.cpp:1681-1686). Without
	// this, an idle-but-trickling connection grows its window without bound
	// and then dumps it all at once when the application speeds up.
	//
	if scaledGain > 0 && c.applicationLimited(now) {
		scaledGain = 0
	}

	ledbatCwnd := float64(c.maxWindowSizeBytes) + scaledGain
	if ledbatCwnd < float64(c.minWindowSizeBytes) {
		ledbatCwnd = float64(c.minWindowSizeBytes)
	}

	if c.slowStart {
		// utp_internal.cpp:1691-1702.
		ssCwnd := float64(c.maxWindowSizeBytes) + windowFactor*float64(c.minWindowSizeBytes/2)
		switch {
		case ssCwnd > float64(c.ssthreshBytes):
			c.slowStart = false
		case ourDelayMicros > int64(float64(target)*0.9):
			// "even if we're a little under the target delay, we
			// conservatively discontinue the slow start phase".
			c.slowStart = false
			c.ssthreshBytes = c.maxWindowSizeBytes
		default:
			c.maxWindowSizeBytes = uint32(math.Max(ssCwnd, ledbatCwnd))
		}
	} else {
		c.maxWindowSizeBytes = uint32(ledbatCwnd)
	}

	// utp_internal.cpp:1710.
	c.maxWindowSizeBytes = clampUint32(c.maxWindowSizeBytes, c.minWindowSizeBytes, c.maxWindowUpperBytes)
}

// applicationLimited reports whether the sender has gone more than a second
// without filling its window: libutp's `current_ms - last_maxed_out_window >
// 1000` (utp_internal.cpp:1681).
//
// A sender that has never filled its window counts as application-limited, as
// it does in libutp. libutp initialises last_maxed_out_window to 0 (:2603) and
// reads current_ms from a clock that counts from boot, not from the process or
// the connection (CLOCK_MONOTONIC on POSIX, utp_utils.cpp:158-175), so unless
// the host booted within the last second the difference is already past 1000
// and the gain is zeroed until the window first fills. This used to be the
// other way round here, on the mistaken reading that libutp's counter starts
// near zero; TestConformanceLedbatRules replays libutp's own trace and caught
// it. A bulk sender is unaffected, because its first write fills the window
// and records the time before any acknowledgement arrives. What changes is a
// sender that never fills its window: it now grows only by the slow-start
// step, and not at all after slow start, where it used to take the LEDBAT
// gain as well.
func (c *defaultController) applicationLimited(now time.Time) bool {
	return c.lastMaxedOutWindow.IsZero() || now.Sub(c.lastMaxedOutWindow) > time.Second
}

// OnWindowFull records that the sender had data to send and no window to send
// it in. libutp's `is_full` (utp_internal.cpp:945, :957).
func (c *defaultController) OnWindowFull(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastMaxedOutWindow = now
}

func clampUint32(v, lo, hi uint32) uint32 {
	if hi < lo {
		hi = lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// applyTimeoutAdjustment recomputes the retransmission timeout from the RTT
// estimate, as libutp does: rto = max(rtt + rtt_var * 4, min), capped at max.
//
// rttVarianceMicros is a microsecond count, so it has to be scaled before
// being added to a Duration. It was previously passed to time.Duration
// directly, which reads it as nanoseconds -- understating the variance term
// by a factor of 1000 and leaving the timeout pinned to minTimeout.
func (c *defaultController) applyTimeoutAdjustment() {
	rto := c.rtt + time.Duration(c.rttVarianceMicros*4)*time.Microsecond
	if rto < c.minTimeout {
		rto = c.minTimeout
	}
	if rto > c.maxTimeout {
		rto = c.maxTimeout
	}
	c.timeout = rto
}

// computeMaxWindowSizeAdjustment returns the adjustment in bytes to the maximum window (i.e. congestion window) size
// based on the delta between the packet delay and the target delay and on the portion of the total
// in-flight bytes that the packet corresponds to.
func computeMaxWindowSizeAdjustment(
	targetDelayMicros uint32,
	baseDelayMicros uint32,
	packetDelayMicros uint32,
	windowSizeBytes uint32,
	packetSizeBytes uint32,
	maxWindowSizeIncBytes uint32,
	gain float32,
) int64 {
	// Adjust the delay based on the base delay.
	delayMicros := int64(packetDelayMicros) - int64(baseDelayMicros)

	offTargetMicros := int64(targetDelayMicros) - delayMicros
	delayFactor := float64(offTargetMicros) / float64(targetDelayMicros)
	windowFactor := float64(packetSizeBytes) / float64(windowSizeBytes)

	scaledGain := float64(gain) * float64(maxWindowSizeIncBytes) * delayFactor * windowFactor

	return int64(scaledGain)
}

// wrappingLessThanUint32 is libutp's wrapping_compare_less over the 32-bit
// microsecond timestamp space:
//
//	bool wrapping_compare_less(uint32 l, uint32 r, uint32 mask) {
//	    uint32 dist_down = (l - r) & mask;
//	    uint32 dist_up = (r - l) & mask;
//	    return dist_up < dist_down;
//	}
//	                                        (utp_utils.h)
func wrappingLessThanUint32(a, b uint32) bool {
	return (b - a) < (a - b)
}

// peerDelayBuckets is how many buckets the peer-direction base delay is kept
// in. libutp uses DELAY_BASE_HISTORY = 13 one-minute buckets
// (utp_internal.cpp:50, rotated at :367-380); the count is kept and the
// bucket length is the configured window divided by it.
const peerDelayBuckets = 13

// peerDelayHist tracks the base delay of packets arriving *from* the peer:
// libutp's `their_hist`. Its only purpose is noticing that base fall, which
// is how clock drift becomes visible.
//
// It is deliberately not a delayAccumulator, for one reason: these samples
// must stay in wrapping 32-bit microsecond arithmetic.
//
// A clock drifting slow makes the measured inbound delay *shrink*, and it
// keeps shrinking -- at 100ppm it loses 10ms every 100 seconds, so on an
// ordinary path it passes zero within minutes and the subtraction wraps. In
// Duration terms that wrapped value is about 4295 seconds, which the
// connection then caps to one second as an unusable clock reading, and the
// base stops moving: precisely when the drift has grown large enough to
// matter, the evidence for it disappears.
//
// That was measured before this type existed. The correction reached about
// 10ms and then plateaued in every run, whatever the drift rate -- which is
// how long it took for the accumulated skew to pass the inbound one-way
// delay of the emulated path.
//
// libutp has no such problem: `their_delay` is a raw wrapping uint32
// throughout and every comparison in DelayHist goes through
// wrapping_compare_less. This does the same.
type peerDelayHist struct {
	buckets     [peerDelayBuckets]uint32
	idx         int
	rotatedAt   time.Time
	bucketLen   time.Duration
	base        uint32
	initialised bool
}

func newPeerDelayHist(window time.Duration) *peerDelayHist {
	bucketLen := window / peerDelayBuckets
	if bucketLen <= 0 {
		bucketLen = time.Second
	}
	return &peerDelayHist{bucketLen: bucketLen}
}

// addSample records one inbound delay and returns the base before and after,
// so a caller can see how far it fell. Mirrors DelayHist.add_sample
// (utp_internal.cpp:291-381), with the bucket rotation driven by the
// configured window rather than a hard-coded minute.
func (h *peerDelayHist) addSample(sample uint32, now time.Time) (prev, current uint32, ok bool) {
	if !h.initialised {
		for i := range h.buckets {
			h.buckets[i] = sample
		}
		h.base = sample
		h.rotatedAt = now
		h.initialised = true
		// No previous base to have fallen from. libutp's `prev_delay_base != 0`.
		return sample, sample, false
	}

	prev = h.base
	if wrappingLessThanUint32(sample, h.buckets[h.idx]) {
		h.buckets[h.idx] = sample
	}
	if wrappingLessThanUint32(sample, h.base) {
		h.base = sample
	}

	if now.Sub(h.rotatedAt) > h.bucketLen {
		h.rotatedAt = now
		h.idx = (h.idx + 1) % peerDelayBuckets
		h.buckets[h.idx] = sample
		h.base = h.buckets[0]
		for _, b := range h.buckets {
			if wrappingLessThanUint32(b, h.base) {
				h.base = b
			}
		}
	}
	return prev, h.base, true
}

// maxSkewAdjustment bounds a single clock-drift correction.
//
// libutp: "never adjust more than 10 milliseconds" (utp_internal.cpp:2011).
// A larger apparent fall in the peer's base delay is far more likely to be
// the reverse path genuinely getting faster -- a route change, or a queue
// draining -- than a clock jumping, and treating that as skew would blind
// this sender's own delay signal by however much it moved.
const maxSkewAdjustment = 10 * time.Millisecond

// OnPeerDelay reports the one-way delay measured on a packet arriving from
// the peer, and corrects this sender's delay signal for clock drift.
//
// libutp:
//
//	uint32 prev_delay_base = conn->their_hist.delay_base;
//	if (their_delay != 0) conn->their_hist.add_sample(their_delay, conn->ctx->current_ms);
//
//	// if their new delay base is less than their previous one
//	// we should shift our delay base in the other direction in order
//	// to take the clock skew into account
//	if (prev_delay_base != 0 &&
//	    wrapping_compare_less(conn->their_hist.delay_base, prev_delay_base, TIMESTAMP_MASK)) {
//	    // never adjust more than 10 milliseconds
//	    if (prev_delay_base - conn->their_hist.delay_base <= 10000)
//	        conn->our_hist.shift(prev_delay_base - conn->their_hist.delay_base);
//	}
//	                                        (utp_internal.cpp:2002-2014)
//
// # Why a fall in the other direction means drift in this one
//
// A one-way delay is a difference between two clocks, so it carries their
// relative rate error. If this end's clock runs slow, the time it stamps into
// its own packets falls further behind, and the peer's measurement of this
// sender's path -- which is what LEDBAT here runs on -- grows without bound.
// The same slow clock makes packets *from* the peer appear to arrive sooner,
// so the delay measured in that direction shrinks by exactly as much.
//
// So a falling base delay in the peer's direction is the visible half of a
// bias that is inflating the invisible half. Raising this sender's base by
// the same amount cancels it.
//
// # What it is worth here
//
// Measured, before it existed: the phantom queueing delay settles at the
// delay window multiplied by the drift rate, and what it costs is fairness
// rather than throughput -- a drifted flow took 35% of a shared bottleneck
// against an undrifted flow's 65%. See KNOWN-LIMITATIONS.md, and
// netem.TestClockDriftYieldsShareAtASharedBottleneck.
func (c *defaultController) OnPeerDelay(sample uint32, now time.Time) {
	// libutp: `if (their_delay != 0)`. A zero means the peer has not stamped
	// a usable timestamp, not that the path is instant.
	if sample == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	prev, current, ok := c.peerDelayHist.addSample(sample, now)
	if !ok {
		return
	}
	// Only a *fall* is evidence of drift. A rise is a queue building in the
	// other direction, which says nothing about either clock.
	if !wrappingLessThanUint32(current, prev) {
		return
	}
	drop := time.Duration(prev-current) * time.Microsecond
	if drop > 0 && drop <= maxSkewAdjustment {
		c.delayAcc.shift(drop)
	}
}

// now is this controller's current time, nil-safe for controllers built
// directly in unit tests.
func (c *defaultController) now() time.Time {
	if c.clk != nil {
		return c.clk.Now()
	}
	return time.Now()
}

// absInt64 returns the absolute value of x.
func absInt64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// Additional methods for handling acknowledgments, lost packets, and timeouts would follow...

type delay struct {
	Value    time.Duration
	Deadline time.Time
}

type delayAccumulator struct {
	delays *delayHeap
	window time.Duration
	// clk decides when a sample has aged out of the window. Nil means the
	// real clock; read it through now().
	clk Clock
	// skew is the total correction applied so far, to cancel clock drift.
	// See shift. Zero unless a correction has been applied, and every path
	// through this type is a no-op while it is zero.
	//
	// Samples are stored with the correction *as it stood when they were
	// pushed* already subtracted, so reading them back with the current
	// total gives each one only the correction accumulated since it entered
	// the window. That is what stops the correction running away; see shift.
	skew time.Duration
}

func newDelayAccumulator(window time.Duration) *delayAccumulator {
	return &delayAccumulator{
		delays: &delayHeap{},
		window: window,
	}
}

func newDelayAccumulatorWithClock(window time.Duration, clk Clock) *delayAccumulator {
	return &delayAccumulator{
		delays: &delayHeap{},
		window: window,
		clk:    clk,
	}
}

// now is nil-safe, for the accumulators built directly in unit tests.
func (da *delayAccumulator) now() time.Time {
	if da.clk != nil {
		return da.clk.Now()
	}
	return time.Now()
}

func (da *delayAccumulator) Push(delayTime time.Duration, receivedAt time.Time) {
	heap.Push(da.delays, delay{
		Value:    delayTime - da.skew,
		Deadline: receivedAt.Add(da.window),
	})
}

// shift raises the base delay, cancelling drift that has inflated every
// sample in it.
//
// libutp:
//
//	void shift(const uint32 offset)
//	{
//	    // increase all of our base delays by this amount
//	    // this is used to take clock skew into account
//	    // by observing the other side's changes in its base_delay
//	    for (size_t i = 0; i < DELAY_BASE_HISTORY; i++) delay_base_hist[i] += offset;
//	    delay_base += offset;
//	}
//	                                        (utp_internal.cpp:277-289)
//
// Raising the base lowers the reported queueing delay by the same amount,
// which is the point: the samples were inflated by a clock running slow, and
// the queue they appear to show is not there.
//
// # Why the correction must age out, and how
//
// libutp shifts a *stored* base and thirteen stored history buckets, and
// rotates one of those buckets onto a fresh, unshifted sample every minute
// (utp_internal.cpp:367-380). So a correction only ever survives as long as
// the bucket carrying it, and the history re-bases itself continuously.
//
// That bound is not decoration. Shifts arrive for as long as the peer's base
// keeps falling, which under sustained drift is forever, and they accumulate
// at the full drift rate -- while the error they exist to cancel is only the
// window multiplied by that rate, a constant. A correction that never ages
// out overshoots by however many windows the connection has been open, drives
// the base past every sample, and leaves the controller reading zero queueing
// delay whatever the path is doing.
//
// **That was measured, not reasoned about.** With an unbounded correction a
// drifted flow stopped seeing the bottleneck at all -- 4ms of perceived queue
// against its undrifted neighbour's 40ms -- and took 56.6% of a shared link
// instead of the 35% it took with no correction at all. Delay-blind, and
// winning because of it.
//
// Here the samples are stored net of the correction that stood when they were
// pushed, so reading them back against the current total gives each sample
// only what has accumulated since it entered the window. Old samples age out
// and take their share of the correction with them, which is libutp's bucket
// rotation expressed continuously, and the steady state is exactly the error
// to be cancelled rather than an unbounded multiple of it.
func (da *delayAccumulator) shift(offset time.Duration) {
	if offset <= 0 {
		return
	}
	da.skew += offset
}

func (da *delayAccumulator) BaseDelay() time.Duration {
	now := da.now()
	for da.delays.Len() > 0 {
		min := (*da.delays)[0]
		if now.After(min.Deadline) {
			heap.Pop(da.delays)
			continue
		}
		// Each stored sample carries the correction that stood when it was
		// pushed already subtracted, so adding the current total gives it
		// only what has accumulated since. libutp's equivalent bound --
		// `if (sample < delay_base) delay_base = sample`
		// (utp_internal.cpp:351-355) -- falls out of that: the newest sample
		// is in this heap too, so the minimum cannot sit more than one
		// packet's worth of correction above it.
		base := min.Value + da.skew
		if base < 0 {
			base = 0
		}
		return base
	}
	return time.Duration(0)
}

type delayHeap []delay

func (h delayHeap) Len() int           { return len(h) }
func (h delayHeap) Less(i, j int) bool { return h[i].Value < h[j].Value }
func (h delayHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *delayHeap) Push(x interface{}) {
	*h = append(*h, x.(delay))
}

func (h *delayHeap) Pop() interface{} {
	old := *h
	n := len(old) - 1
	x := old[n]
	*h = old[:n]
	return x
}
