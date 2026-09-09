package utp_go

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/ethereum/go-ethereum/log"
)

const (
	Initiator EndpointType = iota
	Acceptor
)

const (
	ConnConnecting ConnStateType = iota
	ConnConnected
	ConnClosed
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

const DefaultMaxIdleTimeout = 60 * time.Second
const DefaultWindowSize = 1024 * 1024
const DefaultBufferSize = 1024 * 1024

var (
	// ErrEmptyDataPayload is no longer returned: a zero-length ST_DATA is
	// accepted, as libutp accepts it. Retained because it is exported.
	ErrEmptyDataPayload  = errors.New("empty data payload")
	ErrConnInvalidAckNum = errors.New("invalid ack number")
	ErrInvalidFin        = errors.New("invalid fin")
	ErrInvalidSeqNum     = errors.New("invalid seq number")
	ErrInvalidSyn        = errors.New("invalid syn")
	ErrReset             = errors.New("reset")
	ErrSynFromAcceptor   = errors.New("syn from acceptor")
	ErrTimedOut          = errors.New("timed out")
)

type EndpointType int

type Endpoint struct {
	Type     EndpointType
	SynNum   uint16
	SynAck   uint16
	Attempts int
}

type ClosingRecord struct {
	LocalFin  *uint16
	RemoteFin *uint16
}

type ConnStateType int

type ConnState struct {
	stateType   ConnStateType
	connectedCh chan error
	RecvBuf     *receiveBuffer
	SendBuf     *sendBuffer
	SentPackets *sentPackets
	closing     *ClosingRecord
	Err         error
}

func NewConnState(connected chan error) *ConnState {
	return &ConnState{
		stateType:   ConnConnecting,
		connectedCh: connected,
	}
}

type ConnectionConfig struct {
	MaxPacketSize   uint16
	MaxConnAttempts int
	MaxIdleTimeout  time.Duration
	InitialTimeout  time.Duration
	MinTimeout      time.Duration
	MaxTimeout      time.Duration
	TargetDelay     time.Duration
	WindowSize      uint32
	BufferSize      int

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
	ctrlConfigPtr.Algorithm = config.CongestionAlgorithm
	return ctrlConfigPtr
}

type queuedWrite struct {
	data     []byte
	written  int
	resultCh chan *readOrWriteResult
}

type readOrWriteResult struct {
	Err  error
	Len  int
	Data []byte
}

type connection struct {
	ctx            context.Context
	logger         log.Logger
	state          *ConnState
	cid            *ConnectionId
	config         *ConnectionConfig
	endpoint       *Endpoint
	peerTsDiff     time.Duration
	peerRecvWindow uint32
	socketEvents   chan *socketEvent
	// timers is the socket-wide retransmission wheel; timerScope namespaces
	// this connection's keys within it.
	timers     *retransmitTimers
	timerScope uint64
	// armed tracks which sequence numbers this connection currently has
	// scheduled. It exists so acking a range can disarm only this
	// connection's timers instead of scanning a wheel shared by every
	// connection on the socket. Touched only from the event-loop goroutine.
	armed          map[uint16]struct{}
	unackTimeoutCh chan *packet
	reads          chan *readOrWriteResult
	readable       chan struct{}
	pendingWrites  []*queuedWrite
	writable       chan struct{}
	latestTimeout  *time.Time
	synState       *packet
	// readsTerminated records that the single end-of-stream marker has been
	// handed to the reader. Readers block on c.reads until they see it.
	readsTerminated bool

	// Counters, read and written only from the event-loop goroutine.
	packetsSent          uint64
	bytesSent            uint64
	packetsRetransmitted uint64
	bytesRetransmitted   uint64
	packetsReceived      uint64
	bytesReceived        uint64
	timeouts             uint64
	fastRetransmits      uint64
	// retransmitCount is consecutive retransmission timeouts with no
	// intervening ack. libutp calls this retransmit_count.
	retransmitCount int
	// zeroWindowProbeDue is when a closed peer window should be probed. Zero
	// means the peer's window is open and nothing is pending.
	//
	// libutp's `zerowindow_time` (utp_internal.cpp:496), armed when an
	// acknowledgement reports a zero window (:2149-2151) and acted on in
	// check_timeouts (:1142-1145).
	zeroWindowProbeDue time.Time
	// ackPending records that a received packet is owed an acknowledgement,
	// which is sent once at the end of the event-loop pass that received it.
	// libutp's schedule_ack / utp_issue_deferred_acks.
	ackPending bool
	// mtu is this connection's path-MTU search. See mtu.go.
	mtu *mtuSearch
	// lastSentPacket is when this connection last put a packet on the wire.
	// libutp's `last_sent_packet`, which its keep-alive compares against
	// (utp_internal.cpp:1272).
	lastSentPacket time.Time
	// armProbeTimer wakes the event loop when a closed window has gone
	// unprobed for long enough. Installed by the event loop, which owns the
	// timer.
	armProbeTimer func(time.Duration)
	// probingZeroWindow allows one packet through a window the peer has
	// closed. libutp expresses the same thing by writing PACKET_SIZE into
	// max_window_user, which the next acknowledgement then overwrites.
	probingZeroWindow bool

	// rtoDeadline is when this connection's retransmission timeout expires.
	//
	// libutp has exactly one of these per connection (`rto_timeout`,
	// utp_internal.cpp:494): armed when the first packet enters an empty
	// window (:994-998), reset on every acknowledgement of new data
	// (:1388-1389), and compared against the clock before a timeout is
	// declared (:1147-1148).
	//
	// This connection arms one timer per outstanding packet, so without a
	// deadline of its own a single RTO expiry produced several timeout
	// events, and the wheel's resolution let a callback arrive before the
	// timeout had actually elapsed. Measured against libutp: our backoff
	// doubled once every *two* retransmissions instead of every one, and the
	// first retransmission fired at roughly 0.6 of the RTO. See
	// conformance_timing_test.go.
	rtoDeadline time.Time

	// synTimeout is the current retransmission timeout for the SYN, doubled
	// on each attempt. libutp calls this retransmit_timeout.
	synTimeout    time.Duration
	lastMetricsAt time.Time
}

func newConnection(
	ctx context.Context,
	logger log.Logger,
	cid *ConnectionId,
	config *ConnectionConfig,
	syn *packet,
	connected chan error,
	socketEvents chan *socketEvent,
	reads chan *readOrWriteResult,
	timers *retransmitTimers,
) *connection {
	var endpoint *Endpoint
	var peerTsDiff time.Duration
	var peerRecvWindow uint32

	if syn != nil {
		synAck := RandomUint16()
		endpoint = &Endpoint{
			Type:   Acceptor,
			SynNum: syn.Header.SeqNum,
			SynAck: synAck,
		}

		// uint32 wrapping arithmetic: the SYN's timestamp is a uint32 wire
		// value, so it cannot be subtracted from a full-width int64 clock.
		peerTsDiff = timestampDiffMicros(NowMicro(), uint32(syn.Header.Timestamp))
		peerRecvWindow = syn.Header.WndSize
	} else {
		synNum := RandomUint16()
		endpoint = &Endpoint{
			Type:     Initiator,
			SynNum:   synNum,
			Attempts: 0,
		}
		peerTsDiff = 0
		peerRecvWindow = math.MaxUint32
	}

	unackTimeoutCh := make(chan *packet, 1000)

	return &connection{
		ctx:            ctx,
		logger:         logger,
		state:          NewConnState(connected),
		cid:            cid,
		config:         config,
		endpoint:       endpoint,
		peerTsDiff:     peerTsDiff,
		peerRecvWindow: peerRecvWindow,
		socketEvents:   socketEvents,
		timers:         timers,
		timerScope:     timers.newScope(),
		armed:          make(map[uint16]struct{}),
		unackTimeoutCh: unackTimeoutCh,
		reads:          reads,
		readable:       make(chan struct{}, 3),
		pendingWrites:  make([]*queuedWrite, 0),
		writable:       make(chan struct{}, 3),
		latestTimeout:  nil,
		// The ceiling is the largest packet this library will ever try; the
		// search starts at the midpoint between it and 576 and only grows
		// once a probe of that size has been acknowledged. That ordering is
		// why raising the ceiling is safe: nothing large is sent until
		// something large is known to arrive.
		mtu: newMtuSearch(uint32(config.MaxPacketSize), time.Now()),
	}
}

// armRetransmit schedules a retransmission timer for one packet.
func (c *connection) armRetransmit(pkt *packet, delay time.Duration) {
	seq := pkt.Header.SeqNum
	if len(c.armed) == 0 {
		// First packet into an empty window: start the connection's RTO.
		//
		// libutp: "Setup initial timeout timer" in write_outgoing_packet,
		// guarded by `cur_window_packets == 0`
		// (utp_internal.cpp:994-998).
		c.rtoDeadline = time.Now().Add(delay)
	}
	c.armed[seq] = struct{}{}
	c.timers.arm(
		retransmitKey{scope: c.timerScope, seq: seq},
		&retransmitTimer{packet: pkt, deliver: c.unackTimeoutCh, ctx: c.ctx},
		delay,
	)
}

// disarmRetransmit cancels one packet's retransmission timer.
//
// Unconditional on purpose. A timer that has already fired is out of the
// wheel but still in the armed set until the event loop drains it, so
// skipping the wheel when the set does not hold the key could leave a stale
// entry behind. Both operations are no-ops when the key is absent.
func (c *connection) disarmRetransmit(seq uint16) {
	delete(c.armed, seq)
	c.timers.disarm(retransmitKey{scope: c.timerScope, seq: seq})
}

// disarmAcked cancels the timers for every sequence number this connection
// still has armed that falls inside acked.
//
// Only this connection's own armed set is scanned. The wheel is shared with
// every other connection on the socket, so scanning it would be O(all
// outstanding packets on the socket) on every ack.
//
// It returns how many timers it cancelled, which is how many packets this ack
// newly retired.
func (c *connection) disarmAcked(acked *circularRangeInclusive) int {
	n := 0
	for seq := range c.armed {
		if acked.Contains(seq) {
			delete(c.armed, seq)
			c.timers.disarm(retransmitKey{scope: c.timerScope, seq: seq})
			n++
		}
	}
	return n
}

// disarmAll cancels every timer this connection holds. The shared wheel
// outlives the connection, so anything left armed would linger until it
// expired and then be delivered to a dead event loop.
func (c *connection) disarmAll() {
	for seq := range c.armed {
		c.timers.disarm(retransmitKey{scope: c.timerScope, seq: seq})
	}
	clear(c.armed)
}

func (c *connection) eventLoop(stream *UtpStream) error {
	defer c.disarmAll()
	// Tell the socket to drop this connection on *every* exit, not only the
	// one where the state machine reached ConnClosed.
	//
	// It used to be reported from that one branch alone, so a connection torn
	// down by a cancelled context -- which is what an abandoned or failed
	// connection attempt is -- left its entry in the socket's table forever.
	// Measured before the fix: 40 attempts to a dead port left 40 tracked
	// connections behind, permanently. A client dialling unreachable peers,
	// which is most of them, accumulates one of these per attempt.
	//
	// The goroutines were fine; it is the map entry and its channel that
	// leaked, which is why nothing that counted goroutines noticed.
	defer c.notifySocketShutdown()
	if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		c.logger.Trace("uTP conn starting", "dst.peer", c.cid.Peer, "cid.Send", c.cid.Send, "cid.Recv", c.cid.Recv)
	}
	// Initialize connection based on endpoint type
	if c.endpoint.Type == Initiator {
		synSeqNum := c.endpoint.SynNum
		synPkt := c.synPacket(synSeqNum)
		c.emit(synPkt)
		if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			c.logger.Trace("put a initial syn packet to delay map", "socketEvents.len", len(c.socketEvents), "dst.peer", c.cid.Peer, "synSeqNum", synSeqNum)
		}
		c.synTimeout = c.config.InitialTimeout
		c.armRetransmit(synPkt, c.synTimeout)

		c.endpoint.Attempts = 1
	} else {
		// Acceptor connection
		syn := c.endpoint.SynNum
		synAck := c.endpoint.SynAck

		c.synState = c.statePacket()

		c.logger.Debug("a initial state packet", "peer", c.cid.Peer, "cid.Send", c.cid.Send, "cid.Recv", c.cid.Recv)
		c.emit(c.synState)

		recvBuf := newReceiveBufferWithLogger(c.config.BufferSize, syn, c.logger)
		sendBuf := newSendBuffer(c.config.BufferSize)
		congestionCtrl := newDefaultController(fromConnConfig(c.config))
		sentPacketsHolder := newSentPackets(synAck-1, congestionCtrl, c.logger) // wrapping subtraction

		if c.state != nil && c.state.connectedCh != nil {
			c.state.connectedCh <- nil
		} else {
			panic("connection in invalid statePacket prior to event loop beginning")
		}

		c.state.stateType = ConnConnected
		c.state.RecvBuf = recvBuf
		c.state.SendBuf = sendBuf
		c.state.SentPackets = sentPacketsHolder
	}

	idleTimer := time.NewTimer(c.config.MaxIdleTimeout)
	resetIdleTimer := func() {
		idleTimer.Reset(c.config.MaxIdleTimeout)
	}
	defer idleTimer.Stop()

	// The zero-window probe needs a wake-up of its own.
	//
	// Every other reason this loop runs is an event: a packet arrived, the
	// application wrote, a retransmission timer expired. A closed peer window
	// is the absence of all three -- nothing is outstanding, so no
	// retransmission timer exists, and nothing will arrive until the peer
	// chooses to speak. Without this the probe would be armed and never
	// checked.
	// An established connection that has gone quiet still has to say
	// something occasionally, or a NAT drops its mapping and the peer's own
	// idle timeout eventually kills it. libutp checks this on every timeout
	// pass (utp_internal.cpp:1271-1274); this ticks at the same interval.
	keepAliveTicker := time.NewTicker(c.keepAliveIntervalOrDefault())
	defer keepAliveTicker.Stop()

	probeTimer := time.NewTimer(time.Hour)
	if !probeTimer.Stop() {
		<-probeTimer.C
	}
	defer probeTimer.Stop()
	c.armProbeTimer = func(d time.Duration) {
		if !probeTimer.Stop() {
			select {
			case <-keepAliveTicker.C:
				// libutp: `if (state >= CS_CONNECTED && !fin_sent)` and the
				// connection has been silent for the interval
				// (utp_internal.cpp:1271-1274).
				if c.state.stateType == ConnConnected &&
					(c.state.closing == nil || c.state.closing.LocalFin == nil) &&
					time.Since(c.lastSentPacket) >= c.keepAliveIntervalOrDefault() {
					c.emit(c.keepAlivePacket())
				}
			case <-probeTimer.C:
			default:
			}
		}
		probeTimer.Reset(d)
	}
	handleIncoming := func(event *streamEvent) {
		if event.Type == streamIncoming {
			if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				c.logger.Trace("incoming packet",
					"stream.streamEvents.len", len(stream.streamEvents),
					"src.peer", c.cid.Peer,
					"packet.type", event.Packet.Header.PacketType.String(),
					"packet.seqNum", event.Packet.Header.SeqNum,
					"packet.ackNum", event.Packet.Header.AckNum,
					"buf.len", len(event.Packet.Body))
			}
			// reset idle timeout
			resetIdleTimer()
			c.onPacket(event.Packet, time.Now())
		} else if event.Type == streamShutdown {
			stream.shutdown.Store(true)
		}
	}

	handleWrites := func(write *queuedWrite, ok bool) {
		if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			c.logger.Trace("get queued write from writes", "dst.peer", c.cid.Peer, "content", len(write.data))
		}
		resetIdleTimer()
		c.onWrite(write)
	}

	handleTimeout := func(timeoutPkt *packet) {
		// The wheel has already removed this one; keep the armed set in step.
		delete(c.armed, timeoutPkt.Header.SeqNum)
		c.logger.Debug("unack timeout",
			"seq", timeoutPkt.Header.SeqNum,
			"ack", timeoutPkt.Header.AckNum,
			"item.key", timeoutPkt.Header.SeqNum,
			"type", timeoutPkt.Header.PacketType.String())
		c.onTimeout(timeoutPkt, time.Now())
	}

	handleIdleTimeout := func() {
		if c.state.stateType != ConnClosed {
			c.logger.Trace("idle timeout, closing...")
			c.state.stateType = ConnClosed
			c.state.Err = ErrTimedOut
		}
	}

	handleCtxDone := func() {
		if !stream.shutdown.Load() {
			c.logger.Trace("ctx done, uTP conn initiating shutdown...", "err", c.ctx.Err())
			stream.shutdown.Store(true)
		}
	}

	var maxStreamEventLen int
	for {
		maxStreamEventLen = max(maxStreamEventLen, len(stream.streamEvents))
		if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			c.logger.Trace("connection event count",
				"maxStreamEventLen", maxStreamEventLen,
				"streamEvents.len", len(stream.streamEvents),
				"readable.len", len(c.readable),
				"writeable.len", len(c.writable))
		}
		select {
		case event := <-stream.streamEvents:
			handleIncoming(event)
			// Take whatever else has already arrived before answering, so one
			// acknowledgement covers the batch rather than one per packet.
			// This is what libutp's embedder does by calling
			// utp_issue_deferred_acks once per event-loop pass.
			//
			// Bounded so that a peer sending continuously cannot keep this
			// inner loop fed and starve writes and timers; the outer loop
			// returns here immediately anyway if more are waiting.
			for drained := 0; drained < maxAckCoalesce; drained++ {
				select {
				case more := <-stream.streamEvents:
					handleIncoming(more)
				default:
					drained = maxAckCoalesce
				}
			}
			goto afterSelect
		default:
		}
		select {
		case event := <-stream.streamEvents:
			handleIncoming(event)
		case write, ok := <-stream.writes:
			handleWrites(write, ok)
		case <-c.readable:
			c.processReads()
		case <-c.writable:
			c.processWrites(time.Now())
		case timeoutPkt := <-c.unackTimeoutCh:
			handleTimeout(timeoutPkt)
		case <-keepAliveTicker.C:
			// libutp: `if (state >= CS_CONNECTED && !fin_sent)` and the
			// connection has been silent for the interval
			// (utp_internal.cpp:1271-1274).
			if c.state.stateType == ConnConnected &&
				(c.state.closing == nil || c.state.closing.LocalFin == nil) &&
				time.Since(c.lastSentPacket) >= c.keepAliveIntervalOrDefault() {
				c.emit(c.keepAlivePacket())
			}
		case <-probeTimer.C:
			// The peer's window has been closed for a whole interval. Let one
			// packet through, so its acknowledgement carries a fresh window.
			c.processWrites(time.Now())
		case <-idleTimer.C:
			handleIdleTimeout()
		case <-c.ctx.Done():
			handleCtxDone()
			if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				c.logger.Trace("stream context done, will force stop...", "c.cid.peer", c.cid.Peer, "c.cid.Send", c.cid.Send, "c.cid.Recv", c.cid.Recv)
			}
			return c.ctx.Err()
		}
	afterSelect:
		c.flushAck()
		c.sampleMetrics(time.Now(), false)
		if stream.shutdown.Load() && c.state.stateType != ConnClosed {
			c.shutdown()
		}

		if c.state.stateType == ConnClosed {
			if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				c.logger.Trace("uTP conn closing...", "err", c.state.Err, "c.cid.Send", c.cid.Send, "c.cid.Recv", c.cid.Recv)
			}
			// Always drain and terminate the read side here. The previous
			// `if !c.eof()` guard was inverted in effect: eof() is true by
			// definition once stateType is ConnClosed, so processReads was
			// never called and the end-of-stream marker was never delivered.
			// Readers reaching this path blocked in ReadToEOF forever.
			c.processReads()
			c.processWrites(time.Now())
			if c.state.RecvBuf != nil {
				c.state.RecvBuf.close()
			}
			// A final sample, so the last state of a connection is always
			// observed rather than lost to the throttle.
			c.sampleMetrics(time.Now(), true)
			return c.state.Err
		}
	}
}

