package proxy_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/proxy"
)

func TestPrometheusMetrics(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		wantSubs   []string
		wantStatus int
		withPW     bool
	}

	for _, tc := range []testCase{
		{
			name:       "metrics with disconnected powerwall",
			withPW:     false,
			wantStatus: http.StatusOK,
			wantSubs: []string{
				"powerwall_proxy_uptime_seconds",
				"powerwall_proxy_requests_total",
				"powerwall_proxy_errors_total",
				"powerwall_proxy_timeouts_total",
			},
		},
		{
			name:       "metrics with connected powerwall",
			withPW:     true,
			wantStatus: http.StatusOK,
			wantSubs: []string{
				"powerwall_proxy_uptime_seconds",
				"powerwall_proxy_requests_total",
				"powerwall_solar_power_watts",
				"powerwall_battery_power_watts",
				"powerwall_grid_power_watts",
				"powerwall_load_power_watts",
				"powerwall_battery_charge_percent",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := proxy.Config{
				BindAddress: "127.0.0.1",
				Port:        0,
				CacheExpire: 5,
				CacheTTL:    30,
			}

			var srv *proxy.Server
			if tc.withPW {
				gw := newFakeGateway(t, nil)
				pw := newLocalPowerwall(t, gw)
				srv = proxy.NewServer(t.Context(), cfg, pw)
			} else {
				srv = proxy.NewServer(t.Context(), cfg, nil)
			}
			t.Cleanup(func() {
				_ = srv.Close(context.Background())
			})

			ts := httptest.NewServer(srv)
			t.Cleanup(ts.Close)

			client := ts.Client()
			resp, err := client.Get(ts.URL + "/metrics")
			require.NoError(t, err)
			defer func() {
				_ = resp.Body.Close()
			}()

			assert.Equal(t, tc.wantStatus, resp.StatusCode)
			assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			for _, sub := range tc.wantSubs {
				assert.Contains(t, string(body), sub)
			}
		})
	}
}

func TestServerClose(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		withPW bool
	}

	for _, tc := range []testCase{
		{name: "close server with nil PW", withPW: false},
		{name: "close server with active PW", withPW: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := proxy.Config{
				BindAddress: "127.0.0.1",
				Port:        0,
				CacheExpire: 5,
				CacheTTL:    30,
				Timeout:     5,
			}

			var srv *proxy.Server
			if tc.withPW {
				gw := newFakeGateway(t, nil)
				pw := newLocalPowerwall(t, gw)
				srv = proxy.NewServer(t.Context(), cfg, pw)
			} else {
				srv = proxy.NewServer(t.Context(), cfg, nil)
			}

			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()

			err := srv.Close(ctx)
			require.NoError(t, err)

			// Idempotent double close
			err2 := srv.Close(ctx)
			require.NoError(t, err2)
		})
	}
}
