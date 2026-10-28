//go:build cgo

package utp

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arxodeus/utp/internal/libutp"
)

// The responder corpus.
//
// Each case scripts a sequence of steps and runs it against both
// implementations in the same role, comparing what each emits at every step.
// Sequence numbers and connection ids are pinned on both sides, so the
// comparison is exact field by field apart from allowedToDiffer.

// step is one action applied to both implementations.
type step struct {
	name string
	// inject is a packet arriving from the peer.
	inject *packet
	// injectRaw is the same, but as bytes, for packets too malformed to build
	// through the packet builder.
	injectRaw []byte
	// write queues application data.
	write []byte
	// closeStream closes the connection.
	closeStream bool
	// advance moves the reference implementation's virtual clock and runs its
	// timeout check. Our implementation runs on the real clock, so the
	// runner sleeps the same amount; timing comparisons here are coarse.
	advance time.Duration
	// wantNoEmission asserts both sides stay silent.
	wantNoEmission bool
	// wantAck, when set, asserts the ack number on the last packet we emit.
	// Comparison with the reference cannot tell which code path a step took,
	// only that both took the same one; with a sequence number off by one,
	// every "in order" case would become an out-of-order case and still
	// agree. An expected ack number is a claim about this implementation
	// alone.
	wantAck *uint16
	// libutpExtraDuplicates records a known difference: the reference
	// emits this many more packets than we do, each a duplicate of the
	// packet before it. The difference is stated explicitly rather than
	// hidden in a tolerance.
	libutpExtraDuplicates int
	divergenceReason      string
	// oursExtra is the same the other way round: we emit this many packets
	// the reference does not, after everything it does emit, for the reason
	// in divergenceReason.
	oursExtra int
}

// corpusPinnedSeq is the sequence number both implementations are pinned to
// for their SYN-ACK. Packets from the peer ack corpusPinnedSeq-1: a packet
// acknowledging a sequence number not yet sent is dropped, and a bare
// ST_STATE does not consume one.
const (
	corpusPinnedSeq = 0x4321
	corpusSynConnID = 6000
	corpusSynSeq    = 900
	corpusWindow    = 1048576
)

// ackNum is the pointer wrapper for step.wantAck.
func ackNum(v uint16) *uint16 { return &v }

// synPacketFor builds the SYN that opens every responder-role case.
func synPacketFor(connID, seq uint16) *packet {
	return newPacketBuilder(stSyn, connID, 100000, corpusWindow, seq).Build()
}

