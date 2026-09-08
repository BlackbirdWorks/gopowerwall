package proxy_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/proxy"
)

func TestServerServeHTTPDispatch(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	ts := newProxyServer(t, baseTestConfig(), pw)
	client := ts.Client()

	type testCase struct {
		name       string
		method     string
		path       string
		wantSub    string
		wantStatus int
	}

	for _, tc := range []testCase{
		{
			name:       "path traversal is rejected",
			method:     http.MethodGet,
			path:       "/../../../etc/passwd",
			wantStatus: http.StatusBadRequest,
			wantSub:    "Invalid Path",
		},
		{
			name:       "unsupported method is rejected",
			method:     http.MethodPut,
			path:       "/aggregates",
			wantStatus: http.StatusMethodNotAllowed,
			wantSub:    "Method Not Allowed",
		},
		{
			name:       "HEAD is treated like GET",
			method:     http.MethodHead,
			path:       "/health",
			wantStatus: http.StatusOK,
		},
		{
			name:       "disabled endpoint refuses outright",
			method:     http.MethodGet,
			path:       "/api/customer/registration",
			wantStatus: http.StatusNotFound,
			wantSub:    "404 Response - API Disabled",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequestWithContext(t.Context(), tc.method, ts.URL+tc.path, nil)
			require.NoError(t, err)

			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tc.wantStatus, resp.StatusCode)
			if tc.wantSub != "" {
				body, readErr := io.ReadAll(resp.Body)
				require.NoError(t, readErr)
				assert.Contains(t, string(body), tc.wantSub)
			}
		})
	}
}

func TestServerServeHTTPSetsCORSHeader(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	ts := newProxyServer(t, baseTestConfig(), pw)

	resp, err := ts.Client().Get(ts.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))
}

func TestNewServerBuildsPowerwallWhenNilGiven(t *testing.T) {
	t.Parallel()

	cfg := baseTestConfig()
	cfg.Host = "127.0.0.1:9"
	cfg.AuthMode = ""
	// Isolate the auth cache: without this, NewServer's internal Powerwall
	// would fall back to gopowerwall's default "./.powerwall" cache file
	// and could pick up a stale cached session from another test.
	cfg.CacheFile = filepath.Join(t.TempDir(), ".powerwall")

	srv := proxy.NewServer(t.Context(), cfg, nil)
	require.NotNil(t, srv)
	require.NotNil(t, srv.PW)
	assert.False(t, srv.PW.IsConnected())
}

// TestAllowlistPinnedToUpstreamRouteSet is the regression test for the
// proxy's ALLOWLIST parity gap (docs/parity-matrix.md's allowlist row and
// correction #8): gopowerwall's isAllowlisted (proxy/server.go) had drifted
// from pypowerwall's own ALLOWLIST (server.py:173-199) by 13 entries. Since
// isAllowlisted is unexported, this asserts the reconciled route set by its
// observable HTTP behavior against a disconnected Powerwall rather than by
// calling the function directly:
//
//   - Every path on upstream's own ALLOWLIST must not 404: it either goes
//     through handleAllowlistRoute (answering 200 with "null" for a
//     disconnected backend, since every entry starts with "/api/") or one
//     of the handful of dedicated handlers checked earlier in handleGet's
//     dispatch that happen to cover the same path.
//   - Every path this repo's isAllowlisted used to carry beyond upstream's
//     list, with no test/doc/comment anywhere evidencing a deliberate
//     reason to diverge, must now 404: with no allowlist entry, no static
//     file, and proxyLocalGateway's own "/api/*" exclusion, nothing else
//     in handleWeb can answer for it.
//
// "/api/customer/registration" (present on both upstream's ALLOWLIST and
// its DISABLED list, and unaffected by this reconciliation) is deliberately
// excluded here - TestServerServeHTTPDispatch's "disabled endpoint refuses
// outright" case already covers it. "/api/system_status/soe" is likewise
// excluded from the "no longer allowed" set below: it is separately served
// by its own dedicated handler (handleCoreAPIRoutes) regardless of
// isAllowlisted, so removing its now-redundant allowlist entry is a no-op
// this test should not expect to change observable behavior for - pinning
// it to 404 here would itself be a false regression.
func TestAllowlistPinnedToUpstreamRouteSet(t *testing.T) {
	t.Parallel()

	upstreamAllowlisted := []string{
		"/api/status",
		"/api/site_info/site_name",
		"/api/meters/site",
		"/api/meters/solar",
		"/api/sitemaster",
		"/api/powerwalls",
		"/api/system_status",
		"/api/system_status/grid_status",
		"/api/system/update/status",
		"/api/site_info",
		"/api/system_status/grid_faults",
		"/api/operation",
		"/api/site_info/grid_codes",
		"/api/solars",
		"/api/solars/brands",
		"/api/customer",
		"/api/meters",
		"/api/installer",
		"/api/networks",
		"/api/system/networks",
		"/api/meters/readings",
		"/api/synchrometer/ct_voltage_references",
		"/api/troubleshooting/problems",
		"/api/auth/toggle/supported",
		"/api/solar_powerwall",
	}

	noLongerAllowed := []string{
		"/api/system/networks/conn_tests",
		"/api/diagnostics",
		"/api/generators",
		"/api/generators/actions",
		"/api/syncon/vitals",
		"/api/syncon/actions",
		"/api/inverters",
		"/api/inverters/status",
		"/api/meters/status",
		"/api/powerwalls/status",
	}

	pw := newDisconnectedPowerwall(t)
	ts := newProxyServer(t, baseTestConfig(), pw)

	for _, path := range upstreamAllowlisted {
		t.Run("allowed "+path, func(t *testing.T) {
			t.Parallel()

			resp, err := ts.Client().Get(ts.URL + path)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode, "expected %s to be allowlisted", path)
		})
	}

	for _, path := range noLongerAllowed {
		t.Run("no longer allowed "+path, func(t *testing.T) {
			t.Parallel()

			resp, err := ts.Client().Get(ts.URL + path)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusNotFound, resp.StatusCode, "expected %s to no longer be allowlisted", path)
		})
	}
}

func TestServerLifecycleStartAndShutdown(t *testing.T) {
	t.Parallel()

	cfg := proxy.DefaultConfig()
	cfg.Port = 0
	cfg.BindAddress = "127.0.0.1"

	srv := proxy.NewServer(t.Context(), cfg, newDisconnectedPowerwall(t))

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		assert.True(t, err == nil || errors.Is(err, http.ErrServerClosed))
	case <-time.After(2 * time.Second):
		assert.Fail(t, "server did not shut down within timeout")
	}
}
