# conformance_harness_test.go

Every comment in `conformance_harness_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build cgo
```

## Line 11

```
// The M2 conformance harness.
//
// Two implementations are driven with identical packet sequences and every
// emitted packet is compared field by field. libutp is driven through the
// deterministic driver in native/libutp: virtual clock, scripted random
// source, packets in and out by explicit call. This side is driven through a
// scripted transport that does the same for our socket.
//
// Both sequence numbers and connection ids are pinned, so the comparison is
// byte-for-byte on everything except the fields listed in allowedToDiffer.
```

## Line 22

```
// --- comparison -------------------------------------------------------------
```

## Line 24 — type fieldDiff

```
// fieldDiff is one field on which the two implementations disagreed.
```

## Line 35 — func pinnedClockConfig

```
// allowedToDiffer names the fields the corpus does not require to match, and
// why. Everything else must be identical.
//
// The brief's rule is that a deliberate divergence is asserted explicitly
// rather than hidden in a tolerance, so these are named here rather than
// quietly skipped, and the corpus reports when one actually differs.
// pinnedClockConfig returns a connection config whose wall clock is the
// libutp driver's virtual one.
//
// This is what removed "Timestamp" and "TimestampDiff" from allowedToDiffer.
// They were tolerated because libutp's driver reads a virtual clock where
// this library read the real one, so the two could never agree whatever the
// implementations did -- which meant the corpus compared every field of a
// packet except the two that carry its timing.
//
// ConnectionConfig.NowMicros governs only what is stamped and measured, not
// scheduling, so a connection given the driver's clock still retransmits on
// real timers. That is enough for these two fields and not enough for ack
// *latency*, which is still uncompared; see CONFORMANCE.md.
```

## Line 66 — func comparePackets

```
// comparePackets diffs two decoded packets field by field.
```

## Line 99 — func significant

```
// significant splits a diff list into the differences that matter and the ones
// allowedToDiffer explains.
```
