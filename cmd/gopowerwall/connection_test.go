package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildPowerwallModeValidation exercises BuildPowerwall's error paths.
// None of these ever reach gopowerwall.New, so they are safe to run in
// parallel: no network I/O, no session-cache file touched.
func TestBuildPowerwallModeValidation(t *testing.T) {
	t.Parallel()

	type testCase struct {
		wantErr error
		name    string
		flags   ConnectionFlags
	}

	for _, tc := range []testCase{
		{
			name:    "v1r missing gateway password",
			flags:   ConnectionFlags{V1r: true, Host: "127.0.0.1:9"},
			wantErr: ErrV1rMissingGwPwd,
		},
		{
			name:    "v1r missing host",
			flags:   ConnectionFlags{V1r: true, GwPwd: "secret"},
			wantErr: ErrV1rMissingHost,
		},
		{
			name:    "tedapi missing gateway password",
			flags:   ConnectionFlags{TEDAPI: true},
			wantErr: ErrTedapiMissingGw,
		},
		{
			name:    "local missing host",
			flags:   ConnectionFlags{Local: true},
			wantErr: ErrLocalMissingHost,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pw, err := tc.flags.BuildPowerwall(t.Context())
			require.Error(t, err)
			require.ErrorIs(t, err, tc.wantErr)
			assert.Nil(t, pw)
		})
	}
}

// TestBuildPowerwallAppliesEveryConfiguredOption drives BuildPowerwall
// through every "if field set, append option" branch. Each case sets
// AuthPath to its own t.TempDir(): BuildPowerwall relocates the local
// session-cache file under AuthPath (see ConnectionFlags.BuildPowerwall), so
// this isolates every case's cache file from the others and from the
// package's working directory without changing the process-wide working
// directory via t.Chdir, which the testing package forbids combining with
// t.Parallel.
func TestBuildPowerwallAppliesEveryConfiguredOption(t *testing.T) {
	t.Parallel()

	type testCase struct {
		flags func(t *testing.T) ConnectionFlags
		name  string
	}

	for _, tc := range []testCase{
		{
			name: "local mode with host, password and authpath set",
			flags: func(t *testing.T) ConnectionFlags {
				t.Helper()

				return ConnectionFlags{
					Local:    true,
					Host:     "127.0.0.1:9",
					Password: "testpw",
					AuthPath: t.TempDir(),
				}
			},
		},
		{
			name: "v1r mode with an explicit RSA key path",
			flags: func(t *testing.T) ConnectionFlags {
				t.Helper()

				return ConnectionFlags{
					V1r:        true,
					Host:       "127.0.0.1:9",
					GwPwd:      "gatewaypwd",
					RsaKeyPath: "explicit-key.pem",
					AuthPath:   t.TempDir(),
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			flags := tc.flags(t)
			pw, err := flags.BuildPowerwall(t.Context())
			require.NoError(t, err)
			require.NotNil(t, pw)
			assert.False(t, pw.IsConnected())
		})
	}
}