// runResponderCorpus drives both implementations as the responder through the
// same steps, comparing emissions after each.
func runResponderCorpus(t *testing.T, steps []step) {
	t.Helper()

	// --- reference side ---
	drv, err := libutp.NewDriver(1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	defer drv.Close()
	drv.PushRandom(corpusPinnedSeq)
	drv.Listen()

	// --- our side ---
	restore := pinRandom(corpusPinnedSeq)
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn := newScriptedConn()
	sock := NewSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	cid := NewConnectionID(conn.peer, corpusSynConnID+1, corpusSynConnID)
	streamCh := make(chan *Stream, 1)
	go func() {
		s, err := sock.AcceptWithCID(ctx, cid, pinnedClockConfig(drv))
		if err == nil {
			streamCh <- s
		} else {
			t.Logf("our AcceptWithCID: %v", err)
			streamCh <- nil
		}
	}()
	time.Sleep(100 * time.Millisecond)

	var stream *Stream
	getStream := func() *Stream {
		if stream == nil {
			select {
			case stream = <-streamCh:
			case <-time.After(2 * time.Second):
			}
		}
		return stream
	}

	for i, st := range steps {
		// reference
		if raw := st.rawBytes(); raw != nil {
			drv.Inject(raw)
		}
		if st.write != nil {
			if _, err := drv.Write(st.write); err != nil {
				t.Fatalf("step %d (%s): libutp write: %v", i, st.name, err)
			}
		}
		if st.closeStream {
			drv.CloseStream()
		}
		if st.advance > 0 {
			drv.Advance(uint64(st.advance.Microseconds()))
			drv.CheckTimeouts()
		}
		drv.IssueAcks()
		libutpOut := drv.Emitted()
		drv.ClearEmitted()

		// ours
		if raw := st.rawBytes(); raw != nil {
			conn.inject(raw)
		}
		if st.write != nil {
			s := getStream()
			if s == nil {
				t.Fatalf("step %d (%s): no stream to write to", i, st.name)
			}
			wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
			_, _ = s.Write(wctx, st.write)
			wcancel()
		}
		if st.closeStream {
			if s := getStream(); s != nil {
				go s.Close()
			}
		}
		if st.advance > 0 {
			time.Sleep(st.advance)
		}
		conn.settle()
		oursOut := conn.takeEmitted()

		compareStep(t, i, st, oursOut, libutpOut)
	}
}

// compareStep diffs the packets both implementations emitted for one step.
func compareStep(t *testing.T, i int, st step, oursOut, libutpOut [][]byte) {
	t.Helper()

	if st.wantNoEmission {
		if len(oursOut) != 0 || len(libutpOut) != 0 {
			t.Errorf("step %d (%s): expected silence from both, ours emitted %d, libutp %d%s%s",
				i, st.name, len(oursOut), len(libutpOut),
				describePackets(oursOut), describePackets(libutpOut))
		}
		return
	}

	if st.libutpExtraDuplicates > 0 {
		want := len(oursOut) + st.libutpExtraDuplicates
		if len(libutpOut) != want {
			t.Errorf("step %d (%s): expected libutp to emit %d packets (%d of ours plus %d duplicates: %s), got %d%s",
				i, st.name, want, len(oursOut), st.libutpExtraDuplicates, st.divergenceReason,
				len(libutpOut), describePackets(libutpOut))
			return
		}
		// Every extra packet must be byte-identical to the one before it, or it
		// carries new information and the difference is not a harmless one.
		for k := len(oursOut); k < len(libutpOut); k++ {
			prev, cur := libutpOut[k-1], libutpOut[k]
			pp, e1 := decodePacket(prev, false)
			cp, e2 := decodePacket(cur, false)
			if e1 != nil || e2 != nil {
				t.Errorf("step %d (%s): could not decode libutp's extra packet", i, st.name)
				continue
			}
			if diffs := comparePackets(pp, cp); len(diffs) > 0 {
				bad, _ := significant(diffs)
				if len(bad) > 0 {
					t.Errorf("step %d (%s): libutp's extra packet %d is not a duplicate: %v", i, st.name, k, bad)
				}
			}
		}
		t.Logf("step %d (%s): deliberate divergence -- libutp emitted %d extra duplicate packet(s). %s",
			i, st.name, st.libutpExtraDuplicates, st.divergenceReason)
		libutpOut = libutpOut[:len(oursOut)]
	}

	if st.oursExtra > 0 {
		if len(oursOut) != len(libutpOut)+st.oursExtra {
			t.Errorf("step %d (%s): expected us to emit %d packets (%d of libutp's plus %d: %s), got %d%s",
				i, st.name, len(libutpOut)+st.oursExtra, len(libutpOut), st.oursExtra, st.divergenceReason,
				len(oursOut), describePackets(oursOut))
			return
		}
		t.Logf("step %d (%s): deliberate divergence -- we emitted %d packet(s) libutp does not. %s",
			i, st.name, st.oursExtra, st.divergenceReason)
		oursOut = oursOut[:len(libutpOut)]
		if len(oursOut) == 0 {
			return
		}
	}

	if len(oursOut) != len(libutpOut) {
		t.Errorf("step %d (%s): emitted a different number of packets -- ours %d, libutp %d\n  ours:%s\n  libutp:%s",
			i, st.name, len(oursOut), len(libutpOut),
			describePackets(oursOut), describePackets(libutpOut))
		return
	}

	// Two silent sides compare equal, so a step that expected an answer and
	// got none from either would pass while asserting nothing. A step that
	// means to observe silence says so with wantNoEmission; anything else
	// must produce something to compare.
	if len(oursOut) == 0 {
		t.Errorf("step %d (%s): neither side emitted anything, so this step compared "+
			"nothing. Set wantNoEmission if silence is the expected result.",
			i, st.name)
		return
	}

	if st.wantAck != nil {
		last, err := decodePacket(oursOut[len(oursOut)-1], false)
		if err != nil {
			t.Errorf("step %d (%s): could not decode our last packet: %v", i, st.name, err)
		} else if last.Header.AckNum != *st.wantAck {
			t.Errorf("step %d (%s): we acked %d, expected %d. The step is not exercising "+
				"the path it was written for.", i, st.name, last.Header.AckNum, *st.wantAck)
		}
	}

	for j := range oursOut {
		ourPkt, err1 := decodePacket(oursOut[j], false)
		theirPkt, err2 := decodePacket(libutpOut[j], false)
		if err1 != nil || err2 != nil {
			t.Errorf("step %d (%s) packet %d: decode failed -- ours %v, libutp %v", i, st.name, j, err1, err2)
			continue
		}
		bad, allowed := significant(comparePackets(ourPkt, theirPkt))
		for _, d := range allowed {
			t.Logf("step %d (%s) packet %d: %s (allowed: %s)", i, st.name, j, d, allowedToDiffer[d.Field])
		}
		for _, d := range bad {
			t.Errorf("step %d (%s) packet %d: %s", i, st.name, j, d)
		}
	}
}

// --- the corpus -------------------------------------------------------------

func TestConformanceHandshake(t *testing.T) {
	runResponderCorpus(t, []step{
		{
			name:   "incoming SYN produces the SYN-ACK",
			inject: synPacketFor(corpusSynConnID, corpusSynSeq),
		},
	})
}

func TestConformanceDataAndAck(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "first data packet is acked",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("hello world")).Build(),
			wantAck: ackNum(corpusSynSeq + 1),
		},
		{
			name: "second data packet is acked",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("second")).Build(),
			wantAck: ackNum(corpusSynSeq + 2),
		},
	})
}

