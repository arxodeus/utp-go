package utp

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultTargetMicros = 100000 * time.Microsecond
	// defaultInitialTimeout is the initial retransmission timeout and the
	// connect timer, 3 seconds.
	defaultInitialTimeout = 3 * time.Second
	// defaultMinTimeout is the RTO floor: rto = max(rtt + rtt_var*4, 1s).
	defaultMinTimeout = 1000 * time.Millisecond
	// defaultMaxTimeout is zero: no cap. The timeout doubles on each
	// expiry and the connection gives up at the fifth.
	// ConnectionConfig.MaxTimeout sets a cap.
	defaultMaxTimeout = 0
	// defaultMaxPacketSizeBytes is the largest datagram the connection will
	// try: the ceiling of the path-MTU search, which starts here and comes
	// down when a probe of this size is lost. 1402 bytes is a 1500-byte
	// Ethernet MTU less the IPv4 and UDP headers and an allowance for
	// tunnel encapsulation. IPv6 peers start at 1232 (path_mtu.go). See
	// mtu.go.
	defaultMaxPacketSizeBytes = 1402
	// defaultMaxWindowSizeIncBytes is the most the congestion window may
	// grow in one RTT.
	defaultMaxWindowSizeIncBytes = 3000
	defaultGain                  = 1.0
	// defaultDelayWindow is how far back the base delay reaches: thirteen
	// one-minute buckets. See baseDelayHist.
	defaultDelayWindow = 13 * time.Minute
	// ppRTTWindow is how far back LEDBAT++'s minimum round trip reaches. A
	// round trip is measured on one clock and cannot drift, so a shorter
	// window serves.
	ppRTTWindow = 2 * time.Minute
)

const (
	transmitInitial = iota
	transmitRetransmission
)

type transmitKind int

type packetRecord struct {
	// SentWindow is the congestion window when the packet was last
	// transmitted, against which its acknowledgement's share of a round
	// trip's growth is reckoned. See ackBatch.credit.
	SentWindow       uint32
	SizeBytes        uint32
	NumTransmissions uint32
	Acked            bool
	// NeedResend marks a packet declared lost: its bytes no longer count in
	// flight. Resending it counts them again; acknowledging it does not
	// subtract them a second time.
	NeedResend bool
	// SentAt is when the packet was last transmitted. LEDBAT++ uses it to
	// tell which window a loss belongs to; see OnLostPacket.
	SentAt time.Time
}

type ackSample struct {
	Delay      time.Duration
	RTT        time.Duration
	ReceivedAt time.Time
}

var (
	errInsufficientWindowSize = errors.New("utp: insufficient window size")
	errUnknownSeqNum          = errors.New("utp: unknown sequence number")
	errDuplicateTransmission  = errors.New("utp: duplicate transmission")
	errOutOfOrderTransmit     = errors.New("utp: out of order transmit")
	errTransmitExceedsWindow  = errors.New("utp: transmit exceeds available send window")
	errAckOfUnsent            = errors.New("utp: acknowledgement of a packet never sent")

	// errInternal ends a connection whose own bookkeeping has failed: a
	// bug, not anything the peer did or the network lost.
	errInternal = errors.New("utp: internal error")
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
	// Clock is the controller's source of time: the delay window's expiry
	// and the age of the application-limited mark. Defaults to defaultClock.
	Clock      clockSource
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
		Clock:                 defaultClock,
		// The window ceiling, 1 MB by default.
		WindowSize: DefaultWindowSize,
	}
}

type controller interface {
	OnTransmit(seqNum uint16, transmit transmitKind, dataLen uint32) error
	OnAck(seqNum uint16, ack ackSample) error
	OnLostPacket(seqNum uint16, retransmitting bool, now time.Time) error
	OnTimeout(hasPacketsInFlight bool)
	OnWindowFull(now time.Time)
	// OnTick lets a controller act on the passage of time when no acks are
	// arriving. Only LEDBAT++ needs it, for its slowdowns.
	OnTick(now time.Time)
	// OnPeerDelay reports the one-way delay measured on a packet from the
	// peer, in raw wrapping microseconds. It is not a congestion signal for
	// this sender; it is used to detect clock drift. See
	// defaultController.OnPeerDelay.
	OnPeerDelay(sample uint32, now time.Time)
	// OnAckDelay reports the delay the peer measured on our packets, as
	// carried by one incoming acknowledgement, once per acknowledgement and
	// before OnAck for the packets it covers.
	OnAckDelay(delay time.Duration, now time.Time)
	// ApplyAck ends one incoming acknowledgement: after OnAck for every
	// packet it covers, and before any loss it reveals is reported. Classic
	// LEDBAT updates the window here, once, for all the bytes the
	// acknowledgement covered.
	ApplyAck()
	Timeout() time.Duration
	BytesAvailableInWindow() uint32
	// BytesInFlight returns the payload bytes sent and neither acknowledged
	// nor declared lost.
	BytesInFlight() uint32
	// CongestionWindow returns the congestion window in bytes.
	CongestionWindow() uint32
	// OnRTTSample feeds a round trip measured outside OnAck: the SYN's, on
	// the dialing side.
	OnRTTSample(rtt time.Duration)
	// MarkForResend declares a packet lost: its bytes stop counting in
	// flight until it is sent again. A packet already acknowledged or
	// already marked is left alone.
	MarkForResend(seqNum uint16)
	// Stats returns a snapshot of the controller's internal state.
	Stats() controllerStats
}

