package utp

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

const (
	initiator endpointType = iota
	acceptor
)

const (
	connConnecting connStateType = iota
	connConnected
	connClosed
)

var (
	// ErrReset means the peer reset the connection.
	ErrReset = errors.New("reset")
	// ErrTimedOut means the connection was abandoned after repeated
	// retransmission timeouts, the handshake was not answered, or the idle
	// timeout expired.
	ErrTimedOut = errors.New("timed out")
	// ErrConnRefused means an ICMP error arrived for a connection that had
	// only sent its SYN, so nothing is listening. Otherwise an ICMP error,
	// like a received ST_RESET, produces ErrReset.
	ErrConnRefused = errors.New("connection refused")
)

type endpointType int

type endpointInfo struct {
	Type     endpointType
	SynNum   uint16
	SynAck   uint16
	Attempts int
}

type closingRecord struct {
	LocalFin  *uint16
	RemoteFin *uint16
}

type connStateType int

type connState struct {
	stateType   connStateType
	connectedCh chan error
	RecvBuf     *receiveBuffer
	SendBuf     *sendBuffer
	SentPackets *sentPackets
	closing     *closingRecord
	Err         error
}

func newConnState(connected chan error) *connState {
	return &connState{
		stateType:   connConnecting,
		connectedCh: connected,
	}
}

