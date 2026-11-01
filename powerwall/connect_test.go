package powerwall_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/powerwall"
)

type failoverTestCase struct {
	name          string
	wantMode      powerwall.ConnectionMode
	opts          []powerwall.Option
	wantFailover  bool
	expectConnect bool
}

func TestPowerwallFailoverOptions(t *testing.T) {
	t.Parallel()

	testCases := []failoverTestCase{
		{
			name: "default failover is true",
			opts: []powerwall.Option{
				powerwall.WithHost("127.0.0.1:9"),
				powerwall.WithPassword("test"),
				powerwall.WithCloudMode(false),
			},
			wantFailover:  true,
			wantMode:      powerwall.ModeLocal,
			expectConnect: false,
		},
		{
			name: "explicit WithFailover(false)",
			opts: []powerwall.Option{
				powerwall.WithHost("127.0.0.1:9"),
				powerwall.WithPassword("test"),
				powerwall.WithCloudMode(false),
				powerwall.WithFailover(false),
			},
			wantFailover:  false,
			wantMode:      powerwall.ModeLocal,
			expectConnect: false,
		},
		{
			name: "explicit WithFailover(true)",
			opts: []powerwall.Option{
				powerwall.WithHost("127.0.0.1:9"),
				powerwall.WithPassword("test"),
				powerwall.WithCloudMode(false),
				powerwall.WithFailover(true),
			},
			wantFailover:  true,
			wantMode:      powerwall.ModeLocal,
			expectConnect: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cacheDir := t.TempDir()
			opts := append(
				[]powerwall.Option{
					powerwall.WithCacheFile(filepath.Join(cacheDir, ".powerwall")),
				},
				tc.opts...,
			)

			pw, err := powerwall.New(t.Context(), opts...)
			if !tc.expectConnect {
				var connectErr *powerwall.ConnectError
				require.ErrorAs(t, err, &connectErr)
			} else {
				require.NoError(t, err)
			}
			require.NotNil(t, pw)
			assert.Equal(t, tc.wantFailover, pw.Failover())
			assert.Equal(t, tc.wantMode, pw.Mode())
		})
	}
}

type connectContextTestCase struct {
	name        string
	cancelEarly bool
	retry       bool
	failover    bool
}

func TestPowerwallConnectWithContextCancellation(t *testing.T) {
	t.Parallel()

	testCases := []connectContextTestCase{
		{
			name:        "strict mode with canceled context returns false immediately",
			cancelEarly: true,
			retry:       true,
			failover:    false,
		},
		{
			name:        "failover mode with canceled context returns false immediately",
			cancelEarly: true,
			retry:       true,
			failover:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cacheDir := t.TempDir()
			pw, err := powerwall.New(
				t.Context(),
				powerwall.WithHost("127.0.0.1:9"),
				powerwall.WithPassword("test"),
				powerwall.WithCloudMode(false),
				powerwall.WithFailover(tc.failover),
				powerwall.WithCacheFile(filepath.Join(cacheDir, ".powerwall")),
			)
			var connectErr *powerwall.ConnectError
			require.ErrorAs(t, err, &connectErr)
			require.NotNil(t, pw)

			ctx, cancel := context.WithCancel(t.Context())
			if tc.cancelEarly {
				cancel()
			} else {
				defer cancel()
			}

			start := time.Now()
			connected := pw.Connect(ctx, tc.retry)
			assert.False(t, connected)
			assert.Less(t, time.Since(start), 2*time.Second)
			assert.Equal(t, powerwall.ModeLocal, pw.Mode())
		})
	}
}

