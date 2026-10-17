package utp

import (
	"time"
)

// maxConsecutiveTimeouts is how many retransmission timeouts may pass without
// an intervening ack before the connection is given up as dead.
//
// libutp kills the connection once retransmit_count reaches 4, and resets
// that counter whenever a packet is acked (utp_internal.cpp:1191, reset at
// :1398). Without this a connection has no death condition of its own: it
// relies entirely on the idle timer, which any inbound packet resets, so a
// half-broken peer that keeps sending anything at all holds the connection
// open indefinitely while our data goes unacked.
const maxConsecutiveTimeouts = 4

// DefaultMaxIdleTimeout is zero: no idle timeout, as in libutp, which never
// closes a connection for silence. See ConnectionConfig.MaxIdleTimeout.
const DefaultMaxIdleTimeout = time.Duration(0)
const DefaultWindowSize = 1024 * 1024
const DefaultBufferSize = 1024 * 1024

type ConnectionConfig struct {
	MaxPacketSize   uint16
	MaxConnAttempts int
	// MaxIdleTimeout closes the connection with ErrTimedOut once nothing has
	// arrived from the peer and nothing has been written for this long. Zero,
	// the default, is no idle timeout, as in libutp: a connection whose peer
	// vanished while nothing was in flight stays open until its application
	// closes it, and one that a long outage interrupted resumes when the path
	// comes back. Set it to reclaim connections to vanished peers without a
	// timer of the application's own; a live peer sends a keep-alive every
	// 29 seconds, so anything well above that never closes a live one.
	MaxIdleTimeout time.Duration
	InitialTimeout time.Duration
	MinTimeout     time.Duration
	// MaxTimeout caps the retransmission timeout and its backoff. Zero, the
	// default, is no cap, as in libutp.
	MaxTimeout  time.Duration
	TargetDelay time.Duration
	// Clock is where this connection reads time and gets its timers.
	// Defaults to RealClock; see Clock.
	//
	// It governs *scheduling* -- every deadline comparison, and the
	// retransmission, keep-alive, probe and idle timers. NowMicros below
	// governs what is stamped on the wire. A test that wants a connection
	// fully off the real clock sets both.
	Clock Clock

	// NowMicros is where this connection reads the wall clock, in the uint32
	// microseconds uTP puts on the wire. Defaults to nowMicro.
	//
	// It governs only what is *stamped and measured* -- the timestamp field,
	// the timestamp difference echoed back, and the one-way delays derived
	// from them. Scheduling runs on Clock, real time by default: a
	// connection given a frozen NowMicros still retransmits on time. The conformance corpus pins it to
	// libutp's driver's clock, so the timestamp fields can be compared;
	// see conformance_harness_test.go.
	NowMicros func() uint32

	// DelayWindow is how far back the congestion controller looks for its
	// base delay -- the lowest one-way delay it has seen, which it treats as
	// the path with an empty queue.
	//
	// It is libutp's: thirteen buckets (DELAY_BASE_HISTORY,
	// utp_internal.cpp:50), each a thirteenth of this, so the default of
	// thirteen minutes is libutp's thirteen one-minute buckets exactly. See
	// baseDelayHist.
	//
	// Configurable because the window is what bounds the damage clock drift
	// does: a sender whose clock loses time sees its measured delay grow
	// without bound, and the base only follows once the inflated samples have
	// aged out. The steady-state error is the window multiplied by the drift
	// rate. See netem.TestClockDriftInflatesTheDelaySignal.
	DelayWindow time.Duration
	WindowSize  uint32
	BufferSize  int

	// Metrics, when set, receives periodic snapshots of this connection.
	// See MetricsObserver for the constraints on the callback.
	Metrics MetricsObserver
	// MetricsInterval is the minimum gap between snapshots. Defaults to
	// DefaultMetricsInterval.
	MetricsInterval time.Duration
	// KeepAliveInterval is how long an established connection may stay silent
	// before sending a keep-alive.
	//
	// Defaults to libutp's 29 seconds (utp_internal.cpp:74), chosen to sit
	// under the 30-second UDP mapping timeout common in NATs. Configurable
	// only so it can be tested in less than half a minute.
	KeepAliveInterval time.Duration
	// ZeroWindowProbeInterval is how long a closed peer receive window is
	// tolerated before one packet is forced through to elicit an update.
	//
	// A peer that advertises a zero window sends an update when its
	// application drains its buffer, but that update is a single packet on an
	// unreliable path. Lost, it leaves this sender with nothing outstanding,
	// no retransmission timer, and no reason to transmit -- stopped dead with
	// data queued until the idle timeout.
	//
	// Defaults to libutp's 15 seconds (utp_internal.cpp:2151). Configurable
	// only so it can be tested in less than fifteen seconds; libutp hard-codes
	// it, as it hard-codes the retransmission timeouts this library also makes
	// configurable.
	ZeroWindowProbeInterval time.Duration
	// CongestionAlgorithm selects the congestion controller.
	//
	// The zero value is AlgorithmLEDBAT: classic LEDBAT, matching libutp.
	// AlgorithmLEDBATPP selects LEDBAT++, which is a deliberate divergence
	// from the reference implementation -- see DEVIATIONS.md and
	// BENCHMARKS.md for what it changes and what it measures.
	CongestionAlgorithm CongestionAlgorithm
	// RACK replaces libutp's loss detection -- a packet is lost once three
	// after it have been acknowledged, and is fast-retransmitted once --
	// with RFC 8985's time-based rule. Off by default, and off, loss
	// detection is libutp's. Experimental; see DEVIATIONS.md, "RACK loss
	// detection (opt-in)".
	RACK bool
	// NoDelay sends a partial packet as soon as the window allows, as
	// TCP_NODELAY does. By default a connection follows libutp's Nagle rule
	// (flush_packets, utp_internal.cpp:974-982): a packet smaller than a
	// full one waits while anything else is unacknowledged, and later writes
	// join it, until it fills, everything before it is acknowledged, or the
	// write side closes. With the rule small writes share packets; with
	// NoDelay each goes out at once and arrives sooner. The measurement:
	// HISTORY.md, "ConnectionConfig.NoDelay".
	NoDelay bool
}