// controllerStats is a snapshot of a congestion controller's state.
type controllerStats struct {
	// WindowSizeBytes is the volume currently considered in flight.
	WindowSizeBytes uint32
	// MaxWindowSizeBytes is the congestion window: what the controller
	// currently believes the path will carry.
	MaxWindowSizeBytes uint32
	// MinWindowSizeBytes is the floor the window will not drop below.
	MinWindowSizeBytes uint32
	// RTT is the smoothed round-trip estimate in whole milliseconds, and so
	// zero on a path faster than one.
	RTT time.Duration
	// FineRTT is the same estimator run in microseconds, for the loss
	// probe's timeout, which needs a nonzero value on sub-millisecond
	// paths.
	FineRTT time.Duration
	// MinFineRTT is the lowest round-trip sample, in microseconds, from a
	// packet sent once. The loss probe uses it to tell an acknowledgement of
	// its resend from one of the original (see probeAnswered).
	MinFineRTT time.Duration
	// SpuriousLossesUndone counts cuts for lost packets undone because the
	// packet proved only late. See defaultController.undoSpuriousLoss.
	SpuriousLossesUndone uint64
	// RTTVarianceMicros is the RTT variance estimate, in microseconds.
	RTTVarianceMicros int64
	// Timeout is the current retransmission timeout.
	Timeout time.Duration
	// BaseDelay is the lowest one-way delay observed in the delay window,
	// LEDBAT's estimate of the path with no queue. Queuing delay is
	// CurrentDelay minus this. Both are the peer's measurement of our
	// outbound path, the direction LEDBAT controls.
	BaseDelay time.Duration
	// ClockSkewCorrection is how much has been added to the base delay to
	// cancel clock drift between the two ends. Zero when the clocks run at
	// the same rate.
	ClockSkewCorrection time.Duration
	// ClockDrift and ClockDriftPenalty are the drift estimate and the delay
	// penalty it incurs. See ConnectionMetrics.
	ClockDrift        int64
	ClockDriftPenalty time.Duration
	// CurrentDelay is the most recent sample of the same series.
	CurrentDelay time.Duration
	// FilteredQueueingDelay is the queue our packets meet on the way to the
	// peer: the least of the last three samples, each relative to the base
	// at the time. For a connection that only receives, these are its
	// acknowledgements.
	FilteredQueueingDelay time.Duration
	// TargetDelayMicros is the standing queue the controller aims for.
	TargetDelayMicros uint32
	// SlowStart reports whether the controller is still in slow start, where
	// the window grows whether or not the application fills it.
	SlowStart bool
	// AppLimitedSince is how long since the sender last had data to send
	// and no window to send it in. Growth is suppressed once this exceeds
	// one second. Zero means the window has never been filled.
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
	// freeRecords are records forgotten and ready for reuse. See
	// forgetTransmission.
	freeRecords []*packetRecord
	// undo is what the last cut for a lost packet changed, while it can
	// still be undone. See undoSpuriousLoss.
	undo lossUndo
	// spuriousLossesUndone counts undoSpuriousLoss's undos.
	spuriousLossesUndone uint64
	fineRTT              time.Duration // see controllerStats.FineRTT
	minFineRTT           time.Duration // see controllerStats.MinFineRTT
	delayAcc             *baseDelayHist
	// curDelayHist holds the last curDelaySize queuing-delay samples, each
	// relative to the base when it arrived, zero until filled. Classic
	// LEDBAT's delay is their minimum.
	curDelayHist [curDelaySize]uint32
	curDelayIdx  int
	// ackBatch is what the acknowledgement being processed has covered so
	// far, for ApplyAck. Classic LEDBAT only.
	ackBatch ackBatch
	// lastWindowDecay is when the congestion window was last halved for
	// loss.
	lastWindowDecay time.Time
	// slowStart and ssthreshBytes are the slow-start phase and its exit
	// threshold. Every connection starts in slow start, growing the window
	// by a packet per acknowledged packet until the threshold is crossed or
	// the delay approaches the target.
	slowStart     bool
	ssthreshBytes uint32
	// maxWindowUpperBytes is the upper bound on the congestion window.
	maxWindowUpperBytes uint32
	// lastMaxedOutWindow is when the sender last had data to send and no
	// window to send it in. A sender that has not filled its window in the
	// last second is limited by the application, and growing the window
	// further would measure nothing.
	lastMaxedOutWindow time.Time

	// peerDelayHist is the base delay of packets arriving from the peer. It
	// is not a congestion signal for this sender; its only use is detecting
	// clock drift. See OnPeerDelay.
	peerDelayHist *peerDelayHist

	// drift estimates the long-run slope of the delay the peer reports for
	// our packets. See driftEstimator and applyCongestionControl.
	drift *driftEstimator

	// clk is the controller's source of time. Never nil after
	// newDefaultController; read it through now.
	clk clockSource

	// currentDelay is the most recent delay sample, the newest value of the
	// series BaseDelay is the minimum of. Reported only.
	currentDelay time.Duration

	// algorithm selects the congestion controller. Everything above is
	// shared; the LEDBAT++ state below is used only when it is selected.
	algorithm CongestionAlgorithm
	// minRTT is LEDBAT++'s base round trip, the lowest within the delay
	// window, from which its gain is computed (§4.2) and against which its
	// queuing delay is measured (§4.5).
	minRTT time.Duration
	// ppRTTBase holds the round-trip samples minRTT is the minimum of.
	ppRTTBase *delayAccumulator
	// ppRTTRecent is the last ledbatPPRTTFilter round-trip samples, and
	// ppRTTCount how many of them are filled. §4.5: "filter the round trip
	// measurements by using the minimum of the 4 most recent delay samples".
	ppRTTRecent [ledbatPPRTTFilter]time.Duration
	ppRTTCount  int
	ppRTTNext   int
	// ppPhase and the four fields after it drive the periodic slowdowns that
	// keep the base-delay estimate accurate (§4.4).
	ppPhase             ledbatPPPhase
	ppSlowdownStartedAt time.Time
	ppFreezeUntil       time.Time
	ppNextSlowdownAt    time.Time
	ppSlowdownSsthresh  uint32
	// ppWindowFraction is the fraction of a byte LEDBAT++'s last update in
	// congestion avoidance added to the window but could not store in it.
	ppWindowFraction float64

	mu sync.Mutex
}

