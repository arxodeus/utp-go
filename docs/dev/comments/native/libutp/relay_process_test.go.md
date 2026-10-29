# native/libutp/relay_process_test.go

Every comment in `native/libutp/relay_process_test.go` at e56c9aa, verbatim.

## Line 1 — package libutp_test

```
//go:build cgo
```

## Line 19

```
// A relay in its own process.
//
// The relay spins for the last stretch before each datagram is due
// (relaySpin), and in this test binary it spins in the same Go runtime as
// this library's receiver, while libutp's receiver is a thread of its own.
// That was expected to slow our receiver. It did the opposite: a runtime
// with a goroutine running wakes the socket's reader sooner than an idle
// one, so the harness flattered our receiver, and only ours. At 10 Mb/s its
// median turnaround was 80 us with the relay in the process and is 90 us
// without it, against libutp's 50 either way; its 90th percentile was 240 us
// and is 1.1 ms, libutp's. Running the relay as a separate process takes it
// out of the runtime altogether, and leaves both receivers sharing only the
// machine.
//
// The child is this test binary run again, with relayChildEnv set to its
// configuration. It reports its ports, answers requests for counts, and on
// close hands back what it recorded -- every datagram it delivered and every
// one offered to it, both directions, timed on its own clock as before.
```

## Line 38 — var relayInOwnProcess

```
// relayInOwnProcess makes newRelay start its relay in a child process. Set
// UTP_RELAY_PROCESS=1 to run every relay that way.
```

## Line 42 — const relayChildEnv

```
// relayChildEnv carries a child relay's configuration.
```

## Line 51 — type relayPathDump

```
// relayPathDump is what the child recorded for one direction.
```

## Line 73 — func runRelayChild

```
// runRelayChild is the child's side: start the relay, say where it listens,
// then serve "stats" and "close" from in until close or end of input.
```

## Line 105 — func runRelayChild

```
// The parent went away without closing.
```

## Line 110 — type relayChild

```
// relayChild is the parent's handle on a relay in its own process.
```

## Line 123 — func startRelayProcess

```
// startRelayProcess is newRelay in a child process.
//
// onNewRelay sees the relay here, before the child starts, and the child is
// given the paths' configuration as the hook leaves it: hooks set a path's
// impairment that way (libutpSendsTo's return path does), and a child started
// first ran with the configuration the hook was meant to replace.
```

## Line 168 — func (*relayChild).stats

```
// stats asks the child for one direction's counts, or reports the last ones
// once it has closed.
```

## Line 192 — func (*relayChild).close

```
// close closes the child's relay and takes what it recorded into whatever
// the paths here were given to record into.
```

## Line 218 — func TestRelayChildTakesHookedConfig

```
// A path impairment set by onNewRelay reaches a relay in its own process.
// The child once started before the hook ran and kept the configuration the
// hook replaced: TestAsymmetricAckPath's 64 kb/s return path then carried 20
// Mb/s, and libutp's receiver, which takes 7.3 s over the narrow path, took
// 2.0.
```

## Line 251 — func TestRelayChildTakesHookedConfig

```
// 8 KB of 20-byte datagrams is about 400; everything past it is dropped.
```
