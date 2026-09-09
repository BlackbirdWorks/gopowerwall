package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCLIRun exercises run() in cmd/gopowerwall.
//
//nolint:paralleltest // mutating working directory via t.Chdir cannot run in parallel
func TestCLIRun(t *testing.T) {
	type testCase struct {
		setup    func(t *testing.T)
		name     string
		wantOut  string
		args     []string
		wantExit int
	}

	for _, tc := range []testCase{
		{
			name:     "version subcommand succeeds",
			args:     []string{"version"},
			wantExit: 0,
			wantOut:  "gopowerwall [",
		},
		{
			name:     "invalid subcommand returns non-zero",
			args:     []string{"nonexistent-command"},
			wantExit: 1,
		},
		{
			name: "load malformed env reports warning without crashing",
			setup: func(t *testing.T) {
				t.Helper()
				tempDir := t.TempDir()
				badEnv := filepath.Join(tempDir, ".env")
				require.NoError(t, os.WriteFile(badEnv, []byte("INVALID_ENV_LINE\n"), 0o600))
				t.Chdir(tempDir)
			},
			args:     []string{"version"},
			wantExit: 0,
			wantOut:  "gopowerwall [",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup(t)
			}

			var stdout, stderr bytes.Buffer
			got := run(tc.args, &stdout, &stderr)

			assert.Equal(t, tc.wantExit, got)
			if tc.wantOut != "" {
				assert.Contains(t, stdout.String(), tc.wantOut)
			}
		})
	}
}