func newDefaultController(config *ctrlConfig) *defaultController {
	clk := config.Clock
	if clk == nil {
		clk = defaultClock
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
		// The initial RTT variance, 800 ms, so a timeout computed before any
		// sample is conservative. The first sample supersedes it.
		rttVarianceMicros: (800 * time.Millisecond).Microseconds(),
		transmissions:     make(map[uint16]*packetRecord),
		clk:               clk,
		delayAcc:          newBaseDelayHist(config.DelayWindow, clk),
		ppRTTBase:         newDelayAccumulatorWithClock(ppRTTWindow, clk),
		peerDelayHist:     newPeerDelayHist(config.DelayWindow),
		// The first averaging slot ends five seconds after the connection is
		// created, not after the first sample.
		drift: newDriftEstimator(clk.Now()),
		// Every connection starts in slow start with the threshold at the
		// window ceiling.
		slowStart:           true,
		ssthreshBytes:       config.WindowSize,
		maxWindowUpperBytes: config.WindowSize,
		algorithm:           config.Algorithm,
	}
	// A ceiling below the floor would pin the window shut.
	if ctrl.maxWindowUpperBytes == 0 {
		ctrl.maxWindowUpperBytes = DefaultWindowSize
		ctrl.ssthreshBytes = DefaultWindowSize
	}
	if ctrl.maxWindowUpperBytes < ctrl.minWindowSizeBytes {
		// A window smaller than two packets is raised to two: below that
		// nothing can be sent.
		ctrl.maxWindowUpperBytes = ctrl.minWindowSizeBytes
		if ctrl.ssthreshBytes < ctrl.minWindowSizeBytes {
			ctrl.ssthreshBytes = ctrl.minWindowSizeBytes
		}
	}
	if config.Algorithm == AlgorithmLEDBATPP {
		// draft §4.5: LEDBAT++ targets 60 ms of queuing delay rather than
		// RFC 6817's 100 ms.
		ctrl.targetDelayMicros = uint32(ledbatPPTargetDelay.Microseconds())
		// draft §4.1: the initial window is 2 packets, which is already
		// minWindowSizeBytes.
		ctrl.maxWindowSizeBytes = ctrl.minWindowSizeBytes
		ctrl.ppPhase = ppSlowStart
	}
	return ctrl
}

