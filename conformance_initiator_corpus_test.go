//go:build cgo

package utp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/arxodeus/utp/internal/libutp"
)

// The initiator corpus: both implementations open the connection, sending
// the SYN, and the peer answers. This is the role a BitTorrent client takes
// on every dial, and the one in which this library chooses the sequence
// numbers and connection ids.
//
// The cases run on a virtual clock. Both implementations are driven step by
// step with time moved by the same amount on both sides, so every comparison
// is deterministic.

// initiatorRun holds both implementations mid-conversation.
type initiatorRun struct {
	t   *testing.T
	drv *libutp.Driver
	clk *virtualClock
	// streamCh carries the connected stream once the handshake completes. It
	// is written by the goroutine running ConnectWithCID and read by the
	// test, so a plain field would be a data race.
	streamCh <-chan *Stream
	// dialErr carries ConnectWithCID's error, if it failed.
	dialErr <-chan error
	stream  *Stream
	conn    *scriptedConn
	sock    *Socket
	cancel  context.CancelFunc
	unpin   func()
	start   time.Time
}

func (r *initiatorRun) close() {
	// The stream is not closed: closing waits out the two-second flush
	// stall timeout, since the scripted peer never acknowledges the FIN.
	// Canceling the context tears the connection down without that wait, and
	// what the cases assert has already been emitted.
	r.sock.Close()
	r.cancel()
	r.unpin()
	r.drv.Close()
}

// newInitiatorRun opens a connection from both implementations and returns
// the SYN each sent, since comparing them is a case in itself.
func newInitiatorRun(t *testing.T) (*initiatorRun, [][]byte, [][]byte) {
	t.Helper()
	return newInitiatorRunMTU(t, 0)
}

// newInitiatorRunMTU is newInitiatorRun with the MTU the reference driver
// reports set to udpMTU; zero leaves the driver's default, 1472.
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
	sock := NewSocket(ctx, conn, conformanceLogger(), withClock(clk))

	cfg := NewConnectionConfig()
	cfg.clock = clk
	cfg.nowMicros = func() uint32 { return uint32(clk.Now().UnixMicro()) }

	cid := NewConnectionID(conn.peer, initiatorConnSeed, initiatorConnSeed+1)
	streamCh := make(chan *Stream, 1)
	dialErr := make(chan error, 1)
	go func() {
		s, err := sock.ConnectWithCID(ctx, cid, cfg)
		if err != nil {
			dialErr <- err
		}
		streamCh <- s
	}()

	r := &initiatorRun{
		t: t, drv: drv, clk: clk, conn: conn, sock: sock,
		cancel: cancel, unpin: unpin, start: start,
	}

	// Three participants register when the socket is built (the
	// retransmission wheel and the read and event loops), and the
	// connection's event loop is the fourth once ConnectWithCID runs.
	clk.AwaitParticipants(4)
	clk.AwaitQuiet()
	ourSyn := conn.takeEmitted()

	if err := drv.Connect(); err != nil {
		t.Fatalf("libutp connect: %v", err)
	}
	libutpSyn := drv.Emitted()
	drv.ClearEmitted()

	r.streamCh = streamCh
	r.dialErr = dialErr
	return r, ourSyn, libutpSyn
}

// step applies one action to both implementations and compares what each
// emitted.
func (r *initiatorRun) step(i int, st step) {
	r.t.Helper()

	// --- reference ---
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
		// The same amount of time, on a clock that moves only when told.
		r.clk.Advance(st.advance)
	}
	r.clk.AwaitQuiet()
	oursOut := r.conn.takeEmitted()

	compareStep(r.t, i, st, oursOut, libutpOut)
}

