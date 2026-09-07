package fleetapi_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/backend"
	"github.com/blackbirdworks/gopowerwall/backend/fleetapi"
)

const (
	testCacheTTL = time.Minute
	testTimeout  = 5 * time.Second
	testEmail    = "user@example.com"
	testSiteID   = "site-123"
)

// newTestServer starts a plain HTTP test server and registers its cleanup.
func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv
}

// writeConfigFile writes a FleetAPI config JSON file to dir, merging any
// extra fields on top of the required access_token and refresh_token.
func writeConfigFile(t *testing.T, dir string, extra map[string]any) {
	t.Helper()

	data := map[string]any{"access_token": "tok-abc", "refresh_token": "rt-abc", "client_id": "test-client"}
	maps.Copy(data, extra)
	b, err := json.Marshal(data)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, fleetapi.ConfigFile), b, 0o600))
}

// newAuthenticatedBackend writes a config file pointing at srv and returns
// an authenticated FleetAPI backend.
func newAuthenticatedBackend(t *testing.T, srv *httptest.Server) *fleetapi.PyPowerwallFleetAPI {
	t.Helper()

	dir := t.TempDir()
	writeConfigFile(t, dir, map[string]any{"fleet_api_url": srv.URL})
	f := fleetapi.New(testEmail, testCacheTTL, testTimeout, testSiteID, dir)
	require.NoError(t, f.Authenticate(t.Context()))

	return f
}

func TestBaseURL(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		region string
		want   string
	}

	cases := []testCase{
		{name: "EU region", region: "eu", want: fleetapi.FleetAPIURLEU},
		{name: "CN region", region: "cn", want: fleetapi.FleetAPIURLCN},
		{name: "NA region", region: "na", want: fleetapi.FleetAPIURLNA},
		{name: "unknown region defaults to NA", region: "xx", want: fleetapi.FleetAPIURLNA},
		{name: "empty region defaults to NA", region: "", want: fleetapi.FleetAPIURLNA},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, fleetapi.BaseURL(tc.region))
		})
	}
}

func TestFleetAPIAuthenticate(t *testing.T) {
	t.Parallel()

	type testCase struct {
		wantErrIs error
		setup     func(t *testing.T, dir string)
		name      string
		siteID    string
		wantErr   bool
	}

	cases := []testCase{
		{
			name: "success with full config",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeConfigFile(t, dir, map[string]any{
					"site_id":       "cfg-site",
					"fleet_api_url": "https://example.invalid/",
				})
			},
		},
		{
			name:      "missing config file",
			setup:     func(_ *testing.T, _ string) {},
			wantErrIs: backend.ErrMissingAuthFile,
		},
		{
			name: "malformed config JSON",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(dir, fleetapi.ConfigFile), []byte("{not-json"), 0o600))
			},
			wantErr: true,
		},
		{
			name: "missing refresh_token returns ErrLogin",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				b, err := json.Marshal(map[string]any{"site_id": "x"})
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, fleetapi.ConfigFile), b, 0o600))
			},
			wantErrIs: backend.ErrLogin,
		},
		{
			name:   "pre-set site id takes precedence over config",
			siteID: "preset-site",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeConfigFile(t, dir, map[string]any{"site_id": "cfg-site"})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			tc.setup(t, dir)
			f := fleetapi.New(testEmail, testCacheTTL, testTimeout, tc.siteID, dir)

			err := f.Authenticate(t.Context())
			switch {
			case tc.wantErrIs != nil:
				require.ErrorIs(t, err, tc.wantErrIs)
			case tc.wantErr:
				require.Error(t, err)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestFleetAPIUnknownAPIDispatch(t *testing.T) {
	t.Parallel()

	f := fleetapi.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

	pollRes, err := f.Poll(t.Context(), "/api/does-not-exist", false, false, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"ERROR": "Unknown API: /api/does-not-exist"}, pollRes)

	postRes, err := f.Post(t.Context(), "/api/does-not-exist", nil, "", false, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"ERROR": "Unknown API: /api/does-not-exist"}, postRes)
}

func TestFleetAPIKnownStubEndpoints(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		api  string
	}

	cases := []testCase{
		{name: "devices vitals", api: "/api/devices/vitals"},
		{name: "vitals", api: "/vitals"},
		{name: "login", api: "/api/login/Basic"},
		{name: "logout", api: "/api/logout"},
		{name: "powerwalls", api: "/api/powerwalls"},
		{name: "meters site", api: "/api/meters/site"},
		{name: "meters", api: "/api/meters"},
		{name: "sitemaster", api: "/api/sitemaster"},
		{name: "customer", api: "/api/customer"},
		{name: "installer", api: "/api/installer"},
		{name: "networks", api: "/api/networks"},
		{name: "auth toggle supported", api: "/api/auth/toggle/supported"},
		{name: "system update status", api: "/api/system/update/status"},
		{name: "solars", api: "/api/solars"},
		{name: "system status", api: "/api/system_status"},
	}

	f := fleetapi.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := f.Poll(t.Context(), tc.api, false, false, false)
			require.NoError(t, err)
			assert.NotNil(t, res)
		})
	}
}