// Stats returns a snapshot of this controller's state.
func (c *defaultController) Stats() controllerStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	var appLimitedSince time.Duration
	if !c.lastMaxedOutWindow.IsZero() {
		appLimitedSince = c.now().Sub(c.lastMaxedOutWindow)
	}
	return controllerStats{
		WindowSizeBytes:       c.windowSizeBytes,
		MaxWindowSizeBytes:    c.maxWindowSizeBytes,
		MinWindowSizeBytes:    c.minWindowSizeBytes,
		RTT:                   c.rtt,
		FineRTT:               c.fineRTT,
		MinFineRTT:            c.minFineRTT,
		SpuriousLossesUndone:  c.spuriousLossesUndone,
		RTTVarianceMicros:     c.rttVarianceMicros,
		Timeout:               c.timeout,
		BaseDelay:             c.delayAcc.BaseDelay(),
		CurrentDelay:          c.currentDelay,
		FilteredQueueingDelay: time.Duration(c.filteredDelayMicros()) * time.Microsecond,
		ClockSkewCorrection:   c.delayAcc.skew,
		ClockDrift:            c.drift.drift,
		ClockDriftPenalty:     time.Duration(c.drift.penaltyMicros()) * time.Microsecond,
		TargetDelayMicros:     c.targetDelayMicros,
		SlowStart:             c.slowStart,
		AppLimitedSince:       appLimitedSince,
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

// setWindowCeiling changes the upper bound on a live connection's congestion
// window. Nothing more is sent beyond it, and the window is clamped to it at
// once. The slow-start threshold is left alone. Zero restores the default,
// and a ceiling below the floor is raised to it.
func (c *defaultController) setWindowCeiling(bytes uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if bytes == 0 {
		bytes = DefaultWindowSize
	}
	if bytes < c.minWindowSizeBytes {
		bytes = c.minWindowSizeBytes
	}
	c.maxWindowUpperBytes = bytes
	if c.maxWindowSizeBytes > bytes {
		c.maxWindowSizeBytes = bytes
	}
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

// lossUndoEnabled enables undoing a cut whose loss proved spurious. Tests
// switch it off.
var lossUndoEnabled = func() *atomic.Bool {
	b := new(atomic.Bool)
	b.Store(true)
	return b
}()

// lossUndo is the window a cut for a lost packet replaced, kept until that
// packet's fate is known.
type lossUndo struct {
	valid              bool
	seq                uint16
	maxWindow          uint32
	ssthresh           uint32
	ppSlowdownSsthresh uint32
	lastWindowDecay    time.Time
}

// lossCause reports the packet whose loss made the last cut, while that cut
// can still be undone.
func (c *defaultController) lossCause() (uint16, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.undo.seq, c.undo.valid
}

// undoSpuriousLoss restores the window that a cut for seq replaced, because
// the packet was only late and the original arrived. It reports whether
// there was such a cut to undo.
//
// This is RFC 4015's response to a spurious retransmission, as detected by
// RFC 3522: the window and threshold go back to what they were. On a link
// that reorders, a packet overtaken by later ones is taken for lost and the
// window halved; this undoes that once the acknowledgement shows the
// original arrived first. Slow start is not resumed, and a newer cut,
// another loss or a timeout in between leaves the cut standing.
func (c *defaultController) undoSpuriousLoss(seq uint16) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	u := c.undo
	if !u.valid || u.seq != seq {
		return false
	}
	c.undo.valid = false
	c.maxWindowSizeBytes = maxUint32(c.maxWindowSizeBytes, u.maxWindow)
	c.ssthreshBytes = maxUint32(c.ssthreshBytes, u.ssthresh)
	c.ppSlowdownSsthresh = maxUint32(c.ppSlowdownSsthresh, u.ppSlowdownSsthresh)
	c.lastWindowDecay = u.lastWindowDecay
	c.spuriousLossesUndone++
	return true
}

// forgetTransmission drops the record of an acknowledged packet the sender no
// longer holds and keeps it for reuse, so the records track the send window.
// A record still unacknowledged is kept.
func (c *defaultController) forgetTransmission(seqNum uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, exists := c.transmissions[seqNum]
	if !exists || !rec.Acked {
		return
	}
	delete(c.transmissions, seqNum)
	c.freeRecords = append(c.freeRecords, rec)
}

func (c *defaultController) OnTransmit(seqNum uint16, transmission transmitKind, dataLen uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var packetInst *packetRecord
	if transmission == transmitInitial {
		// A record already acknowledged is from 65,536 packets ago: sequence
		// numbers wrap, and the new packet takes its place. Only one still
		// outstanding is a real duplicate.
		old, exists := c.transmissions[seqNum]
		if exists && !old.Acked {
			return errDuplicateTransmission
		}
		switch {
		case exists:
			packetInst = old
		case len(c.freeRecords) > 0:
			packetInst = c.freeRecords[len(c.freeRecords)-1]
			c.freeRecords = c.freeRecords[:len(c.freeRecords)-1]
		default:
			packetInst = new(packetRecord)
		}
		*packetInst = packetRecord{
			SizeBytes:        dataLen,
			NumTransmissions: 1,
			Acked:            false,
		}
		c.transmissions[seqNum] = packetInst
		packetInst.SentAt = c.now()
		packetInst.SentWindow = c.maxWindowSizeBytes
	} else {
		var exists bool
		packetInst, exists = c.transmissions[seqNum]
		if !exists {
			return errUnknownSeqNum
		}
		packetInst.NumTransmissions++
		packetInst.SentAt = c.now()
		packetInst.SentWindow = c.maxWindowSizeBytes
		// A packet declared lost counts in flight again once resent. It is not
		// window-checked: the caller has already decided it may go.
		if packetInst.NeedResend {
			packetInst.NeedResend = false
			c.windowSizeBytes += packetInst.SizeBytes
		}
	}
	if packetInst.NumTransmissions == 1 {
		if c.windowSizeBytes+packetInst.SizeBytes > c.maxWindowSizeBytes {
			return errInsufficientWindowSize
		}
		c.windowSizeBytes += packetInst.SizeBytes
	}

	return nil
}

// ackBatch accumulates what one incoming acknowledgement covers: bytes and
// the minimum round trip.
type ackBatch struct {
	// credit is each acknowledged packet's bytes over the window it was
	// sent in, summed: the share of a round trip's growth this
	// acknowledgement earns. See applyCongestionControl.
	credit  float64
	packets int
	bytes   uint32
	minRTT  time.Duration
	delay   time.Duration
	at      time.Time
}

// ackBatchRTTFallback is the round trip assumed for a packet when the clock
// has not moved since it was sent.
const ackBatchRTTFallback = 50 * time.Millisecond

// curDelaySize is how many recent delay samples the filtered delay is the
// minimum of.
const curDelaySize = 3

// OnAckDelay records one incoming acknowledgement's delay sample. See the
// controller interface. Zero means no measurement (the peer has not timed
// one of our packets yet, or sent the MaxInt32 sentinel); it is not recorded
// and congestion control does not run for that acknowledgement.
func (c *defaultController) OnAckDelay(delay time.Duration, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if delay <= 0 {
		return
	}
	c.delayAcc.Push(delay, now)
	// The same sample drives the clock-drift estimate, on the same
	// condition.
	c.drift.push(uint32(delay.Microseconds()), now)
	queueing := delay - c.delayAcc.BaseDelay()
	if queueing < 0 {
		queueing = 0
	}
	c.curDelayHist[c.curDelayIdx] = uint32(queueing.Microseconds())
	c.curDelayIdx = (c.curDelayIdx + 1) % curDelaySize
}

// filteredDelayMicros returns the least of the last curDelaySize queuing-delay
// samples, the delay congestion control works from. One late packet does not
// move it; three do.
func (c *defaultController) filteredDelayMicros() uint32 {
	v := c.curDelayHist[0]
	for _, d := range c.curDelayHist[1:] {
		if d < v {
			v = d
		}
	}
	return v
}

// QueueingDelay returns Stats().FilteredQueueingDelay without building the
// rest of the snapshot, for the receiver's acknowledgement rule, which reads
// it for every data packet.
func (c *defaultController) QueueingDelay() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Duration(c.filteredDelayMicros()) * time.Microsecond
}

// ApplyAck ends one acknowledgement for classic LEDBAT. See the controller
// interface.
func (c *defaultController) ApplyAck() {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.ackBatch
	c.ackBatch = ackBatch{}
	if b.packets == 0 {
		// No RTT sample in this acknowledgement, so neither step below runs.
		return
	}
	filtered := c.filteredDelayMicros()
	// If the delay estimate exceeds the round trip, a clock has moved:
	// raise the base delay to take it out of the samples that follow. The
	// value the window update below sees is unaffected.
	if minRTT := b.minRTT.Microseconds(); int64(filtered) > minRTT {
		c.delayAcc.shift(time.Duration(int64(filtered)-minRTT) * time.Microsecond)
	}
	// Without a delay measurement there is nothing for congestion control
	// to act on.
	if b.delay > 0 && b.bytes >= 1 {
		credit := 0.0
		if sendWindowCredit.Load() {
			credit = b.credit
		}
		c.applyCongestionControl(0, filtered, b.bytes, b.minRTT, b.at, credit)
	}
}

