# fuzz_conn_test.go

Every comment in `fuzz_conn_test.go` at e56c9aa, verbatim.

## Line 12

```
// Fuzzing a live connection, not just the decoder.
//
// The decoder fuzzers cover one packet at a time. The defects that survive
// those are the ones that need a *sequence*: a state machine that accepts a
// packet it should not have in the state it is in, a counter that wraps, a
// buffer indexed from a sequence number a peer chose. M2's corpus probed that
// by hand and found real divergences; this probes it without having to think
// of the cases.
//
// The property is deliberately weak, because a strong one would be wrong: an
// arbitrary byte sequence is not a valid uTP conversation, so almost any
// behaviour is permitted. What is not permitted is:
//
//   - panicking, which is remotely triggerable by anyone who can send a
//     datagram to the port;
//   - hanging, which is the same thing more slowly;
//   - dying, so that a well-formed packet arriving afterwards gets no answer.
//
// The last one is the interesting one. It says a peer cannot use malformed
// traffic to wedge a socket against everyone else.
//
// Run longer with:
//
//	go test -run xxx -fuzz FuzzResponderPacketSequence -fuzztime 5m
```

## Line 37 — type fuzzConn

```
// fuzzConn is a transport that can be fed packets and read back what the
// socket emitted. It deliberately does not depend on the conformance harness,
// which is behind a cgo build tag: this has to run in a CGO_ENABLED=0 build.
```

## Line 104 — func splitPackets

```
// splitPackets carves the fuzz input into a sequence of packets. The first
// byte of each record is its length, so the fuzzer controls how the stream is
// divided as well as what is in it.
```

## Line 130 — func FuzzResponderPacketSequence

```
// Seeds: the shapes the M2 corpus found worth testing, plus a plain
	// exchange, expressed as length-prefixed records.
```

## Line 162 — func FuzzResponderPacketSequence

```
// Establish, then feed the sequence.
```

## Line 171 — func FuzzResponderPacketSequence

```
// The socket must still be alive. A peer that can wedge a socket with
		// malformed traffic can deny service to every other connection on the
		// same port, so this is the property worth asserting: after whatever
		// the fuzzer sent, a fresh connection still gets answered.
```