func TestFleetAPIPostOperation(t *testing.T) {
	t.Parallel()

	f := fleetapi.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

	res, err := f.Post(t.Context(), "/api/operation", map[string]any{"real_mode": "backup"}, "", false, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"status": "success"}, res)
}

func TestFleetAPIVitalsAndTimeRemaining(t *testing.T) {
	t.Parallel()

	f := fleetapi.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

	vitals, err := f.Vitals(t.Context())
	require.NoError(t, err)
	assert.Empty(t, vitals)

	remaining, err := f.GetTimeRemaining(t.Context())
	require.NoError(t, err)
	require.NotNil(t, remaining)
	assert.InDelta(t, 0.0, *remaining, 0.001)
}

func TestFleetAPISetGridChargingAndExport(t *testing.T) {
	t.Parallel()

	f := fleetapi.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

	res, err := f.SetGridCharging(t.Context(), true)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"status": "success"}, res)

	res, err = f.SetGridExport(t.Context(), "battery_ok")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"status": "success"}, res)
}

func TestFleetAPIClose(t *testing.T) {
	t.Parallel()

	f := fleetapi.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())
	assert.NoError(t, f.Close(t.Context()))
}

func TestFleetAPIGetSiteDataAndAggregates(t *testing.T) {
	t.Parallel()

	t.Run("success powers meters aggregates and caches the result", func(t *testing.T) {
		t.Parallel()

		var calls int
		handler := func(w http.ResponseWriter, r *http.Request) {
			calls++
			assert.Equal(t, "/api/1/energy_sites/"+testSiteID+"/live_status", r.URL.Path)
			assert.Equal(t, "Bearer tok-abc", r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(
				`{"response":{"solar_power":100,"battery_power":-50,"load_power":150,"grid_power":10}}`,
			))
		}
		srv := newTestServer(t, handler)
		f := newAuthenticatedBackend(t, srv)

		res, err := f.Poll(t.Context(), "/api/meters/aggregates", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		solar, ok := m["solar"].(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 100.0, solar["instant_power"], 0.001)

		_, err = f.Poll(t.Context(), "/api/meters/aggregates", false, false, false)
		require.NoError(t, err)
		assert.Equal(t, 1, calls, "the second call should be served from the SITE_DATA cache")
	})

	t.Run("failure returns the zeroed stub", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}
		srv := newTestServer(t, handler)
		f := newAuthenticatedBackend(t, srv)

		res, err := f.Poll(t.Context(), "/api/meters/aggregates", true, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		site, ok := m["site"].(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 0.0, site["instant_power"], 0.001)
	})

	t.Run("unreachable base URL returns the zeroed stub", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		writeConfigFile(t, dir, map[string]any{"fleet_api_url": "http://127.0.0.1:1"})
		f := fleetapi.New(testEmail, testCacheTTL, testTimeout, testSiteID, dir)
		require.NoError(t, f.Authenticate(t.Context()))

		res, err := f.Poll(t.Context(), "/api/meters/aggregates", false, false, false)
		require.NoError(t, err)
		assert.NotNil(t, res)
	})
}

func TestFleetAPIOperation(t *testing.T) {
	t.Parallel()

	t.Run("reads backup reserve and mode from config", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/1/energy_sites/"+testSiteID+"/site_info", r.URL.Path)
			_, _ = w.Write([]byte(`{"response":{"default_real_mode":"backup","backup_reserve_percent":55}}`))
		}
		srv := newTestServer(t, handler)
		f := newAuthenticatedBackend(t, srv)

		res, err := f.Poll(t.Context(), "/api/operation", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "backup", m["real_mode"])
		assert.InDelta(t, 55.0, m["backup_reserve_percent"], 0.001)
	})

	t.Run("defaults are used when config is unreachable", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}
		srv := newTestServer(t, handler)
		f := newAuthenticatedBackend(t, srv)

		res, err := f.Poll(t.Context(), "/api/operation", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "self_consumption", m["real_mode"])
		assert.InDelta(t, 20.0, m["backup_reserve_percent"], 0.001)
	})
}

