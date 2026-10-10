package utp_go

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"sync/atomic"
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

// DefaultMaxIdleTimeout is zero: no idle timeout, as in libutp, which never
// closes a connection for silence. See ConnectionConfig.MaxIdleTimeout.
const DefaultMaxIdleTimeout = time.Duration(0)
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
	// ErrConnRefused is libutp's UTP_ECONNREFUSED: an ICMP error arrived for
	// a connection that had only sent its SYN, so nothing is listening.
	//
	//	const int err = (conn->state == CS_SYN_SENT) ? UTP_ECONNREFUSED
	//	                                             : UTP_ECONNRESET;
	//	                                       (utp_internal.cpp:3122)
	//
	// The other half of that expression is ErrReset, which is also what a
	// received ST_RESET produces -- libutp reports both as UTP_ECONNRESET,
	// so they are deliberately not distinguished here either.
	ErrConnRefused = errors.New("connection refused")
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
	// microseconds uTP puts on the wire. Defaults to NowMicro.
	//
	// It governs only what is *stamped and measured* -- the timestamp field,
	// the timestamp difference echoed back, and the one-way delays derived
	// from them. Scheduling still runs on real timers: this is not a virtual
	// clock, and a connection given a frozen NowMicros still retransmits on
	// time.
	//
	// It exists because libutp's driver reads a virtual clock where this
	// library read the real one, so the conformance corpus could not compare
	// the two timestamp fields at all and listed both as tolerated
	// differences. Pinning this to the driver's clock removes the tolerance;
	// see conformance_harness_test.go.
	NowMicros func() uint32

	// DelayWindow is how far back the congestion controller looks for its
	// base delay -- the lowest one-way delay it has seen, which it treats as
	// the path with an empty queue.
	//
	// libutp's equivalent is a compile-time thirteen one-minute buckets
	// (DELAY_BASE_HISTORY, utp_internal.cpp:50), so about thirteen minutes.
	// This is a sliding-window minimum over two.
	//
	// Configurable because the window is what bounds the damage clock drift
	// does: a sender whose clock loses time sees its measured delay grow
	// without bound, and the base only follows once the inflated samples have
	// aged out. The steady-state error is the window multiplied by the drift
	// rate, which is a relationship nothing could test while the window was a
	// constant two minutes. See netem.TestClockDriftInflatesTheDelaySignal.
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
	// NoDelay sends a partial packet as soon as the window allows, as
	// TCP_NODELAY does. By default a connection follows libutp's Nagle rule
	// (flush_packets, utp_internal.cpp:974-982): a packet smaller than a
	// full one waits while anything else is unacknowledged, and later writes
	// join it, until it fills, everything before it is acknowledged, or the
	// write side closes.
	//
	// Measured against libutp, 2,000 messages of 100 bytes written every
	// 0.5 ms over a 20 ms round trip: with the rule, both send them in about
	// 140 packets; with NoDelay, 2,000 packets, and each message arrives
	// sooner -- a median of 10.9 ms against 15.3 ms, a 99th percentile of
	// 19.9 ms against 44.8 ms.
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

type queuedWrite struct {
	data     []byte
	written  int
	resultCh chan *readOrWriteResult
}

type readOrWriteResult struct {
	Err  error
	Len  int
	Data []byte
	// pooled marks a chunk of received data from readChunks, which the
	// reader returns once it has copied it out. See newReadChunk.
	pooled bool
}

// readChunks holds the chunks received data is handed to the reader in.
//
// Each chunk was a new buffer and a new result: two allocations for every
// packet's worth of data read. The reader copies a chunk out and is done with
// it, so it comes back here.
var readChunks sync.Pool

// pooledReadChunkMin is the least a chunk must hold to come from readChunks.
// A pooled buffer is as large as the largest packet, and a reader that falls
// behind leaves up to a read queue's worth of chunks waiting: small ones are
// sized to their data, so that a trickle of a few bytes at a time does not
// hold a full packet's memory for each.
const pooledReadChunkMin = 512

// newReadChunk is a result holding n bytes of received data, for the reader.
// size is the most any chunk on this connection holds.
func newReadChunk(n, size int) *readOrWriteResult {
	if n >= pooledReadChunkMin {
		if r, _ := readChunks.Get().(*readOrWriteResult); r != nil && cap(r.Data) >= n {
			r.Data, r.Len = r.Data[:n], n
			return r
		}
		return &readOrWriteResult{Data: make([]byte, n, max(n, size)), Len: n, pooled: true}
	}
	return &readOrWriteResult{Data: make([]byte, n), Len: n}
}

// release returns a chunk the reader has finished with to readChunks.
func (r *readOrWriteResult) release() {
	if r.pooled {
		r.Data, r.Len, r.Err = r.Data[:0], 0, nil
		readChunks.Put(r)
	}
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
	// connection on the socket. Guarded by mu.
	armed          map[uint16]struct{}
	unackTimeoutCh chan *packet
	// undoSeq and undoStamp are the packet whose loss made the controller's
	// last cut and the wire timestamp of its first resend, once undoResent
	// is set. See checkSpuriousLoss.
	undoSeq    uint16
	undoStamp  uint32
	undoResent bool
	// payloadScratch is processWrites' list of payloads composed in a pass,
	// kept between passes: growing a new one cost an allocation or two on
	// every pass that sent anything.
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

	// abandoned is closed when the consumer closes the stream. It is the one
	// thing that distinguishes "the reader is behind" from "there is no reader
	// any more", which the final drain has to know: bytes owed to a reader
	// must be delivered, and bytes owed to nobody must not hold the loop open.
	abandoned <-chan struct{}

	// lastStateSent is the most recent STATE packet emitted, kept for the same
	// reason as finAck: after this connection is gone the socket may still
	// need to tell a peer what we acknowledged.
	lastStateSent *packet

	// peerActivity counts packets received from the peer. It exists for
	// UtpStream.Close, which needs to tell a connection that is still
	// flushing from one whose peer has stopped answering, and is read from
	// without mu -- hence the atomic.
	peerActivity atomic.Uint64

	// closeRequested records that the application asked to close the
	// connection entirely, rather than only its sending side.
	//
	// libutp's `close_requested` (utp_internal.cpp:441), set by utp_close
	// (:3374) and not by utp_shutdown. It is what decides whether the
	// acknowledgement of our own FIN ends the connection: "else if (conn->
	// fin_sent && conn->cur_window_packets == acks) { fin_sent_acked = true;
	// if (conn->close_requested) conn->state = CS_DESTROY; }" (:2178-2182).
	// Without it a half-close would end the moment its FIN came back, which is
	// the opposite of the point.
	closeRequested bool
	// writeShut records that the application has closed the write side, so
	// nothing more will join a packet the Nagle rule is holding back. libutp
	// queues its FIN behind the held packet, which is then no longer the last
	// and goes out (utp_close, utp_internal.cpp:3232-3247; flush_packets).
	writeShut bool

	// readShutdown records that the application has finished reading, while
	// the connection carries on.
	//
	// libutp's `read_shutdown` (utp_internal.cpp:439), set by
	// utp_shutdown(SHUT_RD) and by utp_close. Its whole effect is that a
	// payload is not handed upwards -- the acknowledgement number advances
	// either way (:2344-2355, :2392-2395), so the peer keeps sending at full
	// rate into a window that never closes.
	readShutdown bool

	// armIdleRto schedules the wake-up that lets an idle connection's window
	// decay. Set by the event loop, which owns the timer.
	armIdleRto func(d time.Duration)
	// armLossProbe schedules the loss probe; lossProbeBackoff is how many
	// times it has fired since the last acknowledgement. See onLossProbe.
	armLossProbe func(d time.Duration)
	// lossProbeSent is set once the probe has fired, and cleared only when
	// the cumulative acknowledgement moves past lossProbeAckNum: one probe
	// per episode, as RFC 8985 allows.
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
	// same sequence number. libutp's conn->duplicate_ack; see
	// noteDuplicateAck.
	duplicateAcks uint32
	// duplicatesJudged records that the current run of duplicates has
	// already been used to judge an MTU probe. See judgeProbeFromDuplicates.
	duplicatesJudged bool
	// mtuProbesLostToDuplicateAcks counts probes the duplicate-acknowledgement
	// path concluded were too big. Exposed so a test can assert the mechanism
	// ran rather than inferring it from where the search happened to settle.
	mtuProbesLostToDuplicateAcks uint64
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
	// lastAdvertisedWindow is the receive window on the last packet this
	// connection put on the wire. libutp's last_rcv_win. See onReadDrained.
	lastAdvertisedWindow uint32
	// recvBufferDrops counts data packets refused because the receive buffer
	// had no room for them. See ConnectionMetrics.RecvBufferDrops.
	recvBufferDrops uint64
	// readDrainedAcks counts the acknowledgements owed because handing bytes
	// up reopened a window narrower than what is now free. Exposed through
	// ConnectionMetrics so a test can assert the mechanism ran rather than
	// inferring it from timing, which many other things also move.
	readDrainedAcks uint64
	// ackPending records that a received packet is owed an acknowledgement,
	// which flushAck sends once the socket read that brought it has been
	// handed out, or later if ackEvery lets it wait for more packets.
	// libutp's schedule_ack / utp_issue_deferred_acks.
	ackPending bool
	// mu guards every field of this connection. The event loop holds it for
	// each pass, and the socket's reader holds it while it handles a packet
	// for this connection inline (receiveInline, endBatch), which is what
	// lets an acknowledgement leave from the goroutine that read the packet
	// -- libutp's model, where the embedder's read loop runs the protocol.
	mu sync.Mutex
	// ready is set once the event loop has finished setting the connection
	// up. NewUtpStream holds mu from creation until then, so the reader never
	// sees it unset on a connection the socket made; a connection built by
	// hand, without an event loop, declines inline packets. finished is set
	// once the loop has stopped running passes; from then on nothing touches
	// the state but the loop's own teardown. Both under mu.
	ready, finished bool
	// inBatch is set while the socket's reader is part way through a read
	// batch that has delivered packets to this connection, and holds the
	// acknowledgement back until the batch is done: libutp's embedder reads
	// until the socket would block and then calls utp_issue_deferred_acks
	// (utp.h:512-517). See endBatch.
	inBatch bool
	// wantWrite records, within a pass, that there may be data to send:
	// an acknowledgement opened the window, or a write moved bytes into the
	// send buffer. The pass sends it before it ends. These used to be
	// signals the loop sent itself on writable, for the next pass.
	wantWrite bool
	// kick wakes the event loop when a pass the reader ran inline leaves
	// something only the loop can do: tear the connection down.
	kick chan struct{}
	// stream is the stream this connection belongs to, for passes run off
	// the event loop. Set at setup.
	stream *UtpStream
	// lastActivity is when the connection last heard from the peer or the
	// application. The idle timer is not moved for each: when it fires, it
	// re-arms for what is left, and the connection closes only once a whole
	// MaxIdleTimeout has passed since this. Resetting it on every packet cost
	// a timer operation per packet on the reader's path, which libutp, with
	// no timer of its own per connection, does not pay.
	lastActivity time.Time
	// out sends what this connection emits. The socket sets it to write
	// straight to the wire; when it is nil, as in unit tests that build a
	// connection by hand, emissions are queued on socketEvents instead.
	out func(*socketEvent)
	// outEvent is what emitPacket hands out, reused: out finishes with it
	// before returning. Guarded by mu, as every emission is.
	outEvent socketEvent
	// inReadBatch is set while the socket's reader holds this connection in
	// its list for the read batch it is dispatching. The reader's alone.
	inReadBatch bool
	// dropUnacked is set while a packet is being handled when libutp would
	// discard it without scheduling an acknowledgement -- its early `return
	// 0`s in utp_process_incoming (utp_internal.cpp:2381-2386, :2425-2431).
	// Cleared at the start of every packet.
	dropUnacked bool
	// clk is this connection's time source: deadlines and timers. Never nil
	// after newConnection; read through c.now() for the nil-safe path that
	// struct-literal tests need.
	clk Clock

	// clock reads the wall clock in the uint32 microseconds uTP puts on the
	// wire. ConnectionConfig.NowMicros when set, and nil otherwise -- read it
	// through nowMicros, never directly.
	//
	// Every timestamp this connection stamps and every one-way delay it
	// derives goes through that accessor, so a test can pin it. Scheduling
	// does not: see the note on the config field.
	clock func() uint32

	// mtu is this connection's path-MTU search. See mtu.go.
	mtu *mtuSearch
	// lastSentPacket is when this connection last put a packet on the wire.
	// libutp's `last_sent_packet`, which its keep-alive compares against
	// (utp_internal.cpp:1272).
	lastSentPacket time.Time
	// ackHeldSince is when the acknowledgement now pending was first held
	// back, and armAckHold wakes the loop when it is due. dataSinceAck counts
	// the data packets it would cover. See flushAck.
	ackHeldSince time.Time
	armAckHold   func(time.Duration)
	dataSinceAck int
	// lastDataAt, lastDataGap and dataGap time the data packets arriving:
	// when the last came, the gap before it, and a smoothed gap. See
	// ackEvery.
	lastDataAt  time.Time
	lastDataGap time.Duration
	dataGap     time.Duration
	// ackPathQueuedAt is when the way back last showed a queue; the rate
	// term in ackEvery applies for ackRateMemory after it.
	ackPathQueuedAt time.Time
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

	// fastTimeout is libutp's fast_timeout (utp_internal.cpp:444): set by a
	// retransmission timeout (:1247), it makes each acknowledgement that
	// leaves the oldest outstanding packet next in line for a fast resend
	// send that packet again at once (:2256-2282). It is how the packets a
	// timeout gave up as lost come back one per round trip, rather than all
	// at the timeout. See onFastTimeout.
	fastTimeout bool

	// synTimeout is the current retransmission timeout for the SYN, doubled
	// on each attempt. libutp calls this retransmit_timeout.
	synTimeout time.Duration
	// synSentAt is when the SYN first went out, for the RTT sample the
	// SYN-ACK gives. See onState.
	synSentAt     time.Time
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

		// No delay is measured from the SYN, and the SYN-ACK therefore
		// echoes a timestamp difference of zero.
		//
		// libutp processes an incoming SYN with `utp_process_incoming(conn,
		// packet, len, /*syn=*/true)`, and that call returns at
		//
		//	if (syn) {
		//	    return 0;
		//	}
		//
		// before ever reaching `conn->reply_micro = their_delay`
		// (utp_internal.cpp:2002). So reply_micro keeps the zero it was
		// initialised with (:2617) until a non-SYN packet arrives, and zero
		// is a defined signal rather than a measurement: "if the actual delay
		// is 0, it means the other end hasn't received a sample from us yet".
		//
		// This used to measure the SYN and echo it, which gave the initiator
		// a delay sample a round trip earlier than the reference does -- and
		// one derived from a single packet, carrying whatever offset stands
		// between two clocks that have exchanged nothing yet. The corpus
		// could not see it: the timestamp fields were excluded from
		// comparison until this connection's clock became injectable, which
		// is what that exclusion was hiding.
		peerTsDiff = 0
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
		clk:            clk,
		clock:          clock,
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
		kick:           make(chan struct{}, 1),
		reads:          reads,
		abandoned:      abandoned,
		readable:       make(chan struct{}, 3),
		pendingWrites:  make([]*queuedWrite, 0),
		writable:       make(chan struct{}, 3),
		latestTimeout:  nil,
		// The ceiling is the largest packet this library will ever try, and
		// the first it sends, as libutp does; a lost probe brings it down.
		// See newMtuSearch.
		mtu: newMtuSearch(uint32(config.MaxPacketSize), clk.Now()),
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
		c.rtoDeadline = c.now().Add(delay)
		c.rearmLossProbe(c.lossProbeAckNum)
	}
	c.armed[seq] = struct{}{}
	c.timers.arm(
		retransmitKey{scope: c.timerScope, seq: seq},
		retransmitTimer{packet: pkt, deliver: c.unackTimeoutCh, ctx: c.ctx},
		delay,
	)
}

