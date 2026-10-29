# recv_sack_window_test.go

Every comment in `recv_sack_window_test.go` at e56c9aa, verbatim.

## Line 9 — func TestSelectiveAckMatchesAFullScan

```
// The selective ack visits only its window of the reorder buffer, and must
// say exactly what a scan of everything held says -- including where the
// window wraps past 65535.
```

## Line 17 — func TestSelectiveAckMatchesAFullScan

```
// a window that wraps
```

## Line 52 — func TestSelectiveAckCostDoesNotGrowWithTheReorderBuffer

```
// Building one costs the same however much is held: it used to copy every
// held packet's number into a map first, on every acknowledgement.
```
