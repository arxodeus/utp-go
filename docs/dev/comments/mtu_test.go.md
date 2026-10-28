# mtu_test.go

Every comment in `mtu_test.go` at e56c9aa, verbatim.

## Line 9 — func TestMtuSearchConverges

```
// The search must converge on a size the path carries, never above it, and
// then stop. Simulated against a range of true path MTUs, including the
// awkward ones.
```

## Line 17 — func TestMtuSearchConverges

```
// The ceiling starts optimistic, above the real path.
```

## Line 31 — func TestMtuSearchConverges

```
// The path carries anything up to truth.
```

## Line 50 — func TestMtuSearchConverges

```
// Never above the truth: that is the whole safety property.
```

## Line 55 — func TestMtuSearchConverges

```
// And close enough below it to be worth the search.
```

## Line 65 — func TestMtuSearchBelowFloor

```
// A path that carries less than the floor still yields a usable size rather
// than a nonsensical one.
```

## Line 78 — func TestMtuPayloadLeavesRoomForHeaderAndExtension

```
// The payload never exceeds the discovered datagram size, header and a
// selective-ack extension included. A packet that overruns is exactly the one
// discovery exists to prevent.
```

## Line 91 — func TestMtuPayloadLeavesRoomForHeaderAndExtension

```
// One full packet: header, a four-byte selective ack with its two
		// framing bytes, and the payload.
```

## Line 101 — func TestMtuProbeEligibility

```
// Probe eligibility, which is what stops the search sending a probe it cannot
// learn anything from. libutp: utp_internal.cpp:906-911.
```

## Line 126 — func TestMtuProbeEligibility

```
// Once resolved, another may go out.
```

## Line 133 — func TestMtuSearchIsRedoneAfterTheInterval

```
// A converged search is redone after the interval, because paths change.
// libutp: 30 minutes (utp_internal.cpp:1310).
```

## Line 158 — func TestMtuSearchIsRedoneAfterTheInterval

```
// The size stays where it settled, as libutp's does -- mtu_reset does not
	// touch mtu_last -- and the next packet at that size is the first probe.
```

## Line 173 — func TestMtuProbeLostLowersCeiling

```
// A probe lost lowers the ceiling below it, and never below the floor.
```

## Line 190 — func TestMtuProbeLostLowersCeiling

```
// Losing a packet that was not the probe changes nothing.
```

## Line 193 — func TestMtuProbeLostLowersCeiling

```
// a different packet
```

## Line 216 — func TestConnectionStartsAtTheCeiling

```
// A fresh connection sends at the ceiling, as libutp does (`mtu_last =
// mtu_ceiling`, utp_internal.cpp:2561-2562), and a lost first probe brings it
// down to the midpoint.
//
// This test used to assert the opposite: a start at the midpoint, growing
// only on evidence. libutp's start was faster on every healthy path measured
// (DEVIATIONS.md, "The MTU search starts at the ceiling").
```

## Line 242 — func TestConnectionStartsAtTheCeiling

```
// A path that answers nothing never grows.
```
