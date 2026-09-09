package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProxyRun exercises run() in cmd/proxy. Not parallel: uses t.Setenv and t.Chdir.
//
//nolint:paralleltest // mutating process env vars and working directory cannot run in parallel
func TestProxyRun(t *testing.T) {
	type testCase struct {
		setup func(t *testing.T)
		name  string
		want  int
	}

	for _, tc := range []testCase{
		{
			name: "clean shutdown on SIGTERM",
			setup: func(t *testing.T) {
				t.Helper()
				t.Setenv("PW_PORT", "0")
				t.Setenv("PW_BIND_ADDRESS", "127.0.0.1")
				t.Setenv("PW_DEBUG", "false")
				go func() {
					time.Sleep(100 * time.Millisecond)
					p, err := os.FindProcess(os.Getpid())
					if err == nil {
						_ = p.Signal(syscall.SIGTERM)
					}
				}()
			},
			want: 0,
		},
		{
			name: "load malformed env reports warning without failing",
			setup: func(t *testing.T) {
				t.Helper()
				t.Setenv("PW_PORT", "0")
				t.Setenv("PW_BIND_ADDRESS", "127.0.0.1")
				tempDir := t.TempDir()
				badEnv := filepath.Join(tempDir, ".env")
				require.NoError(t, os.WriteFile(badEnv, []byte("INVALID_ENV_LINE\n"), 0o600))
				t.Chdir(tempDir)
				go func() {
					time.Sleep(100 * time.Millisecond)
					p, err := os.FindProcess(os.Getpid())
					if err == nil {
						_ = p.Signal(syscall.SIGTERM)
					}
				}()
			},
			want: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			got := run()
			assert.Equal(t, tc.want, got)
		})
	}
}