// isArmed reports whether seq has a retransmission timer.
func (c *connection) isArmed(seq uint16) bool {
	_, ok := c.armed[seq]
	return ok
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
	// Report the end of the stream to the reader on every exit.
	//
	// Nothing else sends on c.reads by now -- the passes the socket's reader
	// runs stop once finished is set, before teardown -- so closing it here is
	// safe, and it is what lets the loop refuse to block: the end-of-stream
	// marker is best-effort, and a reader that never got it -- because its
	// queue was full when the connection ended -- learns from the close
	// instead, with the reason available in terminalErr.
	//
	// Without this a reader draining a full queue after the loop had gone
	// would wait for a marker that no one was left to send.
	defer func() {
		if c.terminalErr.Load() == nil {
			c.terminalErr.Store(&terminalError{err: c.readEndErr()})
		}
		close(c.reads)
	}()
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
	// mu is held for setup, from NewUtpStream, and for every pass; it is
	// released only while the loop waits. See connection.mu.
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
		c.synSentAt = c.now()

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

	// No idle timeout unless one is configured: the timer is made stopped,
	// as the probe timer below is, and never re-armed.
	var idleTimer Timer
	if c.config.MaxIdleTimeout > 0 {
		idleTimer = c.timeSource().NewTimer(c.config.MaxIdleTimeout)
	} else if idleTimer = c.timeSource().NewTimer(time.Hour); !idleTimer.Stop() {
		<-idleTimer.C()
	}
	c.lastActivity = c.now()
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
	// idle timeout eventually kills it. libutp checks
	// `current_ms - last_sent_packet >= KEEPALIVE_INTERVAL` on every timeout
	// pass (utp_internal.cpp:1271-1274), so its keep-alive leaves one
	// interval after the last packet, to within the 500ms libutp allows
	// between passes however often its embedder calls it
	// (TIMEOUT_CHECK_INTERVAL, :37, :3284).
	//
	// This is a timer aimed at that instant, re-aimed from lastSentPacket
	// every time it fires. It used to be a ticker at the interval, counted
	// from connection start, which checked the same condition but only on
	// the tick: a connection that went quiet just after one failed the check
	// at the next and waited for the one after -- up to twice the interval,
	// 58 seconds where libutp's is 29 and a NAT mapping commonly lasts 30.
	// Measured: 58.21s against 29.70s. See KNOWN-LIMITATIONS.md, section 2e.
	keepAliveTimer := c.timeSource().NewTimer(c.keepAliveIntervalOrDefault())
	defer keepAliveTimer.Stop()
	onKeepAliveTimer := func() {
		interval := c.keepAliveIntervalOrDefault()
		now := c.now()
		// libutp: `if (state >= CS_CONNECTED && !fin_sent)` and the
		// connection has been silent for the interval
		// (utp_internal.cpp:1271-1274).
		if c.state.stateType == ConnConnected &&
			(c.state.closing == nil || c.state.closing.LocalFin == nil) &&
			now.Sub(c.lastSentPacket) >= interval {
			c.emit(c.keepAlivePacket())
		}
		// Aim at the moment the silence will reach the interval. Anything
		// sent since the timer was armed has moved that later; nothing sent,
		// or nothing sendable in this state, means one interval from now.
		next := c.lastSentPacket.Add(interval).Sub(now)
		if next <= 0 {
			next = interval
		}
		keepAliveTimer.Reset(next)
	}

	probeTimer := c.timeSource().NewTimer(time.Hour)
	if !probeTimer.Stop() {
		<-probeTimer.C()
	}
	defer probeTimer.Stop()
	ackHoldTimer := c.timeSource().NewTimer(time.Hour)
	if !ackHoldTimer.Stop() {
		<-ackHoldTimer.C()
	}
	defer ackHoldTimer.Stop()
	c.armAckHold = func(d time.Duration) {
		if !ackHoldTimer.Stop() {
			select {
			case <-ackHoldTimer.C():
			default:
			}
		}
		ackHoldTimer.Reset(d)
	}
	c.armProbeTimer = func(d time.Duration) {
		if !probeTimer.Stop() {
			select {
			case <-probeTimer.C():
			default:
			}
		}
		probeTimer.Reset(d)
	}
	// An idle connection's window has to decay, and nothing else here would
	// wake the loop to do it.
	//
	// Retransmission timers are armed per packet, so a connection with
	// nothing outstanding has none, and its event loop is completely
	// quiescent -- it does not even produce a metrics sample. libutp is not
	// built that way: it keeps one deadline per socket, never clears it when
	// the window empties (it is set at utp_internal.cpp:997, reset on each
	// acknowledged packet at :1389, re-armed on each expiry at :1204), and
	// when it passes with nothing in flight the idle branch decays the window
	// by a third (:1216-1222). Because `retransmit_count` is only incremented
	// when something *is* outstanding (:1240), that repeats without ever
	// killing the connection.
	//
	// Measured against real libutp over the same emulated link, 8 seconds
	// idle after a bulk transfer: libutp's first flight afterwards was 15972
	// bytes against 55176 with no idle -- 29%, which is (2/3)^3, three decays
	// at one, three and seven RTOs. Ours was unchanged at 55375, so it would
	// have put a window it last measured 8 seconds ago straight back onto a
	// path it has not probed since. See netem.TestLibutpIdleWindowDecay.
	idleRtoTimer := c.timeSource().NewTimer(time.Hour)
	if !idleRtoTimer.Stop() {
		<-idleRtoTimer.C()
	}
	defer idleRtoTimer.Stop()
	c.armIdleRto = func(d time.Duration) {
		if !idleRtoTimer.Stop() {
			select {
			case <-idleRtoTimer.C():
			default:
			}
		}
		idleRtoTimer.Reset(d)
	}

	lossProbeTimer := c.timeSource().NewTimer(time.Hour)
	if !lossProbeTimer.Stop() {
		<-lossProbeTimer.C()
	}
	defer lossProbeTimer.Stop()
	c.armLossProbe = func(d time.Duration) {
		if !lossProbeTimer.Stop() {
			select {
			case <-lossProbeTimer.C():
			default:
			}
		}
		lossProbeTimer.Reset(d)
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
			now := c.now()
			c.lastActivity = now
			c.onPacket(event.Packet, now)
		} else if event.Type == streamShutdown {
			stream.shutdown.Store(true)
		} else if event.Type == streamICMP {
			c.onICMP(event.ICMP, c.now())
		} else if event.Type == streamCloseRead {
			c.onCloseRead()
		}
		// streamCloseWrite needs no handling beyond having woken the loop:
		// the flag it refers to is already set, and the pass this event ends
		// will see it and send the FIN.
	}

	handleWrites := func(write *queuedWrite, ok bool) {
		if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			c.logger.Trace("get queued write from writes", "dst.peer", c.cid.Peer, "content", len(write.data))
		}
		c.lastActivity = c.now()
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
		c.onTimeout(timeoutPkt, c.now())
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
		// The socket was closed under a connection still running: that is
		// why it ended, and what its reader and writers are told. A stream
		// whose own context was cancelled -- an abandoned dial -- keeps
		// whatever it had.
		if c.state.Err == nil && c.state.stateType != ConnClosed &&
			stream.socketCtx != nil && stream.socketCtx.Err() != nil {
			c.state.Err = ErrSocketClosed
		}
	}

	// A virtual clock needs to know when this loop has finished reacting, so
	// that it can move time without racing the reaction. Resolved once here
	// rather than per pass; nil for the real clock. See IdleBarrier.
	barrier, _ := c.timeSource().(IdleBarrier)
	if barrier != nil {
		barrier.Register()
		// A connection that ends -- reset by its peer, timed out, closed --
		// can never park again, and a virtual clock waiting for it would
		// stop. Found by the initiator corpus: a peer that answers a SYN
		// with a RESET tore the connection down and hung the clock.
		defer func() {
			// Wakes queued for this loop that it will never take: each was
			// noted as a handoff, and a clock still counting one in flight
			// never moves again. A loop ended by a RESET the reader
			// processed inline left the reader's kick behind, and a test
			// waiting on the clock waited for ever.
			for queued := true; queued; {
				select {
				case <-c.kick:
					barrier.TakeHandoff()
				case <-c.unackTimeoutCh:
					barrier.TakeHandoff()
				default:
					queued = false
				}
			}
			if u, ok := barrier.(interface{ Unregister() }); ok {
				u.Unregister()
			}
		}()
	}

	// takeIncoming takes the handoff the socket noted when it queued an
	// inbound packet. Only streamIncoming is noted -- see handleIncomingBuf --
	// so only streamIncoming is taken. Every receive from streamEvents calls
	// it: the non-blocking ones at the top of the loop took the packet
	// without it, and left a virtual clock waiting for ever.
	takeIncoming := func(event *streamEvent) {
		if barrier != nil && event != nil && event.Type == streamIncoming {
			barrier.TakeHandoff()
		}
	}

	var maxStreamEventLen int
	// Declared outside the loop so the fast-path `goto afterSelect` above does
	// not jump over them. Reset on every pass by the select that assigns them.
	var (
		woke       wakeKind
		markedIdle bool
		wokeEvent  *streamEvent
		wokeWrite  *queuedWrite
		wokeOK     bool
		wokeTimer  *packet
	)
	c.stream = stream
	c.ready = true
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
			takeIncoming(event)
			handleIncoming(event)
			// Take whatever else has already arrived before answering, so one
			// acknowledgement covers it rather than one per packet. flushAck
			// also waits for the rest of the socket read the packets came in
			// (see there).
			//
			// Bounded so that a peer sending continuously cannot keep this
			// inner loop fed and starve writes and timers; the outer loop
			// returns here immediately anyway if more are waiting.
			for drained := 0; drained < maxAckCoalesce; drained++ {
				select {
				case more := <-stream.streamEvents:
					takeIncoming(more)
					handleIncoming(more)
				default:
					drained = maxAckCoalesce
				}
			}
			goto afterSelect
		default:
		}
		// Tell a virtual clock this loop is about to block, so it knows the
		// reaction to the last instant is complete and time may move. A real
		// clock has no barrier and this is nil. See IdleBarrier.
		//
		// Not while a wake this loop queued for itself is still waiting: an
		// incoming packet that reopens the peer's window signals writable, and
		// the data goes out on the next pass. Marked idle with that signal
		// buffered, the select returned at once, but in between the clock saw
		// every participant parked and nothing in flight, called the system
		// quiet, and the packet went out after the step that should have
		// seen it -- TestInitiatorZeroWindow, about once in 160 runs. Only
		// this goroutine queues those wakes before reaching here, so their
		// length is exact; a wake from another goroutine is that goroutine's
		// to account for.
		markedIdle = false
		if barrier != nil && len(c.writable) == 0 && len(c.readable) == 0 && len(c.kick) == 0 {
			barrier.MarkIdle()
			markedIdle = true
		}
		// The socket's reader may handle a packet for this connection while
		// the loop waits. See receiveInline.
		c.mu.Unlock()
		// The select below only *receives*; every case body runs after it,
		// through the switch. That shape is what lets a virtual clock be told
		// the loop is running again before any of those bodies can emit a
		// packet -- one MarkBusy, on the one path out, rather than one per
		// case where missing a single one would put the nondeterminism
		// straight back. See IdleBarrier.
		select {
		case wokeEvent = <-stream.streamEvents:
			woke = wakeStreamEvent
		case wokeWrite, wokeOK = <-stream.writes:
			woke = wakeWrite
		case <-c.readable:
			woke = wakeReadable
		case <-c.writable:
			woke = wakeWritable
		case wokeTimer = <-c.unackTimeoutCh:
			woke = wakeRetransmit
		case <-keepAliveTimer.C():
			woke = wakeKeepAlive
		case <-probeTimer.C():
			woke = wakeProbe
		case <-ackHoldTimer.C():
			woke = wakeAckHold
		case <-idleRtoTimer.C():
			woke = wakeIdleRto
		case <-lossProbeTimer.C():
			woke = wakeLossProbe
		case <-idleTimer.C():
			woke = wakeIdleTimeout
		case <-c.kick:
			woke = wakeKick
		case <-c.ctx.Done():
			woke = wakeCtxDone
		}
		c.mu.Lock()
		if markedIdle {
			barrier.MarkBusy()
		}
		switch woke {
		case wakeStreamEvent:
			takeIncoming(wokeEvent)
			handleIncoming(wokeEvent)
		case wakeWrite:
			handleWrites(wokeWrite, wokeOK)
		case wakeReadable:
			// Nothing to do but wake: the drain happens in afterSelect, for
			// every pass, so that it precedes the acknowledgement.
			//
			// The wake itself is the point. UtpStream.notifyRead signals this
			// when the application takes a chunk, which is the only thing
			// that frees room once the read queue has filled -- without it
			// processReads gives up and waits for a packet that a blocked
			// peer will not send.
		case wakeWritable:
			c.processWrites(c.now())
		case wakeRetransmit:
			// This is the handoff the wheel noted when it queued the timeout.
			if barrier != nil {
				barrier.TakeHandoff()
			}
			handleTimeout(wokeTimer)
		case wakeKeepAlive:
			onKeepAliveTimer()
		case wakeAckHold:
			// Nothing to do but wake: a held acknowledgement goes out from
			// flushAck in afterSelect.
		case wakeKick:
			// The handoff endBatch noted when it kicked. What it wants done
			// happens in afterSelect.
			if barrier != nil {
				barrier.TakeHandoff()
			}
		case wakeProbe:
			// The peer's window has been closed for a whole interval. Let one
			// packet through, so its acknowledgement carries a fresh window.
			c.processWrites(c.now())
		case wakeIdleRto:
			c.onIdleRto(c.now())
		case wakeLossProbe:
			c.onLossProbe(c.now())
		case wakeIdleTimeout:
			// Activity since the timer was set moves the deadline on: arm
			// for what is left of it. See lastActivity.
			if left := c.config.MaxIdleTimeout - c.now().Sub(c.lastActivity); left > 0 {
				idleTimer.Reset(left)
			} else {
				handleIdleTimeout()
			}
		case wakeCtxDone:
			handleCtxDone()
			if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				c.logger.Trace("stream context done, will force stop...", "c.cid.peer", c.cid.Peer, "c.cid.Send", c.cid.Send, "c.cid.Recv", c.cid.Recv)
			}
			c.finished = true
			c.mu.Unlock()
			return c.ctx.Err()
		}
	afterSelect:
		if c.afterPass() {
			// More to send than one pass sends: come round again.
			select {
			case c.writable <- struct{}{}:
			default:
			}
		}

		if c.state.stateType == ConnClosed {
			if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				c.logger.Trace("uTP conn closing...", "err", c.state.Err, "c.cid.Send", c.cid.Send, "c.cid.Recv", c.cid.Recv)
			}
			// No more passes: the reader leaves this connection alone from
			// here, so the teardown below can block for the application
			// without holding up the socket.
			c.finished = true
			c.mu.Unlock()
			// Always drain and terminate the read side here. The previous
			// `if !c.eof()` guard was inverted in effect: eof() is true by
			// definition once stateType is ConnClosed, so processReads was
			// never called and the end-of-stream marker was never delivered.
			// Readers reaching this path blocked in ReadToEOF forever.
			c.drainReadsForTeardown()
			c.processWrites(c.now())
			if c.state.RecvBuf != nil {
				c.state.RecvBuf.close()
			}
			// A final sample, so the last state of a connection is always
			// observed rather than lost to the throttle.
			c.sampleMetrics(c.now(), true)
			return c.state.Err
		}
	}
}

// afterPass is the work that ends every pass, whoever ran it: hand bytes up,
// send what the window allows, acknowledge, and see whether the application
// has asked to close. It reports whether there is still data the window would
// let out, which one pass stops short of sending.
//
// Called with mu held, by the event loop and by endBatch.
func (c *connection) afterPass() (moreToWrite bool) {
	stream := c.stream
	// Hand bytes up before acknowledging, so the acknowledgement carries
	// the window that results rather than the one from before the drain.
	//
	// This ordering is libutp's. It hands bytes to its embedder inside
	// utp_process_incoming and acknowledges afterwards, which is why
	// utp_read_drained finds nothing to report on an ordinary packet.
	// Draining on a later pass instead -- which is what this loop did --
	// makes every drain look like the window growing, and reporting that
	// doubles the reverse traffic. Measured: every data packet drew two
	// acknowledgements instead of one, which the corpus caught at once.
	//
	// ackPending is a bool and flushAck runs once per pass, so the drain
	// and the data that prompted it now share one acknowledgement.
	//
	// What the drain will take is counted first (drainable) and advertised
	// as if already taken (drainCredit, recvWindow); the drain itself runs
	// once the acknowledgement has gone. The packets are the same either
	// way. What moves is the work: copying the bytes out, allocating what
	// the reader is handed, and waking the reader -- measured at 4-5us of
	// a cold reader's path from datagram to acknowledgement, which libutp,
	// handing its bytes up through a callback, does not spend there.
	c.drainCredit = c.drainable()
	// One reading of the clock for the whole pass, as libutp reads
	// ctx->current_ms once per call into it.
	now := c.now()
	// Send before acknowledging, as libutp does: an acknowledgement that
	// opened the window is followed by the data it lets out within
	// utp_process_incoming, and the deferred acknowledgement after it is
	// dropped if a data packet already carried it (send_data, :768).
	for i := 0; c.wantWrite && i < maxWritesPerPass; i++ {
		c.wantWrite = false
		c.processWrites(now)
	}
	c.flushAck()
	c.drainCredit = 0
	c.processReads()
	c.scheduleIdleRto(now)
	c.sampleMetrics(now, false)
	// shutdown() sends the local FIN once everything queued has drained.
	// Both flags reach it: Close means finished entirely, CloseWrite means
	// finished sending. The difference is not here -- it is that a
	// CloseWrite connection is not torn down when the peer's FIN arrives,
	// because its reader is still there.
	if stream.shutdown.Load() {
		c.closeRequested = true
	}
	if (stream.shutdown.Load() || stream.writeClosed.Load()) &&
		c.state.stateType != ConnClosed {
		if !c.writeShut {
			c.writeShut = true
			c.processWrites(now)
		}
		c.shutdown()
		// And re-check: shutdown may have just sent the FIN that finishes
		// this connection, and no packet need ever arrive to notice.
		c.updateClosingState()
	}
	return c.wantWrite
}

