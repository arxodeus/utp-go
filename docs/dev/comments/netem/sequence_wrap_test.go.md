# netem/sequence_wrap_test.go

Every comment in `netem/sequence_wrap_test.go` at e56c9aa, verbatim.

## Line 11 — func TestTransferPastSequenceWrap

```
// A connection that sends more than 65,536 packets, so that its sequence
// numbers wrap, goes on sending at the rate of the path.
//
// It did not. The sender kept every packet it had ever sent and found each by
// its distance from the first sequence number, modulo 65536; past 65,535
// packets a new one landed on an old, acknowledged entry, was booked as a
// retransmission of it, and stopped counting as in flight. The sender then
// ran away at the rate of its event loop -- measured at 22,000 packets a
// second into a 10 Mbps link, sharing it with a Reno flow, from about 90 MB
// into the connection. No test had sent that much on one connection.
//
// 600-byte datagrams get past the wrap in about 40 MB. The gate is on what
// the sender put on the wire against what the payload needed: a sender that
// has lost count of what is in flight sends many times more.
```

## Line 30 — func TestTransferPastSequenceWrap

```
// Twice round, with selective acks and fast retransmission working
	// across the boundary.
```

## Line 35 — func transferPastWrap

```
// transferPastWrap sends packets packets' worth of payload over a 100 Mbps
// link losing lossRate of datagrams, and fails if the sender sent more than
// limitTenths/10 times that.
```
