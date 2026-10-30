package utp

import (
	"context"
	"testing"
	"time"
)

// Differential fuzzing: the fuzzer generates packet sequences, and the
// reference implementation decides whether the answer was right.
//
// # The property
//
// Not that the two agree, since they deliberately differ in places. The
// property is a direction rule:
//
//	Being stricter than the reference is an interoperability risk.
//	Being more permissive is an attack surface.
//
// So:
//
//   - We must never answer more than the reference does. Emitting a packet
//     where it stays silent is the amplification surface a malformed-input
//     attack looks for. This is asserted strictly.
//   - Where both answer, the significant fields must agree. Timestamps and
//     the advertised window are exempt for the reasons in allowedToDiffer.
//   - Answering less is permitted, and is where the known differences lie,
//     such as the selective-ack length check and the single FIN
//     acknowledgement. The curated corpus pins those case by case.
//
// # Speed
//
// Each execution builds two implementations and waits for ours to go quiet,
// so this runs at a few executions per second. Run it for minutes:
//
//	go test -tags cgo -run xxx -fuzz FuzzDifferentialResponder -fuzztime 10m

// settleQuick is settle with a shorter quiet period, for a fuzz target where
// the per-execution cost is what limits coverage.
func (c *scriptedConn) settleQuick() {
	const quiet = 25 * time.Millisecond
	const limit = 600 * time.Millisecond
	// Wait for our socket to take the packet before timing anything, or the
	// quiet period could elapse while the packet was still queued and the
	// reaction land in the next step's transcript.
	c.awaitDelivered(limit)
	deadline := time.Now().Add(limit)
	last := c.emittedCount()
	stableSince := time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Millisecond)
		n := c.emittedCount()
		if n != last {
			last = n
			stableSince = time.Now()
			continue
		}
		if time.Since(stableSince) >= quiet {
			return
		}
	}
}

// fuzzPrimingPacket is one in-order data packet, injected after the handshake
// and before anything the fuzzer generated. The reference implementation's
// acceptor is established only by an ST_DATA and drops everything before
// one, while this library is established after the handshake. Priming
// removes that known difference, pinned by TestConformanceFinBeforeAnyData,
// so generated sequences can find new ones.
func fuzzPrimingPacket() []byte {
	return newPacketBuilder(stData, corpusSynConnID+1, 190000, corpusWindow, corpusSynSeq+1).
		WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("prime")).Build().Encode()
}

// seedDifferentialCorpus adds conversations that reach the interesting
// states, so the fuzzer starts from a completed handshake.
//
// connID is the id a peer addresses packets to, always the recipient's
// receive id: corpusSynConnID+1 for the responder, initiatorConnSeed for the
// initiator. (A SYN carries the sender's own receive id, but no seed is a
// SYN.)
func seedDifferentialCorpus(f *testing.F, connID uint16) {
	seed := func(pkts ...[]byte) {
		var out []byte
		for _, p := range pkts {
			if len(p) > 255 {
				p = p[:255]
			}
			out = append(out, byte(len(p)))
			out = append(out, p...)
		}
		f.Add(out)
	}

	// ack is what the peer acknowledges: the last sequence number the
	// recipient has actually sent. firstInOrder is the next sequence number
	// the recipient expects, so offset 0 is in order and anything above it
	// leaves a gap.
	var ack, firstInOrder uint16
	switch connID {
	case initiatorConnSeed:
		// We sent a SYN numbered initiatorConnSeed and nothing since. The
		// SYN-ACK carried sequence number 900, so ack_nr is 899 and 900 is
		// next.
		ack = initiatorConnSeed
		firstInOrder = 900
	default:
		// Responder: we sent the SYN-ACK numbered corpusPinnedSeq without
		// consuming it, and the priming data packet was corpusSynSeq+1.
		ack = corpusPinnedSeq - 1
		firstInOrder = corpusSynSeq + 2
	}

	mk := func(typ packetType, seqOffset uint16, ts uint32, body []byte) []byte {
		b := newPacketBuilder(typ, connID, ts, corpusWindow, firstInOrder+seqOffset).WithAckNum(ack)
		if len(body) > 0 {
			b = b.WithPayload(body)
		}
		return b.Build().Encode()
	}

	data1 := mk(stData, 0, 200000, []byte("first"))
	data2 := mk(stData, 1, 210000, []byte("second"))
	fin := mk(stFin, 2, 220000, nil)
	reset := mk(stReset, 2, 230000, nil)
	state := mk(stState, 0, 240000, nil)

	seed(data1)
	seed(data1, data2)
	seed(data2, data1) // reordering
	seed(data1, data1) // duplicate
	seed(data1, fin)
	seed(state, data1)
	seed(reset)
	seed(make([]byte, 20))
	seed([]byte{0xFF, 0xFF, 0xFF, 0xFF})
}