func TestFleetAPISiteInfo(t *testing.T) {
	t.Parallel()

	handler := func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"response":{"site_name":"FleetHouse","installation_time_zone":"UTC"}}`))
	}
	srv := newTestServer(t, handler)
	f := newAuthenticatedBackend(t, srv)

	res, err := f.Poll(t.Context(), "/api/site_info", false, false, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"site_name": "FleetHouse", "timezone": "UTC"}, res)

	nameRes, err := f.Poll(t.Context(), "/api/site_info/site_name", false, false, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"site_name": "FleetHouse", "timezone": "UTC"}, nameRes)
}

func TestFleetAPISiteInfoDefaults(t *testing.T) {
	t.Parallel()

	f := fleetapi.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

	res, err := f.Poll(t.Context(), "/api/site_info", false, false, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"site_name": "Powerwall", "timezone": "America/Los_Angeles"}, res)
}

func TestFleetAPIStatus(t *testing.T) {
	t.Parallel()

	t.Run("reads din and version from config", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"response":{"id":"fleet-din","version":"2.0.0","installation_date":"2024-02-02"}}`))
		}
		srv := newTestServer(t, handler)
		f := newAuthenticatedBackend(t, srv)

		res, err := f.Poll(t.Context(), "/api/status", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "fleet-din", m["din"])
		assert.Equal(t, "2.0.0", m["version"])
	})

	t.Run("defaults when config is unreachable", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}
		srv := newTestServer(t, handler)
		f := newAuthenticatedBackend(t, srv)

		res, err := f.Poll(t.Context(), "/api/status", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, testSiteID, m["din"])
	})
}

func TestFleetAPIGridStatus(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		siteData   string
		wantStatus string
	}

	cases := []testCase{
		{
			name:       "active grid status stays connected",
			siteData:   `{"response":{"grid_status":"Active"}}`,
			wantStatus: "SystemGridConnected",
		},
		{
			name:       "unknown grid status stays connected",
			siteData:   `{"response":{"grid_status":"Unknown"}}`,
			wantStatus: "SystemGridConnected",
		},
		{
			name:       "any other grid status is islanded",
			siteData:   `{"response":{"grid_status":"Down"}}`,
			wantStatus: "SystemIslandedActive",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.siteData))
			}
			srv := newTestServer(t, handler)
			f := newAuthenticatedBackend(t, srv)

			res, err := f.Poll(t.Context(), "/api/system_status/grid_status", false, false, false)
			require.NoError(t, err)
			m, ok := res.(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tc.wantStatus, m["grid_status"])
		})
	}
}

func TestFleetAPISystemStatusSOE(t *testing.T) {
	t.Parallel()

	t.Run("reads the live percentage", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"response":{"percentage_charged":44.4}}`))
		}
		srv := newTestServer(t, handler)
		f := newAuthenticatedBackend(t, srv)

		res, err := f.Poll(t.Context(), "/api/system_status/soe", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 44.4, m["percentage"], 0.001)
	})

	t.Run("defaults to 100 when unreachable", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}
		srv := newTestServer(t, handler)
		f := newAuthenticatedBackend(t, srv)

		res, err := f.Poll(t.Context(), "/api/system_status/soe", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 100.0, m["percentage"], 0.001)
	})
}

func TestFleetAPIGridChargingAndExport(t *testing.T) {
	t.Parallel()

	t.Run("GetGridCharging returns the configured value", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"response":{"grid_charging":true}}`))
		}
		srv := newTestServer(t, handler)
		f := newAuthenticatedBackend(t, srv)

		got, err := f.GetGridCharging(t.Context())
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.True(t, *got)
	})

	t.Run("GetGridCharging returns ErrNotFound when unreachable", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}
		srv := newTestServer(t, handler)
		f := newAuthenticatedBackend(t, srv)

		_, err := f.GetGridCharging(t.Context())
		require.Error(t, err)
	})

	t.Run("GetGridExport returns the configured value", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"response":{"customer_preferred_export_rule":"pv_only"}}`))
		}
		srv := newTestServer(t, handler)
		f := newAuthenticatedBackend(t, srv)

		got, err := f.GetGridExport(t.Context())
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "pv_only", *got)
	})

	t.Run("GetGridExport returns ErrNotFound when the field is absent", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"response":{}}`))
		}
		srv := newTestServer(t, handler)
		f := newAuthenticatedBackend(t, srv)

		_, err := f.GetGridExport(t.Context())
		require.ErrorIs(t, err, backend.ErrNotFound)
	})

	t.Run("GetGridCharging returns ErrLogin on a 401 response", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}
		srv := newTestServer(t, handler)
		f := newAuthenticatedBackend(t, srv)

		_, err := f.GetGridCharging(t.Context())
		require.ErrorIs(t, err, backend.ErrLogin)
	})
}

