//go:build cgo

package utp_go

import (
	"context"
	"testing"
	"time"
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

func FuzzDifferentialResponder(f *testing.F) {
	// Seed with the shapes the M2 corpus covers, so the fuzzer starts from
	// conversations that reach the interesting states rather than having to
	// rediscover the handshake.
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

	data1 := NewPacketBuilder(st_data, corpusSynConnID+1, 200000, corpusWindow, corpusSynSeq+2).
		WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("first")).Build().Encode()
	data2 := NewPacketBuilder(st_data, corpusSynConnID+1, 210000, corpusWindow, corpusSynSeq+3).
		WithAckNum(corpusPinnedSeq - 1).WithPayload([]byte("second")).Build().Encode()
	fin := NewPacketBuilder(st_fin, corpusSynConnID+1, 220000, corpusWindow, corpusSynSeq+4).
		WithAckNum(corpusPinnedSeq - 1).Build().Encode()
	reset := NewPacketBuilder(st_reset, corpusSynConnID+1, 230000, corpusWindow, corpusSynSeq+4).
		WithAckNum(corpusPinnedSeq - 1).Build().Encode()

	seed(data1)
	seed(data1, data2)
	seed(data2, data1) // reordering
	seed(data1, data1) // duplicate
	seed(data1, fin)
	seed(reset)
	seed(make([]byte, 20))
	seed([]byte{0xFF, 0xFF, 0xFF, 0xFF})

	f.Fuzz(func(t *testing.T, input []byte) {
		packets := splitPackets(input)
		if len(packets) == 0 {
			return
		}

		theirs := driveLibutpResponder(t, packets)
		ours := driveOurResponder(t, packets)

		// The dangerous direction: we answered where the reference did not.
		if len(ours) > len(theirs) {
			t.Fatalf("we emitted %d packets where libutp emitted %d.\n"+
				"Answering a packet the reference ignores is the direction that "+
				"matters: it is an amplification surface and an attack surface.\n"+
				"  injected:%s\n  ours:%s\n  libutp:%s",
				len(ours), len(theirs),
				describePackets(packets), describePackets(ours), describePackets(theirs))
		}

		// Compare per connection id, not by position in the stream.
		//
		// Order between packets for *different* connections is not
		// observable by any peer -- each sees only its own -- and ours falls
		// out differently because a RESET for a connection we do not have is
		// answered on the socket's goroutine while a connection's own STATE
		// goes through that connection's. libutp is single-threaded and
		// emits them in its own order. Comparing by position reported that
		// as a five-field divergence when the two had in fact emitted the
		// same two packets.
		//
		// Order *within* one connection is observable, and is compared.
		ourByConn := groupByConnection(t, ours)
		theirByConn := groupByConnection(t, theirs)

		for connID, ourPkts := range ourByConn {
			theirPkts := theirByConn[connID]
			if len(ourPkts) > len(theirPkts) {
				t.Fatalf("for connection %d we emitted %d packets and libutp emitted %d.\n"+
					"  injected:%s\n  ours:%s\n  libutp:%s",
					connID, len(ourPkts), len(theirPkts),
					describePackets(packets), describePackets(ours), describePackets(theirs))
			}
			for i := range ourPkts {
				bad, _ := significant(comparePackets(ourPkts[i], theirPkts[i]))
				if len(bad) > 0 {
					t.Fatalf("connection %d, packet %d differs from libutp in %d significant "+
						"field(s): %v\n  injected:%s\n  ours:%s\n  libutp:%s",
						connID, i, len(bad), bad,
						describePackets(packets), describePackets(ours), describePackets(theirs))
				}
			}
		}
	})
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

// driveLibutpResponder runs the real libutp as the accepting side and returns
// everything it emitted after the handshake.
func driveLibutpResponder(t *testing.T, packets [][]byte) [][]byte {
	t.Helper()
	drv, err := libutpNewDriverForCorpus()
	if err != nil {
		t.Skipf("libutp driver unavailable: %v", err)
	}
	defer drv.Close()

	drv.Listen()
	drv.Inject(synPacketFor(corpusSynConnID, corpusSynSeq).Encode())
	drv.IssueAcks()
	drv.Inject(fuzzPrimingPacket())
	drv.IssueAcks()
	drv.ClearEmitted()

	for _, pkt := range packets {
		drv.Inject(pkt)
		drv.IssueAcks()
	}
	return drv.Emitted()
}

// driveOurResponder runs this implementation as the accepting side, with its
// sequence numbers pinned so they can be compared.
func driveOurResponder(t *testing.T, packets [][]byte) [][]byte {
	t.Helper()
	restore := pinRandom(corpusPinnedSeq)
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conn := newScriptedConn()
	defer conn.Close()
	sock := WithSocket(ctx, conn, conformanceLogger())
	defer sock.Close()

	cid := NewConnectionId(conn.peer, corpusSynConnID+1, corpusSynConnID)
	go func() { _, _ = sock.AcceptWithCid(ctx, cid, NewConnectionConfig()) }()
	time.Sleep(20 * time.Millisecond)

	conn.inject(synPacketFor(corpusSynConnID, corpusSynSeq).Encode())
	conn.settleQuick()
	conn.inject(fuzzPrimingPacket())
	conn.settleQuick()
	conn.takeEmitted()

	for _, pkt := range packets {
		conn.inject(pkt)
	}
	conn.settleQuick()
	return conn.takeEmitted()
}