// waitForStream returns the connected stream, or nil if the handshake never
// completed.
func (r *initiatorRun) waitForStream() *Stream {
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

// initiatorFirstInOrder is the sequence number the dialing side expects first
// from its peer. The SYN-ACK carries sequence number 900 and, being a bare
// ST_STATE, consumes none, so ack_nr is 899 and 900 is next. An off-by-one
// here would make every "in order" case exercise the out-of-order path.
const initiatorFirstInOrder = 900

// initiatorData builds a data packet from the peer, offset packets past the
// first one expected, acking our SYN.
func initiatorData(offset uint16, tsMicros uint32, body []byte) *packet {
	return newPacketBuilder(stData, initiatorConnSeed, tsMicros, corpusWindow,
		initiatorFirstInOrder+offset).
		WithAckNum(initiatorConnSeed).WithPayload(body).Build()
}

// The handshake from the dialing side: the SYN we send and the
// acknowledgement we send once the peer answers.
func TestInitiatorHandshake(t *testing.T) {
	runInitiatorCorpus(t, []step{
		{
			name:   "peer answers the SYN",
			inject: nil,
			injectRaw: newPacketBuilder(stState, initiatorConnSeed, 150000, corpusWindow, 900).
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
// the dialing side.
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

// A peer that answers our SYN with a reset, as a closed port or a refusing
// client does. The dial fails as refused.
func TestInitiatorResetInsteadOfSynAck(t *testing.T) {
	r, ourSyn, libutpSyn := newInitiatorRun(t)
	defer r.close()
	compareStep(t, -1, step{name: "our SYN"}, ourSyn, libutpSyn)
	r.step(0, step{
		name: "peer resets instead of answering",
		injectRaw: newPacketBuilder(stReset, initiatorConnSeed, 150000, corpusWindow, 900).
			WithAckNum(initiatorConnSeed).Build().Encode(),
		// A reset is never answered with a reset; both sides go quiet.
		wantNoEmission: true,
	})
	select {
	case err := <-r.dialErr:
		if !errors.Is(err, ErrConnRefused) {
			t.Errorf("the dial failed with %v, want %v", err, ErrConnRefused)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the dial did not fail after its SYN was answered with a reset")
	}
}

// The peer closes a connection we opened.
func TestInitiatorIncomingFin(t *testing.T) {
	runInitiatorCorpus(t, []step{
		{name: "peer answers the SYN", injectRaw: initiatorSynAck(), wantNoEmission: true},
		{name: "data", inject: initiatorData(0, 200000, []byte("one")), wantAck: ackNum(initiatorFirstInOrder)},
		{
			name: "peer's FIN",
			inject: newPacketBuilder(stFin, initiatorConnSeed, 210000, corpusWindow,
				initiatorFirstInOrder+1).
				WithAckNum(initiatorConnSeed).Build(),
			// On reaching a FIN the reference implementation acknowledges twice:
			// once immediately and once from its deferred-ack list. The second
			// carries nothing new, and this library sends one.
			libutpExtraDuplicates: 1,
			divergenceReason: "on reaching a FIN libutp acks twice, once immediately " +
				"and once from the deferred list; the second is a duplicate",
			// The FIN consumes a sequence number, so the ack covers it.
			wantAck: ackNum(initiatorFirstInOrder + 1),
		},
	})
}

// initiatorState builds a bare ST_STATE from the peer advertising wnd.
func initiatorState(tsMicros uint32, wnd uint32) []byte {
	return newPacketBuilder(stState, initiatorConnSeed, tsMicros, wnd, initiatorFirstInOrder).
		WithAckNum(initiatorConnSeed).Build().Encode()
}

// A zero receive window stalls the dialing side until the peer reopens it.
// The case writes against the stall, so the window matters, and the control
// below shows the silence is due to the window rather than some other
// reason the write could not go out.
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

// initiatorAckOf builds a bare ST_STATE from the peer acknowledging our
// packets up to seq.
func initiatorAckOf(tsMicros uint32, seq uint16) []byte {
	return newPacketBuilder(stState, initiatorConnSeed, tsMicros, corpusWindow, initiatorFirstInOrder).
		WithAckNum(seq).Build().Encode()
}

// Short writes behind an unacknowledged packet: the second waits, the third
// joins it, and the acknowledgement of the first sends both as one packet.
// Every step is compared packet for packet, bodies included. The reference
// implementation's window starts at one packet, which holds the second; this
// library's starts at two, so here the Nagle rule holds it
// (TestNagleHoldsAShortPacketBehindAnother isolates it).
func TestInitiatorNagle(t *testing.T) {
	runInitiatorCorpus(t, []step{
		{name: "peer answers the SYN", injectRaw: initiatorSynAck(), wantNoEmission: true},
		{name: "a short write with nothing in flight leaves at once", write: []byte("first")},
		{name: "the next waits behind it", write: []byte("second"), wantNoEmission: true},
		{name: "and the one after joins it", write: []byte("third"), wantNoEmission: true},
		{
			name:      "the acknowledgement of the first sends both as one packet",
			injectRaw: initiatorAckOf(300000, initiatorConnSeed+1),
		},
	})
}

// The control for TestInitiatorNagle: with the first packet acknowledged
// before the next write, nothing is in flight and the write leaves at once.
func TestInitiatorNagleNothingInFlight(t *testing.T) {
	runInitiatorCorpus(t, []step{
		{name: "peer answers the SYN", injectRaw: initiatorSynAck(), wantNoEmission: true},
		{name: "first write", write: []byte("first")},
		{name: "acknowledged", injectRaw: initiatorAckOf(300000, initiatorConnSeed+1), wantNoEmission: true},
		{name: "the next write leaves at once", write: []byte("second")},
	})
}
