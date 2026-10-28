# read_drained_test.go

Every comment in `read_drained_test.go` at e56c9aa, verbatim.

## Line 7

```
// utp_read_drained, at the level where its rules can be stated exactly.
//
// The end-to-end benefit is measured, not asserted here: a stalled reader's
// transfer finishes in 2.16s with the mechanism and 29.7s without it, on a
// network run of about a minute an arm. See KNOWN-LIMITATIONS.md, M4b sweep,
// section 2.
// What these cases pin is the rule itself: when handing bytes up
// reopens a window narrower than what is now free, the peer is owed an
// acknowledgement -- and when it does not, no extra acknowledgement is sent,
// because that doubles the reverse traffic.
```

## Line 34 — func TestReadDrainedOwesAnAckWhenTheWindowGrew

```
// A window larger than the one last advertised is owed an acknowledgement.
```

## Line 51 — func TestReadDrainedIsSilentWhenTheWindowDidNotGrow

```
// A window no larger than the one last advertised owes nothing.
//
// This is the guard against doubling the reverse traffic. The acknowledgement
// owed for incoming data already carries a current window, because the drain
// happens before the acknowledgement in the same pass of the event loop; an
// extra one here would be a second acknowledgement for the same packet.
```

## Line 75 — func TestReadDrainedCoversASubPacketWindow

```
// The test is growth, not growth from zero, and the difference is a stall.
//
// An earlier version fired only when the window last advertised was zero, on
// the reasoning that an ordinary drain is already covered. It left a
// connection wedged: the window fell to 861 bytes, which is not zero but is
// less than a packet, so the sender could not fit one and went quiet; nothing
// arrived, so nothing was acknowledged; the application read and freed the
// buffer, and the zero-only test declined to mention it. A window too small to
// use is as blocking as a closed one.
```

## Line 85 — func TestReadDrainedCoversASubPacketWindow

```
// 861 bytes: the figure from the trace that found this.
```

## Line 99 — func TestReadDrainedIgnoresAnUnestablishedConnection

```
// Nothing is owed on a connection that is not established, where there is no
// peer to tell and no buffer to report.
```

## Line 117 — func TestLastAdvertisedWindowFollowsWhatWasSent

```
// The window this connection last put on the wire is what the comparison is
// against, and it is recorded from the packet itself rather than tracked
// alongside it.
```
