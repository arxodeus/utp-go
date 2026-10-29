# conformance_initiator_corpus_test.go

Every comment in `conformance_initiator_corpus_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build cgo
```

## Line 14

```
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
```

## Line 33 — type initiatorRun

```
// initiatorRun holds both implementations mid-conversation.
```

## Line 38 — type initiatorRun

```
// streamCh carries the connected stream once the handshake completes.
	// A channel rather than a field: it is written by the goroutine running
	// ConnectWithCID and read by the test, and a plain field would be a data
	// race the race detector finds on the first run.
```

## Line 43 — type initiatorRun

```
// dialErr carries ConnectWithCID's error, if it failed.
```

## Line 54 — func (*initiatorRun).close

```
// The stream is deliberately not closed. Closing waits out the flush
	// stall timeout -- two real seconds -- because the scripted peer never
	// acknowledges the FIN, and every case here would pay it. Cancelling the
	// context tears the connection down without that wait, and what these
	// cases assert is what was emitted, which has already been taken.
```

## Line 65 — func newInitiatorRun

```
// newInitiatorRun opens a connection from both implementations and returns
// the SYN each of them sent.
//
// The SYNs are returned rather than discarded because comparing them is a
// case in itself, and one nothing else makes: the differential fuzzer clears
// the handshake before it begins, and the responder corpus never sends one.
```

## Line 76 — func newInitiatorRunMTU

```
// newInitiatorRunMTU is newInitiatorRun with the MTU libutp's driver reports
// set to udpMTU; zero leaves the driver's default, 1472.
```

## Line 119 — func newInitiatorRunMTU

```
// Three participants register when the socket is built -- the
	// retransmission wheel and the read and event loops -- and the
	// connection's event loop is the fourth as soon as ConnectWithCID runs.
```

## Line 137 — func (*initiatorRun).step

```
// step applies one action to both implementations and compares what each
// emitted.
```

## Line 142 — func (*initiatorRun).step

```
// --- libutp ---
```

## Line 162 — func (*initiatorRun).step

```
// --- ours ---
```

## Line 181 — func (*initiatorRun).step

```
// The same amount of time, on a clock that moves only when told --
		// not a sleep that hopes the two sides end up in the same place.
```

## Line 191 — func (*initiatorRun).waitForStream

```
// waitForStream returns the connected stream, or nil if the handshake never
// completed.
```

## Line 204 — func runInitiatorCorpus

```
// runInitiatorCorpus opens a connection and runs the steps through both
// implementations, comparing every emission.
```

## Line 211 — func runInitiatorCorpus

```
// The SYN is step zero, and it is compared like any other emission.
```

## Line 219 — const initiatorFirstInOrder

```
// initiatorFirstInOrder is the sequence number the dialling side expects
// first from its peer.
//
// The SYN-ACK carries sequence number 900 and, being a bare ST_STATE,
// consumes none: the recipient sets ack_nr to 899 and expects 900 next. Off
// by one here and every "in order" case is quietly exercising the
// out-of-order path instead -- which is what the first version of this file
// did, passing all the while.
```

## Line 229 — func initiatorData

```
// initiatorData builds a data packet from the peer, offset packets past the
// first one expected, acking our SYN.
```

## Line 237 — func TestInitiatorHandshake

```
// The handshake, from the dialling side. The SYN we send and the
// acknowledgement we send once the peer answers.
```

## Line 246 — func TestInitiatorHandshake

```
// Neither side answers a bare acknowledgement of its SYN: there
			// is nothing to acknowledge and nothing to send.
```

## Line 253 — func TestInitiatorDataAndAck

```
// Data arriving on a connection we opened.
```

## Line 262 — func TestInitiatorReordering

```
// Out-of-order data on a connection we opened: the selective-ack path from
// the dialling side.
```

## Line 270 — func TestInitiatorReordering

```
// The gap is not filled, so the ack stays behind it: still one
			// short of the first packet we are waiting for.
```

## Line 277 — func TestInitiatorReordering

```
// Both packets are now in order, so the ack jumps over both.
```

## Line 283 — func TestInitiatorDuplicateData

```
// The same data twice, from the dialling side.
```

## Line 291 — func TestInitiatorDuplicateData

```
// A duplicate advances nothing; the ack repeats.
```

## Line 297 — func TestInitiatorResetInsteadOfSynAck

```
// A peer that answers our SYN with a reset rather than an acknowledgement --
// what a closed port or a refusing client does.
//
// The dial fails as refused. libutp intends that and cannot reach it: it sets
// CS_RESET before testing for CS_SYN_SENT (utp_internal.cpp:2865-2870), so
// every reset reads as ECONNRESET.
```

## Line 311 — func TestInitiatorResetInsteadOfSynAck

```
// A reset is never answered with a reset; both sides go quiet.
```

## Line 324 — func TestInitiatorIncomingFin

```
// The peer closes a connection we opened.
```

## Line 334 — func TestInitiatorIncomingFin

```
// On reaching a FIN libutp acknowledges twice: once immediately
			// (utp_internal.cpp:2370, "if the other end wants to close, ack")
			// and once from the deferred-ack list it also schedules at :2404.
			// The second carries nothing the first did not. Same deliberate
			// divergence as the responder corpus records.
```

## Line 342 — func TestInitiatorIncomingFin

```
// The FIN consumes a sequence number, so the ack covers it.
```

## Line 348 — func initiatorState

```
// A peer that advertises no receive window on a connection we opened.
// initiatorState builds a bare ST_STATE from the peer advertising wnd.
```

## Line 355 — func TestInitiatorZeroWindow

```
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
```

## Line 387 — func TestInitiatorWriteFlowsWithoutZeroWindow

```
// The control for TestInitiatorZeroWindow: the same script without the zero
// window, where the write leaves at once.
```

## Line 401 — func initiatorAckOf

```
// initiatorAckOf builds a bare ST_STATE from the peer acknowledging our
// packets up to seq.
```

## Line 408 — func TestInitiatorNagle

```
// Short writes behind an unacknowledged packet, against libutp: the second
// waits, the third joins it, and the acknowledgement of the first sends both
// as one packet. Every step is compared packet for packet, bodies included.
//
// libutp holds the second because its window starts at one packet
// (utp_internal.cpp:2567) and is_full charges a whole packet (:933-936); it
// would hold it with an open window too, by its Nagle rule (flush_packets,
// :974-982). Either way the third write joins the unsent packet
// (write_outgoing_packet, :1013-1023). This library's window starts at two
// packets, so here it is the Nagle rule that holds the second
// (TestNagleHoldsAShortPacketBehindAnother isolates it).
//
// Before this library had the rule, the second and third writes went out at
// once; without the send buffer reading across writes, the packet released
// at the end carried "second" alone.
```

## Line 436 — func TestInitiatorNagleNothingInFlight

```
// The control for TestInitiatorNagle: with the first packet acknowledged
// before the next write, nothing is in flight and the write leaves at once.
```
