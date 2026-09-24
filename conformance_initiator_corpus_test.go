//go:build cgo

package utp_go

import (
	"context"
	"testing"
	"time"

	"github.com/zen-eth/utp-go/native/libutp"
)

// The initiator corpus.
//
// Every curated case in this repository drove both implementations as the
// side *accepting* a connection. The initiator role -- we send the SYN, the
// peer answers -- was covered only by the differential fuzzer, which
// generates its inputs rather than naming them, and which discards the
// handshake before it starts comparing.
//
// That is the direction a BitTorrent client is exposed to on every dial: it
// opens connections to addresses from a tracker or the DHT constantly, and
// whatever answers is under no obligation to be friendly. It is also the role
// in which this library, not its peer, chooses the sequence numbers and the
// connection ids -- so a divergence here is one nothing else can produce.
//
// These cases run on a virtual clock. Both implementations are driven step by
// step with time moved by the same amount on both sides, so an `advance` is
// exact rather than a sleep, and every comparison is deterministic rather
// than dependent on when a goroutine happened to run.

// initiatorRun holds both implementations mid-conversation.
type initiatorRun struct {
	t   *testing.T
	drv *libutp.Driver
	clk *virtualClock
	// streamCh carries the connected stream once the handshake completes.
	// A channel rather than a field: it is written by the goroutine running
	// ConnectWithCid and read by the test, and a plain field would be a data
	// race the race detector finds on the first run.
	streamCh <-chan *UtpStream
	stream   *UtpStream
	conn     *scriptedConn
	sock     *UtpSocket
	cancel   context.CancelFunc
	unpin    func()
	start    time.Time
}

func (r *initiatorRun) close() {
	// The stream is deliberately not closed. Closing waits out the flush
	// stall timeout -- two real seconds -- because the scripted peer never
	// acknowledges the FIN, and every case here would pay it. Cancelling the
	// context tears the connection down without that wait, and what these
	// cases assert is what was emitted, which has already been taken.
	r.sock.Close()
	r.cancel()
	r.unpin()
	r.drv.Close()
}

// newInitiatorRun opens a connection from both implementations and returns
// the SYN each of them sent.
//
// The SYNs are returned rather than discarded because comparing them is a
// case in itself, and one nothing else makes: the differential fuzzer clears
// the handshake before it begins, and the responder corpus never sends one.
func newInitiatorRun(t *testing.T) (*initiatorRun, [][]byte, [][]byte) {
	t.Helper()
	return newInitiatorRunMTU(t, 0)
}

// newInitiatorRunMTU is newInitiatorRun with the MTU libutp's driver reports
// set to udpMTU; zero leaves the driver's default, 1472.
func newInitiatorRunMTU(t *testing.T, udpMTU uint16) (*initiatorRun, [][]byte, [][]byte) {
	t.Helper()

	drv, err := libutp.NewDriver(1_000_000)
	if err != nil {
		t.Skipf("libutp driver unavailable: %v", err)
	}
	if udpMTU != 0 {
		drv.SetUDPMTU(udpMTU)
	}
	drv.PushRandom(uint32(initiatorConnSeed))

	start := time.Unix(0, int64(time.Duration(1_000_000)*time.Microsecond))
	clk := newVirtualClock(start)

	ctx, cancel := context.WithCancel(context.Background())
	unpin := pinRandom(initiatorConnSeed)

	conn := newScriptedConn()
	sock := WithSocket(ctx, conn, conformanceLogger(), WithClock(clk))

	cfg := NewConnectionConfig()
	cfg.Clock = clk
	cfg.NowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }

	cid := NewConnectionId(conn.peer, initiatorConnSeed, initiatorConnSeed+1)
	streamCh := make(chan *UtpStream, 1)
	go func() {
		s, err := sock.ConnectWithCid(ctx, cid, cfg)
		if err != nil {
			t.Logf("our ConnectWithCid: %v", err)
		}
		streamCh <- s
	}()

	r := &initiatorRun{
		t: t, drv: drv, clk: clk, conn: conn, sock: sock,
		cancel: cancel, unpin: unpin, start: start,
	}

	// Four participants register when the socket is built -- the
	// retransmission wheel and the read, write and event loops -- and the
	// connection's event loop is the fifth as soon as ConnectWithCid runs.
	clk.AwaitParticipants(5)
	clk.AwaitQuiet()
	ourSyn := conn.takeEmitted()

	if err := drv.Connect(); err != nil {
		t.Fatalf("libutp connect: %v", err)
	}
	libutpSyn := drv.Emitted()
	drv.ClearEmitted()

	r.streamCh = streamCh
	return r, ourSyn, libutpSyn
}

