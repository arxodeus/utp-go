# native/libutp/driver.go

Every comment in `native/libutp/driver.go` at e56c9aa, verbatim.

## Line 1 — package libutp

```
//go:build cgo
```

## Line 5

```
/*
#include <stdlib.h>
#include "driver.h"
*/
```

## Line 17 — type Driver

```
// Driver is libutp with no socket and no clock of its own.
//
// Where Peer is for proving a transfer completes against real UDP, Driver is
// for comparing two implementations packet by packet. Nothing here runs on
// its own: the clock only moves when told, the random source is scripted, and
// packets go in and out through explicit calls. The same script produces the
// same bytes on every run, which is what makes a conformance corpus possible.
//
// Not safe for concurrent use. That is deliberate -- every call runs to
// completion before returning, so there is no scheduling to reason about --
// except for the clock: Now, SetTime, Advance and Close may be called from
// any goroutine. A connection of this library given the driver's clock as its
// own (ConnectionConfig.NowMicros) reads it from its own goroutine, and goes
// on reading it after its test has closed the driver: a resting connection
// woken by its socket's close finishes on its own time. That read went to
// freed memory and crashed the process -- FuzzDifferentialInitiator's
// workers, as "hung or terminated unexpectedly" -- and Now after Close is the
// time the driver was closed at.
```

## Line 41 — func NewDriver

```
// NewDriver creates a driver whose virtual clock starts at nowMicros.
```

## Line 50 — func (*Driver).Close

```
// Close releases the driver.
```

## Line 61 — func (*Driver).PushRandom

```
// PushRandom queues a value for libutp's random source, in draw order. libutp
// takes its connection id and initial sequence number from here, so this is
// what pins them. Once the queue is exhausted the last value repeats.
```

## Line 66 — func (*Driver).SetUDPMTU

```
// SetUDPMTU sets what the driver reports to libutp as the path MTU
// (UTP_GET_UDP_MTU), which libutp takes as the ceiling of its MTU search and
// as its first packet size. The default is 1472, a 1500-byte Ethernet MTU
// less the IPv4 and UDP headers. libutp's own default callback reports 1402
// on IPv4 (utp_utils.cpp:228), which is what an embedder that registers no
// callback gets. It must be set before Connect or the first SYN arrives.
```

## Line 74 — func (*Driver).EnableCCLog

```
// EnableCCLog starts capturing libutp's congestion-control log: the line
// apply_ccontrol writes after every acknowledgement it acts on
// (utp_internal.cpp:1713), with the delay it used, the bytes acknowledged,
// when the window was last full and the window that resulted. It turns on
// UTP_LOG_NORMAL, a run-time option; libutp itself is unchanged.
```

## Line 81 — func (*Driver).CCLog

```
// CCLog returns the captured lines, oldest first.
```

## Line 91 — func (*Driver).EnableWindowLog

```
// EnableWindowLog starts capturing libutp's congestion window, max_window,
// each time libutp asks whether a packet fits (is_full,
// utp_internal.cpp:950-953). libutp keeps the window private and
// utp_socket_stats does not report it; this reads it from a debug log line,
// which is compiled only with UTP_DEBUG_LOGGING. That define is set by the
// libutp_debuglog build tag (debuglog.go), not by default: some of the debug
// blocks it compiles do work whether or not the log is switched on, and the
// libutp the timing comparisons measure should be the plain one. It reports
// whether the build has the log; without it nothing is captured. The
// vendored sources are unchanged either way.
```

## Line 103 — func (*Driver).WindowLog

```
// WindowLog returns the captured windows, oldest first.
```

## Line 113 — func (*Driver).ClearWindowLog

```
// ClearWindowLog discards the captured windows.
```

## Line 116 — func (*Driver).SetTime

```
// SetTime and Advance move the virtual clock, in microseconds.
```

## Line 138 — func (*Driver).Connect

```
// Connect starts an outgoing connection, emitting a SYN.
```

## Line 146 — func (*Driver).Listen

```
// Listen makes the driver accept the next incoming SYN.
```

## Line 149 — func (*Driver).Inject

```
// Inject feeds one packet in, as if it had arrived from the peer. It reports
// whether libutp claimed the packet: 1 means it was for a known connection or
// started one, 0 means it was not recognised.
```

## Line 153 — func (*Driver).Inject

```
// libutp asserts the buffer is non-NULL even for a zero-length datagram,
	// and a real socket always hands it a valid pointer, so pass scratch
	// storage rather than Go's nil for an empty slice.
```

## Line 163 — func (*Driver).CheckTimeouts

```
// CheckTimeouts and IssueAcks run libutp's periodic work. Neither happens on
// its own here.
```

## Line 168 — func (*Driver).Emitted

```
// Emitted returns the packets libutp has sent, oldest first.
```

## Line 185 — func (*Driver).ClearEmitted

```
// ClearEmitted drops the captured packets, so the next step starts clean.
```

## Line 188 — func (*Driver).Write

```
// Write queues application data for sending.
```

## Line 200 — func (*Driver).Read

```
// Read copies out received application data.
```

## Line 208 — func (*Driver).Err

```
// Err reports libutp's last error code: 0 UTP_ECONNREFUSED, 1 UTP_ECONNRESET,
// 2 UTP_ETIMEDOUT. Meaningful only in StateError.
//
// Which one it is decides whose defect an interop failure is: a timeout says
// libutp gave up waiting, a reset says the peer told it to stop.
```

## Line 215 — func (*Driver).State

```
// State reports the connection state.
```

## Line 218 — func (*Driver).CloseStream

```
// CloseStream closes the connection once queued data has been handed over.
```