// sendWindowCredit selects reckoning each acknowledged packet's share of a
// round trip's growth against the window it was sent in, rather than only
// the current window. See applyCongestionControl. Tests switch it.
var sendWindowCredit = func() *atomic.Bool {
	b := new(atomic.Bool)
	b.Store(true)
	return b
}()

func (c *defaultController) OnAck(seqNum uint16, ack ackSample) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	packetInst, exists := c.transmissions[seqNum]
	if !exists {
		return errUnknownSeqNum
	}

	if packetInst.Acked {
		return nil
	}
	packetInst.Acked = true
	c.transmissions[seqNum] = packetInst

	// The delay sample was recorded once for the whole acknowledgement, by
	// OnAckDelay.
	c.currentDelay = ack.Delay

	if c.algorithm == AlgorithmLEDBATPP {
		// §4.5: LEDBAT++ measures its queuing delay from round trips, not from
		// the one-way delay the peer reports. See ledbatPPRTTDelay.
		if packetInst.NumTransmissions == 1 {
			c.pushLedbatPPRTT(ack.RTT, ack.ReceivedAt)
		}
		base, current := c.ledbatPPRTTDelay()
		c.applyLedbatPP(uint32(base.Microseconds()), uint32(current.Microseconds()),
			packetInst.SizeBytes, packetInst.SentWindow, ack.RTT, ack.ReceivedAt)
	} else {
		// Counted towards the acknowledgement's single update in ApplyAck.
		rtt := ack.RTT
		if rtt <= 0 {
			rtt = ackBatchRTTFallback
		}
		b := &c.ackBatch
		if b.packets == 0 || rtt < b.minRTT {
			b.minRTT = rtt
		}
		b.packets++
		b.bytes += packetInst.SizeBytes
		if packetInst.SentWindow > 0 {
			b.credit += float64(packetInst.SizeBytes) / float64(packetInst.SentWindow)
		}
		b.delay, b.at = ack.Delay, ack.ReceivedAt
	}

	// A packet already declared lost is no longer counted in flight.
	if !packetInst.NeedResend {
		c.windowSizeBytes -= packetInst.SizeBytes
	}

	// Only packets sent once update the RTT estimate: an ack for a packet
	// sent more than once cannot be attributed to one transmission.
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

// updateRTT updates the RTT estimate and timeout from one sample in
// microseconds. Called with the lock held. The estimate is kept in whole
// milliseconds, truncating each division; FineRTT keeps a microsecond one.
func (c *defaultController) updateRTT(erttMicros int64) {
	// The microsecond estimate, for the loss probe; see FineRTT.
	if sample := time.Duration(erttMicros) * time.Microsecond; sample > 0 &&
		(c.minFineRTT == 0 || sample < c.minFineRTT) {
		c.minFineRTT = sample
	}
	if c.fineRTT == 0 {
		c.fineRTT = time.Duration(erttMicros) * time.Microsecond
	} else {
		fine := c.fineRTT.Microseconds()
		c.fineRTT = time.Duration(maxInt64(fine-fine/8+erttMicros/8, 0)) * time.Microsecond
	}

	ertt := erttMicros / 1000
	if ertt < 0 {
		ertt = 0
	}
	var rtt, rttVar int64
	if c.rtt == 0 {
		// First sample: adopt it outright rather than easing an average up from
		// zero. A sub-millisecond sample is 0, so the next one counts as the
		// first again.
		rtt, rttVar = ertt, ertt/2
	} else {
		// The variance step is signed and truncates toward zero.
		rtt, rttVar = c.rtt.Milliseconds(), c.rttVarianceMicros/1000
		delta := rtt - ertt
		rttVar += (absInt64(delta) - rttVar) / 4
		rtt = rtt - rtt/8 + ertt/8
	}
	c.rtt = time.Duration(rtt) * time.Millisecond
	c.rttVarianceMicros = rttVar * 1000

	c.applyTimeoutAdjustment()
}

// maxWindowDecayInterval is the shortest gap between two halvings of the
// congestion window. A halving happens at most once per acknowledgement that
// resent anything, not once per packet resent.
const maxWindowDecayInterval = 100 * time.Millisecond

// decayOnLoss halves the window for a loss. Called with the lock held.
func (c *defaultController) decayOnLoss(now time.Time) {
	c.maxWindowSizeBytes = uint32(math.Max(float64(c.maxWindowSizeBytes/2), float64(c.minWindowSizeBytes)))
	c.lastWindowDecay = now
	// Any decay ends slow start and sets the threshold to the new window.
	// LEDBAT++ leaves its own slow start on the same signal: a loss is
	// congestion however the delay looked.
	c.slowStart = false
	c.ssthreshBytes = c.maxWindowSizeBytes
	if c.algorithm == AlgorithmLEDBATPP &&
		(c.ppPhase == ppSlowStart || c.ppPhase == ppSlowdownRamp) {
		c.exitLedbatPPSlowStart(now)
	}
}

// onLedbatPPLoss is LEDBAT++'s response to a lost packet. Called with the
// lock held; decay reports whether the rate limit classic LEDBAT applies
// would allow a halving now.
//
// Two further rules decide which window a loss belongs to:
//
//   - A packet sent before the window was last cut belongs to the
//     congestion event that cut it and is not charged again, as in NewReno
//     (RFC 6582's "recover"). The rate limit alone would let one overflow
//     halve the window twice when its losses are detected more than 100 ms
//     apart.
//   - A packet sent before a slowdown was sent from the window the slowdown
//     saved, not from the freeze's two packets or the ramp's partial window.
//     Its loss halves the saved window, which is where the ramp is going;
//     the window the ramp has reached is left alone.
//
// The draft does not cover a loss during a slowdown.
func (c *defaultController) onLedbatPPLoss(pkt *packetRecord, decay bool, now time.Time) {
	if !c.lastWindowDecay.IsZero() && pkt.SentAt.Before(c.lastWindowDecay) {
		return
	}
	if !decay {
		return
	}
	inSlowdown := c.ppPhase == ppSlowdownFreeze || c.ppPhase == ppSlowdownRamp
	if inSlowdown && (c.ppPhase == ppSlowdownFreeze || pkt.SentAt.Before(c.ppSlowdownStartedAt)) {
		// During the freeze everything lost was sent from the saved window or
		// from the two packets the freeze allows, and charging either to
		// those two packets would be wrong.
		c.ppSlowdownSsthresh = maxUint32(c.ppSlowdownSsthresh/2, c.minWindowSizeBytes)
		c.ssthreshBytes = c.ppSlowdownSsthresh
		c.lastWindowDecay = now
		c.slowStart = false
		if c.ppPhase == ppSlowdownRamp && c.maxWindowSizeBytes >= c.ssthreshBytes {
			c.exitLedbatPPSlowStart(now)
		}
		return
	}
	c.decayOnLoss(now)
}

