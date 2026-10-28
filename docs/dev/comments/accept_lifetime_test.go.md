# accept_lifetime_test.go

Every comment in `accept_lifetime_test.go` at e56c9aa, verbatim.

## Line 10 — func TestAcceptObservesSocketCancellation

```
// An Accept already queued ends when its socket does, whatever the caller's
// context. It watched only the caller's, so one on context.Background()
// outlived the socket: the dispatcher that would have answered it had gone.
// From the release review of 9c44c4f (R7).
```

## Line 23 — func TestAcceptObservesSocketCancellation

```
// the dispatcher takes the request, then the socket ends
```

## Line 35 — func TestAcceptReturnsWhenTheSocketCloses

```
// The same through Close, for both kinds of Accept.
```

## Line 51 — func TestAcceptReturnsWhenTheSocketCloses

```
// both queued
```

## Line 65 — func TestTimedOutAcceptsAreNotRetained

```
// Accepts that time out on a socket nobody connects to are not kept. They
// were dropped only when a SYN came to claim one, so an accept loop with a
// timeout grew the queue by one per timeout for as long as it ran.
```

## Line 90 — func TestConnectWithANilConfig

```
// A dial given a nil config gets the defaults, as an accept does. It panicked
// making the connection.
```
