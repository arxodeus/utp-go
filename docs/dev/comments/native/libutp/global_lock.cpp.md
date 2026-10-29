# native/libutp/global_lock.cpp

Every comment in `native/libutp/global_lock.cpp` at e56c9aa, verbatim.

## Line 1

```
// See global_lock.h for why this exists.
```

## Line 13

```
	// Recursive: libutp calls the embedder back from inside its own calls,
	// and the bridge answers some of those callbacks by calling libutp again
	// (cb_on_state_change -> pump_writes -> utp_write).
```
