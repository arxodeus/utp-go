//go:build cgo

package utp

// hold and release are used only by the cgo conformance tests
// (conformance_ack_test.go), so they build only with them.

// hold stops packets being delivered without stopping them being injected,
// so a test can queue a whole batch and release it at once. Otherwise a batch
// is only as batched as the injecting goroutine is fast, and under the race
// detector each packet may arrive alone.
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
