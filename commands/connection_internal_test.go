package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall"
)

// TestResolveModeOptions white-box tests the mode-resolution switch behind
// BuildPowerwall: every sentinel-error branch, plus the option lists each
// success branch returns, applied to a fresh Config to observe their effect.
func TestResolveModeOptions(t *testing.T) {
	t.Parallel()

	type testCase struct {
		verify func(t *testing.T, cfg *gopowerwall.Config, err error)
		name   string
		flags  ConnectionFlags
	}

	for _, tc := range []testCase{
		{
			name:  "v1r missing gateway password",
			flags: ConnectionFlags{V1r: true, Host: "192.168.91.1"},
			verify: func(t *testing.T, _ *gopowerwall.Config, err error) {
				t.Helper()
				assert.ErrorIs(t, err, ErrV1rMissingGwPwd)
			},
		},
		{
			name:  "v1r missing host",
			flags: ConnectionFlags{V1r: true, GwPwd: "secret"},
			verify: func(t *testing.T, _ *gopowerwall.Config, err error) {
				t.Helper()
				assert.ErrorIs(t, err, ErrV1rMissingHost)
			},
		},
		{
			name:  "v1r fully configured applies no extra options",
			flags: ConnectionFlags{V1r: true, GwPwd: "secret", Host: "192.168.91.1"},
			verify: func(t *testing.T, cfg *gopowerwall.Config, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.Equal(t, gopowerwall.DefaultConfig(), cfg)
			},
		},
		{
			name:  "tedapi missing gateway password",
			flags: ConnectionFlags{TEDAPI: true},
			verify: func(t *testing.T, _ *gopowerwall.Config, err error) {
				t.Helper()
				assert.ErrorIs(t, err, ErrTedapiMissingGw)
			},
		},
		{
			name:  "tedapi without host defaults to the local gateway IP",
			flags: ConnectionFlags{TEDAPI: true, GwPwd: "secret"},
			verify: func(t *testing.T, cfg *gopowerwall.Config, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.Equal(t, "192.168.91.1", cfg.Host)
			},
		},
		{
			name:  "tedapi with host set applies no extra options",
			flags: ConnectionFlags{TEDAPI: true, GwPwd: "secret", Host: "10.0.0.5"},
			verify: func(t *testing.T, cfg *gopowerwall.Config, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.Equal(t, gopowerwall.DefaultConfig(), cfg)
			},
		},
		{
			name:  "local missing host",
			flags: ConnectionFlags{Local: true},
			verify: func(t *testing.T, _ *gopowerwall.Config, err error) {
				t.Helper()
				assert.ErrorIs(t, err, ErrLocalMissingHost)
			},
		},
		{
			name:  "local with host disables cloud mode",
			flags: ConnectionFlags{Local: true, Host: "10.0.0.5"},
			verify: func(t *testing.T, cfg *gopowerwall.Config, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.False(t, cfg.CloudMode)
			},
		},
		{
			name:  "cloud enables cloud mode and disables fleetapi",
			flags: ConnectionFlags{Cloud: true},
			verify: func(t *testing.T, cfg *gopowerwall.Config, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.True(t, cfg.CloudMode)
				assert.False(t, cfg.FleetAPI)
			},
		},
		{
			name:  "fleetapi enables both cloud mode and fleetapi",
			flags: ConnectionFlags{FleetAPI: true},
			verify: func(t *testing.T, cfg *gopowerwall.Config, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.True(t, cfg.CloudMode)
				assert.True(t, cfg.FleetAPI)
			},
		},
		{
			name:  "no mode flag falls back to autoselect",
			flags: ConnectionFlags{},
			verify: func(t *testing.T, cfg *gopowerwall.Config, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.True(t, cfg.AutoSelect)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts, err := tc.flags.resolveModeOptions()

			cfg := gopowerwall.DefaultConfig()
			for _, opt := range opts {
				opt(cfg)
			}

			tc.verify(t, cfg, err)
		})
	}
}

// TestResolveRSAKeyFallbackOrder white-box tests resolveRSAKey's lookup
// order, including its "./tedapi_rsa_private.pem in the working directory"
// fallback. That fallback is exercised through the unexported workDir field
// instead of t.Chdir: workDir substitutes for the process's real working
// directory in that one check, so no subtest ever mutates process-wide
// state and every case can run in parallel.
func TestResolveRSAKeyFallbackOrder(t *testing.T) {
	t.Parallel()

	type testCase struct {
		setup func(t *testing.T) (ConnectionFlags, string)
		name  string
	}

	for _, tc := range []testCase{
		{
			name: "v1r disabled returns the configured path as-is",
			setup: func(t *testing.T) (ConnectionFlags, string) {
				t.Helper()

				return ConnectionFlags{RsaKeyPath: "explicit.pem"}, "explicit.pem"
			},
		},
		{
			name: "v1r disabled with nothing configured returns empty",
			setup: func(t *testing.T) (ConnectionFlags, string) {
				t.Helper()

				return ConnectionFlags{}, ""
			},
		},
		{
			name: "v1r enabled with an explicit path short-circuits the filesystem lookup",
			setup: func(t *testing.T) (ConnectionFlags, string) {
				t.Helper()

				return ConnectionFlags{V1r: true, RsaKeyPath: "explicit.pem"}, "explicit.pem"
			},
		},
		{
			name: "v1r enabled falls back to <authpath>/tedapi_rsa_private.pem when present",
			setup: func(t *testing.T) (ConnectionFlags, string) {
				t.Helper()

				authDir := t.TempDir()
				keyPath := filepath.Join(authDir, "tedapi_rsa_private.pem")
				require.NoError(t, os.WriteFile(keyPath, []byte("key"), 0o600))

				// workDir points at an empty directory: only authpath can
				// satisfy this case.
				return ConnectionFlags{V1r: true, AuthPath: authDir, workDir: t.TempDir()}, keyPath
			},
		},
		{
			name: "v1r enabled falls back to ./tedapi_rsa_private.pem in the working directory",
			setup: func(t *testing.T) (ConnectionFlags, string) {
				t.Helper()

				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "tedapi_rsa_private.pem"), []byte("key"), 0o600))

				return ConnectionFlags{V1r: true, AuthPath: t.TempDir(), workDir: dir}, "tedapi_rsa_private.pem"
			},
		},
		{
			name: "v1r enabled with nothing found anywhere returns empty",
			setup: func(t *testing.T) (ConnectionFlags, string) {
				t.Helper()

				return ConnectionFlags{V1r: true, workDir: t.TempDir()}, ""
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			flags, want := tc.setup(t)
			assert.Equal(t, want, flags.resolveRSAKey())
		})
	}
}
