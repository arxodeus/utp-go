//go:build cgo

package utp_go

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/zen-eth/utp-go/native/libutp"
)

// The M2 corpus.
//
// Each case scripts a sequence of steps and runs it against both
// implementations as the same role, comparing what each emits at every step.
// Sequence numbers and connection ids are pinned on both sides, so the
// comparison is field-by-field exact apart from allowedToDiffer.

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
	// advance moves libutp's virtual clock and runs its timeout check. Our
	// implementation runs on the real clock, so the runner sleeps the same
	// amount; see CONFORMANCE.md on why timing comparison is coarse.
	advance time.Duration
	// wantNoEmission asserts both sides stay silent.
	wantNoEmission bool
	// libutpExtraDuplicates records a deliberate divergence: libutp emits
	// this many more packets than we do, and each extra one is a duplicate of
	// the packet before it. Stating the number and the reason here is the
	// point -- the brief's rule is that a deliberate divergence is asserted
	// explicitly rather than hidden inside a tolerance.
	libutpExtraDuplicates int
	divergenceReason      string
}

// corpusPinnedSeq is the sequence number both implementations are pinned to
// for their SYN-ACK. Packets from the peer ack corpusPinnedSeq-1: libutp
// drops anything acking a sequence number it has not sent yet
// (utp_internal.cpp:1795-1806), and a bare ST_STATE does not consume one.
const (
	corpusPinnedSeq = 0x4321
	corpusSynConnID = 6000
	corpusSynSeq    = 900
	corpusWindow    = 1048576
)

// synPacketFor builds the SYN that opens every responder-role case.
func synPacketFor(connID, seq uint16) *packet {
	return NewPacketBuilder(st_syn, connID, 100000, corpusWindow, seq).Build()
}

// runResponderCorpus drives both implementations as the responder through the
// same steps, comparing emissions after each.
func runResponderCorpus(t *testing.T, steps []step) {
	t.Helper()

	// --- libutp side ---
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
	sock := WithSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	cid := NewConnectionId(conn.peer, corpusSynConnID+1, corpusSynConnID)
	streamCh := make(chan *UtpStream, 1)
	go func() {
		s, err := sock.AcceptWithCid(ctx, cid, NewConnectionConfig())
		if err == nil {
			streamCh <- s
		} else {
			t.Logf("our AcceptWithCid: %v", err)
			streamCh <- nil
		}
	}()
	time.Sleep(100 * time.Millisecond)

	var stream *UtpStream
	getStream := func() *UtpStream {
		if stream == nil {
			select {
			case stream = <-streamCh:
			case <-time.After(2 * time.Second):
			}
		}
		return stream
	}

	for i, st := range steps {
		// libutp
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
		// Every extra packet must be byte-identical to the one before it.
		// If it carried new information this would not be a divergence we
		// could dismiss.
		for k := len(oursOut); k < len(libutpOut); k++ {
			prev, cur := libutpOut[k-1], libutpOut[k]
			pp, e1 := DecodePacket(prev)
			cp, e2 := DecodePacket(cur)
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

	if len(oursOut) != len(libutpOut) {
		t.Errorf("step %d (%s): emitted a different number of packets -- ours %d, libutp %d\n  ours:%s\n  libutp:%s",
			i, st.name, len(oursOut), len(libutpOut),
			describePackets(oursOut), describePackets(libutpOut))
		return
	}

	for j := range oursOut {
		ourPkt, err1 := DecodePacket(oursOut[j])
		theirPkt, err2 := DecodePacket(libutpOut[j])
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
			inject: NewPacketBuilder(st_data, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("hello world")).Build(),
		},
		{
			name: "second data packet is acked",
			inject: NewPacketBuilder(st_data, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("second")).Build(),
		},
	})
}

// A packet arriving twice must not be acked as if it were new data.
func TestConformanceDuplicateData(t *testing.T) {
	dup := NewPacketBuilder(st_data, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
		WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("payload")).Build()
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{name: "data", inject: dup},
		{name: "the same data again", inject: dup},
	})
}

