//go:build cgo

package utp

// hold and release are used only by the cgo conformance tests
// (conformance_ack_test.go), so they build only with them.

// hold stops packets being delivered without stopping them being injected, so
// a test can queue a whole batch and then release it at once.
//
// Without it, a "batch" is only as batched as the injecting goroutine is fast:
// under -race, injecting eight packets in a loop took longer than the
// connection took to process one, so each arrived alone and was acknowledged
// alone. That measures the test, not the implementation. libutp has no such
// problem because its embedder hands it a whole batch of datagrams before
// calling utp_issue_deferred_acks(); hold/release gives our side the same
// starting position.
func (c *scriptedConn) hold() {
	c.inMu.Lock()
	c.holding = true
	c.inMu.Unlock()
}

func (c *scriptedConn) release() {
	c.inMu.Lock()
	c.holding = false
	c.inMu.Unlock()
	c.signal()
}
