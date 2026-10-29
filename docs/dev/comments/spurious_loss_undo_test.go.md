# spurious_loss_undo_test.go

Every comment in `spurious_loss_undo_test.go` at e56c9aa, verbatim.

## Line 9 — type dataSent

```
// dataSent is one of our data packets as it went out.
```

## Line 30 — func peerAck

```
// peerAck is a STATE from the scripted peer acknowledging ackNum, with the
// given selective ack, echoing echo as the timestamp of the packet that drew
// it: the receiver reports the difference between its clock and that
// timestamp, and the sender subtracts it from the STATE's own.
```

## Line 44 — func TestSpuriousLossCutIsUndone

```
// A cut for a packet taken for lost is undone when the acknowledgement shows
// the original arrived after all, and stands when it shows the resend did.
//
// On a link that reorders, a packet displaced far enough that three later
// ones are selectively acknowledged first is taken for lost, resent, and the
// window halved, as libutp does. The original then arrives. Under LEDBAT++
// on the reordering benchmark link, how often that happened decided which of
// two modes a run fell in, 2.35 or 2.62 Mbps.
```

## Line 69 — func TestSpuriousLossCutIsUndone

```
// The writer is not a participant of the clock: wait for what it
			// queues to go out.
```

## Line 78 — func TestSpuriousLossCutIsUndone

```
// Acknowledge whole flights until the window has grown to more
			// than four packets in flight.
```

## Line 99 — func TestSpuriousLossCutIsUndone

```
// The first of the flight is late: the next three are selectively
			// acknowledged, and it is taken for lost.
```

## Line 122 — func TestSpuriousLossCutIsUndone

```
// Now the hole is filled, by one copy or the other.
```
