# compat/golibutp/callbacks_test.go

Every comment in `compat/golibutp/callbacks_test.go` at e56c9aa, verbatim.

## Line 12 — func TestSendToRaceErrorAddr

```
// Test for a race that occurs if the error returned from PacketConn.WriteTo in sendtoCallback holds
// a reference to the addr passed to the call, and the addr storage is reused between calls to
// sendtoCallback in this instance.
```