// maxWritesPerPass bounds how many times one pass goes back to processWrites
// for data it moved into the send buffer. Each time either sends or finds the
// window full, so this is reached only by an application writing faster than
// the loop can turn round, and then the next pass carries on.
const maxWritesPerPass = 64

// receiveInline handles a packet for this connection on the caller's
// goroutine -- the socket's reader -- and reports whether it did. It does not
// acknowledge: that waits for endBatch, at the end of the read the packet came
// in. A connection still being set up declines, and the packet goes through
// streamEvents; one that has closed takes the packet and drops it, as its
// channel did once the loop had stopped reading it. Closed but not yet
// finished is the moment between the pass that closed it and the event loop
// waking to tear it down.
func (c *connection) receiveInline(pkt *packet) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished || (c.ready && c.state.stateType == ConnClosed) {
		return true
	}
	if !c.ready {
		return false
	}
	c.inBatch = true
	now := c.now()
	c.lastActivity = now
	c.onPacket(pkt, now)
	return true
}

// endBatch ends a read batch that delivered packets to this connection: the
// pass that receiveInline left open runs now, acknowledgement included.
// libutp's utp_issue_deferred_acks, which its embedder calls once the socket
// would block (utp.h:512-517).
//
// Except when the batch has left data to send. Then the whole pass is the
// event loop's: one goroutine per connection does the sending, and the reader,
// which serves every connection on the socket, does not. libutp sends from
// inside utp_process_incoming, on its one thread; done here, the reader's
// write system calls for every sender on the socket left it no time to read.
// Measured with 1000 transfers on one socket pair: the sending side's reader
// was busy 90-100% of the time handling 5,000 acknowledgements a second, they
// waited in the kernel for seconds, RTT samples reached 17 s, and a transfer
// whose last packets were lost waited out a retransmission timeout grown from
// them past the test's two minutes. The acknowledgement this pass owes goes
// out with the data, as it would have here.
//
// Otherwise the event loop is woken only for what it alone can do: tear the
// connection down, or go on sending past one pass's worth.
func (c *connection) endBatch() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished || !c.ready || !c.inBatch {
		return
	}
	c.inBatch = false
	if c.wantWrite && c.hasSendWork() {
		c.kickLoop()
		return
	}
	more := c.afterPass()
	if more || c.state.stateType == ConnClosed {
		c.kickLoop()
	}
}

// hasSendWork reports whether processWrites has anything it might send: bytes
// buffered or queued by the application, or packets a timeout marked for
// resending.
func (c *connection) hasSendWork() bool {
	if c.state.stateType != ConnConnected {
		return false
	}
	if len(c.pendingWrites) > 0 ||
		(c.state.SendBuf != nil && c.state.SendBuf.Pending() > 0) {
		return true
	}
	if c.state.SentPackets == nil {
		return false
	}
	_, resend := c.state.SentPackets.NextNeedingResend()
	return resend
}

// kickLoop wakes the event loop from another goroutine, accounting for the
// wake with a virtual clock. See IdleBarrier.NoteHandoff.
func (c *connection) kickLoop() {
	barrier, _ := c.timeSource().(IdleBarrier)
	if barrier != nil {
		barrier.NoteHandoff()
	}
	select {
	case c.kick <- struct{}{}:
	default:
		// A wake is already pending, and it is enough.
		if barrier != nil {
			barrier.TakeHandoff()
		}
	}
}

// wakeKind names which case of the event loop's blocking select fired.
//
// It exists so the select can receive without acting: the bodies run from a
// switch afterwards, which gives one place to tell a virtual clock that the
// loop is no longer parked.
type wakeKind int

const (
	wakeStreamEvent wakeKind = iota
	wakeWrite
	wakeReadable
	wakeWritable
	wakeRetransmit
	wakeKeepAlive
	wakeProbe
	wakeIdleRto
	wakeLossProbe
	wakeAckHold
	wakeKick
	wakeIdleTimeout
	wakeCtxDone
)

// scheduleIdleRto arms the idle wake-up when there is nothing outstanding, so
// the retransmission deadline is still honoured on a connection that has gone
// quiet. It is a no-op while packets are in flight, where the per-packet
// timers already cover it.
func (c *connection) scheduleIdleRto(now time.Time) {
	if c.armIdleRto == nil || c.state.stateType != ConnConnected ||
		c.state.SentPackets == nil || len(c.armed) != 0 || c.rtoDeadline.IsZero() {
		return
	}
	d := c.rtoDeadline.Sub(now)
	if d < time.Millisecond {
		d = time.Millisecond
	}
	c.armIdleRto(d)
}

// onIdleRto applies libutp's idle branch: the retransmission deadline passed
// with nothing in flight, so the window decays by a third rather than
// collapsing (utp_internal.cpp:1216-1222), and the deadline is set again.
//
// The decay itself is the controller's, reached by the same call the
// in-flight path uses: sentPackets.OnTimeout asks HasUnackedPackets, which is
// false here, which is exactly the distinction libutp draws with
// `cur_window_packets == 0`.
func (c *connection) onIdleRto(now time.Time) {
	if c.state.stateType != ConnConnected || c.state.SentPackets == nil ||
		len(c.armed) != 0 || c.rtoDeadline.IsZero() || now.Before(c.rtoDeadline) {
		return
	}
	c.state.SentPackets.OnTimeout()
	// libutp re-arms from the doubled timeout (:1204), which OnTimeout has
	// just applied.
	c.rtoDeadline = now.Add(c.state.SentPackets.Timeout())
	if c.logger.Enabled(BASE_CONTEXT, log.LevelDebug) {
		stats := c.state.SentPackets.ControllerStats()
		c.logger.Debug("idle window decay", "cwnd", stats.MaxWindowSizeBytes,
			"timeout", stats.Timeout)
	}
}

// lingerAck is the acknowledgement the socket should re-send if the peer keeps
// retransmitting its FIN after this connection is gone.
//
// It exists because a transfer can arrive whole and still end as a failure for
// the peer. We acknowledge the peer's FIN, hand the application its end of
// stream and tear down; if that acknowledgement is lost the peer retransmits
// the FIN, the socket no longer has the connection, and it answers with a
// RESET. libutp cannot do this in return: it keeps the socket in CS_GOT_FIN
// until its own application closes, so a retransmitted FIN is simply
// acknowledged again (utp_internal.cpp:2369-2370).
//
// Measured against real libutp on a 3% loss path with reordering, before this
// existed: 4 of 20 runs ended in UTP_ECONNRESET on a transfer whose every byte
// had already been read.
//
// Only a connection that reached the peer's FIN has anything to linger for.
// Anything else -- a reset, an idle timeout, a local close with no FIN
// received -- should still draw a RESET, which is what libutp does for a
// packet it has no socket for.
func (c *connection) lingerAck() *packet {
	// Only a connection that ended gracefully -- a FIN was sent, received, or
	// both -- has anything to linger for. One killed by a RESET, an idle
	// timeout or an error should still draw a RESET for whatever arrives
	// afterwards, which is what libutp does for a packet it has no socket for.
	if c.state.closing == nil {
		return nil
	}
	if c.finAck != nil {
		return c.finAck
	}
	// We sent the FIN and never received one, so there is no FIN
	// acknowledgement to repeat. The last acknowledgement we did send serves:
	// it tells a peer retransmitting what we already have, and its ack number
	// is what distinguishes a retransmission from data written past our FIN.
	return c.lastStateSent
}

// rememberLingerState captures an acknowledgement for the socket to repeat
// after this connection is gone, while there is still live state to build one
// from.
//
// It is called as the local FIN goes out. By the time the connection actually
// shuts down, statePacket() has nothing left to build from -- the same reason
// the acknowledgement for a peer's FIN cannot be deferred -- and an initiator
// that only ever sent data has no earlier STATE to fall back on, because its
// acknowledgements ride on the data packets.
func (c *connection) rememberLingerState() {
	if c.lastStateSent != nil {
		return
	}
	if pkt := c.statePacket(); pkt != nil {
		c.lastStateSent = pkt
	}
}

// notifySocketShutdown asks the socket to forget this connection.
//
// The wait is bounded because the socket's event loop may already be gone --
// if the socket is closing, it drops its whole table anyway, so there is
// nothing left to clean up and blocking here would leak the very goroutine
// this is trying to tidy up after.
func (c *connection) notifySocketShutdown() {
	if c.out != nil {
		c.out(newShutdownSocketEvent(c.cid, c.lingerAck()))
		return
	}
	timer := c.timeSource().NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case c.socketEvents <- newShutdownSocketEvent(c.cid, c.lingerAck()):
	case <-timer.C():
	}
}

// finFits reports whether the FIN may go now.
//
// libutp queues its FIN in the outgoing buffer like any data packet
// (`write_outgoing_packet(0, ST_FIN, NULL, 0)`, utp_internal.cpp:3377, :3419) and
// flush_packets sends it only while !is_full(), whose default is room for a
// whole packet (:931-985) -- behind anything owed a resend, since the walk
// is oldest first. This sent the FIN as soon as the send buffer was empty,
// whatever the window said. With bytes in flight above a window a loss had
// just halved, the controller refused even the FIN's zero bytes, and
// transmit panicked: 1 run in 6 of fifteen two-way transfers at 5% loss
// took the process down. Shutdown runs on every pass of the event loop, so
// a FIN held back here goes on the pass that finds room.
func (c *connection) finFits(now time.Time) bool {
	sp := c.state.SentPackets
	if sp == nil {
		return false
	}
	if _, owed := sp.NextNeedingResend(); owed {
		return false
	}
	if int(sp.UnackedCount()) >= maxOutstandingPackets {
		sp.OnWindowFull(now)
		return false
	}
	maxSend := minUint32(sp.CongestionWindow(), c.effectivePeerWindow(now))
	if uint64(sp.BytesInFlight())+uint64(c.mtu.payloadSize()) > uint64(maxSend) {
		// libutp's is_full records this too (:945, :957).
		sp.OnWindowFull(now)
		return false
	}
	return true
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
			if localFin == nil && len(c.pendingWrites) == 0 && c.state.SendBuf.IsEmpty() && c.finFits(c.now()) {
				recvWindow := c.recvWindow()
				seqNum := c.state.SentPackets.NextSeqNum()
				ackNum := c.state.RecvBuf.AckNum()

				// No selective ack: see dataPacketsCarryNoSelectiveAck.
				fin := NewPacketBuilder(
					st_fin,
					c.cid.Send,
					c.nowMicros(),
					recvWindow,
					seqNum,
				).WithAckNum(ackNum).Build()

				c.state.closing.LocalFin = &seqNum
				if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
					c.logger.Trace("transmitting FIN", "dst.Peer", c.cid.Peer, "dst.Send", c.cid.Send, "dst.Recv", c.cid.Recv, "seq", seqNum)
				}
				c.rememberLingerState()
				c.transmit(fin, c.now(), true)
			}
		} else {
			var localFin *uint16
			if len(c.pendingWrites) == 0 && c.state.SendBuf.IsEmpty() && c.finFits(c.now()) {
				recvWindow := c.recvWindow()
				seqNum := c.state.SentPackets.NextSeqNum()
				ackNum := c.state.RecvBuf.AckNum()

				// No selective ack: see dataPacketsCarryNoSelectiveAck.
				fin := NewPacketBuilder(
					st_fin,
					c.cid.Send,
					c.nowMicros(),
					recvWindow,
					seqNum,
				).WithAckNum(ackNum).
					Build()

				localFin = &seqNum
				if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
					c.logger.Trace("transmitting FIN", "dst.Peer", c.cid.Peer, "dst.Send", c.cid.Send, "dst.Recv", c.cid.Recv, "seq", seqNum)
				}
				c.rememberLingerState()
				c.transmit(fin, c.now(), true)
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
	c.emitPacket(pkt, false)
}

