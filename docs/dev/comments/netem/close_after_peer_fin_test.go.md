# netem/close_after_peer_fin_test.go

Every comment in `netem/close_after_peer_fin_test.go` at e56c9aa, verbatim.

## Line 13 — func TestCloseAfterThePeersFinDeliversWhatWasWritten

```
// A connection whose peer has closed only its sending side, and is still
// reading, delivers everything written to it before Close, on a link that
// loses packets. Once both ends had sent a FIN and the application had
// closed, the connection ended at once -- with whatever of its data was still
// unacknowledged, which a lost packet then never delivered. libutp ends a
// closed socket only once its FIN is acknowledged (utp_internal.cpp:2174-2181),
// a RESET arrives, or its retransmissions give up.
```

## Line 45 — func TestCloseAfterThePeersFinDeliversWhatWasWritten

```
// The peer closes its sending side and goes on reading.
```

## Line 57 — func TestCloseAfterThePeersFinDeliversWhatWasWritten

```
// Wait for the peer's FIN, then write and close.
```
