# compat/golibutp/options.go

Every comment in `compat/golibutp/options.go` at e56c9aa, verbatim.

## Line 9 — type Option

```
// Option names a libutp context or socket option, for Socket.SetOption. The
// values are libutp's (utp.h).
```

## Line 14 — const LogNormal, LogMtu, LogDebug, SendBuffer, RecvBuffer, TargetDelay, TimedOut

```
// UTP_LOG_NORMAL
```

## Line 15 — const LogNormal, LogMtu, LogDebug, SendBuffer, RecvBuffer, TargetDelay, TimedOut

```
// UTP_LOG_MTU
```

## Line 16 — const LogNormal, LogMtu, LogDebug, SendBuffer, RecvBuffer, TargetDelay, TimedOut

```
// UTP_LOG_DEBUG
```

## Line 17 — const LogNormal, LogMtu, LogDebug, SendBuffer, RecvBuffer, TargetDelay, TimedOut

```
// UTP_SNDBUF
```

## Line 18 — const LogNormal, LogMtu, LogDebug, SendBuffer, RecvBuffer, TargetDelay, TimedOut

```
// UTP_RCVBUF
```

## Line 19 — const LogNormal, LogMtu, LogDebug, SendBuffer, RecvBuffer, TargetDelay, TimedOut

```
// UTP_TARGET_DELAY
```

## Line 21 — const LogNormal, LogMtu, LogDebug, SendBuffer, RecvBuffer, TargetDelay, TimedOut

```
// TimedOut is libutp's UTP_ETIMEDOUT error code.
```

## Line 25 — type contextOptions

```
// contextOptions is libutp's per-context option set, with its defaults
// (utp_api.cpp:74, :91).
```

## Line 30 — type contextOptions

```
// microseconds
```

## Line 41 — func (*contextOptions).set

```
// set is utp_context_set_option (utp_internal.cpp:2640-2673).
```

## Line 54 — func (*contextOptions).set

```
// libutp asserts val >= 1
```

## Line 68 — func (*contextOptions).get

```
// get is utp_context_get_option.
```

## Line 93 — func (*contextOptions).connectionConfig

```
// connectionConfig is what a connection made now gets: libutp copies the
// context's options into each socket it creates (utp_internal.cpp:2616-2621).
```

## Line 101 — func (*contextOptions).connectionConfig

```
// apply_ccontrol's own fallback (:1633-1634)
```
