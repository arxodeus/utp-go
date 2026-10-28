# utpnet/writeto_rawlock_unix_test.go

Every comment in `utpnet/writeto_rawlock_unix_test.go` at e56c9aa, verbatim.

## Line 1 — package utpnet

```
//go:build unix
```

## Line 12 — func TestUDPWriteToDoesNotWaitForAnotherWrite

```
// The first attempt at a UDP datagram does not wait for another write on the
// port. It went through RawConn.Write, which takes the descriptor's write lock
// before anything else, and waited there for as long as any other write --
// the uTP sender's, or one blocked on a full send buffer -- held it: a wait
// the deadline could not end. Release review of 224c363, finding 1.
```

## Line 32 — func TestUDPWriteToDoesNotWaitForAnotherWrite

```
// Another write holds the descriptor's write lock until release.
```
