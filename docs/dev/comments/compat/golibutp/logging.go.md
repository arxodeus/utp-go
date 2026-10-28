# compat/golibutp/logging.go

Every comment in `compat/golibutp/logging.go` at e56c9aa, verbatim.

## Line 14 — var Logger

```
// Logger is the default Socket logger. Override it per Socket with
// WithLogger.
```

## Line 18 — type logHandler

```
// logHandler carries this library's logging to a Socket's anacrolix logger.
//
// What reaches it is set by the Socket's log options, as libutp's are:
// warnings and errors always, LogNormal adds the informational, LogMtu the
// path-MTU search's and everything else at debug level, LogDebug the
// per-packet trace.
```

## Line 62 — const levelCrit

```
// levelCrit is the level the engine logged at for what go-libutp calls
// critical, go-ethereum's crit: above slog's error.
```

## Line 81 — func logLevelFor

```
// logLevelFor is the least level a Socket with these options passes on.
```
