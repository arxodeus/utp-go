# window_ceiling_test.go

Every comment in `window_ceiling_test.go` at e56c9aa, verbatim.

## Line 9 — func streamWindow

```
// streamWindow reads a live stream's congestion window and bytes in flight
// as its event loop sees them.
```

## Line 18 — func TestSetWindowSizeCapsALiveConnection

```
// SetWindowSize is libutp's utp_setsockopt(UTP_SNDBUF) on a live connection:
// from the call on, the window is no larger than the ceiling and nothing is
// sent past it, while the transfer goes on. go-libutp exposes it as
// Conn.SetWriteBufferLen.
```

## Line 47 — func TestSetWindowSizeCapsALiveConnection

```
// Let the window grow well past the ceiling first, so the call has
	// something to cut.
```

## Line 70 — func TestSetWindowSizeCapsALiveConnection

```
// Bytes already in flight when the ceiling dropped drain first; after
	// that nothing goes past it. Drained is waited for: it was eight writes,
	// but a write returns once the send buffer has it, not once anything is
	// acknowledged, and a slow moment in CI left the flight sent under the
	// old window still outstanding when the measuring began. A connection
	// that went on sending past the ceiling never gets here.
```
