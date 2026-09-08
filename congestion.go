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
	WindowSize            uint32
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
	Timeout() time.Duration
	BytesAvailableInWindow() uint32
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
	// difference between the current delay and this.
	BaseDelay time.Duration
	// TargetDelayMicros is the standing queue the controller aims for.
	TargetDelayMicros uint32
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
		delayAcc:          newDelayAccumulator(config.DelayWindow),
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
	return ControllerStats{
		WindowSizeBytes:    c.windowSizeBytes,
		MaxWindowSizeBytes: c.maxWindowSizeBytes,
		MinWindowSizeBytes: c.minWindowSizeBytes,
		RTT:                c.rtt,
		RTTVarianceMicros:  c.rttVarianceMicros,
		Timeout:            c.timeout,
		BaseDelay:          c.delayAcc.BaseDelay(),
		TargetDelayMicros:  c.targetDelayMicros,
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

	baseDelayMicros := uint32(c.delayAcc.BaseDelay().Microseconds())
	packetDelayMicros := uint32(ack.Delay.Microseconds())
	if c.algorithm == AlgorithmLEDBATPP {
		c.applyLedbatPP(baseDelayMicros, packetDelayMicros, packetInst.SizeBytes, ack.RTT, ack.ReceivedAt)
	} else {
		c.applyCongestionControl(baseDelayMicros, packetDelayMicros, packetInst.SizeBytes, ack.RTT, ack.ReceivedAt)
	}

	c.windowSizeBytes -= packetInst.SizeBytes

	// Only unretransmitted packets update the RTT estimate: an ack for a
	// packet sent more than once cannot be attributed to a particular
	// transmission. libutp applies the same rule
	// (`if (pk->transmissions == 1)`, utp_internal.cpp:1362).
	if packetInst.NumTransmissions == 1 {
		ertt := ack.RTT.Microseconds()

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

	return nil
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

	if !retransmitting {
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
	// libutp initialises last_maxed_out_window to zero and compares it
	// against a millisecond counter that also starts near zero, so early in a
	// connection the difference is small and growth is allowed. A zero
	// time.Time here is far in the past instead, which would block all growth
	// until the window was first filled, so an unset value means "not yet
	// application-limited" and permits growth.
	if scaledGain > 0 && !c.lastMaxedOutWindow.IsZero() &&
		now.Sub(c.lastMaxedOutWindow) > time.Second {
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
}

func newDelayAccumulator(window time.Duration) *delayAccumulator {
	return &delayAccumulator{
		delays: &delayHeap{},
		window: window,
	}
}

func (da *delayAccumulator) Push(delayTime time.Duration, receivedAt time.Time) {
	heap.Push(da.delays, delay{
		Value:    delayTime,
		Deadline: receivedAt.Add(da.window),
	})
}

func (da *delayAccumulator) BaseDelay() time.Duration {
	now := time.Now()
	for da.delays.Len() > 0 {
		min := (*da.delays)[0]
		if now.After(min.Deadline) {
			heap.Pop(da.delays)
		} else {
			return min.Value
		}
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