func FuzzDifferentialResponder(f *testing.F) {
	seedDifferentialCorpus(f, corpusSynConnID+1)

	f.Fuzz(func(t *testing.T, input []byte) {
		packets := splitPackets(input)
		if len(packets) == 0 {
			return
		}
		if len(packets) > maxDifferentialSteps {
			packets = packets[:maxDifferentialSteps]
		}

		run := newDifferentialRun(t, differentialResponder)
		defer run.close()
		run.prime()

		for step, pkt := range packets {
			run.inject(pkt)
			run.compare(step, packets)
		}
	})
}

// The initiator role: this implementation opens the connection and the
// fuzzer plays the peer that answers, a malicious server rather than a
// malicious client. A BitTorrent client faces this on every dial.
//
// The connection ids and the SYN's sequence number are pinned to the same
// value on both sides: the reference driver's random source repeats its last
// value once exhausted, as pinRandom does, so one pinned value gives both
// implementations identical connection ids and an identical SYN.
func FuzzDifferentialInitiator(f *testing.F) {
	seedDifferentialCorpus(f, initiatorConnSeed)

	f.Fuzz(func(t *testing.T, input []byte) {
		packets := splitPackets(input)
		if len(packets) == 0 {
			return
		}
		if len(packets) > maxDifferentialSteps {
			packets = packets[:maxDifferentialSteps]
		}

		run := newDifferentialRun(t, differentialInitiator)
		defer run.close()
		run.prime()

		for step, pkt := range packets {
			run.inject(pkt)
			run.compare(step, packets)
		}
	})
}

// maxDifferentialSteps caps how many packets one execution injects. Each
// step waits for our side to go quiet, so cost is linear in this. Eight is
// long enough to reach a reordering or a close and short enough for
// thousands of executions per run.
const maxDifferentialSteps = 8

type differentialRole int

const (
	differentialResponder differentialRole = iota
	differentialInitiator
)

const (
	// initiatorConnSeed is the reference's conn_seed for the initiator
	// target: the SYN carries it as the connection id, the peer answers on
	// conn_seed+1, and it is also the SYN's sequence number.
	initiatorConnSeed = uint16(7000)
)

// differentialRun drives both implementations in lockstep, comparing after
// every step, so a divergence is attributed to the packet that caused it.
//
// The comparison is of cumulative transcripts: our side runs goroutines, so
// an emission can land just after the settle meant to catch it, and is then
// counted at the next step rather than producing a spurious failure here.
type differentialRun struct {
	t    *testing.T
	role differentialRole

	drv    *refDriver
	conn   *scriptedConn
	sock   *Socket
	cancel context.CancelFunc
	unpin  func()

	ours   [][]byte
	theirs [][]byte
}

func newDifferentialRun(t *testing.T, role differentialRole) *differentialRun {
	t.Helper()
	r := &differentialRun{t: t, role: role}

	pinned := uint16(corpusPinnedSeq)
	if role == differentialInitiator {
		pinned = initiatorConnSeed
	}

	drv, err := newRefDriver(t, 1_000_000)
	if err != nil {
		t.Skipf("libutp driver unavailable: %v", err)
	}
	drv.PushRandom(uint32(pinned))
	r.drv = drv

	r.unpin = pinRandom(pinned)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	r.cancel = cancel
	r.conn = newScriptedConn()
	r.sock = NewSocket(ctx, r.conn, conformanceLogger())

	switch role {
	case differentialResponder:
		drv.Listen()
		cid := NewConnectionID(r.conn.peer, corpusSynConnID+1, corpusSynConnID)
		go func() { _, _ = r.sock.AcceptWithCID(ctx, cid, pinnedClockConfig(drv)) }()
		// The acceptor emits nothing until a SYN arrives, so wait for the
		// request to be registered: a cid-specific Accept waits in the
		// awaiting map until its SYN arrives.
		r.waitFor(t, "the acceptor to register", func() bool {
			_, ok := r.sock.getAwaiting(cid.Hash())
			return ok
		})
	case differentialInitiator:
		if err := drv.Connect(); err != nil {
			t.Skipf("libutp connect failed: %v", err)
		}
		cid := NewConnectionID(r.conn.peer, initiatorConnSeed, initiatorConnSeed+1)
		go func() { _, _ = r.sock.ConnectWithCID(ctx, cid, pinnedClockConfig(drv)) }()
		// Wait for the SYN itself rather than a fixed interval, so it cannot
		// land in the first step's transcript.
		r.waitFor(t, "our SYN", func() bool {
			return r.conn.emittedCount() > 0
		})
	}
	return r
}