// step applies one action to both implementations and compares what each
// emitted.
func (r *initiatorRun) step(i int, st step) {
	r.t.Helper()

	// --- libutp ---
	if raw := st.rawBytes(); raw != nil {
		r.drv.Inject(raw)
	}
	if st.write != nil {
		if _, err := r.drv.Write(st.write); err != nil {
			r.t.Fatalf("step %d (%s): libutp write: %v", i, st.name, err)
		}
	}
	if st.closeStream {
		r.drv.CloseStream()
	}
	if st.advance > 0 {
		r.drv.Advance(uint64(st.advance.Microseconds()))
		r.drv.CheckTimeouts()
	}
	r.drv.IssueAcks()
	libutpOut := r.drv.Emitted()
	r.drv.ClearEmitted()

	// --- ours ---
	if raw := st.rawBytes(); raw != nil {
		r.clk.AwaitReactionTo(func() { r.conn.inject(raw) })
	}
	if st.write != nil {
		s := r.waitForStream()
		if s == nil {
			r.t.Fatalf("step %d (%s): no stream to write to", i, st.name)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		r.clk.AwaitReactionTo(func() { _, _ = s.Write(ctx, st.write) })
		cancel()
	}
	if st.closeStream {
		if s := r.waitForStream(); s != nil {
			r.clk.AwaitReactionTo(func() { go s.Close() })
		}
	}
	if st.advance > 0 {
		// The same amount of time, on a clock that moves only when told --
		// not a sleep that hopes the two sides end up in the same place.
		r.clk.Advance(st.advance)
	}
	r.clk.AwaitQuiet()
	oursOut := r.conn.takeEmitted()

	compareStep(r.t, i, st, oursOut, libutpOut)
}

// waitForStream returns the connected stream, or nil if the handshake never
// completed.
func (r *initiatorRun) waitForStream() *UtpStream {
	if r.stream != nil {
		return r.stream
	}
	select {
	case r.stream = <-r.streamCh:
	case <-time.After(5 * time.Second):
	}
	return r.stream
}

// runInitiatorCorpus opens a connection and runs the steps through both
// implementations, comparing every emission.
func runInitiatorCorpus(t *testing.T, steps []step) {
	t.Helper()
	r, ourSyn, libutpSyn := newInitiatorRun(t)
	defer r.close()

	// The SYN is step zero, and it is compared like any other emission.
	compareStep(t, -1, step{name: "our SYN"}, ourSyn, libutpSyn)

	for i, st := range steps {
		r.step(i, st)
	}
}

// initiatorFirstInOrder is the sequence number the dialling side expects
// first from its peer.
//
// The SYN-ACK carries sequence number 900 and, being a bare ST_STATE,
// consumes none: the recipient sets ack_nr to 899 and expects 900 next. Off
// by one here and every "in order" case is quietly exercising the
// out-of-order path instead -- which is what the first version of this file
// did, passing all the while.
const initiatorFirstInOrder = 900

// initiatorData builds a data packet from the peer, offset packets past the
// first one expected, acking our SYN.
func initiatorData(offset uint16, tsMicros uint32, body []byte) *packet {
	return NewPacketBuilder(st_data, initiatorConnSeed, tsMicros, corpusWindow,
		initiatorFirstInOrder+offset).
		WithAckNum(initiatorConnSeed).WithPayload(body).Build()
}

// The handshake, from the dialling side. The SYN we send and the
// acknowledgement we send once the peer answers.
func TestInitiatorHandshake(t *testing.T) {
	runInitiatorCorpus(t, []step{
		{
			name:   "peer answers the SYN",
			inject: nil,
			injectRaw: NewPacketBuilder(st_state, initiatorConnSeed, 150000, corpusWindow, 900).
				WithAckNum(initiatorConnSeed).Build().Encode(),
			// Neither side answers a bare acknowledgement of its SYN: there
			// is nothing to acknowledge and nothing to send.
			wantNoEmission: true,
		},
	})
}

// Data arriving on a connection we opened.
func TestInitiatorDataAndAck(t *testing.T) {
	runInitiatorCorpus(t, []step{
		{name: "peer answers the SYN", injectRaw: initiatorSynAck(), wantNoEmission: true},
		{name: "first data packet", inject: initiatorData(0, 200000, []byte("one")), wantAck: ackNum(initiatorFirstInOrder)},
		{name: "second data packet", inject: initiatorData(1, 210000, []byte("two")), wantAck: ackNum(initiatorFirstInOrder + 1)},
	})
}

// Out-of-order data on a connection we opened: the selective-ack path from
// the dialling side.
func TestInitiatorReordering(t *testing.T) {
	runInitiatorCorpus(t, []step{
		{name: "peer answers the SYN", injectRaw: initiatorSynAck(), wantNoEmission: true},
		{
			name:   "the second packet arrives first, leaving a gap",
			inject: initiatorData(1, 200000, []byte("two")),
			// The gap is not filled, so the ack stays behind it: still one
			// short of the first packet we are waiting for.
			wantAck: ackNum(initiatorFirstInOrder - 1),
		},
		{
			name:   "the gap is filled",
			inject: initiatorData(0, 210000, []byte("one")),
			// Both packets are now in order, so the ack jumps over both.
			wantAck: ackNum(initiatorFirstInOrder + 1),
		},
	})
}

// The same data twice, from the dialling side.
func TestInitiatorDuplicateData(t *testing.T) {
	runInitiatorCorpus(t, []step{
		{name: "peer answers the SYN", injectRaw: initiatorSynAck(), wantNoEmission: true},
		{name: "data", inject: initiatorData(0, 200000, []byte("one")), wantAck: ackNum(initiatorFirstInOrder)},
		{
			name:   "the same data again",
			inject: initiatorData(0, 210000, []byte("one")),
			// A duplicate advances nothing; the ack repeats.
			wantAck: ackNum(initiatorFirstInOrder),
		},
	})
}

// A peer that answers our SYN with a reset rather than an acknowledgement --
// what a closed port or a refusing client does.
func TestInitiatorResetInsteadOfSynAck(t *testing.T) {
	runInitiatorCorpus(t, []step{
		{
			name: "peer resets instead of answering",
			injectRaw: NewPacketBuilder(st_reset, initiatorConnSeed, 150000, corpusWindow, 900).
				WithAckNum(initiatorConnSeed).Build().Encode(),
			// A reset is never answered with a reset; both sides go quiet.
			wantNoEmission: true,
		},
	})
}

// The peer closes a connection we opened.
func TestInitiatorIncomingFin(t *testing.T) {
	runInitiatorCorpus(t, []step{
		{name: "peer answers the SYN", injectRaw: initiatorSynAck(), wantNoEmission: true},
		{name: "data", inject: initiatorData(0, 200000, []byte("one")), wantAck: ackNum(initiatorFirstInOrder)},
		{
			name: "peer's FIN",
			inject: NewPacketBuilder(st_fin, initiatorConnSeed, 210000, corpusWindow,
				initiatorFirstInOrder+1).
				WithAckNum(initiatorConnSeed).Build(),
			// On reaching a FIN libutp acknowledges twice: once immediately
			// (utp_internal.cpp:2370, "if the other end wants to close, ack")
			// and once from the deferred-ack list it also schedules at :2404.
			// The second carries nothing the first did not. Same deliberate
			// divergence as the responder corpus records.
			libutpExtraDuplicates: 1,
			divergenceReason: "on reaching a FIN libutp acks twice, once immediately " +
				"and once from the deferred list; the second is a duplicate",
			// The FIN consumes a sequence number, so the ack covers it.
			wantAck: ackNum(initiatorFirstInOrder + 1),
		},
	})
}

// A peer that advertises no receive window on a connection we opened.
// initiatorState builds a bare ST_STATE from the peer advertising wnd.
func initiatorState(tsMicros uint32, wnd uint32) []byte {
	return NewPacketBuilder(st_state, initiatorConnSeed, tsMicros, wnd, initiatorFirstInOrder).
		WithAckNum(initiatorConnSeed).Build().Encode()
}

// A zero receive window stalls the dialling side until the peer reopens it
// (utp_internal.cpp:936, where max_window_user is one of the three limits
// is_full weighs).
//
// The first version of this case advertised the zero window and stopped
// there, which asserted only that a bare ST_STATE draws no reply -- true of
// any window. It has to write against the stall for the window to bite, and
// it needs the control below, because a case that ends in silence also
// passes when the write could never have gone out for some unrelated reason.
// That is not hypothetical: the responder-role twin of this case was in
// exactly that state, silent because libutp was refusing every write while
// its socket sat in CS_SYN_RECV.
func TestInitiatorZeroWindow(t *testing.T) {
	runInitiatorCorpus(t, []step{
		{name: "peer answers the SYN", injectRaw: initiatorSynAck(), wantNoEmission: true},
		{
			name:           "peer advertises a zero window",
			injectRaw:      initiatorState(200000, 0),
			wantNoEmission: true,
		},
		{
			name:           "writing against the zero window sends nothing",
			write:          []byte("stalled behind a zero window"),
			wantNoEmission: true,
		},
		{
			name:      "peer reopens the window and the write goes out",
			injectRaw: initiatorState(300000, corpusWindow),
		},
	})
}

// The control for TestInitiatorZeroWindow: the same script without the zero
// window, where the write leaves at once.
func TestInitiatorWriteFlowsWithoutZeroWindow(t *testing.T) {
	runInitiatorCorpus(t, []step{
		{name: "peer answers the SYN", injectRaw: initiatorSynAck(), wantNoEmission: true},
		{name: "the write leaves at once", write: []byte("stalled behind a zero window")},
		{
			name:           "nothing left to flush when the peer acks",
			injectRaw:      initiatorState(300000, corpusWindow),
			wantNoEmission: true,
		},
	})
}
