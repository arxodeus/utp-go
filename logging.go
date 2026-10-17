package utp

import "log/slog"

// LevelTrace is the level of this package's per-packet logging, one step
// below slog.LevelDebug: a handler enabled for it sees every packet sent and
// received. It is the trace level go-ethereum's logger has, which this
// package logged through until v0.1.0.
const LevelTrace = slog.Level(-8)

// ulogger is how the package logs: a *slog.Logger with a trace level.
type ulogger struct{ *slog.Logger }

// newLogger wraps l, or discards everything if l is nil.
func newLogger(l *slog.Logger) *ulogger {
	if l == nil {
		l = slog.New(slog.DiscardHandler)
	}
	return &ulogger{l}
}

func (l *ulogger) Trace(msg string, args ...any) {
	l.Log(baseContext, LevelTrace, msg, args...)
}
