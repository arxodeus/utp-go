# conformance_fuzz_test.go

Every comment in `conformance_fuzz_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build cgo
```

## Line 13

```
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
```

## Line 59 — func (*scriptedConn).settleQuick

```
// settleQuick is settle with a shorter quiet period, for a fuzz target where
// the per-execution cost is what limits coverage.
```

## Line 64 — func (*scriptedConn).settleQuick

```
// Wait for our socket to actually take the packet before timing anything.
	// Without this the quiet window could elapse while the packet was still
	// queued, so settleQuick returned before our side had reacted at all --
	// and the reaction then landed in the *next* step's transcript, or was
	// coalesced into the next acknowledgement and never appeared. That showed
	// up only under full-suite load, as FuzzDifferentialInitiator reporting
	// one packet against libutp's two on a reordered pair.
```

## Line 89 — func fuzzPrimingPacket

```
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
```

## Line 107 — func seedDifferentialCorpus

```
// seedDifferentialCorpus adds conversations that reach the interesting states,
// so the fuzzer starts from something that has completed a handshake rather
// than having to rediscover one.
//
// connID is the id a peer addresses packets to, which is always the
// recipient's *receive* id: corpusSynConnID+1 for the responder,
// initiatorConnSeed for the initiator. (A SYN is the exception -- it carries
// the sender's own receive id -- but no seed here is a SYN.)
```

## Line 128 — func seedDifferentialCorpus

```
// ack is what the peer acknowledges: the last sequence number the
	// recipient has actually sent. firstInOrder is the next sequence number
	// the recipient expects, so offset 0 is in order and anything above it
	// leaves a gap.
```

## Line 135 — func seedDifferentialCorpus

```
// We sent a SYN numbered initiatorConnSeed and nothing since. The
		// SYN-ACK carried sequence number 900, so our ack_nr is 899 and the
		// next packet we expect is 900.
```

## Line 141 — func seedDifferentialCorpus

```
// Responder: we sent the SYN-ACK numbered corpusPinnedSeq without
		// consuming it, and the priming data packet was corpusSynSeq+1.
```

## Line 163 — func seedDifferentialCorpus

```
// reordering
```

## Line 164 — func seedDifferentialCorpus

```
// duplicate
```

## Line 195 — func FuzzDifferentialInitiator

```
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
```

## Line 233 — const maxDifferentialSteps

```
// maxDifferentialSteps caps how many packets one execution injects.
//
// Each step waits for our side to go quiet, so the cost is linear in this and
// the exec rate is what limits coverage. Eight is a compromise: long enough to
// reach a reordering or a close, short enough that the fuzzer gets thousands
// of executions in a run rather than hundreds.
```

## Line 249 — const initiatorConnSeed

```
// initiatorConnSeed is libutp's conn_seed for the initiator target: the
	// SYN carries it as the connection id, the peer answers on conn_seed+1,
	// and it doubles as the SYN's sequence number.
```

## Line 255 — type differentialRun

```
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
```

## Line 311 — func newDifferentialRun

```
// The acceptor emits nothing until a SYN arrives, so the only thing
		// to wait for is the request being registered to receive one. A
		// cid-specific Accept parks in the awaiting map until its SYN
		// arrives, so that is where it becomes visible.
```

## Line 325 — func newDifferentialRun

```
// Wait for the SYN itself, not for a fixed interval.
		//
		// This used to sleep 20ms and assume the connection goroutine had run
		// by then. Under load it had not, and the SYN -- emitted before any
		// injection and cleared by prime() -- instead landed in the first
		// step's transcript, where it read as this implementation answering a
		// packet libutp ignored. Seen as a full-suite-only failure of
		// FuzzDifferentialInitiator/seed#2, passing whenever the test ran on
		// its own, which is exactly the shape of a timing assumption rather
		// than a defect in what is under test.
```

## Line 342 — func (*differentialRun).waitFor

```
// waitFor polls until cond holds, and fails the test if it never does. The
// deadline is generous because it only bounds a failure: the common case
// returns on the first or second poll.
```

## Line 365 — func (*differentialRun).prime

```
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
```

## Line 406 — func (*differentialRun).compare

```
// compare applies the direction rule to the transcripts so far.
```

## Line 420 — func (*differentialRun).compare

```
// Per connection id, not by position. Order between packets for different
	// connections is not observable by any peer, and ours falls out
	// differently because a RESET for a connection we do not have is answered
	// on the socket's goroutine while a connection's own STATE goes through
	// that connection's. Order *within* one connection is observable, and is
	// what is compared.
```

## Line 449 — func initiatorSynAck

```
// initiatorSynAck is the peer's answer to our SYN: a STATE addressed to our
// receive id, acknowledging the SYN's sequence number.
```

## Line 456 — func groupByConnection

```
// groupByConnection decodes emissions and buckets them by connection id,
// keeping each bucket in emission order.
```

## Line 464 — func groupByConnection

```
// Either side emitting something this decoder rejects is a real
			// finding: our own output must be parseable by us, and libutp's
			// output is what a real peer sends.
```

## Line 474 — func TestDifferentialHarnessIsLive

```
// A differential target that drives nothing would pass forever: two empty
// transcripts agree. These assert the harness actually reaches a live
// connection on both sides and that both answer, so a regression that made
// the fuzzing vacuous is caught rather than celebrated.
```

## Line 482 — func TestDifferentialHarnessIsLive

```
// probe is an in-order data packet, which both implementations must
		// acknowledge.
```

## Line 504 — func TestDifferentialHarnessIsLive

```
// Before priming clears them, both must have emitted the
			// handshake packet their role owes.
```

## Line 525 — func TestDifferentialHandshakeIsEmitted

```
// The handshake itself has to happen, or priming is hiding a dead connection.
```
