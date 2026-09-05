//go:build cgo

// Package libutp wraps the vendored C libutp so Go tests can talk to the
// reference implementation over a real UDP socket.
//
// This exists for one reason: everything else in this repository is
// conformance by citation -- code read against utp_internal.cpp and matched by
// hand. That is worth a lot, but it is not evidence that a real libutp peer
// will complete a transfer with us. This package is what turns the claim into
// a test.
//
// See VENDOR.md for the pinned upstream commit and how to refresh it.
package libutp

/*
#cgo CXXFLAGS: -DPOSIX -fno-exceptions -fno-rtti -Wno-sign-compare -fpermissive -O2
#cgo LDFLAGS: -lstdc++

#include <stdlib.h>
#include "bridge.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"
)

// State mirrors the peer's connection state.
type State int

const (
	StateIdle       State = C.LIBUTP_STATE_IDLE
	StateConnecting State = C.LIBUTP_STATE_CONNECTING
	StateConnected  State = C.LIBUTP_STATE_CONNECTED
	StateEOF        State = C.LIBUTP_STATE_EOF
	StateDestroyed  State = C.LIBUTP_STATE_DESTROYED
	StateError      State = C.LIBUTP_STATE_ERROR
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateEOF:
		return "eof"
	case StateDestroyed:
		return "destroyed"
	case StateError:
		return "error"
	default:
		return fmt.Sprintf("state(%d)", int(s))
	}
}

// libutp's error codes, from utp.h.
var errorNames = map[int]string{0: "ECONNREFUSED", 1: "ECONNRESET", 2: "ETIMEDOUT"}

// Peer is one libutp endpoint carrying at most one connection.
type Peer struct {
	c    *C.libutp_peer
	done chan struct{}

	closeOnce sync.Once
}

// NewPeer binds a libutp endpoint to 127.0.0.1 on the given port, or an
// arbitrary free port if it is 0, and starts its event loop.
func NewPeer(port uint16) (*Peer, error) {
	c := C.libutp_peer_create(C.uint16_t(port))
	if c == nil {
		return nil, fmt.Errorf("libutp: could not create peer on port %d", port)
	}
	p := &Peer{c: c, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		// Blocks until Shutdown. libutp is single-threaded, so this goroutine
		// owns every call into it; the bridge serialises the rest.
		C.libutp_peer_run(p.c)
	}()
	return p, nil
}

// Port reports the bound UDP port.
func (p *Peer) Port() uint16 { return uint16(C.libutp_peer_port(p.c)) }

// Listen makes the peer accept the next incoming connection. Until it is
// called, incoming SYNs are refused.
func (p *Peer) Listen() { C.libutp_peer_listen(p.c) }

// Connect starts an outgoing connection to 127.0.0.1 on the given port.
func (p *Peer) Connect(remotePort uint16) error {
	if C.libutp_peer_connect(p.c, C.uint16_t(remotePort)) != 0 {
		return errors.New("libutp: connect failed (socket already exists?)")
	}
	return nil
}

// State reports the current connection state.
func (p *Peer) State() State { return State(C.libutp_peer_state(p.c)) }

// Err reports the last libutp error, if the peer is in StateError.
func (p *Peer) Err() error {
	if p.State() != StateError {
		return nil
	}
	code := int(C.libutp_peer_error(p.c))
	name, ok := errorNames[code]
	if !ok {
		name = fmt.Sprintf("code %d", code)
	}
	return fmt.Errorf("libutp: %s", name)
}

// Write queues data for sending. It never blocks: the queue grows as needed
// and libutp drains it as its window allows. Use PendingWrite to see how much
// has not yet been handed over.
func (p *Peer) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	n := C.libutp_peer_queue_write(p.c, unsafe.Pointer(&b[0]), C.size_t(len(b)))
	if n < 0 {
		return 0, errors.New("libutp: out of memory queueing write")
	}
	return int(n), nil
}

// PendingWrite reports bytes queued but not yet accepted by libutp.
func (p *Peer) PendingWrite() int { return int(C.libutp_peer_pending_write(p.c)) }

// Read copies out received data. It returns 0 with no error when nothing has
// arrived yet -- it does not block.
func (p *Peer) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	n := C.libutp_peer_read(p.c, unsafe.Pointer(&b[0]), C.size_t(len(b)))
	return int(n), nil
}

// BytesReceived reports the total delivered to this peer, read out or not.
func (p *Peer) BytesReceived() uint64 { return uint64(C.libutp_peer_bytes_received(p.c)) }

// CloseStream closes the uTP connection once everything queued has been handed
// to libutp. The peer itself stays alive until Shutdown.
func (p *Peer) CloseStream() { C.libutp_peer_close(p.c) }

// Shutdown stops the event loop and releases the peer.
func (p *Peer) Shutdown() {
	p.closeOnce.Do(func() {
		C.libutp_peer_stop(p.c)
		<-p.done
		C.libutp_peer_destroy(p.c)
	})
}

// --- helpers for tests ------------------------------------------------------

// WaitState blocks until the peer reaches one of the given states, or the
// timeout expires. It reports the state it saw.
func (p *Peer) WaitState(timeout time.Duration, want ...State) (State, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s := p.State()
		for _, w := range want {
			if s == w {
				return s, nil
			}
		}
		if s == StateError {
			return s, p.Err()
		}
		time.Sleep(2 * time.Millisecond)
	}
	return p.State(), fmt.Errorf("libutp: timed out after %v waiting for %v, still %v", timeout, want, p.State())
}

// ReadFull collects exactly n bytes, or fails on timeout.
func (p *Peer) ReadFull(n int, timeout time.Duration) ([]byte, error) {
	out := make([]byte, 0, n)
	buf := make([]byte, 64*1024)
	deadline := time.Now().Add(timeout)
	for len(out) < n {
		if time.Now().After(deadline) {
			return out, fmt.Errorf("libutp: timed out after %v with %d of %d bytes", timeout, len(out), n)
		}
		got, err := p.Read(buf)
		if err != nil {
			return out, err
		}
		if got == 0 {
			if s := p.State(); s == StateError {
				return out, p.Err()
			}
			time.Sleep(time.Millisecond)
			continue
		}
		out = append(out, buf[:got]...)
	}
	return out, nil
}

// WaitDrained blocks until everything queued has been handed to libutp.
func (p *Peer) WaitDrained(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p.PendingWrite() == 0 {
			return nil
		}
		if s := p.State(); s == StateError {
			return p.Err()
		}
		time.Sleep(time.Millisecond)
	}
	return fmt.Errorf("libutp: %d bytes still queued after %v", p.PendingWrite(), timeout)
}