// A gap in the sequence space: the second packet arrives before the first.
func TestConformanceReordering(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "seq+2 arrives first, leaving a gap",
			inject: NewPacketBuilder(st_data, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("second")).Build(),
		},
		{
			name: "seq+1 fills the gap",
			inject: NewPacketBuilder(st_data, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+1).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("first")).Build(),
		},
	})
}

// A FIN from the peer.
func TestConformanceIncomingFin(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "data",
			inject: NewPacketBuilder(st_data, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("bye")).Build(),
		},
		{
			name: "FIN",
			inject: NewPacketBuilder(st_fin, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).Build(),
			libutpExtraDuplicates: 1,
			divergenceReason: "on reaching a FIN libutp acks twice: once immediately " +
				"(utp_internal.cpp:2370, \"if the other end wants to close, ack\") and once " +
				"from the deferred-ack list it also schedules at :2404. The second carries " +
				"no information the first did not, so we send one. Emitting a gratuitous " +
				"duplicate would cost a packet and change nothing a peer depends on",
		},
	})
}

// A RESET must terminate the connection without a reply.
func TestConformanceIncomingReset(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "RESET",
			inject: NewPacketBuilder(st_reset, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
				WithAckNum(corpusPinnedSeq - 1).Build(),
			wantNoEmission: true,
		},
	})
}

// A zero-window advertisement from the peer.
func TestConformanceZeroWindow(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "peer advertises a zero receive window",
			inject: NewPacketBuilder(st_state, corpusSynConnID+1, 200000, 0, corpusSynSeq+1).
				WithAckNum(corpusPinnedSeq - 1).Build(),
		},
	})
}

