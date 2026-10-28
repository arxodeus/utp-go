# peer_hold_test.go

Every comment in `peer_hold_test.go` at e56c9aa, verbatim.

## Line 10 — type holdingConn

```
// holdingConn is a scriptedConn that counts the peers it is told to hold.
```

## Line 39 — func TestADialHoldsItsPeerUntilTheSocketForgetsIt

```
// A transport that keeps something per peer is told a dial's peer is in use
// from its first SYN, and let go of it when the dial is abandoned. See Conn.
```
