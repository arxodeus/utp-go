# conformance_corpus_test.go

Every comment in `conformance_corpus_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build cgo
```

## Line 17

```
// The M2 corpus.
//
// Each case scripts a sequence of steps and runs it against both
// implementations as the same role, comparing what each emits at every step.
// Sequence numbers and connection ids are pinned on both sides, so the
// comparison is field-by-field exact apart from allowedToDiffer.
```

## Line 24 — type step

```
// step is one action applied to both implementations.
```

## Line 27 — type step

```
// inject is a packet arriving from the peer.
```

## Line 29 — type step

```
// injectRaw is the same, but as bytes, for packets too malformed to build
	// through the packet builder.
```

## Line 32 — type step

```
// write queues application data.
```

## Line 34 — type step

```
// closeStream closes the connection.
```

## Line 36 — type step

```
// advance moves libutp's virtual clock and runs its timeout check. Our
	// implementation runs on the real clock, so the runner sleeps the same
	// amount; see CONFORMANCE.md on why timing comparison is coarse.
```

## Line 40 — type step

```
// wantNoEmission asserts both sides stay silent.
```

## Line 42 — type step

```
// wantAck, when set, asserts the ack number on the last packet we emit.
	//
	// The rest of this struct compares us against libutp, which cannot tell
	// which code path a step took -- only that both took the same one. Feed
	// the corpus a sequence number one past what the connection expects and
	// every "in order" case quietly becomes an out-of-order case, agreeing
	// with libutp the whole way. Seven of the nine initiator cases passed
	// that way. An expected ack number is a claim about this implementation
	// alone, so it fails when the path changes underneath it.
```

## Line 52 — type step

```
// libutpExtraDuplicates records a deliberate divergence: libutp emits
	// this many more packets than we do, and each extra one is a duplicate of
	// the packet before it. Stating the number and the reason here is the
	// point -- the brief's rule is that a deliberate divergence is asserted
	// explicitly rather than hidden inside a tolerance.
```

## Line 59 — type step

```
// oursExtra is the same thing the other way round: we emit this many
	// packets that libutp does not, after everything it does emit, for the
	// recorded reason in divergenceReason.
```

## Line 65 — const corpusPinnedSeq, corpusSynConnID, corpusSynSeq, corpusWindow

```
// corpusPinnedSeq is the sequence number both implementations are pinned to
// for their SYN-ACK. Packets from the peer ack corpusPinnedSeq-1: libutp
// drops anything acking a sequence number it has not sent yet
// (utp_internal.cpp:1795-1806), and a bare ST_STATE does not consume one.
```

## Line 76 — func ackNum

```
// ackNum is the pointer wrapper for step.wantAck.
```

## Line 79 — func synPacketFor

```
// synPacketFor builds the SYN that opens every responder-role case.
```

## Line 84 — func runResponderCorpus

```
// runResponderCorpus drives both implementations as the responder through the
// same steps, comparing emissions after each.
```

## Line 89 — func runResponderCorpus

```
// --- libutp side ---
```

## Line 98 — func runResponderCorpus

```
// --- our side ---
```

## Line 133 — func runResponderCorpus

```
// libutp
```

## Line 153 — func runResponderCorpus

```
// ours
```

## Line 181 — func compareStep

```
// compareStep diffs the packets both implementations emitted for one step.
```

## Line 202 — func compareStep

```
// Every extra packet must be byte-identical to the one before it.
		// If it carried new information this would not be a divergence we
		// could dismiss.
```

## Line 247 — func compareStep

```
// Two silent sides compare equal, and a step that expected an answer and
	// got none from either would pass while asserting nothing at all. A step
	// that means to observe silence says so with wantNoEmission; anything
	// else has to produce something to compare.
	//
	// This guard exists because the initiator corpus was written with its
	// data packets one sequence number past what the dialling side expects.
	// Every "in order" case was quietly exercising the out-of-order path, and
	// all of them passed.
```

## Line 290

```
// --- the corpus -------------------------------------------------------------
```

## Line 319 — func TestConformanceDuplicateData

```
// A packet arriving twice must not be acked as if it were new data.
```

## Line 326 — func TestConformanceDuplicateData

```
// A duplicate advances nothing; the ack repeats.
```

## Line 331 — func TestConformanceReordering

```
// A gap in the sequence space: the second packet arrives before the first.
```

## Line 339 — func TestConformanceReordering

```
// The gap is open, so the ack stays on the SYN.
```

## Line 346 — func TestConformanceReordering

```
// Both are in order now, so the ack jumps over both.
```

## Line 352 — func TestConformanceIncomingFin

```
// A FIN from the peer.
```

## Line 372 — func TestConformanceIncomingFin

```
// The FIN consumes a sequence number, so the ack covers it.
```

## Line 378 — func TestConformanceIncomingReset

```
// A RESET must terminate the connection without a reply.
```

## Line 391 — func corpusPeerData

```
// A zero-window advertisement from the peer.
// corpusPeerData is the ST_DATA that completes the handshake from the peer's
// side. libutp's responder stays in CS_SYN_RECV until an ST_DATA arrives
// (utp_internal.cpp:2158, "Incoming connection completion"), and utp_writev
// returns 0 for any state but CS_CONNECTED (utp_internal.cpp:3181). So a
// responder-role case that wants to write anything has to be given data
// first, or libutp silently accepts the write and never sends it.
```

## Line 403 — func TestConformanceZeroWindow