// notifySocketShutdown asks the socket to forget this connection.
//
// The wait is bounded because the socket's event loop may already be gone --
// if the socket is closing, it drops its whole table anyway, so there is
// nothing left to clean up and blocking here would leak the very goroutine
// this is trying to tidy up after.
func (c *connection) notifySocketShutdown() {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case c.socketEvents <- newShutdownSocketEvent(c.cid):
	case <-timer.C:
	}
}

func (c *connection) shutdown() {
	switch c.state.stateType {
	case ConnConnecting:
		c.state.stateType = ConnClosed
	case ConnClosed:
		// ignore
	case ConnConnected:
		if c.state.closing != nil {
			localFin := c.state.closing.LocalFin
			// If we have not sent our FIN, and there are no pending writes, and there is no
			// pending data in the send buffer, then send our FIN
			if localFin == nil && len(c.pendingWrites) == 0 && c.state.SendBuf.IsEmpty() {
				recvWindow := uint32(c.state.RecvBuf.Available())
				seqNum := c.state.SentPackets.NextSeqNum()
				ackNum := c.state.RecvBuf.AckNum()
				selectiveAck := c.state.RecvBuf.SelectiveAck()

				fin := NewPacketBuilder(
					st_fin,
					c.cid.Send,
					NowMicro(),
					recvWindow,
					seqNum,
				).WithAckNum(ackNum).WithSelectiveAck(selectiveAck).Build()

				c.state.closing.LocalFin = &seqNum
				if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
					c.logger.Trace("transmitting FIN", "dst.Peer", c.cid.Peer, "dst.Send", c.cid.Send, "dst.Recv", c.cid.Recv, "seq", seqNum)
				}
				c.transmit(fin, time.Now(), true)
			}
		} else {
			var localFin *uint16
			if len(c.pendingWrites) == 0 && c.state.SendBuf.IsEmpty() {
				recvWindow := uint32(c.state.RecvBuf.Available())
				seqNum := c.state.SentPackets.NextSeqNum()
				ackNum := c.state.RecvBuf.AckNum()
				selectiveAck := c.state.RecvBuf.SelectiveAck()

				fin := NewPacketBuilder(
					st_fin,
					c.cid.Send,
					NowMicro(),
					recvWindow,
					seqNum,
				).WithAckNum(ackNum).
					WithSelectiveAck(selectiveAck).
					Build()

				localFin = &seqNum
				if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
					c.logger.Trace("transmitting FIN", "dst.Peer", c.cid.Peer, "dst.Send", c.cid.Send, "dst.Recv", c.cid.Recv, "seq", seqNum)
				}
				c.transmit(fin, time.Now(), true)
			}
			if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				c.logger.Trace("init localFin of closingRecord", "dst.peer", c.cid.Peer, "dst.send", c.cid.Send, "dst.recv", c.cid.Recv, "localFin", localFin)
			}
			c.state.closing = &ClosingRecord{
				LocalFin:  localFin,
				RemoteFin: nil,
			}
		}
	}
}