// libutp drops any packet whose ack_nr acks a sequence number it has not sent
// yet, calling it "a spoofed address or a malicious attempt to attach the uTP
// implementation" (utp_internal.cpp:1795-1806). Accepting such a packet is
// both an incompatibility and an attack surface, so the corpus checks it.
func TestConformanceInvalidAckNumIsIgnored(t *testing.T) {
	runResponderCorpus(t, []step{
		{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)},
		{
			name: "data acking a sequence number we never sent",
			inject: NewPacketBuilder(st_data, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
				// corpusPinnedSeq is our next unsent sequence number; acking it
				// is acking a packet that does not exist.
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
			inject: NewPacketBuilder(st_data, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
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
func goResponderForCorpus(t *testing.T) (*scriptedConn, *UtpSocket, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	conn := newScriptedConn()
	sock := WithSocket(ctx, conn, conformanceLogger())
	cid := NewConnectionId(conn.peer, corpusSynConnID+1, corpusSynConnID)
	go func() {
		_, _ = sock.AcceptWithCid(ctx, cid, NewConnectionConfig())
	}()
	time.Sleep(100 * time.Millisecond)
	return conn, sock, cancel
}

// A reorder gap wider than the 30-packet window libutp scans when it builds a
// selective ack. This is the case that distinguishes a bounded mask from an
// unbounded one: with a packet pending far past the gap, an implementation
// that sizes the mask to reach it emits a much larger extension than libutp,
// which always emits exactly four bytes (utp_internal.cpp:797, :805).
func TestConformanceWideReordering(t *testing.T) {
	steps := []step{{name: "handshake", inject: synPacketFor(corpusSynConnID, corpusSynSeq)}}
	// Leave seq+1 missing, then deliver a packet 40 past it.
	for _, offset := range []uint16{2, 3, 41} {
		steps = append(steps, step{
			name: "data at seq+" + strconv.Itoa(int(offset)) + ", gap still open",
			inject: NewPacketBuilder(st_data, corpusSynConnID+1, 200000+uint32(offset), corpusWindow, corpusSynSeq+offset).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("x")).Build(),
		})
	}
	runResponderCorpus(t, steps)
}

// Data arriving after the peer's FIN has already been reached in order.
//
// A deliberate divergence, and the one M4 finding too large to fix in place.
//
// libutp keeps the socket in CS_GOT_FIN and answers nothing: the peer has
// closed its sending side, but the connection is alive until the local
// application closes it too. We have no half-close -- once the remote FIN is
// reached and everything we sent is acked, `connection.eventLoop` moves
// straight to ConnClosed (conn.go, the RemoteFin branch). The late packet
// then reaches a socket with no connection for it, and draws a RESET.
//
// Recorded rather than fixed: supporting a half-close means a new connection
// state and a write path that survives the peer's FIN, which is a larger
// change than the audit that found it. See KNOWN-LIMITATIONS.md.
func TestConformanceDataAfterReachedFin(t *testing.T) {
	raws := [][]byte{
		NewPacketBuilder(st_data, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
			WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("bye")).Build().Encode(),
		NewPacketBuilder(st_fin, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+2).
			WithAckNum(corpusPinnedSeq - 1).Build().Encode(),
		NewPacketBuilder(st_data, corpusSynConnID+1, 220000, corpusWindow, corpusSynSeq+4).
			WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("late")).Build().Encode(),
	}
	ours, libutpOut := runDivergenceSteps(t, raws)

	if len(libutpOut) != 0 {
		t.Errorf("libutp emitted %d packet(s) for data past a reached FIN; it is expected to "+
			"stay silent in CS_GOT_FIN, so this case no longer documents the divergence it "+
			"was written for", len(libutpOut))
	}
	if len(ours) != 1 {
		t.Fatalf("we emitted %d packets, want exactly 1 (the RESET our lack of half-close "+
			"produces); if this is now 0, half-close has landed and this case should become "+
			"an ordinary corpus entry", len(ours))
	}
	pkt, err := DecodePacket(ours[0])
	if err != nil {
		t.Fatalf("decoding our own emission: %v", err)
	}
	if pkt.Header.PacketType != st_reset {
		t.Errorf("we emitted packet type %d for data past a reached FIN; the documented divergence is a RESET",
			pkt.Header.PacketType)
	}
	t.Logf("deliberate divergence: data past a reached FIN -- libutp stays silent (CS_GOT_FIN), " +
		"we have already torn the connection down and the socket answers with a RESET")
}

// A FIN as the first packet after the handshake, before any data.
//
// A deliberate divergence. libutp completes an incoming connection only on an
// ST_DATA packet:
//
//	// Incoming connection completion
//	if (pk_flags == ST_DATA && conn->state == CS_SYN_RECV) {
//	    conn->state = CS_CONNECTED;
//	}
//	                                        (utp_internal.cpp:2158-2161)
//
// so a FIN -- or anything else -- arriving first leaves the socket in
// CS_SYN_RECV, where the guard at :2314 drops it without a word. The peer
// gets no acknowledgement at all and must retransmit until it happens to send
// data, which for a zero-length transfer it never will.
//
// We treat the connection as established once the handshake completes and
// process whatever arrives next. The reason for not matching libutp here: a
// peer that opens a connection, sends nothing and closes is doing something
// legitimate, and libutp answers it with silence until the idle timeout. Our
// answer is one STATE for one FIN, from a peer that has already completed a
// handshake, so it is neither an amplification vector nor reachable without
// completing one.
//
// Found by FuzzDifferentialResponder. It is pinned here so the divergence
// cannot drift, and so the fuzzer -- which primes both sides with a data
// packet to get past exactly this state difference -- is not the only record
// of it.
func TestConformanceFinBeforeAnyData(t *testing.T) {
	raws := [][]byte{
		NewPacketBuilder(st_fin, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+1).
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
	pkt, err := DecodePacket(ours[0])
	if err != nil {
		t.Fatalf("decoding our own emission: %v", err)
	}
	if pkt.Header.PacketType != st_state {
		t.Errorf("we emitted packet type %d for a FIN before any data; the documented "+
			"divergence is a STATE", pkt.Header.PacketType)
	}
	t.Logf("deliberate divergence: a FIN before any data -- libutp stays in CS_SYN_RECV and " +
		"drops it silently, we acknowledge it")
}
