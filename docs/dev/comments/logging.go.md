# logging.go

Every comment in `logging.go` at e56c9aa, verbatim.

## Line 5 — const LevelTrace

```
// LevelTrace is the level of this package's per-packet logging, one step
// below slog.LevelDebug: a handler enabled for it sees every packet sent and
// received. It is the trace level go-ethereum's logger has, which this
// package logged through until v0.1.0.
```

## Line 11 — type ulogger

```
// ulogger is how the package logs: a *slog.Logger with a trace level.
```

## Line 14 — func newLogger

```
// newLogger wraps l, or discards everything if l is nil.
```
