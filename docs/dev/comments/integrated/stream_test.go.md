# integrated/stream_test.go

Every comment in `integrated/stream_test.go` at e56c9aa, verbatim.

## Line 16 — const expectedIdleTimeout

```
// expectedIdleTimeout is what the tests that wait out an idle timeout set:
// there is none by default, as in libutp.
```

## Line 21 — func TestCloseWhenWriteCompletes

```
// Initialize logging
	//logFile, _ := os.OpenFile("test_close_when_write_completes.log", os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
	//handler := slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: utp.LevelTrace})
```

## Line 30 — func TestCloseWhenWriteCompletes

```
// Create connected socket pair
```

## Line 35 — func TestCloseWhenWriteCompletes

```
// Create sockets
```

## Line 39 — func TestCloseWhenWriteCompletes

```
//loggerB := slog.New(log.DiscardHandler())
```

## Line 45 — func TestCloseWhenWriteCompletes

```
// Accept connection
```

## Line 55 — func TestCloseWhenWriteCompletes

```
// Connect
```

## Line 77 — func TestCloseWhenWriteCompletes

```
// Data to send
```

## Line 81 — func TestCloseWhenWriteCompletes

```
// Send data
```

## Line 97 — func TestCloseWhenWriteCompletes

```
// Receive data
```

## Line 114 — func TestCloseWhenWriteCompletes

```
// Wait for send to complete
```

## Line 117 — func TestCloseWhenWriteCompletes

```
// Close stream with timeout
```

## Line 131 — func TestCloseWhenWriteCompletes

```
// Close successful
```

## Line 134 — func TestCloseWhenWriteCompletes

```
// Wait for receive to complete
```

## Line 144 — func TestCloseErrorsIfAllPacketsDropped

```
// Create connected socket pair
```

## Line 147 — func TestCloseErrorsIfAllPacketsDropped

```
// Create sockets
```

## Line 155 — func TestCloseErrorsIfAllPacketsDropped

```
// Accept connection
```

## Line 163 — func TestCloseErrorsIfAllPacketsDropped

```
// Connect
```

## Line 180 — func TestCloseErrorsIfAllPacketsDropped

```
// Disable network link, will not receive any packets
```

## Line 183 — func TestCloseErrorsIfAllPacketsDropped

```
// Data to send
```

## Line 187 — func TestCloseErrorsIfAllPacketsDropped

```
// Send data
```

## Line 202 — func TestCloseErrorsIfAllPacketsDropped

```
// Receive data
```

## Line 217 — func TestCloseErrorsIfAllPacketsDropped

```
// Wait for send to complete
```

## Line 220 — func TestCloseErrorsIfAllPacketsDropped

```
// Try to close stream with timeout.
	// The budget must exceed the connection's own idle timeout: Close waits
	// for the event loop, which cannot exit until the idle timer fires at
	// expectedIdleTimeout. Giving Close exactly that long was a dead heat the
	// test always lost. The rest of this file already uses a 2x budget.
```

## Line 238 — func TestCloseErrorsIfAllPacketsDropped

```
// Close successful
```

## Line 241 — func TestCloseErrorsIfAllPacketsDropped

```
// Wait for receive to complete
```

## Line 251 — func TestCloseSucceedsIfOnlyFinAckDropped

```
// Build connected pair
```

## Line 254 — func TestCloseSucceedsIfOnlyFinAckDropped

```
// Create UTP sockets
```

## Line 259 — func TestCloseSucceedsIfOnlyFinAckDropped

```
// Create channels for coordination
```

## Line 263 — func TestCloseSucceedsIfOnlyFinAckDropped

```
// Accept connection in goroutine
```

## Line 271 — func TestCloseSucceedsIfOnlyFinAckDropped

```
// Connect
```

## Line 279 — func TestCloseSucceedsIfOnlyFinAckDropped

```
// Wait for connection establishment
```

## Line 289 — func TestCloseSucceedsIfOnlyFinAckDropped

```
// Data to send
```

## Line 293 — func TestCloseSucceedsIfOnlyFinAckDropped

```
// Send data
```

## Line 309 — func TestCloseSucceedsIfOnlyFinAckDropped

```
// Receive data
```

## Line 319 — func TestCloseSucceedsIfOnlyFinAckDropped

```
// Wait for send to complete
```

## Line 322 — func TestCloseSucceedsIfOnlyFinAckDropped

```
// Wait for some time to ensure data is fully sent
```

## Line 325 — func TestCloseSucceedsIfOnlyFinAckDropped

```
// Disable network link
```

## Line 328 — func TestCloseSucceedsIfOnlyFinAckDropped

```
// Try to close send stream with timeout
```

## Line 341 — func TestCloseSucceedsIfOnlyFinAckDropped

```
// The reader has to finish before its stream is closed: closing it first
	// drops whatever has not reached it yet, which is not what this checks.
```

## Line 349 — func TestCloseSucceedsIfOnlyFinAckDropped

```
// Try to close receive stream
```

## Line 370 — func TestDataValidWhenResendingSynStateResponse

```
// Create UTP sockets
```

## Line 375 — func TestDataValidWhenResendingSynStateResponse

```
// Create channels for coordination
```

## Line 379 — func TestDataValidWhenResendingSynStateResponse

```
// Accept connection in goroutine
	// Accept connection
```

## Line 388 — func TestDataValidWhenResendingSynStateResponse

```
// Connect
```

## Line 396 — func TestDataValidWhenResendingSynStateResponse

```
// Wait for connection establishment
```

## Line 406 — func TestDataValidWhenResendingSynStateResponse

```
// Data to send
```

## Line 425 — func TestDataValidWhenResendingSynStateResponse

```
// Receive data
```

## Line 435 — func TestDataValidWhenResendingSynStateResponse

```
// Wait for send to complete
```

## Line 438 — func TestDataValidWhenResendingSynStateResponse

```
// Try to close send stream with timeout
```

## Line 451 — func TestDataValidWhenResendingSynStateResponse

```
// The reader has to finish before its stream is closed: closing it first
	// drops whatever has not reached it yet, which is not what this checks.
```

## Line 459 — func TestDataValidWhenResendingSynStateResponse

```
// Try to close receive stream
```
