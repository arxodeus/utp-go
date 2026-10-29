# utpnet/accept_answer_test.go

Every comment in `utpnet/accept_answer_test.go` at e56c9aa, verbatim.

## Line 9 — func TestDialCompletesWithNoAcceptWaiting

```
// A Socket answers a SYN as it arrives, as libutp does: a dial completes
// with nothing waiting in Accept, and the next Accept returns it. Before,
// the dial waited for an Accept and failed after 21 s if none came.
```
