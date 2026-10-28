# write_cancel_test.go

Every comment in `write_cancel_test.go` at e56c9aa, verbatim.

## Line 11 — func TestCancelledWriteReportsWhatWillBeSent

```
// A write whose context ends while it waits for room reports what the
// connection took, and the connection sends that and no more. It reported 0
// and went on taking the rest, so the peer received bytes the writer had been
// told were not written -- and utpnet, which retries a write that reported
// nothing when its deadline moves, sent them twice. From the release review
// of 9c44c4f (R1).
```

## Line 28 — func TestCancelledWriteReportsWhatWillBeSent

```
// Nothing reads at the server, so the windows fill and the write waits.
```

## Line 38 — func TestCancelledWriteReportsWhatWillBeSent

```
// Give the connection time to take anything it was still going to.
```
