package proxy_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/gopowerwall/proxy"
)

// TestDefaultConfigDefaults checks the zero-environment defaults. It is safe
// to run in the parallel phase: it never calls t.Setenv itself, and the Go
// test runner only starts parallel tests once every non-parallel test (all
// the t.Setenv-driven ones below) has finished and restored the environment.
func TestDefaultConfigDefaults(t *testing.T) {
	t.Parallel()

	cfg := proxy.DefaultConfig()

	assert.Equal(t, 8675, cfg.Port)
	assert.Equal(t, "email@example.com", cfg.Email)
	assert.Equal(t, "America/Los_Angeles", cfg.Timezone)
	assert.Equal(t, "clear.js", cfg.Style)
	assert.Equal(t, "cookie", cfg.AuthMode)
	assert.Equal(t, ".powerwall", cfg.CacheFile)
	assert.Equal(t, "V2024_06", cfg.TedapiAPIVersion)
	assert.Equal(t, "basic", cfg.TedapiAuthMode)
	assert.True(t, cfg.NegSolar)
	assert.True(t, cfg.GracefulDegradation)
	assert.True(t, cfg.HealthCheckEnabled)
	assert.True(t, cfg.TedapiRecoveryEnabled)
	assert.False(t, cfg.DebugMode)
	assert.False(t, cfg.FailFastMode)
	assert.Equal(t, "/", cfg.APIBaseURL)
	assert.Equal(t, 5, cfg.CacheExpire)
	assert.Equal(t, 30, cfg.CacheTTL)
	assert.Equal(t, 5, cfg.Timeout)
	assert.Equal(t, 30, cfg.TedapiProbeInterval)
	assert.Equal(t, 300, cfg.FirmwareCheckInterval)
}

func TestDefaultConfigStyleGetsJSSuffix(t *testing.T) {
	t.Setenv("PW_STYLE", "white")

	cfg := proxy.DefaultConfig()
	assert.Equal(t, "white.js", cfg.Style)
}

func TestDefaultConfigStyleKeepsExistingJSSuffix(t *testing.T) {
	t.Setenv("PW_STYLE", "white.js")

	cfg := proxy.DefaultConfig()
	assert.Equal(t, "white.js", cfg.Style)
}

func TestDefaultConfigAuthPathAffectsCacheFile(t *testing.T) {
	t.Setenv("PW_AUTH_PATH", "/tmp/pw-auth")

	cfg := proxy.DefaultConfig()
	assert.Equal(t, "/tmp/pw-auth", cfg.AuthPath)
	assert.Equal(t, "/tmp/pw-auth/.powerwall", cfg.CacheFile)
}

func TestDefaultConfigEnvOverrides(t *testing.T) {
	type testCase struct {
		check  func(t *testing.T, cfg proxy.Config)
		name   string
		envKey string
		envVal string
	}

	for _, tc := range []testCase{
		{
			name: "port", envKey: "PW_PORT", envVal: "9999",
			check: func(t *testing.T, cfg proxy.Config) { t.Helper(); assert.Equal(t, 9999, cfg.Port) },
		},
		{
			name: "invalid int falls back to default", envKey: "PW_PORT", envVal: "not-a-number",
			check: func(t *testing.T, cfg proxy.Config) { t.Helper(); assert.Equal(t, 8675, cfg.Port) },
		},
		{
			name: "neg solar off via no", envKey: "PW_NEG_SOLAR", envVal: "no",
			check: func(t *testing.T, cfg proxy.Config) { t.Helper(); assert.False(t, cfg.NegSolar) },
		},
		{
			name: "bool true via yes", envKey: "PW_DEBUG", envVal: "yes",
			check: func(t *testing.T, cfg proxy.Config) { t.Helper(); assert.True(t, cfg.DebugMode) },
		},
		{
			name: "bool true via 1", envKey: "PW_DEBUG", envVal: "1",
			check: func(t *testing.T, cfg proxy.Config) { t.Helper(); assert.True(t, cfg.DebugMode) },
		},
		{
			name: "bool true via TRUE case-insensitive", envKey: "PW_DEBUG", envVal: "TRUE",
			check: func(t *testing.T, cfg proxy.Config) { t.Helper(); assert.True(t, cfg.DebugMode) },
		},
		{
			name: "bool false via anything else", envKey: "PW_DEBUG", envVal: "nope",
			check: func(t *testing.T, cfg proxy.Config) { t.Helper(); assert.False(t, cfg.DebugMode) },
		},
		{
			name: "site zero threshold", envKey: "PW_SITE_ZERO_THRESHOLD", envVal: "50",
			check: func(t *testing.T, cfg proxy.Config) { t.Helper(); assert.Equal(t, 50, cfg.SiteZeroThreshold) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envKey, tc.envVal)
			tc.check(t, proxy.DefaultConfig())
		})
	}
}

func TestDefaultConfigTedapiProbeIntervalClamp(t *testing.T) {
	t.Setenv("PW_TEDAPI_PROBE_INTERVAL", "1")

	cfg := proxy.DefaultConfig()
	assert.Equal(t, 30, cfg.TedapiProbeInterval)
}

func TestDefaultConfigTedapiProbeIntervalAboveMinKept(t *testing.T) {
	t.Setenv("PW_TEDAPI_PROBE_INTERVAL", "10")

	cfg := proxy.DefaultConfig()
	assert.Equal(t, 10, cfg.TedapiProbeInterval)
}

func TestDefaultConfigFirmwareCheckIntervalClamp(t *testing.T) {
	t.Setenv("PW_FIRMWARE_CHECK_INTERVAL", "5")

	cfg := proxy.DefaultConfig()
	assert.Equal(t, 300, cfg.FirmwareCheckInterval)
}

func TestConfigDurationHelpers(t *testing.T) {
	t.Parallel()

	cfg := proxy.Config{CacheExpire: 5, CacheTTL: 30, Timeout: 10}

	assert.Equal(t, 5*time.Second, cfg.CacheExpireDuration())
	assert.Equal(t, 30*time.Second, cfg.CacheTTLDuration())
	assert.Equal(t, 10*time.Second, cfg.TimeoutDuration())
}
