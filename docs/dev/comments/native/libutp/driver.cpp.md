# native/libutp/driver.cpp

Every comment in `native/libutp/driver.cpp` at e56c9aa, verbatim.

## Line 1

```
// driver.cpp -- see driver.h.
//
// Single-threaded by construction: every entry point is called from Go and
// runs to completion before returning. That is the point -- the socket-backed
// peer needs a thread and a mutex, and neither is reproducible.
//
// One driver still takes the process-wide libutp lock on the way in, because
// "single-threaded" is a property of one driver and not of the process: two
// tests each driving their own driver from their own goroutine are two threads
// inside libutp, which shares mutable statics across every context. See
// global_lock.h. The lock is uncontended in the single-driver case and changes
// nothing about what the driver emits.
```

## Line 48

```
	// udp_mtu is what UTP_GET_UDP_MTU reports; zero means 1472. See
	// drv_get_udp_mtu.
```

## Line 52

```
	// cc_log holds libutp's congestion-control log lines -- the one
	// apply_ccontrol writes after every acknowledgement it acts on
	// (utp_internal.cpp:1713) -- when enabled. See
	// libutp_driver_enable_cc_log.
```

## Line 60

```
	// win_log holds max_window as is_full logs it each time libutp asks
	// whether a packet fits (utp_internal.cpp:950-953): the window itself,
	// which libutp keeps private and utp_socket_stats does not report. The
	// line is compiled only with UTP_DEBUG_LOGGING, so this works only in a
	// libutp_debuglog build. See libutp_driver_enable_window_log.
```

## Line 72

```
	// Each buffer holds len bytes at buf + head; see drv_consume.
```

## Line 84

```
// A fixed peer address. The driver never touches a socket, so the only thing
// that matters is that it is stable and consistent between calls.
```

## Line 89

```
// 127.0.0.1
```

## Line 113

```
// drv_consume moves head on; the bytes are moved to the front only when an
// append needs the room, so taking a packet's worth off a large send queue
// does not move the rest of it, as in bridge.cpp's byte_buf.
```

## Line 138

```
// --- callbacks --------------------------------------------------------------
```

## Line 211

```
// libutp asks its embedder for the path MTU and uses the answer as the ceiling
// of its own MTU search (mtu_reset, utp_internal.cpp:1314-1322).
//
// This comment used to say that with no callback registered the answer is 0
// and libutp settles on 288-byte packets. It is not so: every context is
// created with libutp's own defaults installed (utp_api.cpp:77-78), and with
// this driver's callback left out libutp sends 1402-byte datagrams --
// measured. libutp's default reports 1402 for an IPv4 peer and 1232 for an
// IPv6 one, which it assumes is a Teredo tunnel (utp_utils.cpp:228-236).
//
// The driver reports 1472 instead: a standard 1500-byte Ethernet MTU less 20
// bytes of IPv4 header and 8 of UDP, which is what an embedder that knows its
// path is ordinary Ethernet reports. Measurements made against it are of
// libutp so configured, not of libutp's defaults -- and for an IPv6 peer
// libutp's default would never send more than 1232 (KNOWN-LIMITATIONS.md).
// SetUDPMTU sets any other figure.
```

## Line 244

```
	// Hold at the last scripted value once the queue runs out, so a test that
	// scripts fewer draws than libutp makes still behaves predictably.
```

## Line 251

```
	// UTPSocket::log prefixes the socket, its address and its id
	// (utp_internal.cpp:577), so the line's own text is found, not assumed.
```

## Line 273

```
	// Only apply_ccontrol's line: it is the one that starts with the delay
	// it acted on.
```

## Line 287

```
// --- lifecycle --------------------------------------------------------------
```

## Line 290

```
	// See global_lock.h: libutp's statics are process-wide, so two
	// drivers on two goroutines are two threads inside one libutp.
```

## Line 321

```
	// See global_lock.h: libutp's statics are process-wide, so two
	// drivers on two goroutines are two threads inside one libutp.
```

## Line 402

```
	// See global_lock.h: libutp's statics are process-wide, so two
	// drivers on two goroutines are two threads inside one libutp.
```

## Line 419

```
	// See global_lock.h: libutp's statics are process-wide, so two
	// drivers on two goroutines are two threads inside one libutp.
```

## Line 432

```
	// See global_lock.h: libutp's statics are process-wide, so two
	// drivers on two goroutines are two threads inside one libutp.
```

## Line 441

```
	// See global_lock.h: libutp's statics are process-wide, so two
	// drivers on two goroutines are two threads inside one libutp.
```

## Line 466

```
	// See global_lock.h: libutp's statics are process-wide, so two
	// drivers on two goroutines are two threads inside one libutp.
```

## Line 475

```
	// See global_lock.h: libutp's statics are process-wide, so two
	// drivers on two goroutines are two threads inside one libutp.
```

## Line 490

```
	// See global_lock.h: libutp's statics are process-wide, so two
	// drivers on two goroutines are two threads inside one libutp.
```