func TestPowerwallConnectErrorReason(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name           string
		wantReasonSub  string
		wantModeString string
		opts           []powerwall.Option
	}

	for _, tc := range []testCase{
		{
			name: "local mode failure records first local failure reason",
			opts: []powerwall.Option{
				powerwall.WithHost("127.0.0.1:9"),
				powerwall.WithPassword("test"),
				powerwall.WithCloudMode(false),
			},
			wantModeString: string(powerwall.ModeLocal),
			wantReasonSub:  "local mode failed:",
		},
		{
			name: "v1r mode missing password records v1r password reason",
			opts: []powerwall.Option{
				powerwall.WithHost("127.0.0.1:9"),
				powerwall.WithRSAKeyPath("nonexistent.pem"),
				powerwall.WithCloudMode(false),
			},
			wantModeString: string(powerwall.ModeLocal),
			wantReasonSub:  "customer password or gw_pwd",
		},
		{
			name: "cloud mode failure records first cloud failure reason",
			opts: []powerwall.Option{
				powerwall.WithCloudMode(true),
			},
			wantModeString: string(powerwall.ModeCloud),
			wantReasonSub:  "cloud mode failed:",
		},
		{
			name: "fleetapi mode failure records first fleetapi failure reason",
			opts: []powerwall.Option{
				powerwall.WithCloudMode(true),
				powerwall.WithFleetAPI(true),
			},
			wantModeString: string(powerwall.ModeFleetAPI),
			wantReasonSub:  "fleetapi mode failed:",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			opts := append(
				[]powerwall.Option{
					powerwall.WithAuthPath(dir),
					powerwall.WithCacheFile(filepath.Join(dir, ".powerwall")),
				},
				tc.opts...,
			)

			pw, err := powerwall.New(t.Context(), opts...)
			var connectErr *powerwall.ConnectError
			require.ErrorAs(t, err, &connectErr)
			require.NotNil(t, pw)
			assert.Equal(t, tc.wantModeString, connectErr.Mode)
			assert.Contains(t, connectErr.Reason, tc.wantReasonSub)
			assert.Contains(t, pw.ConnectError(), tc.wantReasonSub)
			assert.Contains(t, connectErr.Error(), tc.wantReasonSub)
		})
	}
}

func TestPowerwallFailedReconnectKeepsPreviousClientAndMode(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name     string
		failover bool
	}

	for _, tc := range []testCase{
		{name: "with failover enabled", failover: true},
		{name: "with failover disabled", failover: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/login/Basic" {
					http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "cookie-value"})
					http.SetCookie(w, &http.Cookie{Name: "UserRecord", Value: "user-value"})
					w.WriteHeader(http.StatusOK)

					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))

			dir := t.TempDir()
			cacheFile := filepath.Join(dir, ".powerwall")
			pw, err := powerwall.New(
				t.Context(),
				powerwall.WithHost(srv.Listener.Addr().String()),
				powerwall.WithPassword("password"),
				powerwall.WithCloudMode(false),
				powerwall.WithFailover(tc.failover),
				powerwall.WithAuthPath(dir),
				powerwall.WithCacheFile(cacheFile),
			)
			require.NoError(t, err)
			require.True(t, pw.IsConnected())
			require.Equal(t, powerwall.ModeLocal, pw.Mode())
			assert.Empty(t, pw.ConnectError())

			// Shut down the server and remove cached session so a reconnect fails across all modes.
			srv.Close()
			require.NoError(t, os.Remove(cacheFile))

			reconnected := pw.Connect(t.Context(), false)
			assert.False(t, reconnected)
			assert.True(t, pw.IsConnected(), "failed reconnect must preserve previous client")
			assert.Equal(t, powerwall.ModeLocal, pw.Mode())
			assert.True(t, pw.IsLocal())
			assert.NotEmpty(t, pw.ConnectError())
		})
	}
}

func TestPowerwallWiFiV1rFallbackOptions(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name         string
		fallback     bool
		useV1r       bool
		wantFallback bool
	}

	for _, tc := range []testCase{
		{
			name:         "local mode without v1r reports WiFiV1rReady false",
			fallback:     false,
			useV1r:       false,
			wantFallback: false,
		},
		{
			name:         "v1r mode with WiFiV1rFallback enabled wires secondary v1r transport",
			fallback:     true,
			useV1r:       true,
			wantFallback: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			opts := []powerwall.Option{
				powerwall.WithHost("127.0.0.1:9"),
				powerwall.WithWiFiHost("127.0.0.1:9"),
				powerwall.WithWiFiV1rFallback(tc.fallback),
				powerwall.WithCloudMode(false),
				powerwall.WithCacheFile(filepath.Join(dir, ".powerwall")),
			}
			if tc.useV1r {
				opts = append(
					opts,
					powerwall.WithGwPwd("gatewaypassword"),
					powerwall.WithRSAKeyPath(writeTestRSAKey(t)),
				)
			} else {
				opts = append(opts, powerwall.WithPassword("test"))
			}

			pw, _ := powerwall.New(t.Context(), opts...)
			require.NotNil(t, pw)
			assert.Equal(t, tc.wantFallback, pw.WiFiV1rFallback())
			assert.False(t, pw.WiFiV1rReady(t.Context()))
		})
	}
}