func (c *defaultController) OnLostPacket(seqNum uint16, retransmitting bool, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	packetInst, exists := c.transmissions[seqNum]
	if !exists {
		return errUnknownSeqNum
	}

	// Halve the window at most once per maxWindowDecayInterval, so a burst
	// of losses from one queue overflow is one congestion event.
	decay := c.lastWindowDecay.IsZero() || now.Sub(c.lastWindowDecay) >= maxWindowDecayInterval
	// Another loss while one is still undoable: the cut stands for both,
	// and one turning out spurious does not excuse the other.
	c.undo.valid = false
	before := lossUndo{
		seq:                seqNum,
		maxWindow:          c.maxWindowSizeBytes,
		ssthresh:           c.ssthreshBytes,
		ppSlowdownSsthresh: c.ppSlowdownSsthresh,
		lastWindowDecay:    c.lastWindowDecay,
	}
	if c.algorithm == AlgorithmLEDBATPP {
		c.onLedbatPPLoss(packetInst, decay, now)
	} else if decay {
		c.decayOnLoss(now)
	}
	if lossUndoEnabled.Load() && c.lastWindowDecay != before.lastWindowDecay {
		before.valid = true
		c.undo = before
	}

	if !retransmitting && !packetInst.NeedResend {
		packetInst.NeedResend = true
		c.windowSizeBytes -= packetInst.SizeBytes
	}

	return nil
}

// OnTimeout applies a retransmission timeout to the window.
// hasPacketsInFlight distinguishes an idle connection, whose window decays
// by a third, from one with data outstanding, whose window collapses to one
// packet and restarts slow start.
func (c *defaultController) OnTimeout(hasPacketsInFlight bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// A timeout's cut is not undone, and the loss cut before it no longer
	// stands as the last.
	c.undo.valid = false

	packetSize := c.minWindowSizeBytes / 2
	if !hasPacketsInFlight && c.maxWindowSizeBytes > packetSize {
		// Nothing in flight: the connection is idle, so decay the window by a
		// third rather than reset it.
		c.maxWindowSizeBytes = maxUint32(c.maxWindowSizeBytes*2/3, packetSize)
	} else {
		// Reset the window to one packet and start over in slow start.
		c.maxWindowSizeBytes = packetSize
		c.slowStart = true
		if c.algorithm == AlgorithmLEDBATPP {
			// Back to the start of the cycle: ramp up again, and schedule the
			// slowdowns when that ramp ends.
			c.ppPhase = ppSlowStart
			c.ppNextSlowdownAt = time.Time{}
		}
	}
	if c.maxWindowSizeBytes < c.minWindowSizeBytes {
		c.maxWindowSizeBytes = c.minWindowSizeBytes
	}
	c.timeout = capTimeout(c.timeout*2, c.maxTimeout)
}

// applyCongestionControl is classic LEDBAT's window update (RFC 6817), run
// once per acknowledged packet. The window factor scales each step by that
// packet's share of the window, so a full window of acks adds one RTT's
// worth of increase.
//
// It is called with the controller's lock held.
func (c *defaultController) applyCongestionControl(
	baseDelayMicros uint32,
	packetDelayMicros uint32,
	bytesAcked uint32,
	rtt time.Duration,
	now time.Time,
	credit float64,
) {
	// The queuing delay this ack reports: how far above the lowest delay
	// seen on this path the packet ran.
	ourDelayMicros := int64(packetDelayMicros) - int64(baseDelayMicros)
	if ourDelayMicros < 0 {
		ourDelayMicros = 0
	}

	// The delay can never exceed the round trip: clamp it to the minimum
	// RTT of the packets this ack covers, as ApplyAck does. Otherwise a peer
	// reporting a wild timestamp, by malice, clock step or wrap, could
	// collapse the window in one ack.
	if rttMicros := rtt.Microseconds(); rttMicros > 0 && ourDelayMicros > rttMicros {
		ourDelayMicros = rttMicros
	}

	// A clock drifting fast enough to be dishonest gets a penalty added to
	// the delay it measures. It is applied after the RTT clamp, since its
	// purpose is to make the flow yield rather than to describe the path.
	// Beyond 40,000 ppm, far outside crystal drift, it inflates the delay
	// so a peer running its clock slow gains nothing.
	if penalty := c.drift.penaltyMicros(); penalty > 0 {
		ourDelayMicros += penalty
	}

	target := int64(c.targetDelayMicros)
	if target <= 0 {
		// Default target: 100 ms.
		target = 100000
	}

	offTarget := target - ourDelayMicros
	delayFactor := float64(offTarget) / float64(target)

	// The window factor: min(bytes_acked, max_window) / max(max_window,
	// bytes_acked), at most 1.
	maxWindow := float64(c.maxWindowSizeBytes)
	acked := float64(bytesAcked)
	windowFactor := math.Min(acked, maxWindow) / math.Max(maxWindow, acked)

	// When the caller knows it (credit > 0), each acknowledged packet counts
	// against the window it was sent in. Reckoned only against the current
	// window, each update grows the window the next divides by, so a round
	// trip split into more acknowledgements grows less. The larger of the
	// two factors is used, so after a loss halves the window the next round
	// trip still grows as fast as it would against the new window, and
	// above target the window shrinks no slower.
	if credit > 0 {
		windowFactor = math.Max(windowFactor, math.Min(credit, 1))
	}

	scaledGain := float64(c.gain) * float64(c.maxWindowSizeIncBytes) * windowFactor * delayFactor

	// A sender that has not filled its window in the last second is limited
	// by the application, not the path, so the delay it measures says
	// nothing about how much more the path would carry; do not grow the
	// window. Otherwise a trickling connection would grow its window without
	// bound and then send it all at once when the application speeds up.
	//
	if scaledGain > 0 && c.applicationLimited(now) {
		scaledGain = 0
	}

	ledbatCwnd := float64(c.maxWindowSizeBytes) + scaledGain
	if ledbatCwnd < float64(c.minWindowSizeBytes) {
		ledbatCwnd = float64(c.minWindowSizeBytes)
	}

	if c.slowStart {
		// Slow start.
		ssCwnd := float64(c.maxWindowSizeBytes) + windowFactor*float64(c.minWindowSizeBytes/2)
		switch {
		case ssCwnd > float64(c.ssthreshBytes):
			c.slowStart = false
		case ourDelayMicros > int64(float64(target)*0.9):
			// Even slightly under the target delay, conservatively end slow
			// start.
			c.slowStart = false
			c.ssthreshBytes = c.maxWindowSizeBytes
		default:
			c.maxWindowSizeBytes = uint32(math.Max(ssCwnd, ledbatCwnd))
		}
	} else {
		c.maxWindowSizeBytes = uint32(ledbatCwnd)
	}

	// Clamp to the window ceiling.
	c.maxWindowSizeBytes = clampUint32(c.maxWindowSizeBytes, c.minWindowSizeBytes, c.maxWindowUpperBytes)
}

