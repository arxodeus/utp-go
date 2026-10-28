# native/libutp/driver.h

Every comment in `native/libutp/driver.h` at e56c9aa, verbatim.

## Line 1

```
// driver.h -- libutp with no socket and no clock of its own.
//
// The socket-backed peer in bridge.h is what the interoperability tests use:
// real UDP, real time, real concurrency. That is the right shape for proving
// a transfer completes, and the wrong shape for comparing two implementations
// packet by packet, because nothing about it is reproducible.
//
// This driver is the other shape. libutp reads the clock and the random
// number source only through callbacks, so both can be supplied: the driver
// has a virtual clock that only moves when told, and a scripted random
// source. Packets go in through inject and come out through a capture list.
// Nothing runs on its own, so the same script produces the same bytes every
// time.
```

## Line 27

```
// Create a driver. now_micros seeds the virtual clock.
```

## Line 31

```
// Queue the values utp_call_get_random will return, in order. Once the queue
// is exhausted the last value repeats. libutp draws its connection id and
// initial sequence number from here, so this is what pins them.
```

## Line 37

```
// libutp's congestion-control log: one line per apply_ccontrol, captured once
// enabled. Enabling it turns on UTP_LOG_NORMAL, which libutp checks at run
// time; nothing in libutp is rebuilt or changed.
```

## Line 49

```
// The virtual clock. Nothing advances it but these.
```

## Line 54

```
// Start an outgoing connection; emits a SYN.
```

## Line 56

```
// Accept the next incoming SYN.
```

## Line 59

```
// Feed one packet in, as if it had arrived from the peer.
```

## Line 62

```
// Periodic work. Neither happens on its own.
```

## Line 66

```
// Packets libutp emitted, oldest first.
```

## Line 68

```
// Copy emitted packet i; returns its length, or -1 if i is out of range.
```

## Line 82

```
// LIBUTP_DRIVER_H
```