// emit hands a packet to the socket and records when this connection last
// spoke.
//
// Every outbound packet goes through here so that the keep-alive can tell
// whether the connection has been silent, which is libutp's
// `last_sent_packet` (utp_internal.cpp:1272).
func (c *connection) emit(pkt *packet) {
	if pkt == nil {
		return
	}
	c.lastSentPacket = time.Now()
	c.socketEvents <- newOutgoingSocketEvent(pkt, c.cid)
}

// keepAlivePacket is a STATE acknowledging one less than we actually have.
//
// libutp: `ack_nr--; send_ack(); ack_nr++` (utp_internal.cpp:834-844). Acking
// one behind makes the packet look like a stale acknowledgement to the peer,
// which answers it -- so the exchange proves both directions still work
// without consuming a sequence number or delivering anything.
func (c *connection) keepAlivePacket() *packet {
	pkt := c.statePacket()
	if pkt == nil {
		return nil
	}
	pkt.Header.AckNum-- // wrapping
	return pkt
}

// keepAliveIntervalOrDefault is the configured interval, or libutp's.
func (c *connection) keepAliveIntervalOrDefault() time.Duration {
	if c.config.KeepAliveInterval > 0 {
		return c.config.KeepAliveInterval
	}
	return defaultKeepAliveInterval
}

// zeroWindowProbeIntervalOrDefault is the configured interval, or libutp's.
func (c *connection) zeroWindowProbeIntervalOrDefault() time.Duration {
	if c.config.ZeroWindowProbeInterval > 0 {
		return c.config.ZeroWindowProbeInterval
	}
	return defaultZeroWindowProbeInterval
}

// effectivePeerWindow is how much the peer says it can accept, with the
// zero-window probe applied.
//
// libutp does this by overwriting max_window_user with PACKET_SIZE once
// zerowindow_time has passed (utp_internal.cpp:1142-1145); the next
// acknowledgement overwrites it again with whatever the peer reports. The
// effect is the same: exactly one packet gets through a closed window per
// interval, and the peer's answer to it carries a fresh window.
func (c *connection) effectivePeerWindow(now time.Time) uint32 {
	if c.peerRecvWindow > 0 {
		return c.peerRecvWindow
	}
	if c.zeroWindowProbeDue.IsZero() || now.Before(c.zeroWindowProbeDue) {
		return 0
	}
	c.probingZeroWindow = true
	return uint32(c.config.MaxPacketSize)
}

func (c *connection) processWrites(now time.Time) {
	if c.state.SentPackets != nil {
		// LEDBAT++'s slowdowns are driven by the clock, not by acks. Without
		// this, a connection whose application goes quiet mid-slowdown would
		// stay pinned at two packets until an ack happened to arrive.
		c.state.SentPackets.OnTick(now)
	}
	switch c.state.stateType {
	case ConnConnecting:
		return
	case ConnClosed:
		if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			c.logger.Warn("connection is closed, will not process pending writes",
				"c.cid.send", c.cid.Send, "c.cid.recv", c.cid.Recv)
		}
		result := &readOrWriteResult{
			Err: c.state.Err,
		}
		for _, w := range c.pendingWrites {
			w.resultCh <- result
		}
		return
	default:
	}

	// Compose data packets
	windowSize := minUint32(c.state.SentPackets.Window(), c.effectivePeerWindow(now))
	var payloads [][]byte

	// libutp's `is_full` marks the connection application-limited or not
	// every time it considers sending a packet (utp_internal.cpp:945, :957),
	// and the congestion controller refuses to grow a window the application
	// never fills (:1681-1686). The equivalent signal here is "we had data
	// and no room for it": either no window at all, or the window ran out
	// before the send buffer did.
	windowFull := windowSize == 0 && c.state.SendBuf.Pending() > 0

	for windowSize > 0 {
		if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			c.logger.Trace("has window size to send a packet data in sendBuffer", "windowSize", windowSize)
		}
		maxDataSize := minUint32(windowSize, c.mtu.payloadSize())
		data := make([]byte, maxDataSize)
		n := c.state.SendBuf.Read(data)
		if n == 0 {
			break
		}
		payloads = append(payloads, data[:n])
		windowSize -= uint32(n)
		if windowSize == 0 && c.state.SendBuf.Pending() > 0 {
			windowFull = true
		}
	}
	if windowFull {
		c.state.SentPackets.OnWindowFull(now)
	}

	// Write pending data to send buffer
	for len(c.pendingWrites) > 0 {
		bufSpace := c.state.SendBuf.Available()
		if bufSpace <= 0 {
			break
		}

		writeReq := c.pendingWrites[0]

		if len(writeReq.data) <= bufSpace {
			c.state.SendBuf.Write(writeReq.data)
			result := &readOrWriteResult{
				Len: len(writeReq.data) + writeReq.written,
			}
			writeReq.resultCh <- result
			c.pendingWrites = c.pendingWrites[1:]
		} else {
			nextWrite := writeReq.data[:bufSpace]
			remainingData := writeReq.data[bufSpace:]
			c.state.SendBuf.Write(nextWrite)

			writeReq.data = remainingData
			writeReq.written += bufSpace
		}
		select {
		case c.writable <- struct{}{}:
		default:
		}
	}

	// transmit data packets
	seqNum := c.state.SentPackets.NextSeqNum()
	recvWindow := uint32(c.state.RecvBuf.Available())
	ackNum := c.state.RecvBuf.AckNum()
	selectiveAck := c.state.RecvBuf.SelectiveAck()

	for _, payload := range payloads {
		packetInst := NewPacketBuilder(
			st_data,
			c.cid.Send,
			NowMicro(),
			recvWindow,
			seqNum,
		).WithPayload(payload).WithTsDiffMicros(uint32(c.peerTsDiff.Microseconds())).WithAckNum(ackNum).WithSelectiveAck(selectiveAck).Build()

		c.transmit(packetInst, now, true)
		seqNum = seqNum + 1 // wrapping add in uint16
	}
}