// applicationLimited reports whether the sender has gone more than a second
// without filling its window. A sender that has never filled its window
// counts as application-limited. A bulk sender is unaffected, because its
// first write fills the window before any acknowledgement arrives; a sender
// that never fills its window grows only by the slow-start step.
func (c *defaultController) applicationLimited(now time.Time) bool {
	return c.lastMaxedOutWindow.IsZero() || now.Sub(c.lastMaxedOutWindow) > time.Second
}

// OnWindowFull records that the sender had data to send and no window to send
// it in.
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
// estimate: rto = max(rtt + rtt_var*4, min), capped at max. The variance is
// in microseconds and is scaled accordingly.
func (c *defaultController) applyTimeoutAdjustment() {
	rto := c.rtt + time.Duration(c.rttVarianceMicros*4)*time.Microsecond
	if rto < c.minTimeout {
		rto = c.minTimeout
	}
	c.timeout = capTimeout(rto, c.maxTimeout)
}

// wrappingLessThanUint32 reports whether l precedes r in the wrapping 32-bit
// microsecond timestamp space: the distance up from l to r is shorter than
// the distance down.
func wrappingLessThanUint32(a, b uint32) bool {
	return (b - a) < (a - b)
}

// peerDelayBuckets is how many buckets the peer-direction base delay is kept
// in. Each bucket is the configured window divided by this.
const peerDelayBuckets = 13

