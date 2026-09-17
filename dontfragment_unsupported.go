//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !windows

package utp_go

import "net"

// setDontFragment reports that this platform has no per-socket don't-fragment
// option, or none this package knows the number for.
//
// This file is what makes the feature safe to add to a library whose point is
// that it builds anywhere with CGO_ENABLED=0. js/wasm, wasip1, plan9 and any
// future port compile against this and behave exactly as the library did
// before per-packet don't-fragment existed: the MTU search still runs, its
// probes are simply fragmentable.
//
// Compiling everywhere and working everywhere are different claims, and only
// the first one is being made.
func setDontFragment(_ *net.UDPConn, _ bool) error {
	return ErrDontFragmentUnsupported
}
