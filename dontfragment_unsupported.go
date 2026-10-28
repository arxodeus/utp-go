//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !windows

package utp

import "net"

// setDontFragment reports ErrDontFragmentUnsupported: this platform has no
// per-socket don't-fragment option the package knows. MTU probes are sent
// fragmentable.
func setDontFragment(_ *net.UDPConn, _ bool) error {
	return ErrDontFragmentUnsupported
}
