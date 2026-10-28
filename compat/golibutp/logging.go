package utp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/anacrolix/log"

	utpgo "github.com/arxodeus/utp"
)

// Logger is the default Socket logger. Override it per Socket with
// WithLogger.
var Logger = log.Default.WithContextText("go-libutp")

// logHandler forwards the engine's logging to a Socket's anacrolix logger.
// Warnings and errors always pass; LogNormal adds informational messages,
// LogMtu adds path-MTU and other debug messages, and LogDebug adds the
// per-packet trace.
type logHandler struct {
	s     *Socket
	attrs string
}

var _ slog.Handler = (*logHandler)(nil)

func newSlogLogger(s *Socket) *slog.Logger {
	return slog.New(&logHandler{s: s})
}

func (h *logHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.Level(h.s.minLogLevel.Load())
}

func (h *logHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	b.WriteString(h.attrs)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})
	h.s.logger.Levelf(anacrolixLevel(r.Level), "%s", b.String())
	return nil
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var b strings.Builder
	b.WriteString(h.attrs)
	for _, a := range attrs {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
	}
	return &logHandler{s: h.s, attrs: b.String()}
}

func (h *logHandler) WithGroup(string) slog.Handler { return h }

// levelCrit is the level used for critical messages, above slog.LevelError.
const levelCrit = slog.LevelError + 4

func anacrolixLevel(l slog.Level) log.Level {
	switch {
	case l >= levelCrit:
		return log.Critical
	case l >= slog.LevelError:
		return log.Error
	case l >= slog.LevelWarn:
		return log.Warning
	case l >= slog.LevelInfo:
		return log.Info
	default:
		return log.Debug
	}
}

// logLevelFor returns the lowest level a Socket with these options passes on.
func logLevelFor(o contextOptions) slog.Level {
	switch {
	case o.logDebug:
		return utpgo.LevelTrace
	case o.logMtu:
		return slog.LevelDebug
	case o.logNormal:
		return slog.LevelInfo
	default:
		return slog.LevelWarn
	}
}
