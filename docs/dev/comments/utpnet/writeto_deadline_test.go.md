# utpnet/writeto_deadline_test.go

Every comment in `utpnet/writeto_deadline_test.go` at e56c9aa, verbatim.

## Line 11 — type blockingPacketConn

```
// blockingPacketConn is a real UDP socket whose WriteTo waits until
// released.
```

## Line 23 — func TestWriteToObservesItsDeadlineWhileBlocked

```
// A WriteTo that blocks ends at its deadline, and at a deadline moved while
// it waits. It checked the deadline once and then wrote directly, so a write
// that blocked waited however long the write took. From the release review
// of 9c44c4f (R8).
```

## Line 44 — func TestWriteToObservesItsDeadlineWhileBlocked

```
// The deadline passing.
```

## Line 59 — func TestWriteToObservesItsDeadlineWhileBlocked

```
// The deadline moved, from none to soon, while the write waits.
```

## Line 77 — type blockingUDP

```
// blockingUDP is a UDP socket whose WriteToUDP waits until released.
```

## Line 88 — func TestUDPWriteToObservesADeadlineSetWhileBlocked

```
// On a UDP socket too, a WriteTo that blocks ends at a deadline set after it
// began. With no deadline set it wrote directly, so a deadline set while it
// was blocked could not end it. Review of 294cc2e, finding 2. The kernel's
// send buffer cannot be made to block on demand, so the socket's write is
// made to, and the attempt that never waits is made to find it busy.
```

## Line 102 — func TestUDPWriteToObservesADeadlineSetWhileBlocked

```
// as if the kernel could not take it at once
```