// peerDelayHist tracks the base delay of packets arriving from the peer. Its
// only purpose is noticing that base fall, which reveals clock drift.
//
// It is not a delayAccumulator because the samples must stay in wrapping
// 32-bit microsecond arithmetic: a slow clock makes the measured inbound
// delay shrink until it passes zero and wraps, and treating the wrapped
// value as an unusable reading would freeze the base just as the drift
// becomes large enough to matter.
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
// so the caller can see how far it fell. Buckets rotate according to the
// configured window.
func (h *peerDelayHist) addSample(sample uint32, now time.Time) (prev, current uint32, ok bool) {
	if !h.initialised {
		for i := range h.buckets {
			h.buckets[i] = sample
		}
		h.base = sample
		h.rotatedAt = now
		h.initialised = true
		// No previous base to have fallen from.
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

// baseDelayHist holds this sender's own delay samples. The base delay is the
// least of thirteen buckets; the newest bucket takes each sample lower than
// it, and the history rotates onto a fresh bucket when a sample arrives a
// bucket's length after the last rotation. With the default window a bucket
// is a minute. An idle connection's history does not age, since only a
// sample rotates it. The memory is fixed however many samples arrive.
//
// Clock drift inflates a base kept this long by the drift rate times the
// window; OnPeerDelay corrects for it.
type baseDelayHist struct {
	buckets   [delayBaseHistory]time.Duration
	idx       int
	base      time.Duration
	rotatedAt time.Time
	bucketLen time.Duration
	init      bool
	// skew is the total of every shift, for controllerStats. The shifts
	// themselves live in the buckets and age out with them.
	skew time.Duration
}

// delayBaseHistory is the number of buckets in the base-delay history.
const delayBaseHistory = 13

// newBaseDelayHist starts the history at now, so the first rotation is a
// bucket's length after the connection began.
func newBaseDelayHist(window time.Duration, clk clockSource) *baseDelayHist {
	bucketLen := window / delayBaseHistory
	if bucketLen <= 0 {
		bucketLen = time.Second
	}
	if clk == nil {
		clk = defaultClock
	}
	return &baseDelayHist{bucketLen: bucketLen, rotatedAt: clk.Now()}
}

// Push records a sample taken at now.
func (h *baseDelayHist) Push(sample time.Duration, now time.Time) {
	if !h.init {
		for i := range h.buckets {
			h.buckets[i] = sample
		}
		h.base = sample
		h.init = true
	}
	if sample < h.buckets[h.idx] {
		h.buckets[h.idx] = sample
	}
	if sample < h.base {
		h.base = sample
	}
	if now.Sub(h.rotatedAt) > h.bucketLen {
		h.rotatedAt = now
		h.idx = (h.idx + 1) % delayBaseHistory
		h.buckets[h.idx] = sample
		h.base = h.buckets[0]
		for _, b := range h.buckets {
			if b < h.base {
				h.base = b
			}
		}
	}
}

// BaseDelay returns the least delay in the history, never below zero.
func (h *baseDelayHist) BaseDelay() time.Duration {
	if !h.init || h.base < 0 {
		return 0
	}
	return h.base
}

// shift raises the base delay by adding offset to every bucket, canceling
// drift that has inflated the samples. Each bucket is replaced by a fresh,
// unshifted sample when the history rotates onto it, so a correction lasts
// only as long as the history, and shifts that keep arriving while drift
// continues cannot accumulate without bound.
func (h *baseDelayHist) shift(offset time.Duration) {
	if offset <= 0 {
		return
	}
	for i := range h.buckets {
		h.buckets[i] += offset
	}
	h.base += offset
	h.skew += offset
}

// maxSkewAdjustment bounds a single clock-drift correction. A larger apparent
// fall in the peer's base delay is more likely the reverse path getting
// faster, such as a route change or a queue draining, than clock drift, and
// treating it as drift would blind this sender's delay signal.
const maxSkewAdjustment = 10 * time.Millisecond

// OnPeerDelay records the one-way delay measured on a packet from the peer,
// and corrects this sender's delay signal for clock drift.
//
// A one-way delay is a difference between two clocks, so it carries their
// relative rate error. If this end's clock runs slow, the peer's measurement
// of our outbound path, which LEDBAT runs on, grows without bound; the same
// slow clock makes packets from the peer appear to arrive sooner, so the
// delay in that direction shrinks by as much. A fall in the peer-direction
// base delay is therefore the visible half of a bias inflating the other
// half, and raising this sender's base by the same amount, up to
// maxSkewAdjustment, cancels it.
func (c *defaultController) OnPeerDelay(sample uint32, now time.Time) {
	// Zero means the peer has not stamped a usable timestamp, not that the
	// path is instant.
	if sample == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	prev, current, ok := c.peerDelayHist.addSample(sample, now)
	if !ok {
		return
	}
	// Only a fall is evidence of drift. A rise is a queue building in the
	// other direction, which says nothing about either clock.
	if !wrappingLessThanUint32(current, prev) {
		return
	}
	drop := time.Duration(prev-current) * time.Microsecond
	if drop > 0 && drop <= maxSkewAdjustment {
		c.delayAcc.shift(drop)
	}
}

// now returns the controller's current time, or time.Now for a controller
// built without a clock.
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

type delay struct {
	Value    time.Duration
	Deadline time.Time
}

type delayAccumulator struct {
	delays *delayWindow
	window time.Duration
	// clk decides when a sample has aged out of the window. Nil means the
	// real clock; read it through now.
	clk clockSource
}

func newDelayAccumulator(window time.Duration) *delayAccumulator {
	return &delayAccumulator{
		delays: &delayWindow{},
		window: window,
	}
}

func newDelayAccumulatorWithClock(window time.Duration, clk clockSource) *delayAccumulator {
	return &delayAccumulator{
		delays: &delayWindow{},
		window: window,
		clk:    clk,
	}
}

// now returns the current time, or time.Now for an accumulator built
// without a clock.
func (da *delayAccumulator) now() time.Time {
	if da.clk != nil {
		return da.clk.Now()
	}
	return time.Now()
}

func (da *delayAccumulator) Push(delayTime time.Duration, receivedAt time.Time) {
	da.delays.push(delay{
		Value:    delayTime,
		Deadline: receivedAt.Add(da.window),
	})
}

func (da *delayAccumulator) BaseDelay() time.Duration {
	now := da.now()
	for da.delays.len() > 0 {
		min := da.delays.front()
		if now.After(min.Deadline) {
			da.delays.popFront()
			continue
		}
		return max(min.Value, 0)
	}
	return time.Duration(0)
}

// delayWindow is a sliding-window minimum of samples. Samples are kept in
// arrival order, and a new one first drops every earlier sample that is no
// smaller, since such a sample leaves the window first and can never again
// be the least. The front is therefore always the minimum, and the oldest,
// and expires first.
type delayWindow struct {
	items []delay
	head  int
}

func (w *delayWindow) len() int     { return len(w.items) - w.head }
func (w *delayWindow) front() delay { return w.items[w.head] }

// push adds a sample. Callers pass the arrival time of an acknowledgement,
// which only moves forward; a deadline earlier than the last is treated as
// the last, so the front stays the first to expire.
func (w *delayWindow) push(d delay) {
	if w.len() > 0 && d.Deadline.Before(w.items[len(w.items)-1].Deadline) {
		d.Deadline = w.items[len(w.items)-1].Deadline
	}
	for w.len() > 0 && w.items[len(w.items)-1].Value >= d.Value {
		w.items = w.items[:len(w.items)-1]
	}
	if w.len() == 0 {
		w.items, w.head = w.items[:0], 0
	} else if w.head > 0 && len(w.items) == cap(w.items) {
		// Reclaim what has expired off the front before growing.
		n := copy(w.items, w.items[w.head:])
		w.items, w.head = w.items[:n], 0
	}
	w.items = append(w.items, d)
}

func (w *delayWindow) popFront() {
	w.head++
	if w.head == len(w.items) {
		w.items, w.head = w.items[:0], 0
	}
}

// capTimeout bounds a timeout by max when max is positive; zero or less
// means no bound, the default.
func capTimeout(d, max time.Duration) time.Duration {
	if max > 0 && d > max {
		return max
	}
	return d
}
