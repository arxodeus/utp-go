# accept_answer_test.go

Every comment in `accept_answer_test.go` at e56c9aa, verbatim.

## Line 33 — func TestSynIsAnsweredWithNoAcceptWaiting

```
// With a configuration of its own, a socket answers a SYN as it arrives, as
// libutp does: the dial completes with nothing waiting in Accept, and the
// next Accept returns the connection, which carries data.
```

## Line 71 — func TestSynWaitsForAcceptWithoutASocketConfig

```
// Without one, the configuration is each Accept's, and a SYN waits for the
// Accept that says how to make its connection: the dial does not complete
// until one comes, and then does.
```

## Line 102 — func TestAcceptWithCidTakesAnAnsweredConnection

```
// AcceptWithCid takes the answered connection it names, leaving others for
// Accept.
```

## Line 120 — func TestAcceptWithCidTakesAnAnsweredConnection

```
// The acceptor's id for the second connection: it sends on the
	// dialler's receive id and receives on the dialler's send id.
```