type connection struct {
	ctx    context.Context
	logger *ulogger
	state  *connState
	cid    *ConnectionId
	config *ConnectionConfig
	// windowCeiling, when set, replaces config.WindowSize for this
	// connection (UtpStream.SetWindowSize). config may be shared with other
	// connections, so it is not modified. Guarded by mu.
	windowCeiling  uint32
	endpoint       *endpointInfo
	peerTsDiff     time.Duration
	peerRecvWindow uint32
	socketEvents   chan *socketEvent
	// timers is the socket-wide retransmission wheel; timerScope namespaces
	// this connection's keys within it.
	timers     *retransmitTimers
	timerScope uint64
	// armed tracks which sequence numbers this connection has scheduled, so
	// acknowledging a range disarms only this connection's timers rather
	// than scanning the shared wheel. Guarded by mu.
	armed          map[uint16]struct{}
	unackTimeoutCh chan *packet
	// undoSeq and undoStamp are the packet whose loss made the controller's
	// last cut and the wire timestamp of its first resend, once undoResent
	// is set. See checkSpuriousLoss.
	undoSeq    uint16
	undoStamp  uint32
	undoResent bool
	// payloadScratch is processWrites' list of payloads composed in a pass,
	// reused between passes.
	payloadScratch [][]byte
	reads          chan *readOrWriteResult
	readable       chan struct{}
	pendingWrites  []*queuedWrite
	writable       chan struct{}
	latestTimeout  *time.Time
	synState       *packet
	// readsTerminated records that the single end-of-stream marker has been
	// handed to the reader. Readers block on c.reads until they see it.
	readsTerminated bool
	// readsTruncated records that teardown dropped received bytes the reader
	// had not been given. See readEndErr.
	readsTruncated bool
	// drainCredit is what the pass under way will hand the reader once its
	// acknowledgement has gone, counted into the window that acknowledgement
	// advertises. See afterPass and recvWindow.
	drainCredit int

	// abandoned is closed when the consumer closes the stream. It lets the
	// final drain tell a reader that is behind, whose bytes must be
	// delivered, from no reader at all, whose bytes must not hold the loop
	// open.
	abandoned <-chan struct{}

	// lastStateSent is the most recent STATE packet sent, kept for the same
	// reason as finAck: after this connection is gone the socket may still
	// need to tell the peer what we acknowledged.
	lastStateSent *packet

	// peerActivity counts packets received from the peer. UtpStream.Close
	// reads it without mu to tell a connection still flushing from one
	// whose peer has stopped answering.
	peerActivity atomic.Uint64

	// closeRequested records that the application closed the connection
	// entirely, not only its sending side. It decides whether the
	// acknowledgement of our FIN ends the connection; without it a
	// half-close would end as soon as its FIN was acknowledged.
	closeRequested bool
	// writeShut records that the application has closed the write side, so
	// nothing more will join a packet the Nagle rule is holding back, and
	// that packet is sent.
	writeShut bool

	// readShutdown records that the application has finished reading while
	// the connection continues. Payloads are no longer delivered, but the
	// acknowledgement number still advances, so the peer keeps sending into
	// an open window.
	readShutdown bool

	// armIdleRto schedules the wake-up that lets an idle connection's window
	// decay. Set by the event loop, which owns the timer.
	armIdleRto func(d time.Duration)
	// armLossProbe schedules the loss probe, or stops it for a zero
	// duration. See onLossProbe.
	armLossProbe func(d time.Duration)
	// armRack schedules RACK's reordering timer, or stops it for a zero
	// duration. See onRackTimer.
	armRack func(d time.Duration)
	// lossProbeSent is set once the probe has fired, and cleared when the
	// cumulative acknowledgement passes lossProbeAckNum: one probe per
	// episode, as RFC 8985 allows.
	lossProbeSent   bool
	lossProbeAckNum uint16
	lossProbes      uint64
	// lossProbeSeq and lossProbeAt are the packet the probe resent and when.
	// probeRecovery is set while the packets the probe's acknowledgement
	// showed lost are being resent: see probeAnswered.
	lossProbeSeq uint16
	lossProbeAt  time.Time
	// lossProbeStamp is the timestamp the probe carried on the wire, and
	// ackEcho the send timestamp of the packet that drew the acknowledgement
	// now being processed, when its sender reported a delay: see
	// probeAnswered.
	lossProbeStamp uint32
	ackEcho        uint32
	ackEchoKnown   bool
	probeRecovery  bool

	// finAck is the acknowledgement sent for the peer's FIN, kept so the
	// socket can send it again if the peer retransmits that FIN after this
	// connection has gone.
	finAck *packet

	// terminalErr carries the error that ended the stream to a reader that was
	// not handed the marker directly. Written under mu and read by the
	// stream's reader without it, so it is atomic.
	terminalErr atomic.Pointer[terminalError]
	// endErr is the error the connection itself ended with, for a writer,
	// set as the event loop returns. It differs from terminalErr, the
	// reader's, when the connection failed after the peer's whole stream
	// had arrived: the reader has had everything and ends cleanly, and a
	// writer is still told why its connection ended.
	endErr atomic.Pointer[terminalError]

	// Counters, guarded by mu.
	packetsSent          uint64
	bytesSent            uint64
	packetsRetransmitted uint64
	bytesRetransmitted   uint64
	packetsReceived      uint64
	bytesReceived        uint64
	timeouts             uint64
	fastRetransmits      uint64
	// duplicateAcks counts consecutive bare acknowledgements repeating the
	// same sequence number. See noteDuplicateAck.
	duplicateAcks uint32
	// duplicatesJudged records that the current run of duplicates has
	// already been used to judge an MTU probe. See judgeProbeFromDuplicates.
	duplicatesJudged bool
	// mtuProbesLostToDuplicateAcks counts probes the duplicate-acknowledgement
	// path judged too large.
	mtuProbesLostToDuplicateAcks uint64
	// retransmitCount counts consecutive retransmission timeouts without an
	// intervening ack.
	retransmitCount int
	// zeroWindowProbeDue is when a closed peer window should be probed. Zero
	// means the peer's window is open.
	zeroWindowProbeDue time.Time
	// lastAdvertisedWindow is the receive window on the last packet sent.
	// See onReadDrained.
	lastAdvertisedWindow uint32
	// recvBufferDrops counts data packets refused because the receive buffer
	// had no room for them. See ConnectionMetrics.RecvBufferDrops.
	recvBufferDrops uint64
	// readDrainedAcks counts acknowledgements sent because delivering bytes
	// opened a window larger than the one last advertised.
	readDrainedAcks uint64
	// ackPending records that a received packet is owed an acknowledgement.
	// flushAck sends it once the socket read that brought the packet has
	// been dispatched, or later if ackEvery lets it wait for more.
	ackPending bool
	// mu guards every field of this connection. The event loop holds it for
	// each pass, and the socket's reader holds it while handling a packet
	// for this connection inline (receiveInline, endBatch), so an
	// acknowledgement can be sent from the goroutine that read the packet.
	mu sync.Mutex
	// ready is set once the event loop has set the connection up.
	// newUtpStream holds mu from creation until then, so the reader never
	// sees it unset on a connection the socket created; a connection built
	// without an event loop declines inline packets. finished is set once
	// the loop has stopped running passes; after that only the loop's
	// teardown touches the state. Both are guarded by mu.
	ready, finished bool
	// inBatch is set while the socket's reader is partway through a read
	// batch that has delivered packets to this connection, and holds the
	// acknowledgement back until the batch is done. See endBatch.
	inBatch bool
	// wantWrite records within a pass that there may be data to send: an
	// acknowledgement opened the window, or a write moved bytes into the
	// send buffer. The pass sends it before it ends.
	wantWrite bool
	// kick wakes the event loop when a pass the reader ran inline leaves
	// something only the loop can do: tear the connection down.
	kick chan struct{}
	// loopTimers are the timers the event loop waits on. Guarded by mu.
	loopTimers loopTimers
	// barrier is the connection's clock as an idleBarrier, or nil. Resolved
	// once, in newConnection.
	barrier idleBarrier
	// Resting state; see dormant.go. dormant means the loop's goroutine has
	// gone, and is read and cleared without mu; asleep is the same under
	// mu. restClock is nil for a connection that never rests. attached is
	// set once the socket delivers this connection's packets inline
	// (UtpSocket.attachInline). wakeFn is wake, bound once.
	dormant   atomic.Bool
	asleep    bool
	restClock funcTimerClock
	restTimer Timer
	restAt    time.Time
	attached  atomic.Bool
	wakeFn    func()
	// readsHeld is set while the receive buffer holds bytes the reader's
	// full queue has no room for. Written under mu, read without it by
	// UtpStream.notifyRead.
	readsHeld atomic.Bool
	// stream is the stream this connection belongs to, for passes run off
	// the event loop. Set at setup.
	stream *UtpStream
	// lastActivity is when the connection last heard from the peer or the
	// application. The idle timer is not reset for each event: when it
	// fires it re-arms for the remainder, and the connection closes only
	// once a whole MaxIdleTimeout has passed since lastActivity.
	lastActivity time.Time
	// out sends what this connection emits. The socket sets it to write
	// directly to the wire; when nil, as for a connection built by hand,
	// emissions are queued on socketEvents.
	out func(*socketEvent)
	// outEvent is the event emitPacket hands to out, reused: out is done
	// with it before returning. Guarded by mu.
	outEvent socketEvent
	// inReadBatch is set while the socket's reader holds this connection in
	// the list for the batch it is dispatching. Used only by the reader.
	inReadBatch bool
	// dropUnacked is set while handling a packet that is discarded without
	// scheduling an acknowledgement. Cleared at the start of every packet.
	dropUnacked bool
	// clk is this connection's source of deadlines and timers. Never nil
	// after newConnection; read it through now, which tolerates a nil clk.
	clk Clock

	// clock returns the wall clock in the uint32 microseconds uTP puts on
	// the wire: ConnectionConfig.NowMicros when set, nil otherwise. Read it
	// through nowMicros. Every timestamp and one-way delay goes through that
	// accessor; scheduling uses clk.
	clock func() uint32

	// mtu is this connection's path-MTU search. See mtu.go.
	mtu *mtuSearch
	// lastSentPacket is when this connection last sent a packet, for the
	// keep-alive.
	lastSentPacket time.Time
	// ackHeldSince is when the acknowledgement now pending was first held
	// back, and armAckHold wakes the loop when it is due. dataSinceAck counts
	// the data packets it would cover. See flushAck.
	ackHeldSince time.Time
	armAckHold   func(time.Duration)
	dataSinceAck int
	// lastDataAt, lastDataGap and dataGap time arriving data packets: when
	// the last came, the gap before it, and a smoothed gap. See ackEvery.
	lastDataAt  time.Time
	lastDataGap time.Duration
	dataGap     time.Duration
	// ackPathQueuedAt is when the return path last showed a queue; the rate
	// term in ackEvery applies for ackRateMemory after it.
	ackPathQueuedAt time.Time
	// ackQueueSamples holds the return path's queuing delay, sampled every
	// ackQueueSampleEvery (the next at ackQueueNext), and ackSelfQueuedAt
	// is when it was last seen climbing as this receiver's own
	// acknowledgements make it climb. See ackPathSelfQueued.
	ackQueueSampledAt time.Time
	ackQueueSamples   [2*ackQueueSpan + ackQueueWindow]time.Duration
	ackQueueNext      int
	ackSelfQueuedAt   time.Time
	// armProbeTimer wakes the event loop when a closed window has gone
	// unprobed long enough. Installed by the event loop, which owns the
	// timer.
	armProbeTimer func(time.Duration)
	// probingZeroWindow lets one packet through a window the peer has
	// closed.
	probingZeroWindow bool

	// rtoDeadline is when this connection's retransmission timeout expires.
	// It is armed when the first packet enters an empty window, reset on
	// every acknowledgement of new data, and checked before a timeout is
	// declared. Timers are armed per outstanding packet; this deadline makes
	// their expiries a single timeout, never early.
	rtoDeadline time.Time

	// fastTimeout is set by a retransmission timeout. While set, each
	// acknowledgement that leaves the oldest outstanding packet next in line
	// resends that packet at once, so packets declared lost by a timeout
	// return one per round trip rather than all together. See
	// onFastTimeout.
	fastTimeout bool

	// synTimeout is the current retransmission timeout for the SYN, doubled
	// on each attempt.
	synTimeout time.Duration
	// synSentAt is when the SYN first went out, for the RTT sample the
	// SYN-ACK gives. See onState.
	synSentAt     time.Time
	lastMetricsAt time.Time
}

