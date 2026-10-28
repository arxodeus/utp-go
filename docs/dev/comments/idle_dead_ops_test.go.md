# idle_dead_ops_test.go

Every comment in `idle_dead_ops_test.go` at e56c9aa, verbatim.

## Line 10 — func deadByIdleTimeout

```
// deadByIdleTimeout returns an accepted stream whose connection the idle
// timeout has closed: the peer sends a SYN and one data packet, then nothing
// for longer than MaxIdleTimeout.
```

## Line 61 — func deadByIdleTimeout

```
// Silence, past the idle timeout, in steps so each timer fires when due.
```

## Line 73 — func TestWriteAfterIdleTimeoutFails

```
// A write on a connection the idle timeout has closed fails at once, with the
// reason, rather than waiting for its own deadline.
```

## Line 89 — func TestReadAfterIdleTimeoutReportsIt

```
// A read on a connection the idle timeout has closed reports the timeout, not
// a clean end of stream -- also once the socket's context has been cancelled
// as well. Read then had two ready cases, the closed read queue carrying the
// error and the cancelled stream context mapped to io.EOF, and Go picks
// between them at random, so this repeats.
```

## Line 102 — func TestReadAfterIdleTimeoutReportsIt

```
// A fresh stream's Read, and ReadToEOF, after the socket went too.
```

## Line 112 — func TestReadAfterIdleTimeoutReportsIt

```
// Read's cached error does not exercise its own select a second time,
	// so Read is checked on a stream whose socket went first.
```
