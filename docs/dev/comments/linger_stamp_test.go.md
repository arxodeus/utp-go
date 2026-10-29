# linger_stamp_test.go

Every comment in `linger_stamp_test.go` at e56c9aa, verbatim.

## Line 9 — func TestALingeringAcknowledgementIsStampedAsItIsSent

```
// An acknowledgement the socket sends again for a connection that has gone is
// stamped as it is sent, as libutp stamps every packet (send_data,
// utp_internal.cpp:733-738): the time now, and the delay of the packet it
// answers. It went out with the stamps it was built with, and a peer
// retransmitting its FIN fed its delay estimate the same old sample from
// every copy. Review of 224c363.
```

## Line 24 — func TestALingeringAcknowledgementIsStampedAsItIsSent

```
// A FIN retransmitted after ours was acknowledged, and one that
		// completes the stream.
```

## Line 31 — func TestALingeringAcknowledgementIsStampedAsItIsSent

```
// the peer's clock, 40ms behind ours
```