```
// A zero receive window stalls the sender until the peer reopens it
// (utp_internal.cpp:936, where max_window_user is one of the three limits
// is_full weighs). Advertising the zero window on its own proves nothing -- a
// bare ST_STATE is answered with silence whatever window it carries -- so the
// case writes through the stall and out the other side.
//
// TestConformanceWriteFlowsWithoutZeroWindow is the control: the same script
// without the zero window, where the write leaves at once. Without it this
// case would still pass if writes never went out at all, which is exactly the
// state it was in before -- libutp was refusing every write for an unrelated
// reason and the silence read as a zero-window stall.
```

## Line 437 — func TestConformanceWriteFlowsWithoutZeroWindow

```
// The control for TestConformanceZeroWindow: identical but for the zero
// window, and here the write leaves immediately.
```

## Line 453 — func TestConformanceWriteIsNotCutToFitPeerWindow

```
// libutp sends nothing into less than a full packet of room and never cuts a
// packet down to fit: flush_packets checks is_full() with no argument, which
// charges a whole packet_size (utp_internal.cpp:933-936, :974). We used to
// send whatever the window had room for. The rest of the rule -- bytes in
// flight spend the peer's window -- cannot be isolated against libutp at
// connection start and is asserted in peer_window_test.go.
```

## Line 475 — func TestConformanceWriteIsNotCutToFitPeerWindow

```
// Wide enough for a full packet on both sides: libutp's first
			// packet_size is 1452 and ours 962 (KNOWN-LIMITATIONS.md, M6).
```

## Line 484 — func TestConformanceInvalidAckNumIsIgnored

```
// libutp drops any packet whose ack_nr acks a sequence number it has not sent
// yet, calling it "a spoofed address or a malicious attempt to attach the uTP
// implementation" (utp_internal.cpp:1795-1806). Accepting such a packet is
// both an incompatibility and an attack surface, so the corpus checks it.
```

## Line 494 — func TestConformanceInvalidAckNumIsIgnored

```
// corpusPinnedSeq is our next unsent sequence number; acking it
				// is acking a packet that does not exist.
```

## Line 502 — func TestConformanceWildlyInvalidAckNumIsIgnored

```
// The same, far outside the window rather than one past it.
```

## Line 515 — func (step).rawBytes

```
// rawBytes is the wire form of whatever this step injects, if anything.
```

## Line 526 — func libutpNewDriverForCorpus

```
// libutpNewDriverForCorpus builds a driver pinned the way the corpus expects.
```

## Line 536 — func goResponderForCorpus

```
// goResponderForCorpus starts one of our sockets accepting the corpus
// connection, and returns its scripted transport.
```

## Line 545 — func goResponderForCorpus

```
// Not clock-pinned: this helper's callers measure our side's own
		// behaviour (ack counts, malformed-packet handling) rather than
		// comparing packet fields against libutp, so there is no virtual
		// clock to agree with.
```

## Line 555 — func TestConformanceWideReordering

```
// A reorder gap wider than the 30-packet window libutp scans when it builds a
// selective ack. This is the case that distinguishes a bounded mask from an
// unbounded one: with a packet pending far past the gap, an implementation
// that sizes the mask to reach it emits a much larger extension than libutp,
// which always emits exactly four bytes (utp_internal.cpp:797, :805).
```

## Line 562 — func TestConformanceWideReordering

```
// Leave seq+1 missing, then deliver a packet 40 past it.
```

## Line 568 — func TestConformanceWideReordering

```
// seq+1 never arrives, so every ack here stays on the SYN.
```

## Line 575 — func TestConformanceDataAfterReachedFin

```
// Data arriving after the peer's FIN has already been reached in order.
//
// libutp keeps the socket in CS_GOT_FIN and answers nothing: the peer has
// closed its sending side, but the connection is alive until the local
// application closes it too, and data arriving past the FIN it has already
// reached is dropped in silence.
//
// So does this library. Data past the FIN is data past the end of the stream:
// there is no sequence space left for it and nothing to deliver it to.
//
// What this case pins is the wire answer, which used to be wrong in a way that
// mattered. A late packet once drew a RESET, telling a peer exercising a
// legitimate half-close that its connection had broken. The socket now holds
// the connection's last acknowledgement in a linger table for
// `lingerAckTimeout` (utp_socket.go) and, for a packet past that ack, stays
// silent exactly as libutp does.
//
// This comment used to end by calling the delivery difference a real one --
// libutp keeping the socket readable in CS_GOT_FIN where this library closed
// on reaching the remote FIN. That was true when it was written and is not
// now: the half-close is implemented, the connection stays in connConnected
// until its own application closes, and the reader goes on being served. See
// "No half-close -- implemented" in KNOWN-LIMITATIONS.md. Only data genuinely
// past the FIN is dropped, by both.
```

## Line 631 — func TestConformanceFinBeforeAnyData

```
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
```

## Line 686 — func TestConformanceDataPacketCarriesNoSelectiveAck

```
// Only an ST_STATE carries a selective ack. With a hole in what the peer has
// sent, libutp's data packet still goes out with no extension: send_ack is the
// one place it sets `ext = 1` (utp_internal.cpp:795), and a data packet is
// built with `ext = 0` (:1080). Its deferred acknowledgement, which would
// have carried the selective ack, is cancelled by the data packet (:768).
// This library used to attach the selective ack to every data packet.
```

## Line 708 — func TestConformanceZeroTimestampIsNotADelay

```
// A packet whose timestamp is zero carries no timestamp. libutp echoes no delay
// for it and takes no sample: `their_delay = (p == 0 ? 0 : time - p)`
// (utp_internal.cpp:2000). This library used to echo `now - 0`, the whole of
// its clock.
```