// emitPacket sends a packet, through the socket. dontFragment marks
// it as an MTU probe; see DontFragmentWriter.
func (c *connection) emitPacket(pkt *packet, dontFragment bool) {
	if pkt == nil {
		return
	}
	c.lastSentPacket = c.now()
	if pkt.Header != nil {
		c.lastAdvertisedWindow = pkt.Header.WndSize
	}
	if pkt.Header.PacketType == st_state {
		// Kept for the socket to repeat after this connection is gone.
		c.lastStateSent = pkt
	}
	if c.out != nil {
		// Straight to the wire, on this goroutine: nothing is in flight
		// between deciding to send and sending, so a virtual clock has
		// nothing to wait for. The event is done with when out returns, so
		// one serves every packet.
		c.outEvent = socketEvent{Type: outgoing, Packet: pkt, ConnectionId: c.cid, DontFragment: dontFragment}
		c.out(&c.outEvent)
		c.outEvent = socketEvent{}
		return
	}
	ev := newOutgoingSocketEvent(pkt, c.cid)
	if dontFragment {
		ev = newOutgoingProbeSocketEvent(pkt, c.cid)
	}
	// A virtual clock must not consider the system quiet while this packet is
	// queued but not yet taken by whoever reads socketEvents. See
	// IdleBarrier.NoteHandoff.
	if b, ok := c.timeSource().(IdleBarrier); ok {
		b.NoteHandoff()
	}
	c.socketEvents <- ev
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
	// The selective ack must be read against the decremented number too.
	// libutp decrements ack_nr before send_ack builds the mask
	// (utp_internal.cpp:836-843, :804-808), so its bit i is ack_nr + 1 + i
	// of the real acknowledgement; ours was built against the real one, bit i
	// being ack_nr + 2 + i. Shift it up one: the new bit 0 is ack_nr + 1, the
	// packet a selective ack exists because we lack, and the top bit falls
	// out of the window as it does in libutp. Left unshifted, the peer read
	// every bit one packet early and could free a packet we never received.
	// TestKeepAliveSelectiveAckMatchesItsAckNumber.
	if pkt.Eack != nil {
		old := pkt.Eack.Acked()[:SELECTIVE_ACK_WINDOW]
		shifted := make([]bool, len(old))
		copy(shifted[1:], old[:len(old)-1])
		pkt.Eack = NewSelectiveAck(shifted)
	}
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
		// A writer still waiting has not had all its bytes taken, so it is
		// told why -- with ErrNotConnected for a connection that closed
		// cleanly, which has no error of its own -- and how many were taken
		// before the end. Both used to be wrong: a nil error with nothing
		// written, which an io.Writer may not return, and 0 for a writer
		// part of whose bytes were already in the send buffer.
		err := c.state.Err
		if err == nil {
			err = ErrNotConnected
		}
		// Each writer is answered once. The queue used to be left as it was,
		// so the next call sent to every writer again, and a result channel
		// holds one: the second send blocked for ever, with the connection's
		// lock held -- and the socket's reader, which serves every connection,
		// waiting behind it.
		for _, w := range c.pendingWrites {
			w.resultCh <- &readOrWriteResult{Len: w.written, Err: err}
		}
		c.pendingWrites = nil
		return
	default:
	}

	// Compose data packets.
	//
	// libutp queues a write as packets of up to packet_size and flush_packets
	// sends the next one while !is_full() (utp_internal.cpp:3197-3212,
	// :963-985). is_full is called there with no argument, so it charges a
	// whole packet_size whatever the queued packet actually holds (:933-934),
	// and refuses when cur_window + packet_size > min(max_window, opt_sndbuf,
	// max_window_user) (:936, :956). Two things follow, and both used to be
	// different here:
	//
	//   - Bytes in flight count against the peer's window, not only against
	//     the congestion window. The peer's advertisement is how much it can
	//     take beyond what it has already acknowledged; everything we have
	//     sent since is already spending it. Without this we overran a
	//     receiver's buffer by up to a full window whenever its application
	//     fell behind, and every packet past its room was dropped and had to
	//     be retransmitted.
	//   - Nothing is sent into less than a full packet of room, and a packet
	//     is never cut down to fit what room there is. We used to fill a
	//     window to the byte with whatever size fitted.
	//
	// opt_sndbuf has no counterpart: our send buffer bounds what the
	// application may queue, not what may be in flight.
	packetSize := c.mtu.payloadSize()
	maxSend := minUint32(c.state.SentPackets.CongestionWindow(), c.effectivePeerWindow(now))
	// And never more than maxOutstandingPackets in flight, however small
	// they are. libutp's is_full refuses at `cur_window_packets >=
	// OUTGOING_BUFFER_MAX_SIZE - 1` before it looks at bytes
	// (utp_internal.cpp:939-947), so the flush stops there, resends
	// included. There was no such bound here: a run of small writes into a
	// large window could put more packets in flight than a libutp receiver
	// will reorder (1024, :1890) -- and, past 32768, more than 16-bit
	// sequence numbers can order at all.
	outstanding := int(c.state.SentPackets.UnackedCount())

	// Packets a retransmission timeout gave up as lost go first, oldest
	// first, under the same rule as new data. libutp's flush_packets walks
	// its outgoing buffer from the oldest packet and sends each one that is
	// unsent or need_resend while !is_full() (utp_internal.cpp:970-985), so
	// a resend always precedes the new data behind it.
	resendsWaiting := false
	for {
		pkt, ok := c.state.SentPackets.NextNeedingResend()
		if !ok {
			break
		}
		if outstanding >= maxOutstandingPackets ||
			uint64(c.state.SentPackets.BytesInFlight())+uint64(packetSize) > uint64(maxSend) {
			// No room even for what is owed; new data waits behind it.
			resendsWaiting = true
			break
		}
		c.resendSentPacket(pkt, now)
	}
	inFlight := c.state.SentPackets.BytesInFlight()
	// Taken, not shared: nothing here should come back into processWrites,
	// but if it did it would start a slice of its own.
	payloads := c.payloadScratch[:0]
	c.payloadScratch = nil
	var composed uint32

	// libutp's `is_full` marks the connection application-limited or not
	// every time it considers sending a packet (utp_internal.cpp:945, :957),
	// and the congestion controller refuses to grow a window the application
	// never fills (:1681-1686). The equivalent signal here is "we had data
	// and no room for it".
	windowFull := resendsWaiting

	for !resendsWaiting && c.state.SendBuf.Pending() > 0 {
		if outstanding+len(payloads) >= maxOutstandingPackets ||
			uint64(inFlight)+uint64(composed)+uint64(packetSize) > uint64(maxSend) {
			windowFull = true
			break
		}
		n := minUint32(uint32(c.state.SendBuf.Pending()), packetSize)
		// libutp's Nagle rule (flush_packets, utp_internal.cpp:974-982): the
		// last packet waits while it is short of a full one and anything is
		// ahead of it in the window -- sent and unacknowledged, or composed in
		// this pass -- and what is written next joins it. The acknowledgement
		// that empties the window releases it, as libutp's "flush Nagle" does
		// (:2246-2252): every acknowledgement brings this loop round again.
		if n < packetSize && !c.config.NoDelay && !c.writeShut && outstanding+len(payloads) > 0 {
			break
		}
		data := c.state.SendBuf.Take(int(n))
		n = uint32(len(data))
		payloads = append(payloads, data)
		composed += n
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

		// The queued data is the stream's own copy of what was written
		// (writeQueued), so the send buffer takes it as it is.
		if len(writeReq.data) <= bufSpace {
			c.state.SendBuf.Adopt(writeReq.data)
			result := &readOrWriteResult{
				Len: len(writeReq.data) + writeReq.written,
			}
			writeReq.resultCh <- result
			c.pendingWrites = c.pendingWrites[1:]
		} else {
			nextWrite := writeReq.data[:bufSpace]
			remainingData := writeReq.data[bufSpace:]
			c.state.SendBuf.Adopt(nextWrite)

			writeReq.data = remainingData
			writeReq.written += bufSpace
		}
		c.wantWrite = true
	}

	// transmit data packets
	seqNum := c.state.SentPackets.NextSeqNum()
	recvWindow := c.recvWindow()
	ackNum := c.state.RecvBuf.AckNum()

	for _, payload := range payloads {
		// No selective ack: see dataPacketsCarryNoSelectiveAck.
		packetInst := NewPacketBuilder(
			st_data,
			c.cid.Send,
			c.nowMicros(),
			recvWindow,
			seqNum,
		).WithPayload(payload).WithTsDiffMicros(uint32(c.peerTsDiff.Microseconds())).WithAckNum(ackNum).Build()

		c.transmit(packetInst, now, true)
		seqNum = seqNum + 1 // wrapping add in uint16
	}
	clear(payloads)
	c.payloadScratch = payloads[:0]
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
				// Our FIN is out: nothing more may be sent. libutp's
				// utp_writev refuses the same (`if (conn->fin_sent)
				// return 0`, utp_internal.cpp:3188). This answered Len 0
				// with no error, which an io.Writer may not do: a write
				// queued just before Close or CloseWrite reported success
				// and its bytes were never sent.
				writeReq.resultCh <- &readOrWriteResult{Err: ErrNotConnected}
			}
		} else {
			c.logger.Debug("append a queuedWrite to pending writes")
			c.pendingWrites = append(c.pendingWrites, writeReq)
		}

	case ConnClosed:
		c.logger.Warn("discard a queuedWrite when closed the conn...")
		// A connection that closed cleanly has no error of its own, and a
		// write that sends nothing must still report one.
		err := c.state.Err
		if err == nil {
			err = ErrNotConnected
		}
		writeReq.resultCh <- &readOrWriteResult{Err: err}
	}
	c.processWrites(c.now())
	c.wantWrite = true
}

func (c *connection) processReads() {
	if c.state.stateType == ConnConnecting {
		return
	}
	recvBuf := c.state.RecvBuf

	if recvBuf != nil && c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		c.logger.Trace("read data saving in the recvBuf, start...", "available", recvBuf.Available(), "isEmpty", recvBuf.IsEmpty())
	}
	// Drain contiguous data even once the connection is closed: bytes that
	// arrived before teardown are still owed to the reader.
	//
	// Never block the event loop waiting for the application to read. libutp
	// does not: utp_call_on_read hands the embedder its bytes and returns, and
	// a slow application is handled by the advertised receive window shrinking
	// (utp_call_get_read_buffer_size), not by libutp stopping. This connection
	// advertises RecvBuf.Window(), so leaving unread bytes in the receive
	// buffer is exactly that backpressure.
	//
	// Every sender on c.reads holds mu, so a free slot observed here is
	// still free at the send below: consumers only ever remove.
	drained := true
	for recvBuf != nil && !recvBuf.IsEmpty() {
		if len(c.reads) == cap(c.reads) {
			drained = false
			break
		}
		// Size the buffer to the data, not to the largest packet this
		// connection might carry.
		//
		// This allocated MaxPacketSize every time and handed the whole thing to
		// the reader with a separate length, so a 40-byte chunk kept 1400 bytes
		// alive until the reader dropped it. IsEmpty() is also true of a buffer
		// holding only out-of-order bytes, which are not readable yet; that
		// case used to allocate, read nothing, and break.
		readable := recvBuf.Readable()
		if readable == 0 {
			break
		}
		if maxSize := int(c.config.MaxPacketSize); readable > maxSize {
			readable = maxSize
		}
		chunk := newReadChunk(readable, int(c.config.MaxPacketSize))
		n := recvBuf.Read(chunk.Data)
		if n == 0 {
			chunk.release()
			break
		}
		chunk.Data, chunk.Len = chunk.Data[:n], n
		if !c.sendRead(chunk) {
			return
		}
	}
	if recvBuf != nil && c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
		c.logger.Trace("read data saving in the recvBuf, end...", "available", recvBuf.Available(), "isEmpty", recvBuf.IsEmpty())
	}

	// If we have reached eof, hand the reader the end-of-stream marker -- but
	// only once everything already received has been handed over first.
	//
	// eof() means every byte the peer sent has *arrived*, not that the reader
	// has been given it. While the drain above ran to completion that
	// distinction did not exist. Now that it can stop early with bytes still
	// in the receive buffer, announcing the end here truncates the transfer:
	// the reader sees end-of-stream and stops, and the rest is discarded.
	// Measured, when this guard was missing: 409308 bytes delivered out of
	// 524288 against real libutp.
	if drained && c.eof() {
		c.deliverTerminalRead()
	}

	c.onReadDrained()
}

// drainable is how many bytes processReads would hand the reader now: what is
// contiguous in the receive buffer, up to a chunk of at most MaxPacketSize for
// each free slot in the reader's queue -- processReads' own loop, counted.
func (c *connection) drainable() int {
	if c.state.stateType == ConnConnecting || c.state.RecvBuf == nil {
		return 0
	}
	free := cap(c.reads) - len(c.reads)
	if free <= 0 {
		return 0
	}
	return min(c.state.RecvBuf.Readable(), free*int(c.config.MaxPacketSize))
}

// recvWindow is the receive window an outgoing packet advertises: the
// buffer's, plus what this pass will hand the reader after the packet goes
// (drainCredit). It never exceeds the buffer: the credit is bytes the buffer
// holds.
func (c *connection) recvWindow() uint32 {
	return uint32(c.state.RecvBuf.Window() + c.drainCredit)
}

// onReadDrained tells the peer when handing bytes up has reopened a receive
// window smaller than the one now available.
//
// libutp's utp_read_drained (utp_internal.cpp:3242-3261), which its embedder
// calls whenever it has drained the socket:
//
//	const size_t rcvwin = conn->get_rcv_window();
//	if (rcvwin > conn->last_rcv_win) {
//	    if (conn->last_rcv_win == 0) conn->send_ack();
//	    else conn->schedule_ack();
//	}
//
// Without it the peer finds out only from the next acknowledgement this end
// happens to send -- and while it is blocked on a window too small to fit a
// packet it sends nothing, so nothing draws one.
//
// **The test is growth, not growth from zero, and the difference is a hang.**
// An earlier version of this fired only when the window last advertised was
// zero, on the reasoning that an ordinary drain is already covered by the
// acknowledgement owed for the data. It deadlocked one run in three. The
// trace: the window fell to 861 bytes, which is not zero but is less than a
// packet, so the sender could not fit one and went quiet; nothing arrived, so
// nothing was acknowledged; the application read and freed the buffer, and the
// zero-only test declined to mention it. Both ends sat silent for seconds at a
// time and a 512KB transfer delivered 154KB before the test gave up. A window
// too small to use is as blocking as a closed one, and libutp's condition
// covers both because it compares against what was actually last sent.
//
// libutp's distinction between acknowledging now and scheduling is not
// preserved, because it does not exist here: flushAck runs at the end of this
// same pass either way.
func (c *connection) onReadDrained() {
	if c.state == nil || c.state.stateType != ConnConnected || c.state.RecvBuf == nil {
		return
	}
	if uint32(c.state.RecvBuf.Window()) > c.lastAdvertisedWindow {
		c.ackPending = true
		c.readDrainedAcks++
	}
}

// drainReadsForTeardown hands over everything left in the receive buffer as the
// connection ends.
//
// The ordinary drain in processReads gives up when the read queue is full and
// waits to be woken, which is what keeps the event loop from blocking on the
// application. That is not good enough here: this is the last pass, the receive
// buffer is about to be closed, and anything not handed over now is lost. A
// truncated transfer is worse than a slow one -- when this path merely called
// processReads, a large loopback transfer arrived short and
// TestUdpTransfer failed on the payload comparison.
//
// So this one waits for room, and the wait has two escapes: the connection's
// context, and the consumer closing the stream. The second is what keeps the
// deadlock from coming back. A consumer that closed without reading is not
// owed these bytes, and waiting for it to take them would be waiting forever,
// because UtpStream.Close is itself blocked waiting for this goroutine.
func (c *connection) drainReadsForTeardown() {
	recvBuf := c.state.RecvBuf
	for recvBuf != nil && !recvBuf.IsEmpty() {
		buf := make([]byte, c.config.MaxPacketSize)
		n := recvBuf.Read(buf)
		if n == 0 {
			break
		}
		select {
		case c.reads <- &readOrWriteResult{Data: buf, Len: n}:
		case <-c.ctx.Done():
			c.readsTruncated = true
			return
		case <-c.abandoned:
			c.logger.Debug("stream closed by its consumer; dropping undelivered received bytes",
				"bytes", n)
			c.readsTruncated = true
			return
		}
	}
	if c.eof() {
		c.deliverTerminalRead()
	}
}

// sampleMetrics hands a snapshot to the configured observer, throttled to
// MetricsInterval.
//
// It runs with mu held -- on the event loop, or on the socket's reader in
// endBatch -- which is the whole point: reading the connection's state
// without it would be a race.
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
		PendingWrites:        len(c.pendingWrites),
		MtuCurrent:           c.mtu.current,
		MtuFloor:             c.mtu.floor,
		MtuCeiling:           c.mtu.ceiling,

		MtuProbesLostToDuplicateAcks: c.mtuProbesLostToDuplicateAcks,

		WindowReopenedAcks: c.readDrainedAcks,
		State:              connStateName(c.state.stateType),
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
	// close (RESET, idle timeout) carries an error. See readEndErr.
	err := c.readEndErr()
	c.logger.Debug("read eof...", "err", err)

	// Published before delivery is attempted, so the reason the stream ended
	// is available however the reader finds out it has.
	c.terminalErr.Store(&terminalError{err: err})

	// Never block the event loop on this either. UtpStream.Close waits for
	// this goroutine to finish, and only the reader drains c.reads, so a
	// consumer that stopped reading and then closed deadlocked against
	// itself: Close waited for the loop, the loop waited for the reader, and
	// the context that would break the tie is cancelled by Close only after
	// its wait returns.
	//
	// If there is no room the marker is simply not sent. Closing c.reads when
	// this loop exits reports the end of the stream just as well, and carries
	// the error through terminalErr.
	select {
	case c.reads <- &readOrWriteResult{Err: err, Data: make([]byte, 0)}:
	default:
		c.readsTerminated = false // let a later pass deliver it if room appears
		c.logger.Debug("read queue full at end of stream; deferring the marker", "err", err)
	}
}

// readEndErr is what the reader is told when the stream ends: the error that
// ended the connection, or nil for a clean end -- and a clean end means the
// reader has been given everything the peer sent, up to its FIN.
//
// A connection that ended without error but short of that reported nil too.
// The case that showed it: the consumer closes the stream while bytes are
// still on their way to its reader, teardown drops them (they are owed to
// nobody), and a ReadToEOF still running returned the bytes it had so far
// with a nil error -- a truncated transfer reported as a complete one.
// TestDataValidWhenResendingSynStateResponse saw it about once in 20 runs
// under -race, before this change as after. It is ErrReadClosed now: this end
// closed before it had read everything.
func (c *connection) readEndErr() error {
	if c.state.Err != nil {
		return c.state.Err
	}
	if c.readsTruncated || !c.receivedThroughFin() {
		return ErrReadClosed
	}
	return nil
}

// receivedThroughFin reports whether every byte the peer sent before its FIN
// has arrived. eof() asks the same of a connection still open.
func (c *connection) receivedThroughFin() bool {
	st := c.state
	return st.closing != nil && st.closing.RemoteFin != nil && st.RecvBuf != nil &&
		st.RecvBuf.AckNum() == *st.closing.RemoteFin
}