func (c *connection) onWrite(writeReq *queuedWrite) {
	writeReq.written = 0
	switch c.state.stateType {
	case ConnConnecting:
		// There are 0 bytes written so far
		c.pendingWrites = append(c.pendingWrites, writeReq)

	case ConnConnected:
		if c.state.closing != nil {
			if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				c.logger.Trace("append a queuedWrite to pending writes when closing the conn...")
			}
			if c.state.closing.LocalFin == nil && c.state.closing.RemoteFin != nil {
				c.pendingWrites = append(c.pendingWrites, writeReq)
			} else {
				writeReq.resultCh <- &readOrWriteResult{Len: 0}
			}
		} else {
			c.logger.Debug("append a queuedWrite to pending writes")
			c.pendingWrites = append(c.pendingWrites, writeReq)
		}

	case ConnClosed:
		c.logger.Warn("discard a queuedWrite when closed the conn...")
		result := &readOrWriteResult{
			Err: c.state.Err,
			Len: 0,
		}
		writeReq.resultCh <- result
	}
	c.processWrites(time.Now())
	select {
	case c.writable <- struct{}{}:
	default:
	}
}

func (c *connection) processReads() {
	if c.state.stateType == ConnConnecting {
		return
	}
	recvBuf := c.state.RecvBuf

	currentTime := time.Now()
	if recvBuf != nil && c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		c.logger.Trace("read data saving in the recvBuf, start...", "available", recvBuf.Available(), "isEmpty", recvBuf.IsEmpty())
	}
	// Drain contiguous data even once the connection is closed: bytes that
	// arrived before teardown are still owed to the reader.
	for recvBuf != nil && !recvBuf.IsEmpty() {
		buf := make([]byte, c.config.MaxPacketSize)
		n := recvBuf.Read(buf)
		if n == 0 {
			break
		}
		if !c.sendRead(&readOrWriteResult{Data: buf, Len: n}) {
			return
		}
	}
	if recvBuf != nil && c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		c.logger.Trace("read data saving in the recvBuf, end...", "duration", time.Since(currentTime), "available", recvBuf.Available(), "isEmpty", recvBuf.IsEmpty())
	}

	// If we have reached eof, hand the reader the end-of-stream marker.
	if c.eof() {
		c.deliverTerminalRead()
	}
}

// sampleMetrics hands a snapshot to the configured observer, throttled to
// MetricsInterval.
//
// It runs on the event-loop goroutine, which is the whole point: the
// connection's state is owned by that goroutine, so reading it from anywhere
// else would be a race.
func (c *connection) sampleMetrics(now time.Time, force bool) {
	if c.config.Metrics == nil {
		return
	}
	interval := c.config.MetricsInterval
	if interval <= 0 {
		interval = DefaultMetricsInterval
	}
	if !force && !c.lastMetricsAt.IsZero() && now.Sub(c.lastMetricsAt) < interval {
		return
	}
	c.lastMetricsAt = now

	m := ConnectionMetrics{
		At:                   now,
		Cid:                  c.cid,
		PeerRecvWindow:       c.peerRecvWindow,
		PeerTsDiff:           c.peerTsDiff,
		PacketsSent:          c.packetsSent,
		BytesSent:            c.bytesSent,
		PacketsRetransmitted: c.packetsRetransmitted,
		BytesRetransmitted:   c.bytesRetransmitted,
		PacketsReceived:      c.packetsReceived,
		BytesReceived:        c.bytesReceived,
		Timeouts:             c.timeouts,
		FastRetransmits:      c.fastRetransmits,
		PendingWrites:        len(c.pendingWrites),
		State:                connStateName(c.state.stateType),
	}
	if c.state.SentPackets != nil {
		cs := c.state.SentPackets.ControllerStats()
		m.CwndBytes = cs.MaxWindowSizeBytes
		m.InFlightBytes = cs.WindowSizeBytes
		m.MinCwndBytes = cs.MinWindowSizeBytes
		m.RTT = cs.RTT
		m.RTTVarianceMicros = cs.RTTVarianceMicros
		m.Timeout = cs.Timeout
		m.BaseDelay = cs.BaseDelay
		m.TargetDelayMicros = cs.TargetDelayMicros
	}
	if c.state.SendBuf != nil {
		m.SendBufferPending = c.state.SendBuf.Pending()
	}
	if c.state.RecvBuf != nil {
		m.RecvBufferPending = c.state.RecvBuf.Pending()
	}
	c.config.Metrics(m)
}

// deliverTerminalRead hands the reader the single end-of-stream marker: a
// result with an empty Data slice, which is what UtpStream.ReadToEOF treats as
// the end of the stream. It is delivered at most once.
//
// Every path that closes the connection must reach this, including idle
// timeout, RESET and a locally-initiated close. A reader blocks on c.reads
// forever if the marker never arrives.
func (c *connection) deliverTerminalRead() {
	if c.readsTerminated {
		return
	}
	c.readsTerminated = true
	// A clean end-of-stream reports no error, matching io.ReadAll: ReadToEOF
	// reads to EOF by definition, so reaching it is success. Only an abnormal
	// close (RESET, idle timeout) carries an error.
	err := c.state.Err
	c.logger.Debug("read eof...", "err", err)
	c.sendRead(&readOrWriteResult{Err: err, Data: make([]byte, 0)})
}

// sendRead delivers a read result, giving up if the connection context is
// cancelled. Without the context arm a reader that has already gone away
// wedges the event loop permanently.
func (c *connection) sendRead(res *readOrWriteResult) bool {
	select {
	case c.reads <- res:
		return true
	case <-c.ctx.Done():
		return false
	}
}

func (c *connection) eof() bool {
	switch c.state.stateType {
	case ConnConnecting:
		return false
	case ConnConnected:
		if c.state.closing != nil && c.state.closing.RemoteFin != nil {
			return c.state.RecvBuf.AckNum() == *c.state.closing.RemoteFin
		}
		return false
	case ConnClosed:
		return true
	default:
		return false
	}
}