// A packet arriving twice must not be acked as if it were new data.
func TestConformanceDuplicateData(t *testing.T) {
	dup := newPacketBuilder(stData, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
		WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("payload")).Build()
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{name: "data", inject: dup, wantAck: ackNum(corpusSynSeq + 1)},
		// A duplicate advances nothing; the ack repeats.
		{name: "the same data again", inject: dup, wantAck: ackNum(corpusSynSeq + 1)},
	})
}

// A gap in the sequence space: the second packet arrives before the first.
func TestConformanceReordering(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "seq+2 arrives first, leaving a gap",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("second")).Build(),
			// The gap is open, so the ack stays on the SYN.
			wantAck: ackNum(corpusSynSeq),
		},
		{
			name: "seq+1 fills the gap",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+1).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("first")).Build(),
			// Both are in order now, so the ack jumps over both.
			wantAck: ackNum(corpusSynSeq + 2),
		},
	})
}

// A FIN from the peer.
func TestConformanceIncomingFin(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "data",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("bye")).Build(),
			wantAck: ackNum(corpusSynSeq + 1),
		},
		{
			name: "FIN",
			inject: newPacketBuilder(stFin, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).Build(),
			libutpExtraDuplicates: 1,
			divergenceReason: "on reaching a FIN libutp acks twice: once immediately " +
				"(utp_internal.cpp:2370, \"if the other end wants to close, ack\") and once " +
				"from the deferred-ack list it also schedules at :2404. The second carries " +
				"no information the first did not, so we send one. Emitting a gratuitous " +
				"duplicate would cost a packet and change nothing a peer depends on",
			// The FIN consumes a sequence number, so the ack covers it.
			wantAck: ackNum(corpusSynSeq + 2),
		},
	})
}

// A RESET must terminate the connection without a reply.
func TestConformanceIncomingReset(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "RESET",
			inject: newPacketBuilder(stReset, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
				WithAckNum(corpusPinnedSeq - 1).Build(),
			wantNoEmission: true,
		},
	})
}

// corpusPeerData is the ST_DATA that completes the handshake from the peer's
// side. The reference implementation's responder is established only when an
// ST_DATA arrives and accepts no writes until then, so a responder-role case
// that writes must be given data first.
func corpusPeerData(seq uint16, ts uint32, body []byte) *packet {
	return newPacketBuilder(stData, corpusSynConnID+1, ts, corpusWindow, seq).
		WithAckNum(corpusPinnedSeq - 1).WithPayload(body).Build()
}

// A zero receive window stalls the sender until the peer reopens it. A bare
// ST_STATE is answered with silence whatever window it carries, so the case
// writes through the stall and out the other side.
// TestConformanceWriteFlowsWithoutZeroWindow is the control: the same script
// without the zero window, where the write leaves at once.
func TestConformanceZeroWindow(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{name: "peer data completes the handshake", inject: corpusPeerData(corpusSynSeq+1, 150000, []byte("hi"))},
		{
			name: "peer advertises a zero receive window",
			inject: newPacketBuilder(stState, corpusSynConnID+1, 200000, 0, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).Build(),
			wantNoEmission: true,
		},
		{
			name:           "writing against the zero window sends nothing",
			write:          []byte("stalled behind a zero window"),
			wantNoEmission: true,
		},
		{
			name: "peer reopens the window and the write goes out",
			inject: newPacketBuilder(stState, corpusSynConnID+1, 300000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).Build(),
		},
	})
}

