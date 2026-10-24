//go:build cgo

package libutp

/*
#include <stdlib.h>
#include "driver.h"
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"
)

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
type Driver struct {
	mu       sync.Mutex
	c        *C.libutp_driver
	closedAt uint64
}

// NewDriver creates a driver whose virtual clock starts at nowMicros.
func NewDriver(nowMicros uint64) (*Driver, error) {
	c := C.libutp_driver_create(C.uint64_t(nowMicros))
	if c == nil {
		return nil, errors.New("libutp: could not create driver")
	}
	return &Driver{c: c}, nil
}

// Close releases the driver.
func (d *Driver) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.c != nil {
		d.closedAt = uint64(C.libutp_driver_now(d.c))
		C.libutp_driver_destroy(d.c)
		d.c = nil
	}
}

// PushRandom queues a value for libutp's random source, in draw order. libutp
// takes its connection id and initial sequence number from here, so this is
// what pins them. Once the queue is exhausted the last value repeats.
func (d *Driver) PushRandom(v uint32) { C.libutp_driver_push_random(d.c, C.uint32_t(v)) }

// SetUDPMTU sets what the driver reports to libutp as the path MTU
// (UTP_GET_UDP_MTU), which libutp takes as the ceiling of its MTU search and
// as its first packet size. The default is 1472, a 1500-byte Ethernet MTU
// less the IPv4 and UDP headers. libutp's own default callback reports 1402
// on IPv4 (utp_utils.cpp:228), which is what an embedder that registers no
// callback gets. It must be set before Connect or the first SYN arrives.
func (d *Driver) SetUDPMTU(mtu uint16) { C.libutp_driver_set_udp_mtu(d.c, C.uint16_t(mtu)) }

// EnableCCLog starts capturing libutp's congestion-control log: the line
// apply_ccontrol writes after every acknowledgement it acts on
// (utp_internal.cpp:1713), with the delay it used, the bytes acknowledged,
// when the window was last full and the window that resulted. It turns on
// UTP_LOG_NORMAL, a run-time option; libutp itself is unchanged.
func (d *Driver) EnableCCLog() { C.libutp_driver_enable_cc_log(d.c) }

// CCLog returns the captured lines, oldest first.
func (d *Driver) CCLog() []string {
	n := int(C.libutp_driver_cc_log_count(d.c))
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, C.GoString(C.libutp_driver_cc_log_get(d.c, C.int(i))))
	}
	return out
}

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
func (d *Driver) EnableWindowLog() bool { return C.libutp_driver_enable_window_log(d.c) != 0 }

// WindowLog returns the captured windows, oldest first.
func (d *Driver) WindowLog() []uint32 {
	n := int(C.libutp_driver_window_log_count(d.c))
	out := make([]uint32, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, uint32(C.libutp_driver_window_log_get(d.c, C.int(i))))
	}
	return out
}

// ClearWindowLog discards the captured windows.
func (d *Driver) ClearWindowLog() { C.libutp_driver_window_log_clear(d.c) }

// SetTime and Advance move the virtual clock, in microseconds.
func (d *Driver) SetTime(micros uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	C.libutp_driver_set_time(d.c, C.uint64_t(micros))
}

func (d *Driver) Advance(micros uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	C.libutp_driver_advance(d.c, C.uint64_t(micros))
}

func (d *Driver) Now() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.c == nil {
		return d.closedAt
	}
	return uint64(C.libutp_driver_now(d.c))
}

// Connect starts an outgoing connection, emitting a SYN.
func (d *Driver) Connect() error {
	if C.libutp_driver_connect(d.c) != 0 {
		return errors.New("libutp: driver connect failed")
	}
	return nil
}

// Listen makes the driver accept the next incoming SYN.
func (d *Driver) Listen() { C.libutp_driver_listen(d.c) }

// Inject feeds one packet in, as if it had arrived from the peer. It reports
// whether libutp claimed the packet: 1 means it was for a known connection or
// started one, 0 means it was not recognised.
func (d *Driver) Inject(pkt []byte) int {
	// libutp asserts the buffer is non-NULL even for a zero-length datagram,
	// and a real socket always hands it a valid pointer, so pass scratch
	// storage rather than Go's nil for an empty slice.
	if len(pkt) == 0 {
		var empty [1]byte
		return int(C.libutp_driver_inject(d.c, unsafe.Pointer(&empty[0]), 0))
	}
	return int(C.libutp_driver_inject(d.c, unsafe.Pointer(&pkt[0]), C.size_t(len(pkt))))
}

// CheckTimeouts and IssueAcks run libutp's periodic work. Neither happens on
// its own here.
func (d *Driver) CheckTimeouts() { C.libutp_driver_check_timeouts(d.c) }
func (d *Driver) IssueAcks()     { C.libutp_driver_issue_acks(d.c) }

// Emitted returns the packets libutp has sent, oldest first.
func (d *Driver) Emitted() [][]byte {
	n := int(C.libutp_driver_emitted_count(d.c))
	out := make([][]byte, 0, n)
	buf := make([]byte, 65536)
	for i := 0; i < n; i++ {
		size := C.libutp_driver_emitted_get(d.c, C.int(i), unsafe.Pointer(&buf[0]), C.size_t(len(buf)))
		if size < 0 {
			continue
		}
		pkt := make([]byte, int(size))
		copy(pkt, buf[:int(size)])
		out = append(out, pkt)
	}
	return out
}

// ClearEmitted drops the captured packets, so the next step starts clean.
func (d *Driver) ClearEmitted() { C.libutp_driver_emitted_clear(d.c) }

// Write queues application data for sending.
func (d *Driver) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	n := C.libutp_driver_write(d.c, unsafe.Pointer(&b[0]), C.size_t(len(b)))
	if n < 0 {
		return 0, errors.New("libutp: out of memory queueing write")
	}
	return int(n), nil
}

// Read copies out received application data.
func (d *Driver) Read(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	return int(C.libutp_driver_read(d.c, unsafe.Pointer(&b[0]), C.size_t(len(b))))
}

// Err reports libutp's last error code: 0 UTP_ECONNREFUSED, 1 UTP_ECONNRESET,
// 2 UTP_ETIMEDOUT. Meaningful only in StateError.
//
// Which one it is decides whose defect an interop failure is: a timeout says
// libutp gave up waiting, a reset says the peer told it to stop.
func (d *Driver) Err() int { return int(C.libutp_driver_error(d.c)) }

// State reports the connection state.
func (d *Driver) State() State { return State(C.libutp_driver_state(d.c)) }

// CloseStream closes the connection once queued data has been handed over.
func (d *Driver) CloseStream() { C.libutp_driver_close(d.c) }
