// Package logging configures structured JSON logging and request-scoped
// attributes such as request IDs.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
)

type ctxKey struct{}

// New returns a JSON slog logger at the given level.
func New(level string, w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stdout
	}
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl, ReplaceAttr: redact}))
}

// sensitiveKeys are never written to logs even if passed by mistake.
var sensitiveKeys = map[string]bool{
	"password": true, "token": true, "secret": true, "authorization": true,
	"cookie": true, "private_key": true, "otp": true, "code": true, "session": true,
}

func redact(_ []string, a slog.Attr) slog.Attr {
	if sensitiveKeys[a.Key] {
		return slog.String(a.Key, "[REDACTED]")
	}
	return a
}

// WithLogger stores a logger in the context.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// From returns the context logger or the default logger.
func From(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