func NewConnectionConfig() *ConnectionConfig {
	return &ConnectionConfig{
		// libutp gives up on a connection attempt once retransmit_count
		// reaches 2 while in CS_SYN_SENT, which is three transmissions of the
		// SYN in total (utp_internal.cpp:1191).
		MaxConnAttempts:         3,
		MaxIdleTimeout:          DefaultMaxIdleTimeout,
		MaxPacketSize:           defaultMaxPacketSizeBytes,
		InitialTimeout:          defaultInitialTimeout,
		MinTimeout:              defaultMinTimeout,
		MaxTimeout:              defaultMaxTimeout,
		TargetDelay:             defaultTargetMicros,
		DelayWindow:             defaultDelayWindow,
		WindowSize:              DefaultWindowSize,
		BufferSize:              DefaultBufferSize,
		KeepAliveInterval:       defaultKeepAliveInterval,
		ZeroWindowProbeInterval: defaultZeroWindowProbeInterval,
		// Classic LEDBAT by default, because matching libutp is the default
		// everywhere else in this library.
		CongestionAlgorithm: AlgorithmLEDBAT,
	}
}

func fromConnConfig(config *ConnectionConfig) *ctrlConfig {
	ctrlConfigPtr := defaultCtrlConfig()
	ctrlConfigPtr.MaxPacketSizeBytes = uint32(config.MaxPacketSize)
	ctrlConfigPtr.InitialTimeout = config.InitialTimeout
	ctrlConfigPtr.MinTimeout = config.MinTimeout
	ctrlConfigPtr.MaxTimeout = config.MaxTimeout
	ctrlConfigPtr.TargetDelayMicros = uint32(config.TargetDelay.Microseconds())
	ctrlConfigPtr.WindowSize = config.WindowSize
	if config.Clock != nil {
		ctrlConfigPtr.Clock = config.Clock
	}
	if config.DelayWindow > 0 {
		ctrlConfigPtr.DelayWindow = config.DelayWindow
	}
	ctrlConfigPtr.Algorithm = config.CongestionAlgorithm
	return ctrlConfigPtr
}

// controllerConfig is the congestion controller's configuration for this
// connection: the connection config's, with a window ceiling set on the
// stream before the controller existed in place of the configured one.
func (c *connection) controllerConfig() *ctrlConfig {
	cfg := fromConnConfig(c.config)
	if c.windowCeiling != 0 {
		cfg.WindowSize = c.windowCeiling
	}
	return cfg
}

// setWindowCeiling is UtpStream.SetWindowSize.
func (c *connection) setWindowCeiling(bytes uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.windowCeiling = bytes
	if c.state == nil || c.state.SentPackets == nil {
		return // the controller is made with it (controllerConfig)
	}
	if ctrl, ok := c.state.SentPackets.congestionCtrl.(*defaultController); ok {
		ctrl.setWindowCeiling(bytes)
	}
}

// windowCeilingSetting is UtpStream.WindowSize.
func (c *connection) windowCeilingSetting() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.windowCeiling != 0 {
		return c.windowCeiling
	}
	if c.config.WindowSize != 0 {
		return c.config.WindowSize
	}
	return DefaultWindowSize
}