// terminalError boxes the end-of-stream error so it can be stored atomically,
// including the nil case that means a clean end of stream.
type terminalError struct{ err error }

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
			c.synTimeout = capTimeout(c.synTimeout*2, c.config.MaxTimeout)

			c.armRetransmit(originPacket, c.synTimeout)

			// Re-send SYN packet
			c.emit(c.synPacket(seq))
		}

	case ConnConnected:
		// If the timed out packet is a SYN, do nothing
		if originPacket.Header.PacketType == st_syn {
			return
		}

		// A timeout for a packet the peer has already acknowledged is not a
		// timeout at all, and acting on one is what made a quiet connection
		// punish itself.
		//
		// Timers are armed per packet and cancelled when the ack arrives, but
		// cancelling cannot stop one that has already fired: it is out of the
		// wheel and on its way to this loop, and disarmAcked will not find
		// it. The delivery then arrived for a packet that had been
		// acknowledged in the meantime -- and the early-timer guard below,
		// which asks only what the clock says, re-armed it. Each re-arm put
		// the same dead timer back in the wheel, so the armed set grew by one
		// per write and never shrank, and whenever several of them landed
		// past the deadline at once they were counted as a real timeout: the
		// window halved and packets the peer already had were sent again.
		//
		// Measured on a link with no loss configured, after a bulk transfer
		// followed by 200-byte writes every 200ms: 2 timeouts, 12
		// retransmissions of delivered data, and the congestion window taken
		// from 55513 to 24831 bytes for nothing. That is the shape a
		// BitTorrent peer connection spends most of its life in.
		//
		// libutp cannot reach this state. It has one timeout deadline rather
		// than a timer per packet, and it retransmits out of its outgoing
		// buffer (utp_internal.cpp:1230-1244), which no longer holds a packet
		// the peer has acknowledged.
		if c.state.SentPackets != nil &&
			!c.state.SentPackets.Outstanding(originPacket.Header.SeqNum) {
			return
		}

		// One timeout per RTO expiry, measured against the clock, and one
		// packet resent for it.
		//
		// libutp keeps a single deadline per connection and acts only when
		// `current_ms - rto_timeout >= 0` (utp_internal.cpp:1147-1148). Then
		// it marks every packet in flight need_resend, which takes their bytes
		// out of cur_window, and resends only the oldest (:1230-1252). The
		// rest go out oldest first from flush_packets, as the congestion
		// window -- which the timeout has just cut to one packet (:1225) --
		// allows, or one per acknowledgement through the fast-timeout path
		// (:2256-2282).
		//
		// This connection arms a timer per packet, so an expiry arrives as
		// several callbacks. It used to resend every packet whose callback
		// came, on the reasoning that libutp "marks every outstanding packet
		// need_resend". Marking is not sending. Measured with the data
		// direction blacked out for three seconds (netem.TestRTOBurstMeasure):
		// libutp resent one packet at the timeout; this resent its whole
		// window, 75 packets, and most of them again a second later, because
		// the callbacks after the first were measured against the old
		// deadline and never counted as a timeout. On a path that is
		// congested or down, that is the opposite of what a timeout is for.
		//
		// An earlier attempt at this went wrong the other way: it skipped the
		// other callbacks' resends and nothing replaced them, which left holes
		// fast retransmit could not fill, and the 5%-loss benchmark stopped
		// completing. What replaces them here is libutp's own two routes:
		// processWrites resends marked packets ahead of new data, and
		// fastTimeout resends the oldest on each acknowledgement.
		now := c.now()

		// A retransmission timeout on the MTU probe, with nothing else
		// outstanding, says the path will not carry that size -- not that it
		// is congested. libutp lowers the ceiling and sets `ignore_loss`, so
		// the window is left alone and the binary search moves on
		// (utp_internal.cpp:1152-1167).
		probeTimedOut := c.mtu.probeOutstanding(originPacket.Header.SeqNum) &&
			c.state.SentPackets.UnackedCount() == 1
		if probeTimedOut && !now.Before(c.rtoDeadline) {
			// Everything else in libutp's timeout still happens: the give-up
			// check comes first (:1191), and the timeout counts towards it
			// and starts the fast-timeout retry (:1240, :1247). Only the
			// doubling and the window collapse are skipped (:1179, :1206).
			if c.retransmitCount >= maxConsecutiveTimeouts {
				c.state.stateType = ConnClosed
				c.state.Err = ErrTimedOut
				return
			}
			c.retransmitCount++
			c.fastTimeout, c.probeRecovery = true, false
			c.mtu.onProbeLost(now)
			c.logger.Debug("MTU probe timed out",
				"floor", c.mtu.floor, "ceiling", c.mtu.ceiling, "current", c.mtu.current)
			c.rtoDeadline = now.Add(c.state.SentPackets.Timeout())
			c.packetsRetransmitted++
			c.bytesRetransmitted += uint64(len(originPacket.Body))
			c.retransmit(originPacket, now)
			return
		}

		// Not yet the connection's timeout: this packet's callback came
		// before the deadline, because it was armed before the deadline last
		// moved, or because the wheel ran ahead of the clock (it counts
		// ticks, and a descheduled wheel catches up in a burst; measured, a
		// 200ms timer delivered at 198.6ms). libutp would do nothing at all
		// here. Keep this packet's timer alive against the deadline and send
		// nothing.
		if now.Before(c.rtoDeadline) {
			c.armRetransmit(originPacket, c.rtoDeadline.Sub(now))
			return
		}

		// Give up once enough consecutive RTOs have passed with the peer
		// acking nothing, as libutp does (utp_internal.cpp:1191). The check
		// precedes the increment there, so the connection dies on the RTO
		// after the fourth retransmission.
		if c.retransmitCount >= maxConsecutiveTimeouts {
			c.logger.Warn("giving up on connection",
				"consecutiveTimeouts", c.retransmitCount,
				"cid.send", c.cid.Send, "cid.recv", c.cid.Recv)
			c.state.stateType = ConnClosed
			c.state.Err = ErrTimedOut
			return
		}
		// Forget any outstanding MTU probe, whether or not it was the one
		// that timed out. libutp clears it on every RTO, outside the branch
		// that lowers the ceiling (utp_internal.cpp:1166-1167): the probe is
		// gone either way, and a search that keeps waiting for it never sends
		// another.
		c.mtu.clearProbe()

		c.retransmitCount++
		c.state.SentPackets.OnTimeout()
		c.timeouts++
		currentTime := c.now()
		c.latestTimeout = &currentTime
		// libutp: `rto_timeout = ctx->current_ms + new_timeout`
		// (utp_internal.cpp:1204), with new_timeout already doubled.
		c.rtoDeadline = currentTime.Add(c.state.SentPackets.Timeout())

		// "every packet should be considered lost" (:1230-1237), and the
		// oldest resent (:1249-1251).
		c.state.SentPackets.MarkAllForResend()
		c.fastTimeout, c.probeRecovery = true, false
		oldest, ok := c.state.SentPackets.OldestOutstanding()
		if !ok {
			return
		}
		c.resendSentPacket(oldest, now)
		if oldest.seqNum != originPacket.Header.SeqNum {
			// This packet's callback is spent. Keep a timer on it, against
			// the new deadline, so every outstanding packet stays armed.
			c.armRetransmit(originPacket, c.rtoDeadline.Sub(now))
		}
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

// Acknowledgement coalescing. See ackEvery.
const (
	// ackQueueStep is how much queueing delay on the way back adds one packet
	// to the run an acknowledgement waits for.
	ackQueueStep = 500 * time.Microsecond
	// ackRatePeriod is the shortest spacing this receiver keeps between its
	// acknowledgements when data packets arrive closer together than that,
	// up to maxAckRateRun packets apiece.
	ackRatePeriod  = time.Millisecond
	maxAckRateRun  = 4
	maxAckQueueRun = 16
	// maxAckWait is the longest an acknowledgement waits for the rest of its
	// run. It waits no longer than two of the latest gap between packets per
	// packet it is waiting for, so the end of a burst is acknowledged once
	// the packets stop coming. Waiting by a smoothed gap, or with a 1 ms
	// floor, held each slow-start burst's acknowledgement for packets that
	// could not come until it was sent.
	maxAckWait = 5 * time.Millisecond
	// ackRateGate is how much queueing delay the way back must show before
	// the rate term applies, and ackRateMemory how long it goes on applying
	// after the last time it did. On paths where nothing queues, the delay
	// this receiver reads for its acknowledgements still reaches 2-6 ms: the
	// sender stamps an acknowledgement when it reads it, and a sender busy
	// sending reads late. A return path that cannot carry the
	// acknowledgements queues them by tens of milliseconds within a round
	// trip or two.
	ackRateGate   = 10 * time.Millisecond
	ackRateMemory = 10 * time.Second
)

// ackEvery is how many data packets an acknowledgement may wait to cover.
//
// libutp acknowledges once per pass of its embedder's loop: whatever arrived
// while it was busy shares one acknowledgement, and a packet that arrives
// alone gets its own. On a link, where packets arrive spaced, that is one per
// packet -- measured over real sockets with the relay pacing each datagram to
// its time, 0.99 per data packet at 10 Mb/s and 0.98 at 20 Mb/s -- and only
// at 100 Mb/s, where packets come 110 us apart, does its loop gather a few
// (0.58-0.62). So libutp sends a 20 Mb/s flow's acknowledgements at 1,750 a
// second whatever the return path can carry: over a 160 kb/s return path,
// which carries 1,000 a second, it took 2.97 s to receive 4 MB where 2.0 s
// was the data path's limit, and over 64 kb/s 7.3 s.
//
// This receiver acknowledges as libutp does until its acknowledgements are
// seen to queue on the way back, and then lets one wait for more packets, by
// two terms, the larger winning:
//
//   - The return path: the peer reports on every packet how long ours take to
//     reach it, and for a connection that only receives, ours are its
//     acknowledgements. Each ackQueueStep of filtered queueing delay on that
//     path (libutp's our_hist.get_value()) adds one packet to the run.
//   - The rate, for ackRateMemory after that delay last reached ackRateGate:
//     while packets arrive closer together than ackRatePeriod, round(1 ms /
//     gap) of them share one, at most maxAckRateRun. The gap is the smaller
//     of the latest and a smoothed one, so a burst after a pause is coalesced
//     from its second packet. Without it the queue the first term answers to
//     stands: 2.13 s over 160 kb/s and 3.38 s over 64 kb/s.
//
// Measured against libutp receiving, a libutp sender, over real sockets
// (native/libutp TestAsymmetricAckPath), 20 runs each: 2.03 s against 2.99 s
// over 160 kb/s and 2.43 s against 7.33 s over 64 kb/s; over 320 kb/s, 20
// Mb/s and 100 Mb/s the same times and as many acknowledgements as libutp's.
// The rate term used to apply on every path, and cost the wait it imposes: at
// 100 Mb/s the median acknowledgement left 240 us after its packet arrived,
// against libutp's 30 us and 50 us now (TestAckTurnaround), for no gain in
// throughput there. DEVIATIONS.md, "Acknowledgements: one per read, fewer
// when they would crowd the way back".
//
// Never while a gap is open: the selective ack is how the peer learns of a
// loss, and holding it back would delay recovery.
func (c *connection) ackEvery() int {
	if c.state == nil || c.state.SentPackets == nil || c.state.RecvBuf == nil {
		return 1
	}
	if c.state.RecvBuf.SelectiveAck() != nil {
		return 1
	}
	q := c.state.SentPackets.QueueingDelay()
	k := min(1+int(q/ackQueueStep), maxAckQueueRun)
	now := c.now()
	if q >= ackRateGate {
		c.ackPathQueuedAt = now
	}
	if c.ackPathQueuedAt.IsZero() || now.Sub(c.ackPathQueuedAt) >= ackRateMemory {
		return k
	}
	gap := c.dataGap
	if c.lastDataGap > 0 {
		gap = min(gap, c.lastDataGap)
	}
	if gap > 0 {
		k = max(k, min(max(int((ackRatePeriod+gap/2)/gap), 1), maxAckRateRun))
	}
	return k
}

// flushAck sends the acknowledgement owed for whatever was received, if any,
// unless ackEvery says it should wait for more.
func (c *connection) flushAck() {
	if !c.ackPending {
		return
	}
	// One acknowledgement per socket read, as libutp's embedder sends it: the
	// datagrams already waiting in the kernel are all processed, and then
	// utp_issue_deferred_acks runs once (utp.h:512-517). A read that is still
	// being handed out holds the acknowledgement; the reader ends the batch
	// (endBatch) once it has dispatched the whole read, and the
	// acknowledgement then covers everything the read brought.
	//
	// This used to flush at the end of every pass of this loop, and the batch
	// boundary was wherever the goroutine hand-offs between the socket and
	// here happened to fall. With batches alone, and ackEvery at one, a
	// libutp sender at 100 Mb/s got 0.57-0.68 acknowledgements per data
	// packet from this receiver and 0.58-0.62 from libutp's.
	if c.inBatch {
		return
	}
	if k := c.ackEvery(); k > 1 && c.dataSinceAck > 0 && c.dataSinceAck < k {
		now := c.now()
		if c.ackHeldSince.IsZero() {
			c.ackHeldSince = now
		}
		hold := min(maxAckWait, 2*time.Duration(k)*c.lastDataGap)
		if due := c.ackHeldSince.Add(hold); now.Before(due) {
			if c.armAckHold != nil {
				c.armAckHold(due.Sub(now))
			}
			return
		}
	}
	c.ackHeldSince = time.Time{}
	c.dataSinceAck = 0
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
			PacketType: originPacket.Header.PacketType,
			Version:    originPacket.Header.Version,
			// No selective ack: see dataPacketsCarryNoSelectiveAck.
			Extension:     0,
			ConnectionId:  originPacket.Header.ConnectionId,
			SeqNum:        originPacket.Header.SeqNum,
			WndSize:       c.recvWindow(),
			Timestamp:     int64(c.nowMicros()),
			TimestampDiff: uint32(c.peerTsDiff.Microseconds()),
			AckNum:        c.state.RecvBuf.AckNum(),
		},
		Body: originPacket.Body,
	}
	c.transmit(retransmissionPacket, now, false)
}

// lossUndoer is a controller that can undo a cut for a loss that proved
// spurious. See defaultController.undoSpuriousLoss.
type lossUndoer interface {
	lossCause() (uint16, bool)
	undoSpuriousLoss(seq uint16) bool
}

// noteResend records the wire timestamp of the first resend of the packet
// whose loss made the controller's last cut, so that the acknowledgement can
// tell which copy arrived. See checkSpuriousLoss.
func (c *connection) noteResend(pkt *packet) {
	u, ok := c.state.SentPackets.congestionCtrl.(lossUndoer)
	if !ok || pkt.Header.PacketType != st_data {
		return
	}
	seq, pending := u.lossCause()
	if !pending || seq != pkt.Header.SeqNum || (c.undoResent && c.undoSeq == seq) {
		return
	}
	c.undoSeq, c.undoStamp, c.undoResent = seq, uint32(pkt.Header.Timestamp), true
}

// checkSpuriousLoss undoes the controller's last cut if the packet it was
// for turns out to have arrived after all: RFC 3522's detection, by
// timestamp. An acknowledgement echoes the timestamp of the packet that drew
// it (ackEcho), so the first one to cover the packet says which copy filled
// the hole. If it echoes a time before the resend was sent, the original did;
// the packet was late, not lost. One that covers it before it was resent at
// all says the same without a timestamp. Anything else -- the resend's echo,
// a peer that reports no delay, an acknowledgement drawn by a later packet --
// leaves the cut standing, so a mistake can only leave a cut that libutp
// would also have made.
//
// Every resend goes through transmit, which notes it (noteResend), so "not
// resent" is not a guess.
func (c *connection) checkSpuriousLoss(fullAcked *circularRangeInclusive, selected []uint16) {
	u, ok := c.state.SentPackets.congestionCtrl.(lossUndoer)
	if !ok {
		return
	}
	seq, pending := u.lossCause()
	if !pending {
		c.undoResent = false
		return
	}
	if !(fullAcked != nil && fullAcked.Contains(seq)) && !slices.Contains(selected, seq) {
		return
	}
	resent := c.undoResent && c.undoSeq == seq
	if !resent || (c.ackEchoKnown && int32(c.ackEcho-c.undoStamp) < 0) {
		u.undoSpuriousLoss(seq)
	}
	c.undoResent = false
}

// resendSentPacket sends a packet again from what was recorded when it was
// first sent, with its acknowledgement fields brought up to date. It is the
// resend for a packet a retransmission timeout gave up as lost, whether the
// timeout itself, processWrites or onFastTimeout is sending it. It returns the
// packet as sent.
func (c *connection) resendSentPacket(pkt *sentPacket, now time.Time) *packet {
	builder := NewPacketBuilder(pkt.packetType, c.cid.Send, c.nowMicros(),
		c.recvWindow(), pkt.seqNum)
	if pkt.data != nil {
		builder.WithPayload(pkt.data)
	}
	// No selective ack: see dataPacketsCarryNoSelectiveAck.
	resend := builder.
		WithTsDiffMicros(uint32(c.peerTsDiff.Microseconds())).
		WithAckNum(c.state.RecvBuf.AckNum()).
		Build()
	c.packetsRetransmitted++
	c.bytesRetransmitted += uint64(len(pkt.data))
	// Not a first transmission, so never an MTU probe (utp_internal.cpp:911).
	c.transmit(resend, now, false)
	return resend
}

