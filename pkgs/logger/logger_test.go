package logger_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
)

func TestLevelFor(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name  string
		debug bool
		want  slog.Level
	}

	for _, tc := range []testCase{
		{name: "debug enabled", debug: true, want: slog.LevelDebug},
		{name: "debug disabled", debug: false, want: slog.LevelInfo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, logger.LevelFor(tc.debug))
		})
	}
}

func TestNewLevelFiltering(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		wantLines []string
		skipLines []string
		level     slog.Level
	}

	for _, tc := range []testCase{
		{
			name:      "info level drops debug records",
			level:     slog.LevelInfo,
			wantLines: []string{"visible-info", "visible-warn"},
			skipLines: []string{"hidden-debug"},
		},
		{
			name:      "debug level keeps every record",
			level:     slog.LevelDebug,
			wantLines: []string{"hidden-debug", "visible-info", "visible-warn"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			log := logger.New(&buf, tc.level)
			log.Debug("hidden-debug")
			log.Info("visible-info")
			log.Warn("visible-warn")

			out := buf.String()
			for _, want := range tc.wantLines {
				assert.Contains(t, out, want)
			}
			for _, skip := range tc.skipLines {
				assert.NotContains(t, out, skip)
			}
		})
	}
}

func TestNewOmitsTimestamp(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger.New(&buf, slog.LevelInfo).Info("hello")

	assert.NotContains(t, buf.String(), "time=")
	assert.Contains(t, buf.String(), "msg=hello")
}

// unrelatedKey is a distinct context key type used to prove that Load ignores
// values it did not store.
type unrelatedKey struct{}

func TestLoad(t *testing.T) {
	t.Parallel()

	type testCase struct {
		ctx        func(t *testing.T) context.Context
		name       string
		wantStored bool
	}

	for _, tc := range []testCase{
		{
			name: "returns the stored logger",
			ctx: func(t *testing.T) context.Context {
				t.Helper()

				return logger.Into(t.Context(), logger.New(&bytes.Buffer{}, slog.LevelDebug))
			},
			wantStored: true,
		},
		{
			name: "falls back to the default logger",
			ctx: func(t *testing.T) context.Context {
				t.Helper()

				return t.Context()
			},
			wantStored: false,
		},
		{
			name: "falls back when the context carries an unrelated value",
			ctx: func(t *testing.T) context.Context {
				t.Helper()

				return context.WithValue(t.Context(), unrelatedKey{}, "unrelated")
			},
			wantStored: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := logger.Load(tc.ctx(t))
			require.NotNil(t, got)
			assert.Equal(t, tc.wantStored, got != slog.Default())
		})
	}
}

//nolint:staticcheck // Exercising the documented nil-context fallback.
func TestLoadNilContext(t *testing.T) {
	t.Parallel()

	assert.Same(t, slog.Default(), logger.Load(nil))
}

func TestWithAccumulatesAttributes(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	ctx := logger.Into(t.Context(), logger.New(&buf, slog.LevelInfo))
	ctx = logger.With(ctx, "backend", "tedapi")
	ctx = logger.With(ctx, "din", "1234")

	logger.Load(ctx).InfoContext(ctx, "polled")

	out := buf.String()
	assert.Contains(t, out, "backend=tedapi")
	assert.Contains(t, out, "din=1234")
	assert.Equal(t, 1, strings.Count(out, "msg=polled"))
}
