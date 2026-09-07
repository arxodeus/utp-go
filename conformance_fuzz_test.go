//go:build cgo

package utp_go

import (
	"context"
	"testing"
	"time"

	"github.com/zen-eth/utp-go/native/libutp"
)

// Differential fuzzing: the fuzzer generating input, and libutp deciding
// whether the answer was right.
//
// The two halves existed separately. M2's harness can drive both
// implementations through the same packets and compare what each emits, field
// by field -- but only on cases someone wrote down. M8's fuzzer generates
// packet sequences nobody wrote down -- but could only tell that this
// implementation had crashed, hung, or died, because it had nothing to compare
// against. Neither half can find a *behavioural* divergence on input nobody
// thought of. Together they can.
//
// This is the target that turns "libutp would have answered differently" into
// a fuzz failure.
//
// # The property
//
// Not "the two agree". They deliberately do not, in ways CONFORMANCE.md and
// DEVIATIONS.md set out, and a fuzzer that failed on those would be useless.
// The property is the direction rule those documents already use:
//
//	Being stricter than the reference is an interoperability risk.
//	Being more permissive is an attack surface.
//
// So:
//
//   - **We must never answer more than libutp does.** Emitting a packet where
//     the reference stays silent is the dangerous direction: it is the
//     amplification surface, and it is what a malformed-input attack looks
//     for. This is asserted strictly.
//   - **Where both answer, the significant fields must agree.** Timestamps and
//     the advertised window are exempt for the reasons in
//     `allowedToDiffer`; everything a peer's state machine depends on is not.
//   - **Answering less than libutp is permitted**, and is where the documented
//     divergences live (the selective-ack length check, the FIN duplicate
//     ack). Being quieter cannot be exploited; it can only cost
//     interoperability, which the M2 corpus pins case by case.
//
// # Speed
//
// This runs at a few executions per second, against ~50,000 for the decoder
// fuzzers, because each execution builds two implementations and waits for
// ours to go quiet. That is the price of an oracle. Run it for minutes, not
// seconds:
//
//	go test -tags cgo -run xxx -fuzz FuzzDifferentialResponder -fuzztime 10m

// settleQuick is settle with a shorter quiet period, for a fuzz target where
// the per-execution cost is what limits coverage.
func (c *scriptedConn) settleQuick() {
	const quiet = 25 * time.Millisecond
	const limit = 600 * time.Millisecond
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
// and before anything the fuzzer generated.
//
// It exists to remove a state difference that is documented elsewhere and is
// not what this target is hunting. libutp completes an incoming connection
// only on an ST_DATA packet (utp_internal.cpp:2158-2161); until one arrives it
// sits in CS_SYN_RECV and drops everything (:2314). We treat the connection as
// established once the handshake completes. Without priming, every generated
// sequence starting with anything but data reports that one known divergence
// instead of finding a new one.
//
// The divergence itself is pinned by TestConformanceFinBeforeAnyData, so
// removing it here does not hide it.
func fuzzPrimingPacket() []byte {
	return NewPacketBuilder(st_data, corpusSynConnID+1, 190000, corpusWindow, corpusSynSeq+1).
		WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("prime")).Build().Encode()
}

// seedDifferentialCorpus adds conversations that reach the interesting states,
// so the fuzzer starts from something that has completed a handshake rather
// than having to rediscover one.
//
// connID is the id a peer addresses packets to, which is always the
// recipient's *receive* id: corpusSynConnID+1 for the responder,
// initiatorConnSeed for the initiator. (A SYN is the exception -- it carries
// the sender's own receive id -- but no seed here is a SYN.)
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
		// SYN-ACK carried sequence number 900, so our ack_nr is 899 and the
		// next packet we expect is 900.
		ack = initiatorConnSeed
		firstInOrder = 900
	default:
		// Responder: we sent the SYN-ACK numbered corpusPinnedSeq without
		// consuming it, and the priming data packet was corpusSynSeq+1.
		ack = corpusPinnedSeq - 1
		firstInOrder = corpusSynSeq + 2
	}

	mk := func(typ PacketType, seqOffset uint16, ts uint32, body []byte) []byte {
		b := NewPacketBuilder(typ, connID, ts, corpusWindow, firstInOrder+seqOffset).WithAckNum(ack)
		if len(body) > 0 {
			b = b.WithPayload(body)
		}
		return b.Build().Encode()
	}

	data1 := mk(st_data, 0, 200000, []byte("first"))
	data2 := mk(st_data, 1, 210000, []byte("second"))
	fin := mk(st_fin, 2, 220000, nil)
	reset := mk(st_reset, 2, 230000, nil)
	state := mk(st_state, 0, 240000, nil)

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

// The initiator role: this implementation opens the connection and the fuzzer
// plays the peer that answers.
//
// The responder target above covers a malicious *client*. This covers a
// malicious *server* -- the side that answers a SYN we sent. That is the
// direction a BitTorrent client is exposed to every time it dials a peer
// address it got from a tracker or the DHT, which is to say constantly, and
// nothing in this repository modelled it: the M2 corpus is responder-only too.
//
// The connection ids and the SYN's sequence number are pinned to the same
// value on both sides. libutp derives conn_id_recv from one random draw and
// its SYN sequence number from another (utp_internal.cpp:2535 and :2768), and
// its driver's random source repeats its last value once exhausted -- the same
// behaviour as pinRandom here -- so a single pinned value gives both
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