// lossProbeEnabled switches the loss probe. It is not libutp's: see
// DEVIATIONS.md, "A loss probe resends before the retransmission timeout".
var lossProbeEnabled = func() *atomic.Bool {
	b := new(atomic.Bool)
	b.Store(true)
	return b
}()

// lossProbeActive reports whether this connection uses the loss probe:
// classic LEDBAT only. Under LEDBAT++ the retransmission timeout it avoids is
// part of how the flow yields -- after one, the window collapses to two
// packets -- and with the probe LEDBAT++ took more than its share from a
// loss-based flow in about one run in five (DEVIATIONS.md). Classic LEDBAT's
// share was unchanged.
func (c *connection) lossProbeActive() bool {
	return lossProbeEnabled.Load() && c.config.CongestionAlgorithm != AlgorithmLEDBATPP
}

// minLossProbeTimeout is RFC 8985's floor on the probe timeout (§7.2).
const minLossProbeTimeout = 10 * time.Millisecond

// lossProbeTimeout is RFC 8985's PTO, max(2 x SRTT, 10 ms), or zero before
// there is a round-trip estimate.
func (c *connection) lossProbeTimeout() time.Duration {
	// The microsecond estimate: libutp's own is in whole milliseconds, and
	// on a path faster than one it is zero.
	rtt := c.state.SentPackets.ControllerStats().FineRTT
	if rtt <= 0 {
		return 0
	}
	return max(2*rtt, minLossProbeTimeout)
}

// rearmLossProbe restarts the loss probe's timeout. It runs on every
// acknowledgement and when the first packet enters an empty window, so the
// probe fires only after a probe timeout with nothing acknowledged. An
// acknowledgement that moves the cumulative ack forward starts a new
// episode, with a new probe to spend.
func (c *connection) rearmLossProbe(ackNum uint16) {
	if !c.lossProbeActive() || c.armLossProbe == nil || c.state.stateType != ConnConnected ||
		c.state.SentPackets == nil {
		return
	}
	if ackNum != c.lossProbeAckNum {
		c.lossProbeAckNum = ackNum
		c.lossProbeSent = false
	}
	if c.lossProbeSent || c.fastTimeout || !c.state.SentPackets.HasUnackedPackets() {
		return
	}
	if pto := c.lossProbeTimeout(); pto > 0 {
		c.armLossProbe(pto)
	}
}

// onLossProbe resends the oldest outstanding packet when nothing has been
// acknowledged for a probe timeout, and backs off for the next probe.
//
// libutp has nothing between fast retransmission and its retransmission
// timeout, whose floor is a second. A packet whose fast retransmission is
// lost as well -- common on a queue that has just overflowed, which is when
// fast retransmission happens -- and the last packet of a transfer, which
// has nothing after it to raise duplicate acknowledgements, both wait for
// it. On a 1 ms LAN path that made a 0.4 s transfer take 1.4 s, in every
// pairing of this library and libutp (KNOWN-LIMITATIONS.md). This is TCP's
// answer, RFC 8985's tail loss probe, resending the oldest packet rather
// than the newest because the oldest is the one known to be missing.
//
// One probe per episode, as in RFC 8985: after it, only an acknowledgement
// that moves the cumulative ack forward allows another, and none is sent
// once a retransmission timeout has fired (libutp's fast-timeout retry takes
// over from there) or when the timeout is already due. It does not touch the
// congestion window: the packet it resends is already counted in flight, and
// the loss that led here has already been charged.
func (c *connection) onLossProbe(now time.Time) {
	if !c.lossProbeActive() || c.lossProbeSent || c.fastTimeout ||
		c.state.stateType != ConnConnected || c.state.SentPackets == nil ||
		c.rtoDeadline.IsZero() || !now.Before(c.rtoDeadline) {
		return
	}
	oldest, ok := c.state.SentPackets.OldestOutstanding()
	if !ok {
		return
	}
	c.lossProbeSent = true
	c.lossProbes++
	c.lossProbeSeq, c.lossProbeAt = oldest.seqNum, now
	resent := c.resendSentPacket(oldest, now)
	c.lossProbeStamp = uint32(resent.Header.Timestamp)
	// transmit armed the packet's timer a whole timeout from now; the
	// timeout itself is due where it was, and the probe must not move it.
	// The wheel places a timer by its deadline, so this keeps its tick.
	c.armRetransmit(resent, c.rtoDeadline.Sub(now))
}

// onFastTimeout is libutp's fast-timeout retry, run on every acknowledgement
// (utp_internal.cpp:2256-2282):
//
//	if (((conn->seq_nr - conn->cur_window_packets) & ACK_NR_MASK) != conn->fast_resend_seq_nr) {
//	    conn->fast_timeout = false;
//	} else {
//	    OutgoingPacket *pkt = (OutgoingPacket*)conn->outbuf.get(conn->seq_nr - conn->cur_window_packets);
//	    if (pkt && pkt->transmissions > 0) {
//	        conn->fast_resend_seq_nr++;
//	        conn->send_packet(pkt);
//	    }
//	}
//
// After a timeout resends the oldest packet, the acknowledgement for it moves
// fast_resend_seq_nr up to the next one (:2186-2188), which is then the oldest
// outstanding, so it is resent at once -- and so on, one per acknowledgement,
// until a packet turns out to have arrived after all. That ends it: the
// oldest outstanding is then past fast_resend_seq_nr.
//
// libutp runs it after the cumulative acknowledgement and before the
// selective one; here it runs after both, before any fast retransmission,
// which sees fast_resend_seq_nr already moved on exactly as libutp's does.
func (c *connection) onFastTimeout(now time.Time) {
	if !c.fastTimeout || c.state.stateType != ConnConnected || c.state.SentPackets == nil {
		return
	}
	if c.probeRecovery {
		c.resendProbedLosses(now)
		return
	}
	oldest, ok := c.state.SentPackets.OldestOutstanding()
	if !ok || oldest.seqNum != c.state.SentPackets.fastResendSeqNum {
		c.fastTimeout = false
		return
	}
	c.state.SentPackets.fastResendSeqNum++
	c.resendSentPacket(oldest, now)
}

// probeAnswered reports whether this acknowledgement answered the loss probe
// by showing packets lost, and if so starts resending them.
//
// The probe resends the oldest outstanding packet. When the acknowledgement
// that retires it arrives, a round trip later, every packet sent before the
// probe and still unacknowledged has had a probe timeout and a round trip to
// be acknowledged and has not been: it is lost. RFC 8985 pairs the probe with
// that rule (RACK: a packet sent before one that has been delivered is lost
// once the reordering allowance has passed); without it the probe repaired one
// hole per probe timeout. In the 1000-transfer stress test a burst of 13
// consecutive packets lost at the tail of a window, with one packet after them
// to raise a selective ack -- fast retransmission needs three -- came back one
// every 4.3 seconds, and each repair restarted the retransmission timeout, so
// libutp's recovery never began either.
//
// The packets are resent the way libutp resends them after its timeout: one
// per acknowledgement, the fast-timeout retry (onFastTimeout), here stopping
// at the first packet sent after the probe, whose fate is not yet known. The
// loss is charged once, as a detected loss rather than a timeout: the window
// decays and does not collapse.
func (c *connection) probeAnswered(fullAcked *circularRangeInclusive, now time.Time) bool {
	if !c.lossProbeSent || c.fastTimeout || fullAcked == nil || !fullAcked.Contains(c.lossProbeSeq) {
		return false
	}
	sp := c.state.SentPackets
	// The acknowledgement must be the probe's, not the original's. If the
	// original was never lost, the packets behind it are in flight, not
	// missing, and taking its acknowledgement for the answer resends them
	// all: 86-101 needless retransmissions on a lossless 100 Mb/s link, each
	// time a probe had fired early.
	//
	// It fired early two ways. The process stalled and woke to the probe and
	// the acknowledgements together: the answer came 11 us to 1.4 ms after
	// the probe, under the 2.2 ms the path had ever taken for a round trip.
	// RFC 8985 makes that call (section 6.2: an ACK arriving within min_RTT
	// of a retransmission is ambiguous and is not taken as evidence). Or a
	// queue filling in slow start took the round trip from 10 ms to 33 ms
	// faster than the smoothed estimate the probe's timer runs on: then the
	// original's acknowledgement came 2.8-10 ms after the probe, past the
	// minimum, and only the timestamps tell them apart. The peer reports, on
	// every packet, how long ours took to reach it; from that and its own
	// timestamp comes the timestamp of the packet that drew the
	// acknowledgement, and the original's is older than the probe's.
	if c.ackEchoKnown {
		if int32(c.ackEcho-c.lossProbeStamp) < 0 {
			return false
		}
	} else if minRTT := sp.ControllerStats().MinFineRTT; minRTT == 0 || now.Sub(c.lossProbeAt) < minRTT {
		return false
	}
	oldest, ok := sp.OldestOutstanding()
	if !ok || !oldest.retransmission.Before(c.lossProbeAt) {
		return false
	}
	_ = sp.OnLost(oldest.seqNum, true, now)
	c.fastTimeout, c.probeRecovery = true, true
	return true
}

// resendProbedLosses is onFastTimeout for losses the probe showed: resend the
// oldest outstanding packet if it was last sent before the probe.
func (c *connection) resendProbedLosses(now time.Time) {
	sp := c.state.SentPackets
	oldest, ok := sp.OldestOutstanding()
	if !ok || !oldest.retransmission.Before(c.lossProbeAt) {
		c.fastTimeout, c.probeRecovery = false, false
		return
	}
	if wrappingLessThan(sp.fastResendSeqNum, oldest.seqNum+1) {
		sp.fastResendSeqNum = oldest.seqNum + 1
	}
	c.resendSentPacket(oldest, now)
}

// maxOutstandingPackets is how many packets may be in flight at once:
// libutp's `OUTGOING_BUFFER_MAX_SIZE - 1` (utp_internal.cpp:52, applied in
// is_full at :939). See processWrites.
const maxOutstandingPackets = 1024 - 1

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
	// Every packet from the peer is evidence the connection is still alive,
	// which is what Close watches to decide whether waiting for the flush is
	// still worth anything.
	c.peerActivity.Add(1)
	c.dropUnacked = false
	c.packetsReceived++
	c.bytesReceived += uint64(len(packet.Body))

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

	// An "extension bits" extension must be exactly 8 bytes, or libutp drops
	// the packet (utp_internal.cpp:1850-1856). Not for a SYN: libutp creates
	// the connection and answers it whatever its extensions say
	// (:2984-2990), and a SYN does not reach here once the connection
	// exists.
	if packet.Header.PacketType != st_syn && packet.malformedExtensionBits() {
		return
	}

	if c.outsideReorderWindow(packet) {
		return
	}

	// The peer's window, and the zero-window probe, from a packet that has
	// passed both checks above. libutp reads `pf1->windowsize` only there
	// (utp_internal.cpp:2144, reached after :1794-1807 and :1886-1899); this
	// used to take it first thing, so a packet the reference discards -- one
	// anybody who can guess a connection id can send -- could close our view
	// of the peer's window.
	c.peerRecvWindow = packet.Header.WndSize
	// libutp restarts the deadline on *every* packet reporting a zero window
	// (`zerowindow_time = current_ms + 15000`, :2148-2151), so the probe goes
	// 15 seconds after the last one, not the first; this used to keep the
	// first deadline. The next packet overwrites the forced one-packet
	// window with whatever the peer now reports (:2145).
	if c.peerRecvWindow == 0 {
		interval := c.zeroWindowProbeIntervalOrDefault()
		c.zeroWindowProbeDue = now.Add(interval)
		if c.armProbeTimer != nil {
			c.armProbeTimer(interval)
		}
	} else {
		c.zeroWindowProbeDue = time.Time{}
	}
	c.probingZeroWindow = false

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
	// packet's timestamp from the current time (utp_internal.cpp:2000-2002).
	//
	// **It is measured here, after the validation above, and not before.**
	// libutp reaches `conn->reply_micro = their_delay` only once the packet
	// has passed the acknowledgement-number rule (:1794-1807) and the
	// reorder-window check (:1886-1899); a packet either of those rejects
	// never touches it. This did it first thing, so a packet the reference
	// discards still changed what we echoed to the peer -- which is the
	// peer's whole delay signal, and reachable by anyone who can guess a
	// connection id and send a packet bad enough to be dropped.
	//
	// Found by FuzzDifferentialResponder once the clock became injectable:
	// libutp's ack carried the *previous* packet's delay where ours carried
	// the rejected one's. Invisible while the timestamp fields were excluded
	// from comparison.
	// Not capped. This used to pin anything above MaxIdleTimeout to exactly
	// one second, which was protection against the defect described above --
	// the wrong field, producing ~1.79e15 on every packet -- and outlived it.
	//
	// libutp echoes `time - p` raw, however it wraps (utp_internal.cpp:2000).
	// A peer whose clock is ahead of ours produces a value that wraps to
	// something enormous, and that is what the reference passes on; the
	// receiving end has its own rule for it, not the sending end. Capping
	// here put a number on the wire that no libutp would have sent, and
	// FuzzDifferentialResponder showed it the moment the timestamp fields
	// could be compared: ours 1000000 against libutp's 3487502864.
	//
	// A zero timestamp is no timestamp: `their_delay = (p == 0 ? 0 : time -
	// p)` (:2000), and a zero is neither echoed as a delay nor sampled
	// (:2002). This used to echo the whole of our clock back.
	peerStamp := uint32(packet.Header.Timestamp)
	// One reading of the microsecond clock for both uses below.
	var nowMicros uint32
	if peerStamp == 0 {
		c.peerTsDiff = 0
	} else {
		nowMicros = c.nowMicros()
		c.peerTsDiff = timestampDiffMicros(nowMicros, peerStamp)
	}
	// The delay in the peer's direction is not a congestion signal for this
	// sender -- it describes the other half of the path -- but it is how
	// clock drift between the two ends becomes visible. libutp feeds its
	// `their_hist` from exactly this value, on every packet that passes the
	// checks above (utp_internal.cpp:2000-2004).
	//
	// The *uncapped*, raw wrapping value is what goes to the controller,
	// which is what libutp feeds its their_hist (`their_delay`, :2000-2004).
	// The cap above exists to stop an unusable clock reading being echoed
	// back to the peer, and applying it here would break the drift detection
	// at exactly the point it starts to matter: a clock drifting slow makes
	// this measurement shrink until it passes zero and wraps, and a wrapped
	// value capped to one second is a base that has stopped moving. See
	// peerDelayHist.
	if c.state != nil && c.state.SentPackets != nil && peerStamp != 0 {
		c.state.SentPackets.OnPeerDelay(
			wrappingSubUint32(nowMicros, peerStamp), now)
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
		// INT_MAX is the peer saying it has no measurement, not a delay of
		// 35 minutes. libutp:
		//
		//	const uint32 actual_delay = (uint32(pf1->reply_micro)==INT_MAX?0:uint32(pf1->reply_micro));
		//	                                        (utp_internal.cpp:2017)
		//
		// Without this the value goes into the delay history as a sample,
		// where being a maximum it changes nothing for the base but reports a
		// 35-minute queue for as long as it is the current sample.
		replyMicros := packet.Header.TimestampDiff
		if replyMicros == math.MaxInt32 {
			replyMicros = 0
		}
		delay := time.Duration(replyMicros) * time.Microsecond
		// The peer's timestamp less the delay it measured is the timestamp of
		// the packet of ours it last received, in our own clock: the two
		// clocks' offset cancels, and holding the acknowledgement back only
		// makes it later. See probeAnswered.
		c.ackEchoKnown = replyMicros != 0
		c.ackEcho = uint32(packet.Header.Timestamp) - replyMicros
		// Before the acknowledgement retires anything: libutp counts
		// duplicates against the window as it stood when the packet arrived
		// (utp_internal.cpp:1921, with ack_packet not reached until :2194).
		c.noteDuplicateAck(packet.Header.PacketType, packet.Header.AckNum)
		err = c.processAck(packet.Header.AckNum, packet.Eack, delay, now)
		if err == nil && packet.Eack != nil {
			c.noteSelectiveAckCount(packet.Header.AckNum, packet.Eack)
		}
		c.onFastTimeout(now)
		c.rearmLossProbe(packet.Header.AckNum)
		if err != nil {
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
				// Built as libutp's send_rst builds every reset, and as the
				// socket's own does: no timestamps, no window, and the
				// sequence number of the packet that drew it
				// (utp_internal.cpp:846-865). This one used to carry a
				// timestamp and a 100000-byte window.
				randSeqNum := RandomUint16()
				resetPacket := NewPacketBuilder(st_reset, packet.Header.ConnectionId, 0, 0, randSeqNum).
					WithAckNum(packet.Header.SeqNum).Build()
				c.emit(resetPacket)
			}
		} else {
			// A SYN for a connection we have already accepted is the initiator
			// retransmitting because our SYN-ACK was lost. Re-send the same
			// SYN-ACK: nothing else ever retransmits it, so dropping this
			// duplicate silently strands the peer until it gives up.
			//
			// libutp does not do this. A SYN whose connection already exists
			// is dropped in utp_process_udp (utp_internal.cpp:2955-2958), and
			// a libutp acceptor whose SYN-ACK was lost answers none of the
			// retries. This comment used to say the opposite, citing a line
			// in the socket destructor. DEVIATIONS.md, "A retransmitted SYN
			// is answered"; TestConformanceDuplicateSynBeforeData.
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
		if c.dropUnacked {
			break
		}
		if statePacket := c.statePacket(); statePacket != nil {
			// Kept so the socket can send it again if the peer retransmits
			// this FIN after the connection has gone -- see lingerAck. It has
			// to be captured here: by the time the connection shuts down,
			// statePacket() has nothing left to build one from, which is the
			// same reason the acknowledgement above cannot be deferred.
			c.finAck = statePacket
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
		if !c.dropUnacked {
			c.ackPending = true
			c.dataSinceAck++
			if !c.lastDataAt.IsZero() {
				gap := now.Sub(c.lastDataAt)
				c.lastDataGap = gap
				if c.dataGap == 0 {
					c.dataGap = gap
				} else {
					c.dataGap += (gap - c.dataGap) / 8
				}
				c.lastDataAt = now
			} else {
				c.lastDataAt = now
			}
		}
	}

	// Wake the writer on any packet that carries an acknowledgement, not only
	// on ST_STATE. libutp makes the socket writable again whenever an incoming
	// packet leaves the window below full, before the early return that is
	// specific to ST_STATE (utp_internal.cpp:2302-2308), so its embedder
	// writes on the acknowledgements a data packet carries as well.
	//
	// This woke only for ST_STATE. On a connection carrying data both ways,
	// the peer's acknowledgements ride on its data packets -- a data packet
	// carrying the pending acknowledgement replaces the ST_STATE (send_data,
	// :768) -- so the window opened and nothing used it until a timer
	// happened to run. Measured: two-way transfers of 1 MB each way over
	// 20 Mb/s, 20 ms and 5% loss took about 36 s in 3 runs of 15, against a
	// 5 s median, one direction sitting idle with 1 MB queued, nothing in
	// flight and a window that had room, until the idle timeout fired.
	switch packet.Header.PacketType {
	case st_state, st_data, st_fin:
		c.wantWrite = true
	}

	// Data or a FIN is handed up by processReads, which every pass runs.

	c.updateClosingState()
}

