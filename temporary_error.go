package utp

// isTemporaryError reports whether a read error is one the socket should
// read past: one that says it is temporary, or, on Windows, a datagram
// larger than the buffer (see isPacketTooBig). go-ethereum's
// netutil.IsTemporaryError, which this replaced, decides it the same way.
func isTemporaryError(err error) bool {
	tempErr, ok := err.(interface{ Temporary() bool })
	return ok && tempErr.Temporary() || isPacketTooBig(err)
}
