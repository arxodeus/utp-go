# internal_error_test.go

Every comment in `internal_error_test.go` at e56c9aa, verbatim.

## Line 10 — func TestABrokenSendBookkeepingEndsTheConnectionNotTheProcess

```
// A connection whose own send bookkeeping refuses a packet ends, with the
// reason, and the packet is not sent. It used to panic, and a panic in one
// connection's loop ends the process: every other connection and the
// application with it.
```

## Line 19 — func TestABrokenSendBookkeepingEndsTheConnectionNotTheProcess

```
// Two ahead of the next number: out of order, which the send path never
	// does.
```

## Line 34 — func TestABrokenSendBookkeepingEndsTheConnectionNotTheProcess

```
// The rest of the pass sends nothing either.
```

## Line 42 — func TestOnTransmitPastTheWindowIsRefusedAndNotRecorded

```
// A refused transmission is not recorded: OnTransmit reports it and leaves the
// window as it was.
```

## Line 57 — func TestDialWithNoFreeIdFails

```
// A dial to a peer whose every id is taken fails, rather than looking for a
// free one for ever.
```
