# loss_probe_test.go

Every comment in `loss_probe_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build cgo
```

## Line 10 — func withoutLossProbe

```
// withoutLossProbe switches the loss probe off for one test.
```

## Line 17 — func TestLossProbeSendsOnePacketBeforeTheTimeout

```
// TestLossProbeSendsOnePacketBeforeTheTimeout pins the loss probe on a path
// that has gone silent after the handshake: exactly one retransmission before
// the first retransmission timeout, a probe timeout after the data went out,
// and then libutp's timeout schedule -- the probe does not replace the
// timeout, and does not move its deadline: re-arming the packet's timer for
// the same deadline lands it on the same wheel tick (timeWheel.put).
```

## Line 35 — func TestLossProbeSendsOnePacketBeforeTheTimeout

```
// The probe: well before the first timeout, and not before the floor
	// RFC 8985 puts on its timeout.
```

## Line 41 — func TestLossProbeSendsOnePacketBeforeTheTimeout

```
// One probe only: the rest is the timeout schedule, [1 3 7 15] x base.
```

## Line 45 — func TestLossProbeIsNotUsedByLedbatPP

```
// TestLossProbeIsNotUsedByLedbatPP pins that a LEDBAT++ connection keeps
// libutp's behaviour: on a silent path its first retransmission is the
// timeout's, with no probe before it. The timeout the probe avoids is part of
// how LEDBAT++ yields to loss-based traffic (DEVIATIONS.md).
```
