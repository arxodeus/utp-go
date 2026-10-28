# netem/libutp_mtu_test.go

Every comment in `netem/libutp_mtu_test.go` at e56c9aa, verbatim.

## Line 1 — package netem

```
//go:build cgo
```

## Line 11 — func TestLibutpStallsOnAPathItCannotFit

```
// libutp on a path whose MTU is below the size it sends at.
//
// This pins the reference behaviour behind
// TestMtuSearchCannotRecoverFromAPathLimitBelowItsChoice. That test documents a
// limitation this library has; this one establishes that libutp has it too, so
// the limitation is uTP's MTU discovery as implemented rather than a defect
// this fork introduced.
//
// It is the difference between "believed, from reading utp_internal.cpp" and
// "measured" -- which matters here, because the reading alone would suggest
// libutp recovers: it lowers mtu_ceiling on a probe timeout
// (utp_internal.cpp:1152-1160) and clears the probe on every RTO so another
// can go out (:1166-1167). Both are true, and neither rescues a connection
// whose window has stalled, because the ceiling only moves when the probe is
// the *only* packet outstanding and nothing is being acknowledged.
//
// If libutp is ever changed to handle this, this test fails, which is the
// point: it should be re-derived rather than assumed.
```
