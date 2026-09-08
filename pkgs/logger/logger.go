// Package logger provides structured logging built on log/slog, carried on the
// context so that callers control verbosity and destination rather than the
// library holding global state.
package logger

import (
	"context"
	"io"
	"log/slog"
)

// ctxKey is the private context key under which a logger is stored.
type ctxKey struct{}

// New returns a logger writing text-formatted records to w at the given level.
// Timestamps are omitted so that command output stays stable and diffable; the
// gateway supplies its own timestamps on the data it returns.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}

			return a
		},
	}))
}

// LevelFor returns the log level implied by a debug toggle.
func LevelFor(debug bool) slog.Level {
	if debug {
		return slog.LevelDebug
	}

	return slog.LevelInfo
}

// Into returns a copy of ctx carrying l, so that code further down the call
// stack can retrieve it with [Load].
func Into(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// Load returns the logger carried by ctx, falling back to [slog.Default] when
// the context carries none. It never returns nil.
func Load(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
			return l
		}
	}

	return slog.Default()
}

// With returns a copy of ctx carrying the context's logger extended with args,
// so that attributes accumulate as a request descends the call stack.
func With(ctx context.Context, args ...any) context.Context {
	return Into(ctx, Load(ctx).With(args...))
}