func (c *connection) onTimeout(originPacket *packet, now time.Time) {
	switch c.state.stateType {
	case ConnConnecting:
		if c.endpoint.Type == Acceptor {
			return
		}
		if c.endpoint.Attempts >= c.config.MaxConnAttempts {
			c.logger.Error("quitting connection attempt", "attempts", c.endpoint.Attempts)
			err := ErrTimedOut
			if c.state.connectedCh != nil {
				c.state.connectedCh <- err
			}
			c.state.stateType = ConnClosed
			c.state.Err = err
			return
		} else {
			seq := c.endpoint.SynNum
			logMsg := fmt.Sprintf("retrying connection, after %d attempts", c.endpoint.Attempts)
			switch c.endpoint.Attempts {
			case 1, 0:
				c.logger.Trace(logMsg)
			case 2:
				c.logger.Debug(logMsg)
			case 3:
				c.logger.Info(logMsg)
			default:
				c.logger.Warn(logMsg)
			}
			c.endpoint.Attempts += 1

			// Double the previous timeout, which is what libutp does on every
			// retransmission timeout: `retransmit_timeout * 2`
			// (utp_internal.cpp:1179, applied at :1203). This previously
			// computed InitialTimeout * 1.5^attempts, which grows from the
			// initial value rather than the current one and uses a different
			// factor.
			//
			// The cap is defensive rather than libutp's behaviour: libutp has
			// none, because its own give-up limit of two timeouts in
			// CS_SYN_SENT bounds the backoff. It is unreachable at the
			// default MaxConnAttempts and only matters if a caller raises it.
			c.synTimeout *= 2
			if c.synTimeout > c.config.MaxTimeout {
				c.synTimeout = c.config.MaxTimeout
			}

			c.armRetransmit(originPacket, c.synTimeout)

			// Re-send SYN packet
			c.emit(c.synPacket(seq))
		}

	case ConnConnected:
		// If the timed out packet is a SYN, do nothing
		if originPacket.Header.PacketType == st_syn {
			return
		}

		// One timeout event per RTO expiry, measured against the clock.
		//
		// This connection arms one timer per outstanding packet, and the
		// wheel that fires them has a resolution of its own, so a callback
		// arriving is not by itself evidence that the retransmission timeout
		// has elapsed. libutp asks the clock: `current_ms - rto_timeout >= 0`
		// (utp_internal.cpp:1147-1148). So does this.
		//
		// The guard it replaces compared the time since the *last* timeout
		// against the current RTO, which is a different question and gave a
		// different answer: after a timeout doubled the RTO, the next
		// expiry arrived exactly one (old) RTO later, which is not more than
		// the new one -- so it was not counted, the backoff did not double,
		// and the packet was resent anyway. The result was two
		// retransmissions per RTO value instead of one. Measured against
		// libutp with a 50ms floor: ours resent at 29, 128, 231, 429, 629,
		// 1030, 1430 ms where libutp resends at 50, 150, 350, 750 ms.
		// The *backoff* happens once per expiry; the retransmission happens
		// for every packet whose timer fired.
		//
		// That split matters, and getting it wrong cost a benchmark run to
		// discover. An earlier attempt returned without retransmitting when
		// the deadline had not passed, on the theory that only one callback
		// per expiry is a real timeout. It is -- but the others are still
		// packets that need resending: libutp marks every outstanding packet
		// need_resend on an RTO (utp_internal.cpp:1230-1237). Skipping them
		// left holes that fast retransmit could not fill, and the 5%-loss
		// benchmark stopped completing at all.
		//
		// Earliness is not this check's job either. The wheel guarantees it
		// now: an item is never fired before its delay (see timeWheel.put),
		// which is what a retransmission timer has to promise, because
		// resending before the timeout resends a packet the peer was still
		// going to acknowledge.
		now := time.Now()

		// A retransmission timeout on the MTU probe, with nothing else
		// outstanding, says the path will not carry that size -- not that it
		// is congested. libutp lowers the ceiling and sets `ignore_loss`, so
		// the window is left alone and the binary search moves on
		// (utp_internal.cpp:1152-1167).
		probeTimedOut := c.mtu.probeOutstanding(originPacket.Header.SeqNum) &&
			c.state.SentPackets.UnackedCount() == 1
		if probeTimedOut && !now.Before(c.rtoDeadline) {
			c.mtu.onProbeLost(now)
			c.logger.Debug("MTU probe timed out",
				"floor", c.mtu.floor, "ceiling", c.mtu.ceiling, "current", c.mtu.current)
			c.rtoDeadline = now.Add(c.state.SentPackets.Timeout())
			c.packetsRetransmitted++
			c.bytesRetransmitted += uint64(len(originPacket.Body))
			c.retransmit(originPacket, now)
			return
		}

		if isTimeout := !now.Before(c.rtoDeadline); isTimeout {
			// Give up once enough consecutive RTOs have passed with the peer
			// acking nothing, as libutp does (utp_internal.cpp:1191). The
			// check precedes the increment there, so the connection dies on
			// the RTO after the fourth retransmission.
			if c.retransmitCount >= maxConsecutiveTimeouts {
				c.logger.Warn("giving up on connection",
					"consecutiveTimeouts", c.retransmitCount,
					"cid.send", c.cid.Send, "cid.recv", c.cid.Recv)
				c.state.stateType = ConnClosed
				c.state.Err = ErrTimedOut
				return
			}
			c.retransmitCount++
			c.state.SentPackets.OnTimeout()
			c.timeouts++
			currentTime := time.Now()
			c.latestTimeout = &currentTime
			// libutp: `rto_timeout = ctx->current_ms + new_timeout`
			// (utp_internal.cpp:1204), with new_timeout already doubled.
			c.rtoDeadline = currentTime.Add(c.state.SentPackets.Timeout())
		}
		c.packetsRetransmitted++
		c.bytesRetransmitted += uint64(len(originPacket.Body))

		c.retransmit(originPacket, now)
	default:
	}
}

// maxAckCoalesce bounds how many already-queued packets one pass of the event
// loop will take before answering them.
//
// libutp has no explicit bound: its embedder drains its socket and then calls
// utp_issue_deferred_acks. The bound here exists because this loop also
// services writes and timers, and an unbounded inner drain would let a peer
// sending continuously starve them.
const maxAckCoalesce = 64

// flushAck sends the acknowledgement owed for whatever was received in this
// pass of the event loop, if any.
func (c *connection) flushAck() {
	if !c.ackPending {
		return
	}
	c.ackPending = false
	if statePacket := c.statePacket(); statePacket != nil {
		c.emit(statePacket)
	}
}

// retransmit resends a packet with its acknowledgement fields brought up to
// date. The sequence number and payload are the original's; everything the
// peer reads about our current state is not.
func (c *connection) retransmit(originPacket *packet, now time.Time) {
	retransmissionPacket := &packet{
		Header: &PacketHeaderV1{
			PacketType:    originPacket.Header.PacketType,
			Version:       originPacket.Header.Version,
			Extension:     originPacket.Header.Extension,
			ConnectionId:  originPacket.Header.ConnectionId,
			SeqNum:        originPacket.Header.SeqNum,
			WndSize:       uint32(c.state.RecvBuf.Available()),
			Timestamp:     time.Now().UnixMicro(),
			TimestampDiff: uint32(c.peerTsDiff.Microseconds()),
			AckNum:        c.state.RecvBuf.AckNum(),
		},
		Body: originPacket.Body,
		Eack: c.state.RecvBuf.SelectiveAck(),
	}
	c.transmit(retransmissionPacket, now, false)
}

// defaultZeroWindowProbeInterval is how long libutp tolerates a closed peer
// window before forcing a packet through: "Reset max_window_user to 1 every 15
// seconds" (utp_internal.cpp:2150-2151).
const defaultZeroWindowProbeInterval = 15 * time.Second

// defaultKeepAliveInterval is how long libutp lets an established connection
// stay silent before sending a keep-alive: `#define KEEPALIVE_INTERVAL 29000`
// (utp_internal.cpp:74), applied at :1271-1274.
//
// 29 seconds is chosen to sit under the 30-second UDP mapping timeout common
// in NATs. A connection that goes quiet for longer than that loses its
// mapping and cannot be reached again from the outside.
const defaultKeepAliveInterval = 29 * time.Second

// reorderBufferMaxSize bounds how far past the next expected sequence number
// a packet may name and still be processed.
//
// libutp: `#define REORDER_BUFFER_MAX_SIZE 1024` (utp_internal.cpp:54),
// applied at :1890. It is the limit on how far ahead a peer can push this
// connection's pending state, and therefore on what a peer can make it hold.
const reorderBufferMaxSize = uint16(1024)

// reorderOldPacketFloor is the other end of the same test: a distance at or
// above this has wrapped, so the packet is an *old* one within
// reorderBufferMaxSize behind us rather than a wildly future one. libutp
// writes it as `(SEQ_NR_MASK + 1) - REORDER_BUFFER_MAX_SIZE`
// (utp_internal.cpp:1891).
const reorderOldPacketFloor = uint16(1<<16 - 1024)

// ackNrAllowedWindow is how far behind the last sent sequence number a peer's
// acknowledgement may point and still be believed.
//
// libutp: `#define ACK_NR_ALLOWED_WINDOW DUPLICATE_ACKS_BEFORE_RESEND`, which
// is 3 (utp_internal.cpp:64, :69).
const ackNrAllowedWindow = uint16(3)

// invalidAckNum reports whether a packet acknowledges something this
// connection never sent, or something so old it cannot be meaningful.
//
// libutp (utp_internal.cpp:1794-1807), which is unusually explicit about why:
//
//	// ignore packets whose ack_nr is invalid. This would imply a spoofed
//	// address or a malicious attempt to attach the uTP implementation.
//	// acking a packet that hasn't been sent yet!
//
//	const uint16 curr_window = max<uint16>(
//	    conn->cur_window_packets + ACK_NR_ALLOWED_WINDOW, ACK_NR_ALLOWED_WINDOW);
//	if ((pk_flags != ST_SYN || conn->state != CS_SYN_RECV) &&
//	    (wrapping_compare_less(conn->seq_nr - 1, pk_ack_nr, ACK_NR_MASK)
//	     || wrapping_compare_less(pk_ack_nr, conn->seq_nr - 1 - curr_window, ACK_NR_MASK)))
//	    return 0;
//
// The acceptable range is a short window ending at the last sequence number
// actually sent. Anything above it acknowledges a packet that does not exist
// yet; anything below is too stale to act on.
//
// This fork had no such rule. CONFORMANCE.md previously recorded that we
// "already match" here, on the strength of two corpus cases that happened to
// produce the same silence for other reasons -- the behaviour was incidental,
// not implemented. FuzzDifferentialResponder showed the difference with a
// single ST_DATA whose ack_nr named a packet we had never sent: libutp
// dropped it without a word, we answered it.
func (c *connection) invalidAckNum(pkt *packet) bool {
	if pkt.Header.PacketType == st_syn {
		// libutp's exception for a SYN arriving in CS_SYN_RECV: there are no
		// previous packets for it to be acking.
		return false
	}
	if c.state.stateType != ConnConnected || c.state.SentPackets == nil {
		return false
	}

	lastSent := c.state.SentPackets.NextSeqNum() - 1
	window := c.state.SentPackets.UnackedCount() + ackNrAllowedWindow
	if window < ackNrAllowedWindow {
		window = ackNrAllowedWindow
	}

	ackNum := pkt.Header.AckNum
	if wrappingLessThan(lastSent, ackNum) {
		// Acknowledges a packet we have not sent.
		return true
	}
	if wrappingLessThan(ackNum, lastSent-window) {
		// Too far behind to mean anything.
		return true
	}
	return false
}

