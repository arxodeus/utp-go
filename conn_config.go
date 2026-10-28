package utp

import (
	"time"
)

// maxConsecutiveTimeouts is how many retransmission timeouts may pass
// without an acknowledgement before the connection is considered dead. The
// count resets whenever a packet is acknowledged. Without it a peer that
// keeps sending anything, but never acknowledges, would hold the connection
// open indefinitely.
const maxConsecutiveTimeouts = 4

// DefaultMaxIdleTimeout is zero: by default a connection is never closed
// for silence. See ConnectionConfig.MaxIdleTimeout.
const DefaultMaxIdleTimeout = time.Duration(0)

// DefaultWindowSize is the default upper bound on the congestion window, in
// bytes.
const DefaultWindowSize = 1024 * 1024

// DefaultBufferSize is the default size of each connection's send and receive
// buffers, in bytes.
const DefaultBufferSize = 1024 * 1024

// ConnectionConfig configures a connection. Start from NewConnectionConfig,
// which sets the defaults, and change what is needed. A config may be shared
// by several connections, but must not be modified once in use.
type ConnectionConfig struct {
	// MaxPacketSize is the largest datagram the connection sends, in bytes:
	// the ceiling of its path-MTU search.
	MaxPacketSize uint16
	// MaxConnAttempts is how many times the SYN is sent before a dial gives
	// up with ErrTimedOut.
	MaxConnAttempts int
	// MaxIdleTimeout closes the connection with ErrTimedOut once nothing has
	// been received and nothing written for this long. Zero, the default,
	// disables it: a connection whose peer vanished while nothing was in
	// flight stays open until the application closes it, and one
	// interrupted by an outage resumes when the path returns. A live peer
	// sends a keep-alive every 29 seconds, so a value well above that never
	// closes a live connection.
	MaxIdleTimeout time.Duration
	// InitialTimeout is the retransmission timeout before the first
	// round-trip sample, and the SYN's first timeout.
	InitialTimeout time.Duration
	// MinTimeout is the floor of the retransmission timeout.
	MinTimeout time.Duration
	// MaxTimeout caps the retransmission timeout and its backoff. Zero, the
	// default, means no cap.
	MaxTimeout time.Duration
	// TargetDelay is the queuing delay classic LEDBAT aims for.
	TargetDelay time.Duration
	// Clock is the source of time and timers for this connection. It
	// defaults to RealClock; see Clock.
	//
	// It governs scheduling: deadlines and the retransmission, keep-alive,
	// probe and idle timers. NowMicros governs the timestamps sent on the
	// wire. A test that runs a connection entirely off the real clock sets
	// both.
	Clock Clock

	// NowMicros returns the wall clock in the uint32 microseconds uTP puts on
	// the wire. It defaults to nowMicro.
	//
	// It governs only timestamps and the one-way delays derived from them.
	// Scheduling uses Clock, so a connection with a frozen NowMicros still
	// retransmits on time.
	NowMicros func() uint32

	// DelayWindow is how far back the congestion controller looks for its
	// base delay: the lowest one-way delay seen, taken as the delay of an
	// empty queue. The window is kept in thirteen buckets; the default is
	// thirteen minutes. See baseDelayHist.
	//
	// The window bounds the error clock drift causes: a sender whose clock
	// runs slow sees its measured delay grow until inflated samples age out,
	// so the steady-state error is the window times the drift rate.
	DelayWindow time.Duration
	// WindowSize is the upper bound on the congestion window, in bytes.
	WindowSize uint32
	// BufferSize is the size of the send and receive buffers, in bytes. The
	// receive window advertised to the peer is at most this.
	BufferSize int

	// Metrics, when set, receives periodic snapshots of this connection.
	// See MetricsObserver for the constraints on the callback.
	Metrics MetricsObserver
	// MetricsInterval is the minimum gap between snapshots. Defaults to
	// DefaultMetricsInterval.
	MetricsInterval time.Duration
	// KeepAliveInterval is how long an established connection may stay
	// silent before sending a keep-alive. The default of 29 seconds is below
	// the 30-second UDP mapping timeout common in NATs.
	KeepAliveInterval time.Duration
	// ZeroWindowProbeInterval is how long a zero receive window from the
	// peer is tolerated before one packet is sent to elicit a window update.
	// The peer's update when it reopens the window is a single packet; if it
	// is lost, the sender would otherwise have nothing outstanding and no
	// reason to transmit. The default is 15 seconds.
	ZeroWindowProbeInterval time.Duration
	// CongestionAlgorithm selects the congestion controller. The zero value
	// is AlgorithmLEDBAT; AlgorithmLEDBATPP selects LEDBAT++.
	CongestionAlgorithm CongestionAlgorithm
	// RACK replaces the default loss detection, under which a packet is lost
	// once three packets sent after it are acknowledged, with the
	// time-based rule of RFC 8985. Off by default. Experimental.
	RACK bool
	// NoDelay sends a partial packet as soon as the window allows, like
	// TCP_NODELAY. By default a packet smaller than the maximum waits while
	// anything else is unacknowledged, and later writes are appended to it
	// until it is full, everything before it is acknowledged, or the write
	// side closes. NoDelay lowers latency for small writes at the cost of
	// more, smaller packets.
	NoDelay bool
}

// NewConnectionConfig returns a config with the default settings.
func NewConnectionConfig() *ConnectionConfig {
	return &ConnectionConfig{
		// Give up after the SYN has been sent three times.
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
		// Classic LEDBAT by default.
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

// controllerConfig returns the congestion controller's configuration: the
// connection's, with any window ceiling set on the stream before the
// controller existed replacing the configured one.
func (c *connection) controllerConfig() *ctrlConfig {
	cfg := fromConnConfig(c.config)
	if c.windowCeiling != 0 {
		cfg.WindowSize = c.windowCeiling
	}
	return cfg
}

// setWindowCeiling implements UtpStream.SetWindowSize.
func (c *connection) setWindowCeiling(bytes uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.windowCeiling = bytes
	if c.state == nil || c.state.SentPackets == nil {
		return // the controller is created with it (controllerConfig)
	}
	if ctrl, ok := c.state.SentPackets.congestionCtrl.(*defaultController); ok {
		ctrl.setWindowCeiling(bytes)
	}
}

// windowCeilingSetting implements UtpStream.WindowSize.
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
