package utp_go

import "errors"

// DontFragmentWriter is implemented by a Conn that can send one datagram with
// fragmentation disabled, leaving every other datagram fragmentable.
//
// It is libutp's UTP_UDP_DONTFRAG (utp.h:37), which `send_data` passes to the
// embedder's sendto callback for a packet being used as an MTU probe and for
// nothing else (utp_internal.cpp:925-929):
//
//	send_data(..., use_as_mtu_probe ? UTP_UDP_DONTFRAG : 0);
//
// Note where the responsibility sits. libutp does not set a socket option; it
// tells its embedder that *this* datagram must not be fragmented and leaves
// the mechanism to it. This interface is the same division: the library says
// which datagram is a probe, a Conn that can honour it does so, and one that
// cannot is unaffected.
//
// Why it matters. The path-MTU search learns from a probe that does not come
// back: too large, lower the ceiling. On IPv4 a router may fragment an
// oversized datagram instead of dropping it, in which case the probe *is*
// acknowledged, the floor rises, and the search settles on a size that works
// only because every packet at that size is being fragmented -- which costs
// throughput and makes a single lost fragment destroy the whole datagram.
// With the don't-fragment bit the router drops it, and the search learns what
// the path actually carries. On IPv6 routers never fragment, so the search
// already learned correctly there; this is the IPv4 case, mainly.
//
// Why it is not on Conn. Conn is deliberately ReadFrom, WriteTo and Close, so
// that an emulated network and the libutp driver satisfy it as easily as a
// UDP socket. This is the same optional extension PathMTUProvider is: a Conn
// that can answer implements it, everything else keeps today's behaviour.
//
// A Conn that implements this but cannot honour it right now -- an operating
// system with no such socket option -- returns ErrDontFragmentUnsupported
// without sending anything. The caller then sends the datagram normally, so a
// platform with no don't-fragment bit probes exactly as it does today rather
// than losing the packet.
type DontFragmentWriter interface {
	WriteToDontFragment(b []byte, dst ConnectionPeer) (int, error)
}

// ErrDontFragmentUnsupported is returned by WriteToDontFragment when this
// build or this operating system cannot disable fragmentation for a single
// datagram. Nothing has been sent when it is returned.
var ErrDontFragmentUnsupported = errors.New("utp: per-packet don't-fragment is not supported on this platform")
