//go:build !windows

package utp

// isPacketTooBig is Windows' alone: elsewhere a datagram larger than the
// buffer is truncated, not an error.
func isPacketTooBig(error) bool { return false }
