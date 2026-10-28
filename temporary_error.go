package utp

// isTemporaryError reports whether the read loop should continue after err:
// the error says it is temporary, or it reports an oversized datagram.
func isTemporaryError(err error) bool {
	tempErr, ok := err.(interface{ Temporary() bool })
	return ok && tempErr.Temporary() || isPacketTooBig(err)
}
