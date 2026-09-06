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
		MaxConnAttempts: 3,
		MaxIdleTimeout:  DefaultMaxIdleTimeout,
		MaxPacketSize:   defaultMaxPacketSizeBytes,
		InitialTimeout:  defaultInitialTimeout,
		MinTimeout:      defaultMinTimeout,
		MaxTimeout:      defaultMaxTimeout,
		TargetDelay:     defaultTargetMicros,
		WindowSize:      DefaultWindowSize,
		BufferSize:      DefaultBufferSize,
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
	}
}

// armRetransmit schedules a retransmission timer for one packet.
func (c *connection) armRetransmit(pkt *packet, delay time.Duration) {
	seq := pkt.Header.SeqNum
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
	if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		c.logger.Trace("uTP conn starting", "dst.peer", c.cid.Peer, "cid.Send", c.cid.Send, "cid.Recv", c.cid.Recv)
	}
	// Initialize connection based on endpoint type
	if c.endpoint.Type == Initiator {
		synSeqNum := c.endpoint.SynNum
		synPkt := c.synPacket(synSeqNum)
		c.socketEvents <- newOutgoingSocketEvent(synPkt, c.cid)
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
		c.socketEvents <- newOutgoingSocketEvent(c.synState, c.cid)

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
			c.socketEvents <- newShutdownSocketEvent(c.cid)
			return c.state.Err
		}
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
				c.transmit(fin, time.Now())
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
				c.transmit(fin, time.Now())
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
	windowSize := minUint32(c.state.SentPackets.Window(), c.peerRecvWindow)
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
		maxDataSize := minUint32(windowSize, uint32(c.config.MaxPacketSize-64))
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

		c.transmit(packetInst, now)
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
			c.socketEvents <- newOutgoingSocketEvent(c.synPacket(seq), c.cid)
		}

	case ConnConnected:
		// If the timed out packet is a SYN, do nothing
		if originPacket.Header.PacketType == st_syn {
			return
		}

		// Handle timeout amplification prevention.
		//
		// This connection arms one timer per outstanding packet, so a single
		// RTO expiry delivers one callback per packet in flight. libutp has a
		// single connection-wide RTO, and counts one event per expiry. This
		// guard is what distinguishes the two, so everything that must happen
		// once per RTO -- backing off, counting, and the give-up check --
		// belongs inside it.
		var isTimeout bool
		if c.latestTimeout != nil {
			isTimeout = time.Since(*c.latestTimeout) > c.state.SentPackets.Timeout()
		} else {
			isTimeout = true
		}

		if isTimeout {
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
		}
		c.packetsRetransmitted++
		c.bytesRetransmitted += uint64(len(originPacket.Body))

		retransmissionPacket := &packet{
			Header: &PacketHeaderV1{
				PacketType:   originPacket.Header.PacketType,
				Version:      originPacket.Header.Version,
				Extension:    originPacket.Header.Extension,
				ConnectionId: originPacket.Header.ConnectionId,
				SeqNum:       originPacket.Header.SeqNum,
			},
			Body: originPacket.Body,
			Eack: nil,
		}

		recvWindow := uint32(c.state.RecvBuf.Available())
		nowMicros := time.Now().UnixMicro()
		tsDiffMicros := uint32(c.peerTsDiff.Microseconds())

		retransmissionPacket.Header.WndSize = recvWindow
		retransmissionPacket.Header.Timestamp = nowMicros
		retransmissionPacket.Header.TimestampDiff = tsDiffMicros
		retransmissionPacket.Header.AckNum = c.state.RecvBuf.AckNum()
		retransmissionPacket.Eack = c.state.RecvBuf.SelectiveAck()

		//newPacket := NewPacketBuilder(packet.Header.PacketType, packet.Header.ConnectionId, uint32(nowMicros), recvWindow, packet.Header.SeqNum).
		//	WithAckNum(c.state.RecvBuf.AckNum()).
		//	WithSelectiveAck(c.state.RecvBuf.SelectiveAck()).
		//	WithTsDiffMicros(tsDiffMicros).
		//	WithPayload(packet.Body).
		//	Build()

		c.transmit(retransmissionPacket, now)
	default:
	}
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
				c.socketEvents <- newOutgoingSocketEvent(c.synState, c.cid)
			} else {
				randSeqNum := RandomUint16()
				resetPacket := NewPacketBuilder(st_reset, packet.Header.ConnectionId, uint32(time.Now().UnixMicro()), 100_000, randSeqNum).Build()
				c.socketEvents <- newOutgoingSocketEvent(resetPacket, c.cid)
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
			c.socketEvents <- newOutgoingSocketEvent(c.synState, c.cid)
		}

	case st_data, st_fin:
		if statePacket := c.statePacket(); statePacket != nil {
			if c.logger.Enabled(BASE_CONTEXT, log.LevelDebug) {
				c.logger.Debug("create a state packet to send out",
					"packet.seqNum", statePacket.Header.SeqNum,
					"packet.ackNum", statePacket.Header.AckNum,
					"packet.cid", statePacket.Header.ConnectionId)
			}

			c.socketEvents <- newOutgoingSocketEvent(statePacket, c.cid)
		}
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
	retired := c.disarmAcked(fullAcked)
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

		close(c.state.connectedCh)
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
		c.transmit(packetInst, now)
	}
}

func (c *connection) transmit(packet *packet, now time.Time) {
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

	c.state.SentPackets.OnTransmit(packet.Header.SeqNum, packet.Header.PacketType, payload, length, now)
	c.armRetransmit(packet, c.state.SentPackets.Timeout())

	c.socketEvents <- newOutgoingSocketEvent(packet, c.cid)
}
