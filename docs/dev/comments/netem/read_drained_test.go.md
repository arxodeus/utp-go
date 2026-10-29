# netem/read_drained_test.go

Every comment in `netem/read_drained_test.go` at e56c9aa, verbatim.

## Line 12 — func TestReadDrainedRestartsASenderBlockedBySubPacketWindow

```
// utp_read_drained, end to end (utp_internal.cpp:3242-3261).
//
// A reader pauses long enough for the sender to fill its 32KB receive buffer.
// The window falls below a packet but not to zero, so the zero-window probe
// never arms, and nothing is in flight, so no retransmission timer runs. When
// the reader drains, only this end speaking can restart the sender.
//
// Measured, 10 runs an arm: 2.158-2.163s with the mechanism; 29.698-29.702s
// without it, with the keep-alive checked every 500ms as libutp checks it,
// the keep-alive being the only thing that recovered the control. See KNOWN-LIMITATIONS.md, M4b sweep,
// section 2. The budget below sits between them with room on both sides.
```

## Line 32 — func TestReadDrainedRestartsASenderBlockedBySubPacketWindow

```
// A queue far larger than anything in flight: the link drops nothing, so
	// the only way to close the window is the paused reader.
```

## Line 112 — func TestReadDrainedRestartsASenderBlockedBySubPacketWindow

```
// The scenario, before the outcome: otherwise a pass could mean the
	// window never closed, or closed to zero and the probe did the work.
```