// waitFor polls until cond holds, and fails the test if it never does. The
// deadline only bounds a failure; the common case returns on the first or
// second poll.
func (r *differentialRun) waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	const limit = 10 * time.Second
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waited %v for %s and it never happened", limit, what)
		}
		time.Sleep(time.Millisecond)
	}
}

func (r *differentialRun) close() {
	r.sock.Close()
	r.cancel()
	r.conn.Close()
	r.drv.Close()
	r.unpin()
}

// prime brings both implementations to the same established state and
// discards everything emitted on the way: for the responder a SYN and one
// in-order data packet (see fuzzPrimingPacket), for the initiator the
// SYN-ACK.
func (r *differentialRun) prime() {
	r.t.Helper()
	switch r.role {
	case differentialResponder:
		r.injectBoth(synPacketFor(corpusSynConnID, corpusSynSeq).Encode())
		r.injectBoth(fuzzPrimingPacket())
	case differentialInitiator:
		r.injectBoth(initiatorSynAck())
	}
	r.drv.ClearEmitted()
	r.conn.takeEmitted()
	r.ours = nil
	r.theirs = nil
}

func (r *differentialRun) injectBoth(pkt []byte) {
	r.drv.Inject(pkt)
	r.drv.IssueAcks()
	r.conn.inject(pkt)
	r.conn.settleQuick()
}

func (r *differentialRun) inject(pkt []byte) {
	r.injectBoth(pkt)
	r.theirs = append(r.theirs, r.drv.Emitted()...)
	r.drv.ClearEmitted()
	r.ours = append(r.ours, r.conn.takeEmitted()...)
}

// compare applies the direction rule to the transcripts so far.
func (r *differentialRun) compare(step int, injected [][]byte) {
	t := r.t
	t.Helper()

	if len(r.ours) > len(r.theirs) {
		t.Fatalf("after step %d of %d we had emitted %d packets and libutp %d.\n"+
			"Answering a packet the reference ignores is the direction that matters: "+
			"it is an amplification surface and an attack surface.\n"+
			"  injected:%s\n  ours:%s\n  libutp:%s",
			step, len(injected), len(r.ours), len(r.theirs),
			describePackets(injected), describePackets(r.ours), describePackets(r.theirs))
	}

	// Group per connection id, not by position. Order between packets for
	// different connections is not observable by a peer and can differ, since
	// a RESET for an unknown connection is sent from the socket's goroutine
	// and a connection's STATE from that connection's. Order within one
	// connection is observable and is compared.
	ourByConn := groupByConnection(t, r.ours)
	theirByConn := groupByConnection(t, r.theirs)

	for connID, ourPkts := range ourByConn {
		theirPkts := theirByConn[connID]
		if len(ourPkts) > len(theirPkts) {
			t.Fatalf("after step %d, for connection %d we had emitted %d packets and libutp %d.\n"+
				"  injected:%s\n  ours:%s\n  libutp:%s",
				step, connID, len(ourPkts), len(theirPkts),
				describePackets(injected), describePackets(r.ours), describePackets(r.theirs))
		}
		for i := range ourPkts {
			bad, _ := significant(comparePackets(ourPkts[i], theirPkts[i]))
			if len(bad) > 0 {
				t.Fatalf("after step %d, connection %d packet %d differs from libutp in %d "+
					"significant field(s): %v\n  injected:%s\n  ours:%s\n  libutp:%s",
					step, connID, i, len(bad), bad,
					describePackets(injected), describePackets(r.ours), describePackets(r.theirs))
			}
		}
	}
}

