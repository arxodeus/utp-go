# netem/libutp_adverse_test.go

Every comment in `netem/libutp_adverse_test.go` at e56c9aa, verbatim.

## Line 1 — package netem

```
//go:build cgo
```

## Line 12 — func TestLibutpInteropUnderAdverseConditions

```
// Transfers against real libutp on paths that damage them.
//
// The plain interop gate in native/libutp runs over loopback, which loses
// nothing, reorders nothing and delays nothing: it proves the two
// implementations can complete a transfer, not that they can recover one.
// native/libutp.TestInteropUnderAdverseConditions damages the path between
// real sockets; this one runs libutp inside the emulated network, where the
// link can also be measured. Everything that makes
// loss recovery interesting -- selective acks, fast retransmit, the
// retransmission timeout, and the window collapsing and reopening -- is
// exactly what a clean path never exercises.
//
// These are the conditions where two implementations most plausibly disagree,
// because each side is reading the other's acknowledgements to decide what to
// resend. A selective-ack bitfield read from the wrong end, an off-by-one in
// the window it covers, or a different rule for what counts as lost, all
// produce a transfer that completes on loopback and stalls or corrupts here.
//
// This is a correctness gate, not a measurement: what it asserts is that every
// byte arrives, in both directions, on every profile. Goodput on these paths is
// in TestLibutpOverEmulatedNetwork.
```

## Line 34 — func TestLibutpInteropUnderAdverseConditions

```
// Seed 83 is chosen, not arbitrary: it damages every profile below in
	// both directions. The guards at the end of each case check that it still
	// does, because a profile that quietly stopped losing or reordering
	// packets would pass while testing a clean path.
```

## Line 54 — func TestLibutpInteropUnderAdverseConditions

```
// Everything at once, on a queue too shallow to absorb it. If the two
		// implementations are going to disagree about recovery, this is where.
```

## Line 105 — func TestLibutpInteropUnderAdverseConditions

```
// A profile that stopped damaging packets would pass while
				// testing nothing.
```
