# close_fin_ack_test.go

Every comment in `close_fin_ack_test.go` at e56c9aa, verbatim.

## Line 11 — func TestTheSecondFinIsAcknowledgedPromptly

```
// TestTheSecondFinIsAcknowledgedPromptly closes connections the way a
// transfer does -- the sender writes and closes, the receiver reads to the end
// and closes after -- and requires the receiver's connection, which closed
// second, to end within a fraction of a retransmission timeout. Its FIN
// reaches a sender that has closed, or is tearing down, or is gone; libutp
// acknowledges a FIN whatever the state of its socket (utp_internal.cpp:
// 2369-2370), and each of those left it unanswered until it was retransmitted.
// Review of 294cc2e.
```
