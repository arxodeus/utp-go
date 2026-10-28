# rack_test.go

Every comment in `rack_test.go` at e56c9aa, verbatim.

## Line 10 — type rackHarness

```
// rackHarness drives a send window through the real acknowledgement path on
// a clock the test sets.
```

## Line 16 — type rackHarness

```
// seq[i] is packet i's sequence number, from 1
```

## Line 28 — func (*rackHarness).send

```
// send sends the next packet at ms and returns its index.
```

## Line 36 — func (*rackHarness).resend

```
// resend sends packet i again at ms.
```

## Line 41 — func (*rackHarness).ack

```
// ack acknowledges cumulatively through packet upTo (0 for none) and
// selectively the packets in sacked, at ms.
```

## Line 58 — func (*rackHarness).lost

```
// lost is the indexes of the packets queued for resending now.
```

## Line 71 — func TestRackReorderingWithinWindowIsNotLoss

```
// Once the path has been seen to reorder, three packets acknowledged past a
// hole are not enough: a packet is lost only once a round trip and a quarter
// of the minimum round trip have passed since it was sent. libutp's count
// declares it lost at once.
```

## Line 77 — func TestRackReorderingWithinWindowIsNotLoss

```
// 1
```

## Line 78 — func TestRackReorderingWithinWindowIsNotLoss

```
// a 20 ms round trip
```

## Line 79 — func TestRackReorderingWithinWindowIsNotLoss

```
// 2
```

## Line 80 — func TestRackReorderingWithinWindowIsNotLoss

```
// 3
```

## Line 81 — func TestRackReorderingWithinWindowIsNotLoss

```
// 3 overtakes 2...
```

## Line 82 — func TestRackReorderingWithinWindowIsNotLoss

```
// ...which arrives: the path reorders
```

## Line 84 — func TestRackReorderingWithinWindowIsNotLoss

```
// 4..8
```

## Line 86 — func TestRackReorderingWithinWindowIsNotLoss

```
// 6, 7 and 8 past a hole at 4 and 5
```

## Line 97 — func TestRackReorderingWithinWindowIsNotLoss

```
// The minimum round trip is now 17 ms (packet 8's), so the window is a
	// quarter of it. Packet 4, sent at 41 ms, passes it at 41 + 17 (the
	// round trip to packet 8) + 4.25 = 62.25 ms; packet 5 at 63.25.
```

## Line 108 — func TestRackWithoutReorderingMatchesTheCount

```
// Before the path has been seen to reorder, three packets past a hole make
// it loss at once, as libutp's count does.
```

## Line 113 — func TestRackWithoutReorderingMatchesTheCount

```
// 1..5
```

## Line 115 — func TestRackWithoutReorderingMatchesTheCount

```
// three past 1
```

## Line 120 — func TestRackLostRetransmissionIsDetected

```
// A retransmission that is lost too is declared lost a round trip later,
// once something sent after it has been delivered. libutp fast-retransmits
// a packet once and leaves a second loss to its retransmission timeout.
```

## Line 126 — func TestRackLostRetransmissionIsDetected

```
// 1..5
```

## Line 130 — func TestRackLostRetransmissionIsDetected

```
// lost again
```

## Line 131 — func TestRackLostRetransmissionIsDetected

```
// 6
```

## Line 145 — func TestRackInOrderAcksAreNotReordering

```
// One acknowledgement covering several packets retires them in no
// particular order -- its ack number before the packets under it -- and that
// is not reordering.
```

## Line 161 — func TestRackSpuriousLossWidensTheWindow

```
// A loss shown to be spurious widens the reordering window, for
// rackReoWndPersist recoveries.
```

## Line 166 — func TestRackSpuriousLossWidensTheWindow

```
// minimum round trip 20 ms
```

## Line 185 — func TestRackDoesNotJudgePastWhatTheAckReports

```
// A packet past the highest one an acknowledgement reports is not judged:
// uTP's selective ack covers a fixed window past the ack number, so past it
// a packet may have arrived and not been said. Without the bound, a
// delivered resend -- sent after everything outstanding -- made all of them
// lost.
```

## Line 193 — func TestRackDoesNotJudgePastWhatTheAckReports

```
// 1..40
```

## Line 195 — func TestRackDoesNotJudgePastWhatTheAckReports

```
// 1 lost, three past it
```

## Line 198 — func TestRackDoesNotJudgePastWhatTheAckReports

```
// The resend fills the hole and the acknowledgement runs to 31; it says
	// nothing about 32..40, all sent before the resend.
```

## Line 200 — func TestRackDoesNotJudgePastWhatTheAckReports

```
// past the minimum round trip, so the resend counts
```

## Line 204 — func TestRackJudgesBySendOrderNotTimestamp

```
// What was sent before what is decided by send order, not by the times
// recorded: those come from clocks read at different points of the event
// loop, and a packet sent after a resend can carry an earlier time than it.
```

## Line 210 — func TestRackJudgesBySendOrderNotTimestamp

```
// 1..5
```

## Line 215 — func TestRackJudgesBySendOrderNotTimestamp

```
// sent after 2's resend, stamped before it
```
