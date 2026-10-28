//go:build !windows

package utp

// isPacketTooBig reports false: outside Windows an oversized datagram is
// truncated rather than reported as an error.
func isPacketTooBig(error) bool { return false }