// updateClosingState decides whether the connection has finished.
//
// It is called both when a packet arrives and once per pass of the event loop.
// Only the first of those used to happen, and the second is not decoration: the
// side that closes second sends its FIN into a peer whose connection is
// already gone, so nothing ever arrives to trigger the check again. Measured
// when it was packet-driven only -- 29 connections still tracked after 60
// closed cycles and 48 goroutines grown, every one of them that second closer.
func (c *connection) updateClosingState() {
	// Handle connection closing cases
	if c.state.stateType == ConnConnected && c.state.closing != nil && c.state.closing.LocalFin != nil {
		if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
			c.logger.Trace("try close connection locally...", "dst.Peer", c.cid.Peer, "dst.Send", c.cid.Send, "dst.Recv", c.cid.Recv)
		}
		lastAckNum, isNone := c.state.SentPackets.LastAckNum()
		// Only when the application asked to close entirely. A FIN sent by
		// CloseWrite is acknowledged like any other packet and means nothing
		// about the reading side, which is still someone's (:2178-2182).
		if !isNone && lastAckNum == *c.state.closing.LocalFin && c.closeRequested {
			c.state.stateType = ConnClosed
			c.state.Err = nil
		}
	}

	// A connection whose peer has closed its sending side is *not* finished.
	//
	// This used to move to ConnClosed as soon as the remote FIN was reached
	// and everything sent had been acknowledged -- without this end ever
	// sending a FIN of its own, and whatever its application was doing. That
	// is the half-close, and refusing it was the one place libutp could do
	// something an application here could not: a peer closing its sending side
	// took the whole connection with it, its own unread data included.
	//
	// libutp holds the socket in CS_GOT_FIN and keeps delivering
	// (utp_internal.cpp:2314 drops only what arrives past the FIN it has
	// reached), until its application closes too. So does this now: the reader
	// gets its end-of-stream marker from processReads when the receive buffer
	// catches up to the FIN, and the writer carries on.
	//
	// Once the application *has* closed, though, this end is finished the
	// moment everything it sent has been acknowledged. It does not wait for
	// its own FIN to come back, and that is not laziness: the peer closed
	// first, so by the time this FIN goes out the peer's connection is
	// generally gone and nothing is left to acknowledge it. libutp reaches the
	// same place by a different route -- the peer's socket answers the
	// unmatched FIN with a RESET, and a socket with close_requested treats a
	// RESET as a clean destroy rather than an error
	// (utp_internal.cpp:2865-2868). This library cannot use that route,
	// because its socket deliberately stays silent for a packet past a closed
	// connection's FIN so that a peer doing a half-close is not told its
	// connection broke. Ending here instead reaches the same state without
	// needing the RESET.
	//
	// Removing this entirely was the first attempt at the half-close, and the
	// soak test caught it: 29 connections still tracked after 60 closed
	// cycles, 48 goroutines grown, all of them the side that closed second.
	// LocalFin != nil is doing real work in this condition. shutdown() only
	// emits the FIN once the send buffer and the pending writes have drained,
	// so its presence is the proof that there is nothing left to send. Without
	// it, "nothing unacknowledged" is true at every momentary gap between a
	// window emptying and the next packet going out -- and closing there
	// discards the rest. Measured: the half-close test stopped delivering its
	// 512 KB reply, because Write returns when the data is buffered and the
	// application closed straight after it.
	if c.state.stateType == ConnConnected && c.state.closing != nil &&
		c.state.closing.RemoteFin != nil && c.state.closing.LocalFin != nil &&
		c.closeRequested {
		if !c.state.SentPackets.HasUnackedPackets() &&
			c.state.RecvBuf.AckNum() == *c.state.closing.RemoteFin {
			c.processReads()
			c.state.stateType = ConnClosed
			c.state.Err = nil
		}
	}

	// Both sides have sent a FIN. That still does not end it on its own: the
	// application may not have read what arrived before the peer's FIN, and
	// tearing down here would discard it. The same close_requested rule
	// applies -- a connection whose application never closes ends on the idle
	// timeout, which is what libutp does with a socket its embedder forgets.
	if c.state.stateType == ConnConnected && c.state.closing != nil &&
		c.state.closing.RemoteFin != nil && c.state.closing.LocalFin != nil &&
		c.closeRequested {
		c.state.stateType = ConnClosed
		c.state.Err = nil
	}
}

// duplicateAcksBeforeResend is libutp's DUPLICATE_ACKS_BEFORE_RESEND
// (utp_internal.cpp:64).
const duplicateAcksBeforeResend = 3

// noteDuplicateAck counts a peer repeating the same acknowledgement, and uses
// the third one to conclude that an outstanding MTU probe was too big.
//
// libutp: utp_internal.cpp:1921-1941. This is the second of its two routes to
// lowering the MTU ceiling, and the only one that works during a bulk
// transfer. The other needs the probe to be the only packet outstanding
// (:1152-1167, and conn.go's retransmission timeout), which a saturated send
// window never leaves it -- so without this a probe dropped for being too big
// is retransmitted fragmentable, arrives, is acknowledged, and the search
// concludes the size was fine. Measured before this existed: on a 1000-byte
// path with a 1400-byte ceiling, the search settled at 1384 whether or not the
// probe was refused.
//
// Three rules, all libutp's:
//
//   - Only bare ST_STATE packets count. libutp is emphatic about why
//     (:1911-1920): an ST_DATA carrying an acknowledgement was most likely sent
//     because the peer had data of its own, not in response to anything, so a
//     bidirectional connection would otherwise see three "duplicates"
//     immediately after every payload packet it sent.
//   - The acknowledgement must repeat LastAckedSeqNum, the number just before
//     the oldest outstanding packet. Anything else resets the count.
//   - Nothing is counted while nothing is outstanding, and the count is not
//     reset then either -- libutp wraps the whole block in
//     `if (cur_window_packets > 0)`.
//
// On the third such acknowledgement, with a probe outstanding, there are two
// cases and libutp draws a different conclusion from each. If the repeated
// number is the one just before the probe, the probe is the hole: the ceiling
// drops below it. If it is anything else, some other packet was lost ahead of
// the probe, which says nothing about size -- the probe is forgotten so
// another can be sent, and the ceiling is left alone.
//
// It acts once per run of duplicates -- a run ends when the acknowledgement
// moves -- on the first duplicate that finds the count at three or more. That
// is the one place this differs from libutp, whose test is equality with three
// (`duplicate_ack == DUPLICATE_ACKS_BEFORE_RESEND`, :1928). The selective ack
// sets the count (noteSelectiveAckCount), and an acknowledgement covering a
// read batch can name more than three later packets at once: the count jumps
// past three without ever equalling it, and libutp never concludes anything
// from that run. Its own embedders batch reads that way, and so does ours on a
// real socket. Measured over netem with a receiver that batches as a real
// socket does: TestDontFragmentBringsTheSearchWithinThePath failed 9 runs in
// 10 under the equality test, the search never once hearing that a probe was
// refused. Three or more is the same evidence -- at least three packets after
// the hole arrived and the hole did not -- that libutp's selective ack already
// resends on (`count >= DUPLICATE_ACKS_BEFORE_RESEND`, :1538).
func (c *connection) noteDuplicateAck(packetType PacketType, ackNum uint16) {
	if c.state.stateType != ConnConnected || c.state.SentPackets == nil {
		return
	}
	if c.state.SentPackets.UnackedCount() == 0 {
		return
	}
	if packetType != st_state || ackNum != c.state.SentPackets.LastAckedSeqNum() {
		c.duplicateAcks = 0
		c.duplicatesJudged = false
		return
	}

	c.duplicateAcks++
	c.judgeProbeFromDuplicates(ackNum)
}

// judgeProbeFromDuplicates draws libutp's conclusion about an outstanding MTU
// probe once a run of duplicates has reached three. See noteDuplicateAck.
func (c *connection) judgeProbeFromDuplicates(ackNum uint16) {
	if c.duplicateAcks < duplicateAcksBeforeResend || c.duplicatesJudged || !c.mtu.probing {
		return
	}
	c.duplicatesJudged = true

	if ackNum == c.mtu.probeSeq-1 {
		c.mtu.onProbeLost(c.now())
		c.mtuProbesLostToDuplicateAcks++
		if c.logger.Enabled(BASE_CONTEXT, log.LevelDebug) {
			c.logger.Debug("MTU probe presumed too big, from duplicate acks",
				"floor", c.mtu.floor, "ceiling", c.mtu.ceiling, "current", c.mtu.current,
				"probeSeq", c.mtu.probeSeq)
		}
		return
	}
	// A packet ahead of the probe was lost. Nothing follows about size.
	c.mtu.clearProbe()
}

