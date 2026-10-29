# fin_cycle_test.go

Every comment in `fin_cycle_test.go` at e56c9aa, verbatim.

## Line 7 — func TestFinOvertakingDataAfterAFullSequenceCycle

```
// A FIN that overtakes the last data must not strand it, however long the
// connection has run. Whether a packet comes before the FIN was decided from
// the connection's first sequence number, which after 65,536 packets bounds
// nothing: with the first number 1000 and the acknowledgement at 998, DATA 999
// fell outside [1000, 1001] and was dropped each time it was resent, and DATA
// 1000 waited behind it for ever.
//
// From the release review of 9c44c4f (R4). The fixture sets consumed directly
// rather than sending 65,534 packets.
```

## Line 40 — func TestDataPastTheFinIsStillDropped

```
// And what is past the FIN is still refused, at any point in the cycle.
```