// outsideReorderWindow reports whether a packet names a sequence number too
// far from the one expected to be worth processing, and answers it the way
// libutp does if so.
//
// libutp (utp_internal.cpp:1886-1899):
//
//	const uint seqnr = (pk_seq_nr - conn->ack_nr - 1) & SEQ_NR_MASK;
//	if (seqnr >= REORDER_BUFFER_MAX_SIZE) {
//	    if (seqnr >= (SEQ_NR_MASK + 1) - REORDER_BUFFER_MAX_SIZE
//	        && pk_flags != ST_STATE) conn->schedule_ack();
//	    return 0;
//	}
//
// `seqnr` counts how far past the next expected packet this one is, so 0 means
// "exactly the packet we are waiting for". Past 1024 the packet is either
// absurdly far in the future or, by wrapping, an old one already consumed.
// Either way libutp drops it without buffering it, acking it, or letting it
// touch congestion control. An old packet -- within 1024 behind, and not a
// STATE -- additionally re-triggers an ack, because the peer evidently missed
// the one already sent.
//
// This fork had no such bound. A peer could name any sequence number in the
// 16-bit space and we would buffer the packet as out-of-order data and answer
// it with a STATE. Three costs: unbounded pending state driven by a remote
// party, one emitted packet per junk packet where the reference emits none,
// and -- since the sequence number is far outside the 30-entry selective-ack
// window -- a selective ack naming nothing at all.
//
// The check covers ST_DATA, ST_STATE and ST_FIN only. libutp reaches it
// through utp_process_incoming, which a SYN returns from before the check
// (utp_internal.cpp:1878) and which a RESET never enters at all: RESET is
// handled in its own branch at the socket level and returns
// (utp_internal.cpp:2855-2880).
//
// Both the missing bound and the wrongly-included RESET were found by
// FuzzDifferentialResponder, which runs this implementation and libutp over
// the same generated packet sequences and fails when we answer something the
// reference ignores.
func (c *connection) outsideReorderWindow(pkt *packet) bool {
	switch pkt.Header.PacketType {
	case st_data, st_state, st_fin:
	default:
		return false
	}
	if c.state.stateType != ConnConnected || c.state.RecvBuf == nil {
		return false
	}

	distance := pkt.Header.SeqNum - c.state.RecvBuf.AckNum() - 1 // wrapping
	if distance < reorderBufferMaxSize {
		return false
	}

	if distance >= reorderOldPacketFloor && pkt.Header.PacketType != st_state {
		// An old packet the peer is still retransmitting: re-ack, as libutp
		// does, so it can stop.
		//
		// Emitted directly rather than through transmit(), which registers
		// the packet with SentPackets and arms a retransmission timer. A
		// STATE is neither retransmitted nor numbered: libutp's send_ack
		// writes the current seq_nr without consuming it
		// (utp_internal.cpp:781, with :1088-1089 showing where one is
		// consumed). Going through transmit() advanced our sequence number
		// on every re-ack, so the next STATE carried a number libutp's never
		// would -- caught by FuzzDifferentialResponder comparing consecutive
		// acks.
		if statePkt := c.statePacket(); statePkt != nil {
			c.emit(statePkt)
		}
	}
	if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		c.logger.Trace("dropping packet outside the reorder window",
			"seqNum", pkt.Header.SeqNum, "ackNum", c.state.RecvBuf.AckNum(),
			"distance", distance)
	}
	return true
}

func (c *connection) onPacket(packet *packet, now time.Time) {
	if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		c.logger.Trace("on packet start...",
			"packet.body.len", len(packet.Body),
			"packet.type", packet.Header.PacketType.String(),
			"packet.cid", packet.Header.ConnectionId,
			"packet.seqnum", packet.Header.SeqNum,
			"packet.acknum", packet.Header.AckNum,
			"packet.windowSize", packet.Header.WndSize,
			"now", now)
	}
	c.peerRecvWindow = packet.Header.WndSize

	// Arm or disarm the zero-window probe.
	//
	// libutp arms it whenever an acknowledgement reports a zero window
	// (utp_internal.cpp:2149-2151), and lets the next acknowledgement
	// overwrite the forced one-packet window with whatever the peer now
	// reports (:2145).
	if c.peerRecvWindow == 0 {
		if c.zeroWindowProbeDue.IsZero() {
			interval := c.zeroWindowProbeIntervalOrDefault()
			c.zeroWindowProbeDue = now.Add(interval)
			if c.armProbeTimer != nil {
				c.armProbeTimer(interval)
			}
		}
	} else {
		c.zeroWindowProbeDue = time.Time{}
	}
	c.probingZeroWindow = false
	c.packetsReceived++
	c.bytesReceived += uint64(len(packet.Body))

	// Measure how long ago the peer stamped this packet. That value is what we
	// echo back in timestamp_difference_microseconds, and it is the peer's
	// only delay signal for congestion control.
	//
	// This used to subtract the packet's timestamp_difference field from our
	// own full-width wall clock: the wrong field, and mixing an int64 epoch
	// clock with a uint32 wire value. The result was ~1.79e15 microseconds on
	// every packet, which the cap below then pinned to exactly 1 second -- so
	// every packet we sent advertised a constant 1,000,000 us delay and the
	// peer's LEDBAT controller was fed a constant. It had no delay signal at
	// all. See KNOWN-LIMITATIONS.md.
	//
	// libutp computes reply_micro the same way, as a uint32 subtraction of the
	// packet's timestamp from the current time (utp_internal.cpp, reply_micro
	// assignment in UTP_ProcessIncoming).
	peerTsDiff := timestampDiffMicros(NowMicro(), uint32(packet.Header.Timestamp))
	// Cap absurd values, which mean the peer's clock is unusable rather than
	// that the link is slow.
	if peerTsDiff > c.config.MaxIdleTimeout {
		c.peerTsDiff = time.Second
	} else {
		c.peerTsDiff = peerTsDiff
	}

	// libutp validates the acknowledgement number before anything else, and
	// so does this. Order matters: a packet rejected here must not first
	// draw a re-ack from the reorder-window check below.
	if c.invalidAckNum(packet) {
		if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			c.logger.Trace("dropping packet with an out-of-range ack number",
				"ackNum", packet.Header.AckNum, "type", packet.Header.PacketType.String())
		}
		return
	}

	if c.outsideReorderWindow(packet) {
		return
	}

	// Handle different packet types
	var err error
	switch packet.Header.PacketType {
	case st_syn:
		c.onSyn(packet.Header.SeqNum)
	case st_state:
		c.onState(packet.Header.SeqNum, packet.Header.AckNum)
	case st_data:
		err = c.onData(packet.Header.SeqNum, packet.Body)
	case st_fin:
		err = c.onFin(packet.Header.SeqNum, packet.Body)
	case st_reset:
		c.onReset()
	}
	if err != nil {
		c.logger.Warn("on packet handle data or fin err", "err", err)
	}

	// Process acknowledgments
	switch packet.Header.PacketType {
	case st_state, st_data, st_fin:
		delay := time.Duration(packet.Header.TimestampDiff) * time.Microsecond
		if err = c.processAck(packet.Header.AckNum, packet.Eack, delay, now); err != nil {
			if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				c.logger.Trace("ack does not correspond to known seq_num",
					"packet.type", packet.Header.PacketType,
					"packet.seqNum", packet.Header.SeqNum,
					"packet.ackNum", packet.Header.AckNum,
					"err", err)
			}
		}
	default:
	}

	// Handle retransmissions
	c.retransmitLostPackets(now)

	// Send STATE packet if appropriate
	switch packet.Header.PacketType {
	case st_syn:
		if c.synState == nil {
			// The synState is generated at the beginning of the eventLoop
			c.logger.Warn("missing SYN STATE")
			if statePacket := c.statePacket(); statePacket != nil {
				c.synState = statePacket
				c.emit(c.synState)
			} else {
				randSeqNum := RandomUint16()
				resetPacket := NewPacketBuilder(st_reset, packet.Header.ConnectionId, uint32(time.Now().UnixMicro()), 100_000, randSeqNum).Build()
				c.emit(resetPacket)
			}
		} else {
			// A SYN for a connection we have already accepted is the initiator
			// retransmitting because our SYN-ACK was lost. Re-send the same
			// SYN-ACK: nothing else ever retransmits it, so dropping this
			// duplicate silently strands the peer until it gives up.
			// libutp does the same -- utp_internal.cpp:2549 acks a duplicate
			// SYN on an already-established connection rather than ignoring it.
			if c.logger.Enabled(BASE_CONTEXT, log.LevelDebug) {
				c.logger.Debug("re-sending SYN-ACK for retransmitted SYN",
					"dst.peer", c.cid.Peer, "cid.send", c.cid.Send, "cid.recv", c.cid.Recv,
					"seqNum", c.synState.Header.SeqNum, "ackNum", c.synState.Header.AckNum)
			}
			c.emit(c.synState)
		}

	case st_fin:
		// A FIN is acknowledged immediately, not deferred.
		//
		// libutp: `// if the other end wants to close, ack` followed by a
		// direct `conn->send_ack()` (utp_internal.cpp:2369-2370), separately
		// from the deferred one it also schedules at :2404.
		//
		// Deferring it does not work here for a reason worth recording:
		// reaching a FIN tears the connection down in the same pass of the
		// event loop, so by the time the deferred acknowledgement would be
		// sent there is no connection left to build one from, and the peer
		// gets nothing at all. Two corpus cases caught that immediately.
		if statePacket := c.statePacket(); statePacket != nil {
			c.emit(statePacket)
		}
	case st_data:
		// Note that an acknowledgement is owed; do not send it here.
		//
		// libutp calls `schedule_ack()` (utp_internal.cpp:2404, :2472), which
		// adds the socket to a list its embedder flushes once per pass of its
		// event loop. So a batch of packets arriving together draws one
		// acknowledgement, not one each.
		//
		// This library used to answer every packet immediately. Measured
		// against libutp with the driver's deferred-ack flush: eight data
		// packets in one batch drew one acknowledgement from libutp and eight
		// from us. On an asymmetric path -- ADSL, cellular -- that reverse
		// traffic is not free, and on a shared bottleneck it competes with
		// the forward data.
		c.ackPending = true
	}

	// Notify writable on STATE packets
	if packet.Header.PacketType == st_state {
		select {
		case c.writable <- struct{}{}:
		default:
		}
	}

	// Notify readable on data or FIN
	if len(packet.Body) > 0 || packet.Header.PacketType == st_fin {
		select {
		case c.readable <- struct{}{}:
		default:
		}
	}

	// Handle connection closing cases
	if c.state.stateType == ConnConnected && c.state.closing != nil && c.state.closing.LocalFin != nil {
		if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			c.logger.Trace("try close connection locally...", "dst.Peer", c.cid.Peer, "dst.Send", c.cid.Send, "dst.Recv", c.cid.Recv)
		}
		lastAckNum, isNone := c.state.SentPackets.LastAckNum()
		if !isNone && lastAckNum == *c.state.closing.LocalFin {
			c.state.stateType = ConnClosed
			c.state.Err = nil
		}
	}

	if c.state.stateType == ConnConnected && c.state.closing != nil && c.state.closing.RemoteFin != nil {
		if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			c.logger.Trace("close connection remotely...", "dst.Peer", c.cid.Peer, "dst.Send", c.cid.Send, "dst.Recv", c.cid.Recv)
		}
		if !c.state.SentPackets.HasUnackedPackets() && c.state.RecvBuf.AckNum() == *c.state.closing.RemoteFin {
			c.processReads()
			c.state.stateType = ConnClosed
			c.state.Err = nil
		}
	}

	if c.state.stateType == ConnConnected && c.state.closing != nil && c.state.closing.RemoteFin != nil && c.state.closing.LocalFin != nil {
		c.state.stateType = ConnClosed
		c.state.Err = nil
	}
}

