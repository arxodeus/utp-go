# tail_burst_loss_libutp_test.go

Every comment in `tail_burst_loss_libutp_test.go` at e56c9aa, verbatim.

## Line 1 — package utp

```
//go:build cgo
```

## Line 10 — func libutpTailBurstRecovery

```
// libutpTailBurstRecovery is tailBurstRecovery for libutp, through the
// conformance driver on the same virtual clock: the same round trip, the same
// burst, every packet but the burst's own acknowledged a round trip after it
// left, and the burst lost but for its last packet.
```

## Line 99 — func TestTailBurstLossAgainstLibutp

```
// The same tail burst, measured against libutp: ours recovers it no slower.
```