func newConnection(
	ctx context.Context,
	logger *ulogger,
	cid *ConnectionId,
	config *ConnectionConfig,
	syn *packet,
	connected chan error,
	socketEvents chan *socketEvent,
	reads chan *readOrWriteResult,
	abandoned <-chan struct{},
	timers *retransmitTimers,
) *connection {
	var clock func() uint32
	clk := RealClock
	if config != nil {
		clock = config.NowMicros
		if config.Clock != nil {
			clk = config.Clock
		}
	}

	var endpoint *endpointInfo
	var peerTsDiff time.Duration
	var peerRecvWindow uint32

	if syn != nil {
		synAck := randomUint16()
		endpoint = &endpointInfo{
			Type:   acceptor,
			SynNum: syn.Header.SeqNum,
			SynAck: synAck,
		}

		// No delay is measured from the SYN, so the SYN-ACK echoes a timestamp
		// difference of zero, which means "no sample yet".
		peerTsDiff = 0
		peerRecvWindow = syn.Header.WndSize
	} else {
		synNum := randomUint16()
		endpoint = &endpointInfo{
			Type:     initiator,
			SynNum:   synNum,
			Attempts: 0,
		}
		peerTsDiff = 0
		peerRecvWindow = math.MaxUint32
	}

	unackTimeoutCh := make(chan *packet, unackTimeoutQueueLen)

	c := &connection{
		ctx:            ctx,
		logger:         logger,
		clk:            clk,
		clock:          clock,
		state:          newConnState(connected),
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
		kick:           make(chan struct{}, 1),
		reads:          reads,
		abandoned:      abandoned,
		readable:       make(chan struct{}, 3),
		pendingWrites:  make([]*queuedWrite, 0),
		writable:       make(chan struct{}, 3),
		latestTimeout:  nil,
		// The ceiling is the largest packet this connection will try, and the
		// first it sends; a lost probe brings it down. See newMtuSearch.
		mtu: newMtuSearch(uint32(config.MaxPacketSize), clk.Now()),
	}
	c.barrier, _ = c.timeSource().(idleBarrier)
	c.wakeFn = c.wake
	return c
}

