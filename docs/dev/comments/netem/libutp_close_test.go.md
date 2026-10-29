# netem/libutp_close_test.go

Every comment in `netem/libutp_close_test.go` at e56c9aa, verbatim.

## Line 1 — package netem

```
//go:build cgo
```

## Line 12 — func TestLibutpCleanCloseSurvivesLostFinAck

```
// A transfer that completed must not end in a connection reset for the peer.
//
// The failure this pins: libutp finishes sending and closes, so it sends a
// FIN. We receive it, acknowledge it, hand the application its end of stream,
// and tear the connection down. If that acknowledgement is lost -- ordinary on
// a lossy path -- libutp retransmits the FIN, and by then the connection is
// gone from this socket's table, so the retransmission is answered with a
// RESET. libutp reports UTP_ECONNRESET on a transfer that in fact arrived
// whole.
//
// libutp cannot do this to us. It holds the socket in CS_GOT_FIN until its own
// application closes, so a retransmitted FIN is simply acknowledged again.
// This is the concrete cost of the half-close deviation in DEVIATIONS.md,
// which until now had none recorded.
//
// Measured before the fix, on a 3%-loss path with reordering and jitter: 4 of
// 20 seeds ended this way, and seed 3 ended this way 5 times out of 5. Nothing
// was lost from the transfer itself -- the receiving side read all 131072
// bytes without error every time -- so this is purely a failure of the close,
// and it is the peer that is told the connection broke.
```

## Line 38 — func TestLibutpCleanCloseSurvivesLostFinAck

```
// Seed 3 is not arbitrary: it loses the acknowledgement of libutp's FIN
	// reproducibly, which is what makes this a gate rather than a lottery.
```
