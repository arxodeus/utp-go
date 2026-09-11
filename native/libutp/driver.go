//go:build cgo

package libutp

/*
#include <stdlib.h>
#include "driver.h"
*/
import "C"

import (
	"errors"
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
// completion before returning, so there is no scheduling to reason about.
type Driver struct {
	c *C.libutp_driver
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
	if d.c != nil {
		C.libutp_driver_destroy(d.c)
		d.c = nil
	}
}

// PushRandom queues a value for libutp's random source, in draw order. libutp
// takes its connection id and initial sequence number from here, so this is
// what pins them. Once the queue is exhausted the last value repeats.
func (d *Driver) PushRandom(v uint32) { C.libutp_driver_push_random(d.c, C.uint32_t(v)) }

// SetTime and Advance move the virtual clock, in microseconds.
func (d *Driver) SetTime(micros uint64) { C.libutp_driver_set_time(d.c, C.uint64_t(micros)) }
func (d *Driver) Advance(micros uint64) { C.libutp_driver_advance(d.c, C.uint64_t(micros)) }
func (d *Driver) Now() uint64           { return uint64(C.libutp_driver_now(d.c)) }

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
