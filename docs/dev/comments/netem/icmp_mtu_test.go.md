# netem/icmp_mtu_test.go

Every comment in `netem/icmp_mtu_test.go` at e56c9aa, verbatim.

## Line 14 — type icmpTransferResult

```
// icmpTransferResult is what one run of icmpTransfer observed.
```

## Line 26 — func icmpTransfer

```
// icmpTransfer runs one transfer over a size-limited path, optionally with the
// router reporting each refusal the way a real one does.
//
// quoteBytes is how much of the offending datagram the router quotes back. A
// real router is only required to quote 28 bytes past the IP header, of which
// 8 are the UDP header, so 20 bytes of uTP header is the least a receiver of
// ICMP can count on.
```

## Line 54 — func icmpTransfer

```
// Only the sender's own datagrams concern the sender's socket.
```

## Line 88 — func icmpTransfer

```
// A path this narrow stalls the transfer (see the comment on
	// TestIcmpBringsTheSearchWithinThePath), so both ends give up early
	// rather than sitting out the default minute. The search has settled
	// long before then; what is measured is where it settled.
```

## Line 160 — func TestIcmpBringsTheSearchWithinThePath

```
// What the router's report is worth on a path narrower than the search's own
// ceiling: the search ends up at a size the path carries, instead of parked
// above it sending datagrams that can never arrive.
//
// The control is the same transfer with the router silent.
//
// What that control demonstrates has narrowed, and this comment used to
// overstate it. It said the ceiling never comes down without a report,
// because it only falls when a probe times out as the *sole* outstanding
// packet (utp_internal.cpp:1152-1160), which a connection whose every packet
// is too big never reaches. libutp's other route -- three duplicate
// acknowledgements pointing at the packet before the probe (:1927-1940) -- is
// implemented now and works with the window full, so the silent search does
// come down: measured repeatedly at floor=982 current=1083 ceiling=1184
// against this 1100-byte link.
//
// It comes down to a workable *size* and not to a workable *ceiling*. 1184 is
// still above the path, so the search is free to climb back above it. The
// report is what ends that, because it names the next hop's MTU instead of
// bisecting towards it: with the router reporting, the ceiling is exactly
// 1100 every run. That is the difference this case now measures, and it is
// asserted on the ceiling rather than on the size.
//
// **This does not rescue the transfer, and the test says so rather than
// pretending otherwise.** uTP numbers packets, not bytes, so a packet that has
// already been built cannot be re-cut smaller -- doing that would renumber
// every packet behind it. libutp has the same constraint and gets away with it
// by not setting don't-fragment on ordinary data ("now we need it to fragment
// just to get it through", utp_internal.cpp:898-905), leaving the router to
// fragment what it cannot forward whole. This emulated link refuses oversized
// datagrams outright, which is what an IPv6 path does and what Linux's default
// IP_PMTUDISC_WANT makes an IPv4 path do, so the packets already in the window
// stay stuck under either implementation.
//
// So what ICMP fixes here is the search's ceiling, not the window: every
// packet built from the report onwards fits the path, and the search cannot
// wander back above it. That is what this asserts.
```

## Line 212 — func TestIcmpBringsTheSearchWithinThePath

```
// The control's ceiling has to stay above the path, or there is nothing
	// left for the report to fix and the comparison proves nothing.
```

## Line 236 — func TestIcmpBringsTheSearchWithinThePath

```
// Recorded, not asserted: the transfer still stalls, for the reason in the
	// comment above. Asserting the stall would fix it in place; leaving it
	// unmeasured would let it quietly become something else.
```

## Line 244 — func TestIcmpWorksFromAMinimalQuote

```
// A router that quotes only the 20 bytes it has to must still be understood.
// Quoting more is common and quoting less is legal, so the parse cannot depend
// on the extensions or the payload being there.
```

## Line 251 — func TestIcmpWorksFromAMinimalQuote

```
// 20 bytes: the uTP header and nothing else.
```

## Line 270 — func TestIcmpChangesNothingOnAnUnconstrainedPath

```
// On a path that carries everything the search will ever ask for, no report is
// generated and nothing changes. Stated so that "ICMP helps" cannot quietly
// become "ICMP is required".
```
