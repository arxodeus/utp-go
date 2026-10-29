# mtu_duplicate_ack_test.go

Every comment in `mtu_duplicate_ack_test.go` at e56c9aa, verbatim.

## Line 8

```
// The duplicate-acknowledgement route to lowering the MTU ceiling
// (libutp utp_internal.cpp:1921-1941), rule by rule.
//
// The netem case shows what it is worth end to end. These pin the rules it
// has to follow, which that measurement cannot distinguish: a version that
// counted every acknowledgement rather than only bare ones, or that fired on
// the first duplicate rather than the third, would still bring the search
// down on a path that drops probes.
```

## Line 17 — func dupAckConn

```
// dupAckConn builds a connected connection with three packets outstanding and
// an MTU probe on the first of them.
```

## Line 24 — func dupAckConnN

```
// dupAckConnN is dupAckConn with n packets outstanding.
```

## Line 46 — func dupAckConnN

```
// The probe is the first outstanding packet, so the acknowledgement a peer
	// repeats while the hole is open is the one just before it.
```

## Line 51 — func dupAckConnN

```
// The number a peer repeats: just before the oldest outstanding packet.
```

## Line 55 — func TestDuplicateAckLowersTheMtuCeiling

```
// The third bare acknowledgement repeating the number before the probe lowers
// the ceiling below the probe's size. The first two do not.
```

## Line 85 — func TestDuplicateAckIgnoresDataPackets

```
// Only bare ST_STATE packets count.
//
// libutp is emphatic (utp_internal.cpp:1911-1920): an ST_DATA carrying an
// acknowledgement was most likely sent because the peer had data of its own.
// Counting those would give three "duplicates" immediately after every payload
// packet on a bidirectional connection.
```

## Line 110 — func TestDuplicateAckCountResets

```
// An acknowledgement that is not the repeated one resets the count, so
// duplicates have to be consecutive.
```

## Line 118 — func TestDuplicateAckCountResets

```
// Something else in between.
```

## Line 127 — func TestDuplicateAckCountResets

```
// Two more reach three consecutive.
```

## Line 136 — func TestDuplicateAckForANonProbeHoleForgetsTheProbe

```
// A hole ahead of the probe says nothing about size. libutp forgets the probe
// so another can be sent, and leaves the ceiling alone.
```

## Line 142 — func TestDuplicateAckForANonProbeHoleForgetsTheProbe

```
// A repeated acknowledgement pointing somewhere other than just before the
	// probe. It still has to be the number the window says is repeated, or it
	// would simply reset the count -- so move the probe instead.
```

## Line 164 — func TestDuplicateAckIgnoredWithNothingOutstanding

```
// Nothing is counted while nothing is outstanding, and libutp does not reset
// the count there either: the whole block sits inside
// `if (cur_window_packets > 0)`.
```

## Line 170 — func TestDuplicateAckIgnoredWithNothingOutstanding

```
// Two duplicates with packets outstanding.
```

## Line 174 — func TestDuplicateAckIgnoredWithNothingOutstanding

```
// Now nothing is outstanding.
```

## Line 187 — func TestSelectiveAckSetsTheDuplicateCount

```
// A selective ack sets the duplicate count to the number of packets it names
// inside the window (libutp: `duplicate_ack = count`, utp_internal.cpp:1612),
// and the next duplicate counts on from there. With the two packets after the
// probe named, the second duplicate is libutp's third.
```

## Line 194 — func TestSelectiveAckSetsTheDuplicateCount

```
// Bits 0 and 1 are dup+2 and dup+3: the two packets after the probe.
```

## Line 211 — func TestSelectiveAckSetsTheDuplicateCount

```
// Bits outside the window, and the oldest packet's, are not counted.
```

## Line 214 — func TestSelectiveAckSetsTheDuplicateCount

```
// dup2+7: never sent
```

## Line 221 — func TestSelectiveAckSetsTheDuplicateCountOnArrival

```
// The same, through the packet path: the count is taken from a selective ack
// on an arriving packet, after its acknowledgement is applied.
```

## Line 228 — func TestSelectiveAckSetsTheDuplicateCountOnArrival

```
// dup+2 and dup+3, the packets after the probe
```

## Line 246 — func TestSelectiveAckPastThreeJudgesTheProbe

```
// A selective ack that names more than three packets after the probe judges
// it refused there and then. libutp's test is equality with three, so a count
// that jumps from below three to above it -- an acknowledgement covering a
// whole read batch -- concludes nothing in libutp, ever, for that run. It is
// the same evidence as three duplicates: at least three packets after the
// probe arrived and the probe did not.
```

## Line 254 — func TestSelectiveAckPastThreeJudgesTheProbe

```
// dup+2 .. dup+6: five packets after the probe.
```

## Line 269 — func TestDuplicateRunJudgesOnce

```
// One run of duplicates judges once, however long it goes on and whichever
// route took the count past three; an acknowledgement that moves starts a new
// run.
```

## Line 284 — func TestDuplicateRunJudgesOnce

```
// A new probe, and an acknowledgement that is not the repeated one, ends
	// the run: the next three judge again.
```

## Line 296 — func TestAResentProbeIsNotJudged

```
// A probe sent again is no longer a probe: it goes without the
// don't-fragment bit, so its acknowledgement says nothing about whether the
// size fits, and must not raise the floor. The loss probe resends the oldest
// packet outstanding, which can be the probe.
```