func TestFleetAPIPowerAndFetchPower(t *testing.T) {
	t.Parallel()

	handler := func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"response":{"solar_power":5,"battery_power":6,"load_power":7,"grid_power":8}}`))
	}
	srv := newTestServer(t, handler)
	f := newAuthenticatedBackend(t, srv)

	power, err := f.Power(t.Context())
	require.NoError(t, err)
	assert.InDelta(t, 7.0, power["load"], 0.001)

	verbose, err := f.FetchPower(t.Context(), "battery", true)
	require.NoError(t, err)
	m, ok := verbose.(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 6.0, m["instant_power"], 0.001)

	single, err := f.FetchPower(t.Context(), "battery", false)
	require.NoError(t, err)
	assert.InDelta(t, 6.0, single, 0.001)
}

// withRedirectedDefaultTransport points http.DefaultTransport at a local
// test server for the life of the current test, then restores it. Unlike
// fleet_api_url, TeslaAuthURL is a fixed constant, so exercising a real
// token refresh needs the same global-transport redirection cloud's tests
// use for the (also fixed) Tesla Owner API URL.
//
// http.DefaultTransport is process-global mutable state, so callers of this
// helper must not call t.Parallel().
func withRedirectedDefaultTransport(t *testing.T, addr string) {
	t.Helper()

	original := http.DefaultTransport
	//nolint:reassign // deliberate: see doc comment above
	http.DefaultTransport = &localRedirectTransport{addr: addr, upstream: &http.Transport{}}
	t.Cleanup(func() {
		//nolint:reassign // deliberate: restores the original
		http.DefaultTransport = original
	})
}

// localRedirectTransport rewrites every outgoing request to target addr
// instead of its original host, so a test can intercept calls fleetapi makes
// to the hardcoded TeslaAuthURL.
type localRedirectTransport struct {
	upstream http.RoundTripper
	addr     string
}

func (l *localRedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = l.addr
	clone.Host = l.addr

	return l.upstream.RoundTrip(clone)
}

// TestFleetAPIAuthenticateRefresh exercises the OAuth2 refresh path against a
// fake Tesla token endpoint. It does not call t.Parallel() because it
// redirects the process-global http.DefaultTransport.
//
//nolint:paralleltest // subtest redirects the process-global http.DefaultTransport; must run sequentially
func TestFleetAPIAuthenticateRefresh(t *testing.T) {
	t.Run("refreshes an empty access token exactly once and persists it with 0600", func(t *testing.T) {
		var tokenCalls int
		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/oauth2/v3/token":
				tokenCalls++
				assert.NoError(t, r.ParseForm())
				assert.Equal(t, "refresh_token", r.PostForm.Get("grant_type"))
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":  "at-fresh",
					"refresh_token": "rt-abc",
					"token_type":    "Bearer",
					"expires_in":    28800,
				})
			case "/api/1/energy_sites/" + testSiteID + "/live_status":
				assert.Equal(t, "Bearer at-fresh", r.Header.Get("Authorization"))
				_, _ = w.Write([]byte(`{"response":{}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}
		srv := newTestServer(t, handler)
		withRedirectedDefaultTransport(t, srv.Listener.Addr().String())

		dir := t.TempDir()
		writeConfigFile(t, dir, map[string]any{"access_token": "", "fleet_api_url": srv.URL})
		f := fleetapi.New(testEmail, testCacheTTL, testTimeout, testSiteID, dir)

		require.NoError(t, f.Authenticate(t.Context()))

		_, err := f.Poll(t.Context(), "/api/meters/aggregates", true, false, false)
		require.NoError(t, err)
		assert.Equal(t, 1, tokenCalls, "the token endpoint should be hit exactly once")

		cfgPath := filepath.Join(dir, fleetapi.ConfigFile)
		info, statErr := os.Stat(cfgPath)
		require.NoError(t, statErr)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

		b, readErr := os.ReadFile(cfgPath)
		require.NoError(t, readErr)
		var cfgData map[string]any
		require.NoError(t, json.Unmarshal(b, &cfgData))
		assert.Equal(t, "at-fresh", cfgData["access_token"])

		// A second call should reuse the now-valid cached token.
		_, err = f.Poll(t.Context(), "/api/meters/aggregates", true, false, false)
		require.NoError(t, err)
		assert.Equal(t, 1, tokenCalls, "a valid cached token must not be refreshed again")
	})
}
