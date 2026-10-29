# netem/read_errors_test.go

Every comment in `netem/read_errors_test.go` at e56c9aa, verbatim.

## Line 13 — type failingConn

```
// failingConn is an Endpoint whose reads fail on demand: the next pending
// reads return errRead, and once broken is set every read does.
```

## Line 34 — func TestSocketReadsOnAfterAReadError

```
// A read error that does not say it is temporary no longer stops the
// socket from reading. go-libutp, go-utp and rust-utp each tolerate a run of
// 100. This one stopped at the first: every connection on the socket then
// retransmitted into silence until it timed out.
```

## Line 73 — func TestSocketThatCannotReadCloses

```
// A socket that cannot read at all closes, so a dial waiting on it fails at
// once rather than waiting out its context against a socket that will never
// hear an answer.
```
