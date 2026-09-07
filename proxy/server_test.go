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