// noteSelectiveAckCount is the last thing libutp's selective_ack does:
//
//	duplicate_ack = count;          (utp_internal.cpp:1612)
//
// where count is the number of bits the selective ack sets for packets inside
// the send window other than its oldest (:1454-1461) -- counted whether or not
// those packets were already acknowledged, and not at all when the
// acknowledgement has emptied the window (:1440). The next duplicate
// acknowledgement then counts on from there, so on a path where each
// acknowledgement's selective ack names more packets than the last, the third
// "duplicate" that judges an MTU probe too big (noteDuplicateAck) comes
// sooner than three repeats would -- or, once the count has passed three, not
// at all until an acknowledgement that moves resets it.
//
// It runs for every packet type that carries the extension, as libutp's
// does, and after the acknowledgement has been applied, which is where
// libutp's window stands when it counts (:2289, after :2194).
//
// A count that reaches three judges an outstanding probe there and then,
// which libutp does not do: see noteDuplicateAck.
func (c *connection) noteSelectiveAckCount(ackNum uint16, sack *SelectiveAck) {
	sp := c.state.SentPackets
	if c.state.stateType != ConnConnected || sp == nil {
		return
	}
	window := sp.UnackedCount() // libutp's cur_window_packets
	if window == 0 {
		return
	}
	next := sp.NextSeqNum() // libutp's seq_nr
	count := uint32(0)
	for i, set := range sack.Acked() {
		v := ackNum + 2 + uint16(i)
		if set && next-v-1 < window-1 {
			count++
		}
	}
	c.duplicateAcks = count
	if ackNum == sp.LastAckedSeqNum() {
		c.judgeProbeFromDuplicates(ackNum)
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

	// An acknowledgement that names nothing new and carries no selective
	// ack -- every data packet a receiver with nothing in flight is sent --
	// still gives the delay sample libutp takes from every packet
	// (our_hist, utp_internal.cpp:2116-2127), and the MTU search still
	// checks whether it is due. Nothing else below has work to do, and on a
	// reader just woken, walking it was 3.5-4us of each packet's path to its
	// acknowledgement.
	//
	// Except a timer still armed for that number: an initiator's SYN, whose
	// timer the first acknowledgement after the handshake retires. That one
	// takes the full path.
	if sp := c.state.SentPackets; selectiveAck == nil && ackNum == sp.base-1 && !c.isArmed(ackNum) {
		sp.onAckOfNothingNew(delay, now)
		c.mtu.onAck(ackNum, now)
		if c.mtu.dueForSearch(now) {
			c.mtu.research(uint32(c.config.MaxPacketSize), now)
		}
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
			// An acknowledgement number outside what this connection has
			// outstanding acknowledges nothing. It does not end the
			// connection.
			//
			// libutp computes how many packets an acknowledgement covers and
			// then throws the answer away when it exceeds the window:
			//
			//	int acks = (pk_ack_nr - (conn->seq_nr - 1 - conn->cur_window_packets)) & ACK_NR_MASK;
			//	// this happens when we receive an old ack nr
			//	if (acks > conn->cur_window_packets) acks = 0;
			//	                                (utp_internal.cpp:1904-1907)
			//
			// This used to call reset, which is the divergence
			// CONFORMANCE.md records as one the packet-injection corpus
			// cannot see: both sides answer such a packet with silence, so
			// the transcripts match while one connection is dead and the
			// other is not.
			//
			// Two reasons it had to go. It is an attack surface -- a single
			// forged ST_STATE from anyone who can guess a connection id ends
			// an established transfer -- and it is a live defect: a delayed
			// or duplicated acknowledgement arriving after the window has
			// moved on is an ordinary event on a lossy path, and it killed
			// connections that should have carried on. It is what made
			// integrated.TestCloseSucceedsIfOnlyFinAckDropped fail about one
			// run in eight, reporting "invalid ack number" where the test
			// expects the idle timeout.
			//
			// The selective-ack extension on such a packet is dropped with
			// it, where libutp would still apply it. That is deliberate and
			// narrow: our extension is applied relative to this same
			// acknowledgement number, so honouring it would mean indexing the
			// loss-detection machinery off a figure just established to be
			// outside the window.
			c.logger.Debug("ignoring an acknowledgement outside the window",
				"ackNum", ackNum, "seqStart", seqRange.start, "seqEnd", seqRange.end,
				"cid.send", c.cid.Send, "cid.recv", c.cid.Recv)
			return nil
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
		c.mtu.research(uint32(c.config.MaxPacketSize), now)
	}

	retired := c.disarmAcked(fullAcked)
	recovering := c.probeAnswered(fullAcked, now)
	c.checkSpuriousLoss(fullAcked, selectedAcks)

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
	//
	// One deliberate difference: libutp's ack_packet also runs for a packet
	// acknowledged selectively (:1529), so a selective ack moves its deadline
	// too. Here only the cumulative acknowledgement does. Adopting libutp's
	// rule was measured and was slower -- see DEVIATIONS.md, "A selective
	// ack does not restart the retransmission timeout".
	//
	// Not when this is the loss probe's acknowledgement and it has shown
	// other packets lost: probeAnswered has started resending them, and the
	// timeout stays where it was, so the probe never makes recovery wait
	// longer than libutp's timeout would. Restarted here, each probe that
	// repaired one hole pushed the timeout back, and a burst came back one
	// packet per probe timeout.
	if retired > 0 && !recovering {
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
		// The SYN's round trip is the first RTT sample, as it is in libutp:
		// the SYN sits in its outgoing buffer like any packet, and the
		// SYN-ACK acknowledges it through the same ack_packet, which samples
		// a packet sent once (`if (pkt->transmissions == 1)`,
		// utp_internal.cpp:1362). A SYN sent more than once gives no sample,
		// because the answer cannot be matched to one transmission.
		//
		// Without it a connection's first timeout was the 3-second initial
		// value where libutp's was computed from the handshake -- 1 second on
		// any path under about 330ms. Measured against libutp:
		// TestConformanceRetransmissionTimeoutComputation.
		if c.endpoint.Attempts == 1 && !c.synSentAt.IsZero() {
			congestionCtrl.OnRTTSample(c.now().Sub(c.synSentAt))
		}
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
				// Numbered past the end of the stream: dropped, unacknowledged,
				// and the connection carries on. libutp: `if (conn->got_fin &&
				// pk_seq_nr > conn->eof_pkt) return 0;` (utp_internal.cpp:
				// 2381-2386). This used to close the connection with
				// ErrInvalidSeqNum, which handed anyone able to guess a
				// connection id a way to end it once the peer's FIN was in.
				//
				// libutp compares the two numbers without wrapping; this
				// range does wrap, so a stream whose FIN is numbered just
				// past 65535 is not cut short by it.
				c.dropUnacked = true
				return nil
			}
		}
		// The application has finished reading, so the bytes go nowhere --
		// but the sequence number still has to advance, or the peer is
		// left retransmitting a packet that arrived. libutp runs
		// `conn->ack_nr++` outside the `!read_shutdown` guard for exactly
		// this reason (utp_internal.cpp:2344-2355).
		//
		// A zero-length write is how that is expressed here: the receive
		// buffer records the sequence number, copies nothing, and consumes
		// no space, so the advertised window stays open. Out-of-order
		// packets still reorder correctly -- an empty entry fills its gap
		// like any other.
		if c.readShutdown {
			data = nil
		}
		// An out-of-order packet that is already held is discarded without
		// an acknowledgement: libutp returns before schedule_ack
		// (utp_internal.cpp:2425-2431). An in-order duplicate never gets here
		// -- it is behind the acknowledgement number, and
		// outsideReorderWindow re-acknowledges it, as libutp does (:1891).
		if seqNum != c.state.RecvBuf.AckNum()+1 && c.state.RecvBuf.HoldsOutOfOrder(seqNum) {
			c.dropUnacked = true
			return nil
		}
		// not closing should send data
		if len(data) <= c.state.RecvBuf.Available() {
			err := c.state.RecvBuf.Write(data, seqNum)
			if err != nil {
				c.recvBufferDrops++
				c.logger.Warn("write data to recv buffer, but available space is not enough",
					"src.peer", c.cid.Peer, "seqNum", seqNum, "data.len", len(data))
			}
			if c.logger.Enabled(BASE_CONTEXT, log.LevelTrace) {
				c.logger.Trace("write data to recv buffer",
					"src.peer", c.cid.Peer, "seqNum", seqNum, "data.len", len(data), "nextAckNum", c.state.RecvBuf.AckNum())
			}
			return err
		}
		if !c.state.RecvBuf.WasWritten(seqNum) {
			c.recvBufferDrops++
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
		// A FIN may carry a final payload, and it reaches the application by
		// the same route as any other, so a shut-down read side drops it the
		// same way. The sequence number still has to be registered: it is
		// what marks the end of the stream.
		if c.readShutdown {
			data = nil
		}
		if c.state.closing != nil {
			if c.state.closing.RemoteFin != nil {
				// If we have already received a FIN, a subsequent FIN with a different
				// sequence number is incorrect behavior
				if seqNum != *c.state.closing.RemoteFin {
					// The end of the stream is the first FIN's. A later one
					// with another number is handled as data: libutp records
					// eof_pkt only `if (pk_flags == ST_FIN && !conn->got_fin)`
					// (utp_internal.cpp:2316) and the packet then takes the
					// data path below it. This used to reset the connection.
					return c.onData(seqNum, data)
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

	if c.state.stateType == ConnClosed {
		return
	}
	// A reset answering our SYN is a refusal: nothing on the other end will
	// take the connection. libutp means to say so --
	//
	//	if (conn->close_requested) conn->state = CS_DESTROY;
	//	else conn->state = CS_RESET;
	//	const int err = (conn->state == CS_SYN_SENT) ? UTP_ECONNREFUSED : UTP_ECONNRESET;
	//	                                        (utp_internal.cpp:2865-2870)
	//
	// -- but it overwrites the state before testing it, so the refusal is
	// unreachable and every reset reads as one. go-utp's pure Go port
	// corrects the same line. Its ICMP path, which tests the state first
	// (onICMPUnreachable), already distinguished the two here.
	err := ErrReset
	if c.state.stateType == ConnConnecting && c.endpoint.Type == Initiator {
		err = ErrConnRefused
	}
	c.reset(err)
}

// onCloseRead applies libutp's read_shutdown: from here on, what arrives is
// acknowledged and dropped rather than delivered. See UtpStream.CloseRead for
// why the acknowledgement half matters.
func (c *connection) onCloseRead() {
	if c.readShutdown {
		return
	}
	c.readShutdown = true
	c.logger.Debug("read side closed; incoming data will be acknowledged and discarded",
		"cid.send", c.cid.Send, "cid.recv", c.cid.Recv)

	// Whatever is already buffered is owed to a reader that has said it will
	// not come back, and holding it would keep the advertised window closed
	// by exactly that much.
	if c.state.RecvBuf != nil {
		discarded := c.state.RecvBuf.Readable()
		if discarded > 0 {
			buf := make([]byte, discarded)
			c.state.RecvBuf.Read(buf)
		}
	}
}

// onICMP applies an ICMP report about a packet this connection sent.
//
// libutp's two entry points, utp_process_icmp_fragmentation and
// utp_process_icmp_error (utp_internal.cpp:3079-3150), differ only in what
// they do once the connection is found, so the lookup lives in the socket and
// the two decisions live here.
func (c *connection) onICMP(notice *icmpNotice, now time.Time) {
	if notice == nil {
		return
	}
	switch notice.Kind {
	case icmpFragmentationNeeded:
		c.onICMPFragmentationNeeded(notice.NextHopMTU, now)
	case icmpUnreachable:
		c.onICMPUnreachable()
	}
}

// onICMPFragmentationNeeded lowers the path-MTU ceiling on a router's say-so.
// See mtuSearch.icmpFragmentationNeeded for the libutp text and for the one
// deliberate difference, which is a unit conversion.
func (c *connection) onICMPFragmentationNeeded(nextHopMTU uint32, now time.Time) {
	c.mtu.icmpFragmentationNeeded(nextHopMTU, now)
	// libutp logs exactly this line (utp_internal.cpp:3104).
	c.logger.Debug("MTU [ICMP]", "floor", c.mtu.floor, "ceiling", c.mtu.ceiling,
		"current", c.mtu.current, "nextHopMTU", nextHopMTU)
}

// onICMPUnreachable tears the connection down after an ICMP error.
//
// libutp:
//
//	const int err = (conn->state == CS_SYN_SENT) ? UTP_ECONNREFUSED : UTP_ECONNRESET;
//	switch(conn->state) {
//	    case CS_IDLE: return 1;                    // don't pass on errors for idle/closed
//	    default:
//	        conn->state = conn->close_requested ? CS_DESTROY : CS_RESET;
//	}
//	utp_call_on_error(conn->ctx, conn, err);
//	                                        (utp_internal.cpp:3117-3150)
//
// Two mappings are worth stating.
//
// CS_IDLE has no counterpart: libutp's socket object exists before connect or
// accept is called, and ours does not -- the connection is created by the
// call. The nearest thing is a connection that has already finished, and that
// is skipped for the same stated reason ("don't pass on errors for
// idle/closed connections").
//
// CS_DESTROY and CS_RESET both land on ConnClosed here, because that is the
// only terminal state this connection has. The distinction libutp draws is
// how soon the socket object goes away, not what the application is told:
// both branches fall through to the same utp_call_on_error, so the error is
// reported either way.
func (c *connection) onICMPUnreachable() {
	if c.state.stateType == ConnClosed {
		return
	}

	err := ErrReset
	// CS_SYN_SENT is reachable only by the initiator, and only before the
	// handshake completes.
	if c.state.stateType == ConnConnecting && c.endpoint.Type == Initiator {
		err = ErrConnRefused
	}

	c.logger.Warn("ICMP error, closing connection", "err", err,
		"closeRequested", c.closeRequested, "peer", c.cid.Peer)

	// Deliberately not c.reset(): that treats a reset arriving after our own
	// FIN as a clean close and discards the error. libutp has no such branch
	// on this path -- it reports the error whatever state the connection was
	// in -- and the difference is load-bearing, because "the peer went away"
	// is exactly what an application half way through a close needs to hear.
	// A connection still in the handshake has a Connect call waiting on this
	// channel; without it the caller waits out its own context for a failure
	// the network already reported. Same handling as the connect-attempt
	// timeout at onTimeout.
	if c.state.connectedCh != nil {
		c.state.connectedCh <- err
		c.state.connectedCh = nil
	}

	c.state.stateType = ConnClosed
	c.state.Err = err
}

// nowMicros reads this connection's wall clock, in the uint32 microseconds
// uTP puts on the wire.
//
// The fallback is deliberate rather than an oversight: a connection built as
// a struct literal -- which several tests do -- would otherwise carry a nil
// function and panic on its first packet. A clock is not optional behaviour
// that can be left unset; only its *source* is configurable.
// now is this connection's current time.
//
// The fallback mirrors nowMicros: several tests build a connection as a
// struct literal, and a nil clock would panic on the first deadline
// comparison. Time is not optional; only its source is.
func (c *connection) now() time.Time {
	if c.clk != nil {
		return c.clk.Now()
	}
	return time.Now()
}

// clock returns this connection's Clock, never nil.
func (c *connection) timeSource() Clock {
	if c.clk != nil {
		return c.clk
	}
	return RealClock
}

func (c *connection) nowMicros() uint32 {
	if c.clock != nil {
		return c.clock()
	}
	return NowMicro()
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

	// A Connect still waiting for the handshake learns why it failed. This
	// did not: a SYN answered with a reset closed the connection and left
	// the dial to wait out its own context, where libutp reports the error
	// at once (utp_call_on_error, utp_internal.cpp:2870). The timeout and
	// ICMP paths already signalled it; see onICMPUnreachable. Only during the
	// handshake: afterwards nobody waits on the channel, and its one slot may
	// still hold the success already sent.
	if c.state.stateType == ConnConnecting && c.state.connectedCh != nil {
		select {
		case c.state.connectedCh <- err:
		default:
		}
		c.state.connectedCh = nil
	}
	c.state.stateType = ConnClosed
	c.state.Err = err
}

func (c *connection) synPacket(seqNum uint16) *packet {
	nowMicros := int64(c.nowMicros())
	return NewPacketBuilder(
		st_syn,
		c.cid.Recv,
		uint32(nowMicros),
		c.config.WindowSize,
		seqNum,
	).Build()
}

// dataPacketsCarryNoSelectiveAck documents a rule rather than implementing
// one: only an ST_STATE carries a selective ack. libutp attaches the extension
// in send_ack alone (`pfa.pf.ext = 1`, utp_internal.cpp:795); a data packet,
// a FIN and every resend go out with `ext = 0` (:1080, :2781), and its
// packet size leaves room for the 20-byte header and nothing else (:1757-
// 1761). This library used to attach the receive buffer's selective ack to
// all of them, and to reserve room for it in every packet.
//
// On a connection carrying data both ways, that means the peer hears about a
// hole in what it sent only from an ST_STATE -- and a data packet that goes
// out carrying the same acknowledgement number cancels the pending one
// (send_data, :768; transmit here). libutp lives with that, and so does this
// library now.
const dataPacketsCarryNoSelectiveAck = true

func (c *connection) statePacket() *packet {
	now := int64(c.nowMicros())
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
		recvWindow := c.recvWindow()

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
	nowMicros := int64(c.nowMicros())
	recvWindow := c.recvWindow()
	tsDiffMicros := uint32(c.peerTsDiff.Microseconds())

	for _, lostPacket := range c.state.SentPackets.TakeLostPackets() {
		seqNum := lostPacket.SeqNum
		packetType := lostPacket.PacketType
		payload := lostPacket.Data

		builder := NewPacketBuilder(packetType, connID, uint32(nowMicros), recvWindow, seqNum)
		if payload != nil {
			builder.WithPayload(payload)
		}

		// No selective ack: see dataPacketsCarryNoSelectiveAck.
		packetInst := builder.
			WithTsDiffMicros(tsDiffMicros).
			WithAckNum(c.state.RecvBuf.AckNum()).
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
		// Not a first transmission: a fast retransmission must never be taken
		// as an MTU probe. libutp requires `pkt->transmissions == 0`
		// (utp_internal.cpp:911) and says why in the comment above it -- a
		// packet larger than the ceiling "was probably used as a probe already
		// and failed, now we need it to fragment just to get it through".
		//
		// Passing true here made loss shrink the path MTU: a fast
		// retransmission could be adopted as the probe, and a packet that had
		// already been lost once is a poor bet to arrive, so onProbeLost would
		// lower the ceiling on evidence about congestion rather than about
		// size. The RTO retransmission path already passes false.
		c.transmit(packetInst, now, false)
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
	if !firstTransmission {
		c.noteResend(packet)
	}

	// Use this packet as an MTU probe if the search wants one.
	//
	// libutp decides the same thing in send_packet (utp_internal.cpp:906-925)
	// and sends the probe with fragmentation disabled, by passing
	// UTP_UDP_DONTFRAG to its embedder's sendto callback (:925-929). This
	// carries the same bit the same way: the flag travels with the datagram to
	// the socket, which asks the Conn to honour it if it can.
	//
	// A Conn that cannot -- the libutp driver, an emulated network that does
	// not model fragmentation, an operating system with no such socket option
	// -- sends it normally, which is what every probe did before this existed.
	// The cost of that is real and is why the bit is worth carrying: on IPv4 a
	// router may fragment an oversized probe rather than drop it, so the probe
	// is acknowledged, the floor rises, and the search settles on a size that
	// works only because every packet at it is being fragmented.
	isProbe := false
	if datagramSize := uint32(packet.EncodedLen()); c.mtu.eligibleProbe(datagramSize, firstTransmission) {
		c.mtu.beginProbe(packet.Header.SeqNum, datagramSize)
		isProbe = true
		if c.logger.Enabled(BASE_CONTEXT, log.LevelDebug) {
			c.logger.Debug("MTU probe", "size", datagramSize,
				"floor", c.mtu.floor, "ceiling", c.mtu.ceiling, "seq", packet.Header.SeqNum)
		}
	}

	c.state.SentPackets.OnTransmit(packet.Header.SeqNum, packet.Header.PacketType, payload, length, now)
	c.armRetransmit(packet, c.state.SentPackets.Timeout())

	// A packet carrying the current acknowledgement -- and the current
	// selective ack, which every packet this connection builds carries --
	// says everything a deferred ST_STATE would have, so that one is no
	// longer owed. libutp takes the socket off its ack list whenever it sends
	// anything (send_data, utp_internal.cpp:768).
	//
	// It rarely applies here: data received and data sent mostly fall in
	// different event-loop passes. Measured in a two-way transfer against
	// libutp, before socket reads were batched, pure acknowledgements per data
	// packet were 0.90 before and after it (DEVIATIONS.md, "Acknowledgements:
	// one per read, fewer when they would crowd the way back").
	if c.state.RecvBuf != nil && packet.Header.AckNum == c.state.RecvBuf.AckNum() {
		c.ackPending = false
		c.ackHeldSince = time.Time{}
		c.dataSinceAck = 0
	}

	c.emitPacket(packet, isProbe)
}