// maxDifferentialSteps caps how many packets one execution injects.
//
// Each step waits for our side to go quiet, so the cost is linear in this and
// the exec rate is what limits coverage. Eight is a compromise: long enough to
// reach a reordering or a close, short enough that the fuzzer gets thousands
// of executions in a run rather than hundreds.
const maxDifferentialSteps = 8

type differentialRole int

const (
	differentialResponder differentialRole = iota
	differentialInitiator
)

const (
	// initiatorConnSeed is libutp's conn_seed for the initiator target: the
	// SYN carries it as the connection id, the peer answers on conn_seed+1,
	// and it doubles as the SYN's sequence number.
	initiatorConnSeed = uint16(7000)
)

// differentialRun drives both implementations in lockstep so a divergence can
// be attributed to the packet that caused it.
//
// The previous version drove libutp to completion, then ours, and compared the
// two transcripts at the end. That could say *that* they disagreed but not
// *where*, and a divergence that appeared at one step and was undone by a
// later one was invisible. Comparing after every step is strictly stronger.
//
// The comparison is cumulative rather than per-step-in-isolation, and
// deliberately so: our side is goroutine-driven on a real clock, so an
// emission can land just after the settle that was meant to catch it.
// Comparing cumulative transcripts means such an emission is counted at the
// next step instead of producing a spurious "we emitted more than libutp"
// at this one. It still catches the divergence, one step later at worst.
type differentialRun struct {
	t    *testing.T
	role differentialRole

	drv    *libutp.Driver
	conn   *scriptedConn
	sock   *UtpSocket
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

	drv, err := libutp.NewDriver(1_000_000)
	if err != nil {
		t.Skipf("libutp driver unavailable: %v", err)
	}
	drv.PushRandom(uint32(pinned))
	r.drv = drv

	r.unpin = pinRandom(pinned)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	r.cancel = cancel
	r.conn = newScriptedConn()
	r.sock = WithSocket(ctx, r.conn, conformanceLogger())

	switch role {
	case differentialResponder:
		drv.Listen()
		cid := NewConnectionId(r.conn.peer, corpusSynConnID+1, corpusSynConnID)
		go func() { _, _ = r.sock.AcceptWithCid(ctx, cid, NewConnectionConfig()) }()
		time.Sleep(20 * time.Millisecond)
	case differentialInitiator:
		if err := drv.Connect(); err != nil {
			t.Skipf("libutp connect failed: %v", err)
		}
		cid := NewConnectionId(r.conn.peer, initiatorConnSeed, initiatorConnSeed+1)
		go func() { _, _ = r.sock.ConnectWithCid(ctx, cid, NewConnectionConfig()) }()
		time.Sleep(20 * time.Millisecond)
	}
	return r
}

func (r *differentialRun) close() {
	r.sock.Close()
	r.cancel()
	r.conn.Close()
	r.drv.Close()
	r.unpin()
}

// prime brings both implementations to the same established state and
// discards everything emitted getting there.
//
// For the responder that means a SYN followed by one in-order data packet:
// libutp completes an incoming connection only on an ST_DATA
// (utp_internal.cpp:2158-2161) and drops everything else until one arrives,
// while we treat it as established after the handshake. That divergence is
// pinned by TestConformanceFinBeforeAnyData; priming past it here stops every
// generated sequence from reporting it instead of finding something new.
//
// For the initiator it means the SYN-ACK, which is what moves libutp from
// CS_SYN_SENT to CS_CONNECTED (utp_internal.cpp:2164).
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

	// Per connection id, not by position. Order between packets for different
	// connections is not observable by any peer, and ours falls out
	// differently because a RESET for a connection we do not have is answered
	// on the socket's goroutine while a connection's own STATE goes through
	// that connection's. Order *within* one connection is observable, and is
	// what is compared.
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
	return NewPacketBuilder(st_state, initiatorConnSeed, 150000, corpusWindow, 900).
		WithAckNum(initiatorConnSeed).Build().Encode()
}

// groupByConnection decodes emissions and buckets them by connection id,
// keeping each bucket in emission order.
func groupByConnection(t *testing.T, raws [][]byte) map[uint16][]*packet {
	t.Helper()
	out := make(map[uint16][]*packet)
	for _, raw := range raws {
		pkt, err := DecodePacket(raw)
		if err != nil {
			// Either side emitting something this decoder rejects is a real
			// finding: our own output must be parseable by us, and libutp's
			// output is what a real peer sends.
			t.Fatalf("emitted packet does not decode (%v): %x", err, raw)
		}
		out[pkt.Header.ConnectionId] = append(out[pkt.Header.ConnectionId], pkt)
	}
	return out
}

// A differential target that drives nothing would pass forever: two empty
// transcripts agree. These assert the harness actually reaches a live
// connection on both sides and that both answer, so a regression that made
// the fuzzing vacuous is caught rather than celebrated.
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
			probe: NewPacketBuilder(st_data, corpusSynConnID+1, 250000, corpusWindow, corpusSynSeq+2).
				WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("probe")).Build().Encode(),
		},
		{
			name: "initiator",
			role: differentialInitiator,
			probe: NewPacketBuilder(st_data, initiatorConnSeed, 250000, corpusWindow, 900).
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

// The handshake itself has to happen, or priming is hiding a dead connection.
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

		ours, err := DecodePacket(ourSyn[0])
		if err != nil {
			t.Fatalf("our SYN does not decode: %v", err)
		}
		theirs, err := DecodePacket(theirSyn[0])
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
