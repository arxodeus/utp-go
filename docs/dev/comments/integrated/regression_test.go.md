# integrated/regression_test.go

Every comment in `integrated/regression_test.go` at e56c9aa, verbatim.

## Line 18

```
// Regression tests for the transfer-path defects fixed in this fork.
// Each test names the defect it covers; see the root-cause notes in
// KNOWN-LIMITATIONS.md and the commit history.
```

## Line 38 — func TestReaderUnblocksWhenPeerGoesAway

```
// TestReaderUnblocksWhenPeerGoesAway covers the defect where a connection that
// reached connClosed by any path other than "remote FIN fully received" never
// delivered the end-of-stream marker, leaving ReadToEOF blocked forever.
//
// The event loop guarded the final drain with `if !c.eof()`, but eof() is true
// by definition once the state is connClosed, so the drain never ran.
```

## Line 88 — func TestReaderUnblocksWhenPeerGoesAway

```
// The initiator disappears. The acceptor must not block forever.
```

## Line 90 — func TestReaderUnblocksWhenPeerGoesAway

```
// Close must also be idempotent -- this second call used to panic closing
	// an already-closed channel.
```

## Line 101 — func TestConcurrentTransfersScale

```
// TestConcurrentTransfersScale covers the handshake defect where an acceptor
// ignored a retransmitted SYN. Nothing retransmits the SYN-ACK, so a single
// lost SYN-ACK stranded the initiator until it gave up, and its peer then
// blocked forever in ReadToEOF.
//
// It needs enough concurrency to actually drop packets. Override with
// REPRO_N / REPRO_SIZE / REPRO_DEADLINE.
```

## Line 198 — func TestAbortedTransfersTearDown

```
// TestAbortedTransfersTearDown hammers the teardown paths concurrently: idle
// timeout, RESET and local close all racing. It guards the time-wheel change
// (the expiry callback used to run while holding the wheel's lock, which could
// deadlock against the connection event loop) and the socket shutdown fan-out.
```

## Line 266 — func TestAcceptTimesOutWithoutSyn

```
// TestAcceptTimesOutWithoutSyn covers the defect where an accept that expired
// from the awaiting map was dropped silently, blocking AcceptWithCID forever
// when the caller's own context had no deadline.
```

## Line 287 — func TestAcceptTimesOutWithoutSyn

```
// context.Background(): the caller supplies no deadline of its own, so
		// only the socket's own accept expiry can unblock this.
```

## Line 299 — func TestAcceptTimesOutWithoutSyn

```
// The socket gives up on an accept after 20 seconds (see
	// utp.ErrAcceptTimedOut); allow as long again for slow machines.
```

## Line 306 — func TestConnectionIdHashFromStructLiteral

```
// TestConnectionIdHashFromStructLiteral covers the defect where a
// ConnectionID built as a struct literal (which the exported type invites)
// had an empty cached hash, so every such id collided with every other.
```

## Line 325 — func TestAcceptWithoutCidWaitsForIncomingSyn

```
// TestAcceptWithoutCidWaitsForIncomingSyn covers the defect where Accept --
// the path that takes no connection id, and the only one usable against a
// peer that picks its own -- polled once for an already-arrived SYN and
// failed outright with "no incoming conn" if none had come yet.
//
// That made it unusable for the ordinary server pattern: call Accept, then
// have a client connect. The SYN arriving a moment later was parked with
// nobody left to claim it, and the peer retried until it gave up.
//
// The same path also dereferenced the accept's connection id, which is nil
// when none was given, so it panicked as soon as it got that far.
```

## Line 358 — func TestAcceptWithoutCidWaitsForIncomingSyn

```
// Accept is called first, before any SYN exists.
```

## Line 374 — func TestAcceptWithoutCidWaitsForIncomingSyn

```
// let the accept register
```

## Line 398 — func TestCloseReturnsPromptly

```
// TestCloseReturnsPromptly covers the defect where Stream.Close set its
// shutdown flag and then waited, without waking the connection's event loop.
//
// The loop was blocked in a select on cases the flag is not one of, so it only
// noticed once some unrelated packet or timer happened to arrive. Close took
// anywhere from about a second to the idle timeout depending on what was in
// flight -- measured at 29 seconds against a peer that had gone quiet.
```

## Line 447 — func TestCloseReturnsPromptly

```
// Let the transfer settle so nothing is in flight to wake the loop
	// incidentally -- that quiescent state is exactly when the defect bit.
```

## Line 462 — func TestReadAfterLocalCloseIsNotACleanEnd

```
// A read that ends without error has everything the peer sent.
//
// The consumer here closes its stream part way through a transfer and then
// reads what reached it. Teardown drops the bytes still on their way, since
// they are owed to nobody, and ReadToEOF used to report the end of that
// truncated stream as a clean one: a nil error and short data. It reports
// ErrReadClosed now.
```

## Line 505 — func TestReadAfterLocalCloseIsNotACleanEnd

```
// Long enough for the connection to tear down with the transfer unfinished.
```
