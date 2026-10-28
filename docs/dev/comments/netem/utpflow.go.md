# netem/utpflow.go

Every comment in `netem/utpflow.go` at e56c9aa, verbatim.

## Line 14 — type UtpPair

```
// UtpPair is two uTP sockets joined by an emulated network, ready to carry
// flows. It is the thing M5's gates are run against.
```

## Line 22 — func NewUtpPair

```
// NewUtpPair wraps two endpoints in uTP sockets. Close the Network to shut
// everything down.
```

## Line 34 — type TransferResult

```
// TransferResult is what one flow achieved.
```

## Line 36 — type TransferResult

```
// BytesTransferred is what the receiver actually read.
```

## Line 38 — type TransferResult

```
// Elapsed is wall-clock time from connect to end of stream.
```

## Line 40 — type TransferResult

```
// Goodput is application-level throughput: payload delivered per second,
	// excluding headers and retransmissions. This is the number that matters
	// -- a flow can be busy and still deliver nothing.
```

## Line 44 — type TransferResult

```
// Sender and Receiver hold the recorded metric series for each side.
```

## Line 47 — type TransferResult

```
// Verified reports whether the received bytes matched what was sent.
```

## Line 51 — func (*TransferResult).String

```
// String renders the result for a test log.
```

## Line 57 — type FlowOptions

```
// FlowOptions configures one transfer.
```

## Line 59 — type FlowOptions

```
// InitiatorCid is the lower of the two connection ids. Give each
	// concurrent flow a distinct value at least 2 apart.
```

## Line 62 — type FlowOptions

```
// Config, if non-nil, is used as the base connection configuration.
	// Its Metrics observer is replaced.
```

## Line 65 — type FlowOptions

```
// MetricsInterval controls sampling density.
```

## Line 67 — type FlowOptions

```
// Verify compares received bytes against what was sent. Costs a full
	// comparison; leave off for long soaks.
```

## Line 72 — func (*UtpPair).RunTransfer

```
// RunTransfer sends data from the pair's A side to its B side and waits for
// the receiver to read it to end of stream.
//
// A and B here are the *directions of data flow*, not the endpoints' names:
// the sender initiates, the receiver accepts.
```

## Line 110 — func runFlow

```
// Follow the same cid convention as the rest of the package: the SYN
	// carries the initiator id, and the acceptor derives the responder id
	// from it.
```
