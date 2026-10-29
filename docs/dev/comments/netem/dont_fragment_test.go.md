# netem/dont_fragment_test.go

Every comment in `netem/dont_fragment_test.go` at e56c9aa, verbatim.

## Line 12 — type noDontFragment

```
// noDontFragment hides an Endpoint's WriteToDontFragment.
//
// Embedding the utp.Conn *interface* rather than the concrete endpoint is what
// does it: only ReadFrom, WriteTo and Close are promoted, so a type assertion
// to utp.DontFragmentWriter fails. This is every Conn written before that
// interface existed, and it is the control these cases are measured against.
```

## Line 20 — type batchedReader

```
// batchedReader tells the socket how many datagrams are already waiting, so
// it reads them all before acknowledging, as it does from a real socket's
// batched read (utp.QueuedReader). Endpoint itself does not; see there.
```

## Line 27 — func dfTransfer

```
// dfTransfer runs one transfer and reports where the sender's MTU search
// settled, plus the link's stats. wrap is applied to the sender's Conn, and
// batched makes the receiver read as a real socket does.
```

## Line 116 — func TestDontFragmentBringsTheSearchWithinThePath

```
// The don't-fragment bit, and what it is worth.
//
// The path carries 1000-byte uTP datagrams and fragments anything larger
// rather than dropping it, which is what an IPv4 router does. The sender's
// search starts from a 1400-byte ceiling, so it probes above what the path
// carries in one piece.
//
// Without the bit an oversized probe is fragmented, arrives, and is
// acknowledged. The search reads that as "1400 bytes is fine" and settles
// there, on a path that cannot carry 1400 bytes in one piece -- so every data
// packet after that is fragmented too, which costs headers and turns any one
// lost fragment into a lost datagram.
//
// With the bit the router must drop the probe, the peer reports the hole with
// duplicate acknowledgements, and the search lowers its ceiling onto something
// the path really carries.
//
// Both halves are one case because neither proves anything alone: a search
// that settles low might have done so for any reason, and one that settles
// high might be on a path that genuinely carries it. It is the difference
// between them, on the same link with the same seed, that is the evidence.
//
// This needed two mechanisms, and having only the first is why an earlier
// version of this test could claim nothing about where the search settled.
// The bit makes the probe fail; libutp's duplicate-acknowledgement route
// (utp_internal.cpp:1927-1940) is what lets the search hear about it during a
// bulk transfer, because its other route needs the probe to be the only
// packet outstanding and a saturated window never leaves it that way.
//
// The receiver is run both ways: acknowledging each datagram, and reading in
// batches as on a real socket, where one acknowledgement can cover many
// packets. The second needs the duplicate count to be judged at three or
// more rather than at exactly three, as libutp judges it; under libutp's test
// it failed 9 runs in 10 (see connection.noteDuplicateAck).
```

## Line 174 — func dontFragmentNarrowsTheSearch

```
// --- the control has to present the trap, or there is nothing to detect
```

## Line 193 — func dontFragmentNarrowsTheSearch

```
// --- and the bit has to fix it
```

## Line 202 — func dontFragmentNarrowsTheSearch

```
// The search must come down, and by more than a rounding.
	//
	// Not "down to at or below the path MTU", though it usually gets there --
	// six consecutive runs converged on 996 with floor and ceiling equal. Each
	// halving of the search range costs one dropped probe, and each dropped
	// probe costs three duplicate acknowledgements from the peer, which
	// deferred and coalesced acks do not guarantee for every hole. Roughly one
	// run in fourteen gets three narrowings instead of four and stops around
	// 1185, still above the path.
	//
	// So convergence inside one transfer is a property of the receiver's ack
	// cadence, not of the mechanism under test, and asserting it here would
	// make this case fail for a reason it is not about. What is asserted is
	// what the mechanism is responsible for: the ceiling moves, and it moves
	// only when the bit is set. See KNOWN-LIMITATIONS.md.
```