func (c *connection) processAck(
	ackNum uint16,
	selectiveAck *SelectiveAck,
	delay time.Duration,
	now time.Time,
) error {
	if c.state.stateType != ConnConnected {
		return nil
	}

	fullAcked, selectedAcks, err := c.state.SentPackets.onAck(
		ackNum, selectiveAck, delay, now)
	if err != nil {
		seqRange := c.state.SentPackets.SeqNumRange()
		if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			c.logger.Trace("sent packets onAck has error",
				"err", err,
				"cid.send", c.cid.Send,
				"cid.recv", c.cid.Recv,
				"ackNum", ackNum,
				"seqStart",
				seqRange.start,
				"seqEnd", seqRange.end)
		}
		if errors.Is(err, ErrInvalidAckNum) {
			c.reset(err)
			return err
		}
		return err
	}
	if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		c.logger.Trace("process ack",
			"cid.send", c.cid.Send,
			"cid.recv", c.cid.Recv,
			"fullAcked.start", fullAcked.start,
			"fullAcked.end", fullAcked.end)
	}
	// A probe that came back acknowledged proves the path carries its size.
	// libutp: utp_internal.cpp:1969-1974.
	for seq := fullAcked.Start(); ; seq++ {
		if c.mtu.onAck(seq, now) {
			c.logger.Debug("MTU probe acknowledged",
				"floor", c.mtu.floor, "ceiling", c.mtu.ceiling, "current", c.mtu.current)
		}
		if seq == fullAcked.End() {
			break
		}
	}
	for _, selectedAck := range selectedAcks {
		if c.mtu.onAck(selectedAck, now) {
			c.logger.Debug("MTU probe selectively acknowledged",
				"floor", c.mtu.floor, "ceiling", c.mtu.ceiling, "current", c.mtu.current)
		}
	}

	// A converged search stands for a while and is then redone, because paths
	// change. libutp: 30 minutes (utp_internal.cpp:1310).
	if c.mtu.dueForSearch(now) {
		c.mtu.reset(uint32(c.config.MaxPacketSize), now)
	}

	retired := c.disarmAcked(fullAcked)

	// Restart the retransmission timeout, but only when this ack actually
	// retired something.
	//
	// libutp resets rto_timeout from inside its RTT-sample path
	// (utp_internal.cpp:1388-1389), which runs once per newly acknowledged
	// packet -- not once per ack received. Resetting on every ack looks
	// equivalent and is not: duplicate acks are how a peer reports a hole,
	// so on a lossy path they arrive in a stream, and each one would push the
	// deadline further out. The RTO would then never fire, and a packet that
	// fast retransmit could not recover -- it resends each packet once, and
	// at most four per ack -- would never be retransmitted at all.
	//
	// Measured: with the reset unconditional, the 5%-loss and broadband
	// profiles in the netem benchmark suite stopped completing, the receiver
	// timing out after 76 seconds on a transfer that takes one.
	if retired > 0 {
		c.rtoDeadline = now.Add(c.state.SentPackets.Timeout())
	}
	for _, selectedAck := range selectedAcks {
		if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			c.logger.Trace("process ack, will remove acked num from innerMap",
				"cid.send", c.cid.Send,
				"cid.recv", c.cid.Recv,
				"ackNum", selectedAck)
		}
		if _, armed := c.armed[selectedAck]; armed {
			retired++
		}
		c.disarmRetransmit(selectedAck)
	}

	if retired > 0 {
		// The peer retired something we were still timing, so the path is
		// alive: reset the consecutive-timeout count. libutp resets
		// retransmit_count in ack_packet for the same reason
		// (utp_internal.cpp:1398).
		//
		// Selective acks count. Under reordering or loss the only progress
		// may be a SACK, and ignoring those would kill a connection that is
		// in fact delivering.
		c.retransmitCount = 0
	}

	return nil
}

func (c *connection) onSyn(seqNum uint16) {
	var err error

	if c.endpoint.Type == Acceptor {
		// If we are the accepting endpoint, check whether the SYN is a retransmission
		// A non-matching sequence number is incorrect behavior
		if seqNum != c.endpoint.SynNum {
			err = ErrInvalidSyn
		}
	} else {
		// If we are the initiating endpoint, then an incoming SYN is incorrect behavior
		err = ErrSynFromAcceptor
	}

	if err != nil {
		if c.state.stateType != ConnClosed {
			c.reset(err)
		}
	}
}

func (c *connection) onState(seqNum, ackNum uint16) {
	if ConnConnecting != c.state.stateType {
		return
	}

	if c.endpoint.Type == Initiator && ackNum == c.endpoint.SynNum {
		// NOTE: In a deviation from the specification, we initialize the ACK num
		// to the sequence number of the SYN-ACK minus 1. This is consistent with
		// the reference implementation and the libtorrent implementation.
		if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			c.logger.Trace("connect success, will initial connection state...",
				"cid.send", c.cid.Send, "cid.recv", c.cid.Recv)
		}
		recvBuf := newReceiveBufferWithLogger(c.config.BufferSize, seqNum-1, c.logger) // wrapping subtraction for uint16
		sendBuf := newSendBuffer(c.config.BufferSize)

		congestionCtrl := newDefaultController(fromConnConfig(c.config))
		sentPacketsHolder := newSentPackets(c.endpoint.SynNum, congestionCtrl, c.logger)

		// Report success the same way the acceptor path does, at line ~351:
		// send nil.
		//
		// This used to close the channel instead. A closed channel and a
		// successful one are indistinguishable to a receiver that checks the
		// `ok` flag, and UtpSocket.Connect checks it -- so every successful
		// outgoing connection made through Connect was reported to the caller
		// as "connection timed out", after a handshake that had in fact
		// completed. ConnectWithCid happened to use the bare receive form and
		// so was unaffected, which is why every test in this repository
		// passed: they all use ConnectWithCid.
		//
		// The channel is buffered with room for one, and the failure path at
		// onTimeout only fires while the state is still ConnConnecting, so
		// this send cannot block and cannot be followed by another.
		if c.state.connectedCh != nil {
			c.state.connectedCh <- nil
			c.state.connectedCh = nil
		}
		c.state.stateType = ConnConnected
		c.state.RecvBuf = recvBuf
		c.state.SendBuf = sendBuf
		c.state.SentPackets = sentPacketsHolder
	}
}

