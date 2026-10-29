# native/libutp/interop_adverse_test.go

Every comment in `native/libutp/interop_adverse_test.go` at e56c9aa, verbatim.

## Line 1 — package libutp_test

```
//go:build cgo
```

## Line 17 — func TestInteropUnderAdverseConditions

```
// TestInteropUnderAdverseConditions is the interop gate on a damaged path:
// real libutp on its own kernel socket, this library on a real socket, and a
// relay between them that loses, reorders and delays datagrams (relay_test.go).
//
// netem.TestLibutpInteropUnderAdverseConditions covers the same conditions
// with libutp driven by a Go loop on the emulated network. What this adds is
// everything that loop replaces: libutp's own thread, its own clock and
// timeout pass, and our socket's real read and write paths, under loss.
//
// A correctness gate: every byte must arrive, in both directions, on every
// profile, and the profile must have actually damaged the transfer.
```

## Line 52 — func TestInteropUnderAdverseConditionsWithRACK

```
// TestInteropUnderAdverseConditionsWithRACK is the same gate for this
// library sending with ConnectionConfig.RACK to real libutp: it resends
// different packets at different times, and libutp's receiver has to take
// them. Several seeds a profile, since which packets it resends depends on
// which are lost.
```

## Line 87 — func adverseProfiles

```
// Everything at once, on a queue too shallow to absorb it.
```

## Line 96 — func logAdverse

```
// logAdverse logs a transfer and fails a profile that did not damage it.
```

## Line 102 — func logAdverse

```
// A profile that stopped damaging packets would pass while testing a
	// clean path.
```

## Line 112 — func adverseLibutpToGo

```
// adverseLibutpToGo sends payload from libutp to our socket through a relay
// impairing both directions with cfg, and returns the time to the last byte
// and what the data direction did.
```

## Line 184 — func adverseGoToLibutp

```
// adverseGoToLibutp sends payload from our socket to libutp through the relay.
```
