package utp

import "errors"

// DontFragmentWriter is an optional interface for a Conn that can send a
// single datagram with fragmentation disabled. The socket uses it for
// path-MTU probes: a probe too large for the path is then dropped rather
// than fragmented, so the search learns the size the path actually carries.
//
// A Conn that cannot disable fragmentation at the moment returns
// ErrDontFragmentUnsupported without sending, and the socket sends the
// datagram normally.
type DontFragmentWriter interface {
	WriteToDontFragment(b []byte, dst ConnectionPeer) (int, error)
}

// ErrDontFragmentUnsupported is returned by WriteToDontFragment when
// fragmentation cannot be disabled for a single datagram on this platform.
// Nothing has been sent.
var ErrDontFragmentUnsupported = errors.New("utp: per-packet don't-fragment is not supported on this platform")