func (c *connection) onData(seqNum uint16, data []byte) error {
	if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		c.logger.Trace("on data packet", "seqNum", seqNum, "data.len", len(data))
	}
	// An empty payload is not an error. libutp delivers only non-empty data
	// to the application but still advances ack_nr, so the packet is acked
	// and the sequence space moves on (utp_internal.cpp:2342-2355). The
	// receive buffer below does the same: a zero-length write advances the
	// ack number without copying anything.

	switch c.state.stateType {
	case ConnConnecting:
		if c.endpoint.Type == Acceptor {
			return errors.New("unreachable: connection should be marked established")
		} else {
			// connection being established.
			if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				c.logger.Trace("connection being established, ignore", "src.peer", c.cid.Peer, "seqNum", seqNum)
			}
			return nil
		}

	case ConnConnected:
		if c.state.closing != nil && c.state.closing.RemoteFin != nil {
			// connection is closing
			if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				c.logger.Trace("connection has received remote FIN", "src.peer", c.cid.Peer, "seqNum", seqNum)
			}
			start := c.state.RecvBuf.InitSeqNum()
			seqRange := newCircularRangeInclusive(start, *c.state.closing.RemoteFin)
			if !seqRange.Contains(seqNum) {
				c.state.stateType = ConnClosed
				c.state.Err = ErrInvalidSeqNum
				return nil
			}
		}
		// not closing should send data
		if len(data) <= c.state.RecvBuf.Available() {
			err := c.state.RecvBuf.Write(data, seqNum)
			if err != nil {
				c.logger.Warn("write data to recv buffer, but available space is not enough",
					"src.peer", c.cid.Peer, "seqNum", seqNum, "data.len", len(data))
			}
			if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				c.logger.Trace("write data to recv buffer",
					"src.peer", c.cid.Peer, "seqNum", seqNum, "data.len", len(data), "nextAckNum", c.state.RecvBuf.AckNum())
			}
			return err
		}
	default:
		// do nothing
	}
	return nil
}

func (c *connection) onFin(seqNum uint16, data []byte) error {
	switch c.state.stateType {
	case ConnConnecting, ConnClosed:
		return nil

	case ConnConnected:
		if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			c.logger.Trace("received FIN", "seq", seqNum, "c.cid.send", c.cid.Send, "c.cid.recv", c.cid.Recv)
		}
		if c.state.closing != nil {
			if c.state.closing.RemoteFin != nil {
				// If we have already received a FIN, a subsequent FIN with a different
				// sequence number is incorrect behavior
				if seqNum != *c.state.closing.RemoteFin {
					c.reset(ErrInvalidFin)
				}
			} else {
				remoteFin := seqNum
				c.state.closing.RemoteFin = &remoteFin
				return c.state.RecvBuf.Write(data, seqNum)
			}
		} else {
			// Register the FIN with the receive buffer
			if err := c.state.RecvBuf.Write(data, seqNum); err != nil {
				return err
			}
			c.state.closing = &ClosingRecord{
				LocalFin:  nil,
				RemoteFin: &seqNum,
			}
		}
		return nil
	default:
		return nil
	}
}

func (c *connection) onReset() {
	c.logger.Warn("RESET from remote")

	// If the connection is not already closed or reset, then reset the connection
	if c.state.stateType != ConnClosed {
		c.reset(ErrReset)
	}
}

func (c *connection) reset(err error) {
	c.logger.Warn("resetting connection", "err", err)

	// If we already sent our fin and got a reset we assume the receiver already got our fin
	// and has successfully closed their connection, hence mark this as a successful close.
	if c.state.stateType == ConnConnected {
		if c.state.closing != nil && c.state.closing.LocalFin != nil {
			c.state.stateType = ConnClosed
			return
		}
	}

	c.state.stateType = ConnClosed
	c.state.Err = err
}

func (c *connection) synPacket(seqNum uint16) *packet {
	nowMicros := time.Now().UnixMicro()
	return NewPacketBuilder(
		st_syn,
		c.cid.Recv,
		uint32(nowMicros),
		c.config.WindowSize,
		seqNum,
	).Build()
}

func (c *connection) statePacket() *packet {
	now := time.Now().UnixMicro()
	tsDiffMicros := uint32(c.peerTsDiff.Microseconds())

	switch c.state.stateType {
	case ConnConnecting:
		if c.endpoint.Type == Initiator {
			return nil
		}

		syn := c.endpoint.SynNum
		synAck := c.endpoint.SynAck
		return NewPacketBuilder(st_state, c.cid.Send, uint32(now), c.config.WindowSize, synAck).
			WithTsDiffMicros(tsDiffMicros).
			WithAckNum(syn).
			Build()

	case ConnConnected:
		// NOTE: Consistent with the reference implementation and the libtorrent
		// implementation, STATE packets always include the next sequence number.
		seqNum := c.state.SentPackets.NextSeqNum()
		ackNum := c.state.RecvBuf.AckNum()
		recvWindow := uint32(c.state.RecvBuf.Available())

		// No selective ack once the peer's FIN has been reached in order.
		//
		// libutp: "we never need to send EACK for connections that are
		// shutting down" (utp_internal.cpp:786-788, the `!got_fin_reached`
		// term). `eof()` is our equivalent of `got_fin_reached`. Anything
		// still pending past a reached FIN is data the peer sent after
		// declaring it had none left; naming it in a selective ack invites a
		// retransmission of data neither side will deliver.
		//
		// This is close to unreachable as the connection stands: `eof()` only
		// becomes true when the receive buffer has caught up to the FIN, and
		// the same event loop then tears the connection down (the RemoteFin
		// branch below). It is kept because it matches the reference and
		// because it becomes load-bearing the moment a half-close exists --
		// see TestConformanceDataAfterReachedFin.
		var selectiveAck *SelectiveAck
		if !c.eof() {
			selectiveAck = c.state.RecvBuf.SelectiveAck()
		}

		return NewPacketBuilder(st_state, c.cid.Send, uint32(now), recvWindow, seqNum).
			WithTsDiffMicros(tsDiffMicros).
			WithAckNum(ackNum).
			WithSelectiveAck(selectiveAck).
			Build()

	default: // ClosedState
		return nil
	}
}

func (c *connection) retransmitLostPackets(now time.Time) {
	if c.state.stateType != ConnConnected {
		return
	}
	if !c.state.SentPackets.HasLostPackets() {
		return
	}
	connID := c.cid.Send
	nowMicros := time.Now().UnixMicro()
	recvWindow := uint32(c.state.RecvBuf.Available())
	tsDiffMicros := uint32(c.peerTsDiff.Microseconds())

	for _, lostPacket := range c.state.SentPackets.TakeLostPackets() {
		seqNum := lostPacket.SeqNum
		packetType := lostPacket.PacketType
		payload := lostPacket.Data

		builder := NewPacketBuilder(packetType, connID, uint32(nowMicros), recvWindow, seqNum)
		if payload != nil {
			builder.WithPayload(payload)
		}

		packetInst := builder.
			WithTsDiffMicros(tsDiffMicros).
			WithAckNum(c.state.RecvBuf.AckNum()).
			WithSelectiveAck(c.state.RecvBuf.SelectiveAck()).
			Build()
		c.packetsRetransmitted++
		c.bytesRetransmitted += uint64(len(payload))
		c.fastRetransmits++
		if c.logger.Enabled(BASE_CONTEXT, log.LevelDebug) {
			c.logger.Debug("will retransmit lost packet",
				"packet.type", packetInst.Header.PacketType,
				"packet.seqNum", packetInst.Header.SeqNum,
				"packet.ackNum", packetInst.Header.AckNum,
				"packet.cid", packetInst.Header.ConnectionId,
				"packet.data.len", len(payload))
		}
		c.transmit(packetInst, now, true)
	}
}

// transmit sends a packet and arms its retransmission timer.
//
// firstTransmission distinguishes a packet going out for the first time from
// one being resent. Only the former can serve as an MTU probe: libutp excludes
// retransmissions because an oversized packet being resent needs to fragment
// just to get through, which is the opposite of what a probe is for
// (utp_internal.cpp:900-904).
func (c *connection) transmit(packet *packet, now time.Time, firstTransmission bool) {
	var payload []byte
	var length uint32

	if len(packet.Body) > 0 {
		payload = packet.Body
		length = uint32(len(packet.Body))
	}

	if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		c.logger.Trace("will transmit packet",
			"cid", packet.Header.ConnectionId,
			"packet.type", packet.Header.PacketType.String(),
			"packet.seqNum", packet.Header.SeqNum,
			"packet.ackNum", packet.Header.AckNum,
			"innerMap.key", packet.Header.SeqNum,
			"packet.body.len", len(packet.Body))
	}

	c.packetsSent++
	c.bytesSent += uint64(len(packet.Body))

	// Use this packet as an MTU probe if the search wants one.
	//
	// libutp decides the same thing in send_packet (utp_internal.cpp:906-925)
	// and sends the probe with fragmentation disabled. This library cannot
	// set that flag through its abstract Conn, so a probe too large for the
	// path is fragmented on IPv4 rather than dropped -- it is acknowledged,
	// the floor rises, and the search settles on a size that works but costs
	// fragmentation. On IPv6, where routers do not fragment, it is dropped
	// and the search learns correctly. See KNOWN-LIMITATIONS.md.
	if datagramSize := uint32(packet.EncodedLen()); c.mtu.eligibleProbe(datagramSize, firstTransmission) {
		c.mtu.beginProbe(packet.Header.SeqNum, datagramSize)
		if c.logger.Enabled(BASE_CONTEXT, log.LevelDebug) {
			c.logger.Debug("MTU probe", "size", datagramSize,
				"floor", c.mtu.floor, "ceiling", c.mtu.ceiling, "seq", packet.Header.SeqNum)
		}
	}

	c.state.SentPackets.OnTransmit(packet.Header.SeqNum, packet.Header.PacketType, payload, length, now)
	c.armRetransmit(packet, c.state.SentPackets.Timeout())

	c.emit(packet)
}