// sampleMetrics hands a snapshot to the configured observer, at most once
// per MetricsInterval. It runs with mu held, on the event loop or on the
// socket's reader in endBatch.
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
		LossProbes:           c.lossProbes,
		RackLosses:           sentRack(c).losses,
		RackRelosts:          sentRack(c).relosts,
		PendingWrites:        len(c.pendingWrites),
		MtuCurrent:           c.mtu.current,
		MtuFloor:             c.mtu.floor,
		MtuCeiling:           c.mtu.ceiling,

		MtuProbesLostToDuplicateAcks: c.mtuProbesLostToDuplicateAcks,

		WindowReopenedAcks: c.readDrainedAcks,
		State:              connStateName(c.state.stateType),
	}
	if c.state.SentPackets != nil {
		cs := c.state.SentPackets.controllerStats()
		m.CwndBytes = cs.MaxWindowSizeBytes
		m.InFlightBytes = cs.WindowSizeBytes
		m.MinCwndBytes = cs.MinWindowSizeBytes
		m.RTT = cs.RTT
		m.FineRTT = cs.FineRTT
		m.RTTVarianceMicros = cs.RTTVarianceMicros
		m.Timeout = cs.Timeout
		m.BaseDelay = cs.BaseDelay
		m.CurrentDelay = cs.CurrentDelay
		m.ClockSkewCorrection = cs.ClockSkewCorrection
		m.ClockDrift = cs.ClockDrift
		m.ClockDriftPenalty = cs.ClockDriftPenalty
		m.TargetDelayMicros = cs.TargetDelayMicros
		m.SlowStart = cs.SlowStart
		m.AppLimitedSince = cs.AppLimitedSince
	}
	if c.state.SendBuf != nil {
		m.SendBufferPending = c.state.SendBuf.Pending()
	}
	if c.state.RecvBuf != nil {
		m.RecvBufferPending = c.state.RecvBuf.Pending()
		m.RecvBufferReadable = c.state.RecvBuf.Readable()
		m.RecvBufferDrops = c.recvBufferDrops
	}
	c.config.Metrics(m)
}

// now returns this connection's current time, or time.Now for a connection
// built as a struct literal without a clock.
func (c *connection) now() time.Time {
	if c.clk != nil {
		return c.clk.Now()
	}
	return time.Now()
}

// timeSource returns this connection's Clock, never nil.
func (c *connection) timeSource() Clock {
	if c.clk != nil {
		return c.clk
	}
	return RealClock
}

// nowMicros reads this connection's wall clock in the uint32 microseconds uTP
// puts on the wire, falling back to nowMicro when none is configured.
func (c *connection) nowMicros() uint32 {
	if c.clock != nil {
		return c.clock()
	}
	return nowMicro()
}
