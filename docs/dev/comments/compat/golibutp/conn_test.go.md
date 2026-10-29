# compat/golibutp/conn_test.go

Every comment in `compat/golibutp/conn_test.go` at e56c9aa, verbatim.

## Line 16 — func TestConnMethodsAfterClose

```
// We need to trigger libutp to destroy the Conns, the fastest way to do
	// this is destroy the parent Socket.
```

## Line 20 — func TestConnMethodsAfterClose

```
// We're trying to test what happens when the Conn isn't known to
		// libutp anymore.
		// Under mu: the goroutine that waits for the stream to end clears
		// it too.
```

## Line 28 — func TestConnMethodsAfterClose

```
// These functions must not panic. I'm not sure we care what they
		// return.
```
