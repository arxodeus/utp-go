package utp

import "log/slog"

// LevelTrace is the level of the package's per-packet logging, one step
// below slog.LevelDebug. A handler enabled for it sees every packet sent and
// received.
const LevelTrace = slog.Level(-8)

// ulogger is a *slog.Logger with a trace level.
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