// The control for TestConformanceZeroWindow: identical but for the zero
// window, and here the write leaves immediately.
func TestConformanceWriteFlowsWithoutZeroWindow(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{name: "peer data completes the handshake", inject: corpusPeerData(corpusSynSeq+1, 150000, []byte("hi"))},
		{name: "the write leaves at once", write: []byte("stalled behind a zero window")},
		{
			name: "nothing left to flush when the peer acks",
			inject: newPacketBuilder(stState, corpusSynConnID+1, 300000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).Build(),
			wantNoEmission: true,
		},
	})
}

// Nothing is sent into less than a full packet of room, and a packet is
// never cut down to fit. The rest of the rule, that bytes in flight spend the
// peer's window, cannot be isolated against the reference at connection
// start and is tested in peer_window_test.go.
func TestConformanceWriteIsNotCutToFitPeerWindow(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{name: "peer data completes the handshake", inject: corpusPeerData(corpusSynSeq+1, 150000, []byte("hi"))},
		{
			name: "peer advertises a 300-byte window",
			inject: newPacketBuilder(stState, corpusSynConnID+1, 200000, 300, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).Build(),
			wantNoEmission: true,
		},
		{
			name:           "a 400-byte write does not go out as 300",
			write:          bytes.Repeat([]byte("c"), 400),
			wantNoEmission: true,
		},
		{
			// Wide enough for a full packet on both sides: the reference's first
			// packet is 1452 bytes and ours 962.
			name: "peer widens the window and all 400 leave in one packet",
			inject: newPacketBuilder(stState, corpusSynConnID+1, 300000, 2000, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).Build(),
		},
	})
}

// A packet whose ack_nr acknowledges a sequence number not yet sent is
// dropped: it implies a spoofed address or a malicious peer.
func TestConformanceInvalidAckNumIsIgnored(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "data acking a sequence number we never sent",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
				// corpusPinnedSeq is our next unsent sequence number; acknowledging it
				// acknowledges a packet that does not exist.
				WithAckNum(corpusPinnedSeq).WithPayload([]byte("spoofed")).Build(),
			wantNoEmission: true,
		},
	})
}

// The same, far outside the window rather than one past it.
func TestConformanceWildlyInvalidAckNumIsIgnored(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "data acking a sequence number thousands away",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
				WithAckNum(corpusPinnedSeq + 5000).WithPayload([]byte("spoofed")).Build(),
			wantNoEmission: true,
		},
	})
}

// rawBytes is the wire form of whatever this step injects, if anything.
func (s step) rawBytes() []byte {
	if s.injectRaw != nil {
		return s.injectRaw
	}
	if s.inject != nil {
		return s.inject.Encode()
	}
	return nil
}

// libutpNewDriverForCorpus builds a driver pinned the way the corpus expects.
func libutpNewDriverForCorpus() (*libutp.Driver, error) {
	d, err := libutp.NewDriver(1_000_000)
	if err != nil {
		return nil, err
	}
	d.PushRandom(corpusPinnedSeq)
	return d, nil
}

// goResponderForCorpus starts one of our sockets accepting the corpus
// connection, and returns its scripted transport.
func goResponderForCorpus(t *testing.T) (*scriptedConn, *Socket, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	conn := newScriptedConn()
	sock := NewSocket(ctx, conn, conformanceLogger())
	cid := NewConnectionID(conn.peer, corpusSynConnID+1, corpusSynConnID)
	go func() {
		// Not clock-pinned: callers of this helper measure our side's own
		// behavior (ack counts, malformed-packet handling) rather than
		// comparing packet fields with the reference.
		_, _ = sock.AcceptWithCID(ctx, cid, NewConnectionConfig())
	}()
	time.Sleep(100 * time.Millisecond)
	return conn, sock, cancel
}

// A reorder gap wider than the 30-packet selective-ack window. With a packet
// pending far past the gap, an implementation that sized the mask to reach it
// would emit a larger extension; both emit exactly four bytes.
func TestConformanceWideReordering(t *testing.T) {
	steps := []step{{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)}}
	// Leave seq+1 missing, then deliver a packet 40 past it.
	for _, offset := range []uint16{2, 3, 41} {
		steps = append(steps, step{
			name: "data at seq+" + strconv.Itoa(int(offset)) + ", gap still open",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 200000+uint32(offset), corpusWindow, corpusSynSeq+offset).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("x")).Build(),
			// seq+1 never arrives, so every ack here stays on the SYN.
			wantAck: ackNum(corpusSynSeq),
		})
	}
	runResponderCorpus(t, steps)
}