// initiatorSynAck is the peer's answer to our SYN: a STATE addressed to our
// receive id, acknowledging the SYN's sequence number.
func initiatorSynAck() []byte {
	return newPacketBuilder(stState, initiatorConnSeed, 150000, corpusWindow, 900).
		WithAckNum(initiatorConnSeed).Build().Encode()
}

// groupByConnection decodes emissions and buckets them by connection id,
// keeping each bucket in emission order.
func groupByConnection(t *testing.T, raws [][]byte) map[uint16][]*packet {
	t.Helper()
	out := make(map[uint16][]*packet)
	for _, raw := range raws {
		pkt, err := decodePacket(raw, false)
		if err != nil {
			// Either side emitting something this decoder rejects is a real
			// finding: our own output must parse, and the reference's output
			// is what a real peer sends.
			t.Fatalf("emitted packet does not decode (%v): %x", err, raw)
		}
		out[pkt.Header.ConnectionID] = append(out[pkt.Header.ConnectionID], pkt)
	}
	return out
}

// A differential target that drives nothing would pass forever, since two
// empty transcripts agree. These check that the harness reaches a live
// connection on both sides and that both answer.
func TestDifferentialHarnessIsLive(t *testing.T) {
	for _, tc := range []struct {
		name string
		role differentialRole
		// probe is an in-order data packet, which both implementations must
		// acknowledge.
		probe []byte
	}{
		{
			name: "responder",
			role: differentialResponder,
			probe: newPacketBuilder(stData, corpusSynConnID+1, 250000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("probe")).Build().Encode(),
		},
		{
			name: "initiator",
			role: differentialInitiator,
			probe: newPacketBuilder(stData, initiatorConnSeed, 250000, corpusWindow, 900).
				WithAckNum(initiatorConnSeed).WithPayload([]byte("probe")).Build().Encode(),
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			run := newDifferentialRun(t, tc.role)
			defer run.close()

			// Before priming clears them, both must have emitted the
			// handshake packet their role owes.
			run.prime()

			run.inject(tc.probe)
			if len(run.theirs) == 0 {
				t.Errorf("libutp answered an in-order data packet with nothing; the harness is "+
					"not reaching an established connection, so this target compares two empty "+
					"transcripts and would pass whatever the code did.\n  ours:%s",
					describePackets(run.ours))
			}
			if len(run.ours) == 0 {
				t.Errorf("we answered an in-order data packet with nothing; the harness is not "+
					"reaching an established connection.\n  libutp:%s", describePackets(run.theirs))
			}
			t.Logf("%s: an in-order data packet drew %d packet(s) from us and %d from libutp%s",
				tc.name, len(run.ours), len(run.theirs), describePackets(run.ours))
		})
	}
}

// The handshake must happen, or priming would hide a dead connection.
func TestDifferentialHandshakeIsEmitted(t *testing.T) {
	t.Run("initiator sends a SYN", func(t *testing.T) {
		run := newDifferentialRun(t, differentialInitiator)
		defer run.close()

		ourSyn := run.conn.takeEmitted()
		theirSyn := run.drv.Emitted()

		if len(ourSyn) == 0 {
			t.Fatal("we emitted no SYN when connecting")
		}
		if len(theirSyn) == 0 {
			t.Fatal("libutp emitted no SYN when connecting")
		}

		ours, err := decodePacket(ourSyn[0], false)
		if err != nil {
			t.Fatalf("our SYN does not decode: %v", err)
		}
		theirs, err := decodePacket(theirSyn[0], false)
		if err != nil {
			t.Fatalf("libutp's SYN does not decode: %v", err)
		}

		bad, _ := significant(comparePackets(ours, theirs))
		if len(bad) > 0 {
			t.Errorf("our SYN differs from libutp's in %d significant field(s): %v\n"+
				"  ours:%s\n  libutp:%s", len(bad), bad,
				describePackets(ourSyn[:1]), describePackets(theirSyn[:1]))
		}
		t.Logf("SYN: ours%s", describePackets(ourSyn[:1]))
	})
}