// Data arriving after the peer's FIN has already been reached in order. The
// peer has closed its sending side, but the connection stays alive until the
// local application closes it too, and data past the FIN is dropped
// silently by both implementations: there is no sequence space left for it.
// The socket keeps the connection's last acknowledgement for
// lingerAckTimeout (utp_socket.go) and stays silent for a packet past it,
// rather than answering a legitimate half-close with a RESET.
func TestConformanceDataAfterReachedFin(t *testing.T) {
	raws := [][]byte{
		newPacketBuilder(stData, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
			WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("bye")).Build().Encode(),
		newPacketBuilder(stFin, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+2).
			WithAckNum(corpusPinnedSeq - 1).Build().Encode(),
		newPacketBuilder(stData, corpusSynConnID+1, 220000, corpusWindow, corpusSynSeq+4).
			WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("late")).Build().Encode(),
	}
	ours, libutpOut := runDivergenceSteps(t, raws)

	if len(libutpOut) != 0 {
		t.Errorf("libutp emitted %d packet(s) for data past a reached FIN; it is expected to "+
			"stay silent in CS_GOT_FIN, so this case no longer tests what it was written for",
			len(libutpOut))
	}
	if len(ours) != 0 {
		var types []string
		for _, raw := range ours {
			pkt, err := decodePacket(raw, false)
			if err != nil {
				t.Fatalf("decoding our own emission: %v", err)
			}
			types = append(types, fmt.Sprintf("%d", pkt.Header.PacketType))
		}
		t.Fatalf("we emitted %d packet(s) (types %s) for data past a reached FIN; libutp stays "+
			"silent and so must we -- a RESET here tells a peer doing a legitimate half-close "+
			"that its connection is broken", len(ours), strings.Join(types, ","))
	}
	t.Logf("parity: data past a reached FIN draws nothing from either side")
}

// A FIN as the first packet after the handshake, before any data. A
// deliberate difference: the reference implementation's acceptor is
// established only by an ST_DATA and silently drops anything else first, so a
// peer that opens a connection, sends nothing and closes gets no answer
// until it gives up. This library is established once the handshake
// completes and answers the FIN with one STATE. That is not an amplification
// vector, since the peer has completed a handshake. The differential fuzzer
// primes past this state difference, so it is pinned here.
func TestConformanceFinBeforeAnyData(t *testing.T) {
	raws := [][]byte{
		newPacketBuilder(stFin, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
			WithAckNum(corpusPinnedSeq - 1).Build().Encode(),
	}
	ours, libutpOut := runDivergenceSteps(t, raws)

	if len(libutpOut) != 0 {
		t.Errorf("libutp emitted %d packet(s) for a FIN before any data; it is expected to "+
			"stay in CS_SYN_RECV and drop it, so this case no longer documents the divergence "+
			"it was written for", len(libutpOut))
	}
	if len(ours) != 1 {
		t.Fatalf("we emitted %d packets, want exactly 1 (the STATE acknowledging the FIN)", len(ours))
	}
	pkt, err := decodePacket(ours[0], false)
	if err != nil {
		t.Fatalf("decoding our own emission: %v", err)
	}
	if pkt.Header.PacketType != stState {
		t.Errorf("we emitted packet type %d for a FIN before any data; the documented "+
			"divergence is a STATE", pkt.Header.PacketType)
	}
	t.Logf("deliberate divergence: a FIN before any data -- libutp stays in CS_SYN_RECV and " +
		"drops it silently, we acknowledge it")
}

// Only an ST_STATE carries a selective ack. With a hole in what the peer has
// sent, a data packet still goes out without the extension, and cancels the
// deferred acknowledgement that would have carried it.
func TestConformanceDataPacketCarriesNoSelectiveAck(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "data past a hole is selectively acknowledged",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("after the gap")).Build(),
			wantAck: ackNum(corpusSynSeq),
		},
		{
			name:  "a data packet sent with the hole still open",
			write: []byte("reply"),
		},
	})
}

// A packet whose timestamp is zero carries no timestamp: no delay is echoed
// for it and no sample taken.
func TestConformanceZeroTimestampIsNotADelay(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "data with a zero timestamp is acked with a zero delay",
			inject: newPacketBuilder(stData, corpusSynConnID+1, 0, corpusWindow, corpusSynSeq+1).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("no clock")).Build(),
			wantAck: ackNum(corpusSynSeq + 1),
		},
	})
}
