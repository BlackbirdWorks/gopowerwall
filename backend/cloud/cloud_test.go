package cloud_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/backend"
	"github.com/blackbirdworks/gopowerwall/backend/cloud"
)

const (
	testCacheTTL = time.Minute
	testTimeout  = 5 * time.Second
	testEmail    = "user@example.com"
	testSiteID   = "site-123"
)

// localRedirectTransport rewrites every outgoing request to target addr
// instead of its original host, so tests can intercept calls the cloud
// backend makes to the hardcoded Tesla Owner API URL.
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

// withRedirectedDefaultTransport points http.DefaultTransport (which
// PyPowerwallCloud's HTTP client falls back to, since it never sets its own
// Transport) at a local test server for the life of the current test, then
// restores it.
//
// http.DefaultTransport is process-global mutable state, exactly like an
// environment variable, so every test using this helper must NOT call
// t.Parallel(): they are grouped into TestCloudNetworkBackedBehavior, which
// runs its subtests strictly sequentially for that reason.
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

func writeAuthFile(t *testing.T, dir, email, accessToken string) {
	t.Helper()

	data := map[string]any{
		email: map[string]any{"access_token": accessToken, "refresh_token": "r1", "token_type": "Bearer"},
	}
	b, err := json.Marshal(data)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, cloud.AuthFile), b, 0o600))
}

func writeSiteFile(t *testing.T, dir, siteID string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(dir, cloud.SiteFile), []byte(siteID), 0o600))
}

func TestCloudAuthenticate(t *testing.T) {
	t.Parallel()

	type testCase struct {
		wantErrIs error
		setup     func(t *testing.T, dir string)
		name      string
		email     string
		wantErr   bool
	}

	cases := []testCase{
		{
			name:  "success with explicit email and cached site id",
			email: testEmail,
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeAuthFile(t, dir, testEmail, "tok-1")
				writeSiteFile(t, dir, testSiteID)
			},
		},
		{
			name:  "empty email resolves to the sole auth file entry",
			email: "",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeAuthFile(t, dir, "someone@else.com", "tok-2")
				writeSiteFile(t, dir, testSiteID)
			},
		},
		{
			name:      "missing auth file",
			email:     testEmail,
			setup:     func(_ *testing.T, _ string) {},
			wantErrIs: backend.ErrMissingAuthFile,
		},
		{
			name:  "malformed auth file JSON",
			email: testEmail,
			setup: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(dir, cloud.AuthFile), []byte("{not-json"), 0o600))
			},
			wantErr: true,
		},
		{
			name:  "email not present in auth file",
			email: "missing@example.com",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeAuthFile(t, dir, testEmail, "tok-3")
				writeSiteFile(t, dir, testSiteID)
			},
			wantErrIs: backend.ErrEmailNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			tc.setup(t, dir)
			c := cloud.New(tc.email, testCacheTTL, testTimeout, "", dir)

			err := c.Authenticate(t.Context())
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

func TestCloudUnknownAPIDispatch(t *testing.T) {
	t.Parallel()

	c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

	pollRes, err := c.Poll(t.Context(), "/api/does-not-exist", false, false, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"ERROR": "Unknown API: /api/does-not-exist"}, pollRes)

	postRes, err := c.Post(t.Context(), "/api/does-not-exist", nil, "", false, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"ERROR": "Unknown API: /api/does-not-exist"}, postRes)
}

func TestCloudKnownStubEndpoints(t *testing.T) {
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

	c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := c.Poll(t.Context(), tc.api, false, false, false)
			require.NoError(t, err)
			assert.NotNil(t, res)
		})
	}
}

func TestCloudPostAPIOperation(t *testing.T) {
	t.Parallel()

	c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

	res, err := c.Post(t.Context(), "/api/operation", map[string]any{"real_mode": "backup"}, "", false, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"status": "success"}, res)
}

func TestCloudVitalsAndTimeRemaining(t *testing.T) {
	t.Parallel()

	c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

	vitals, err := c.Vitals(t.Context())
	require.NoError(t, err)
	assert.Empty(t, vitals)

	remaining, err := c.GetTimeRemaining(t.Context())
	require.NoError(t, err)
	require.NotNil(t, remaining)
	assert.InDelta(t, 0.0, *remaining, 0.001)
}

func TestCloudSetGridChargingAndExport(t *testing.T) {
	t.Parallel()

	c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

	res, err := c.SetGridCharging(t.Context(), true)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"status": "success"}, res)

	res, err = c.SetGridExport(t.Context(), "battery_ok")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"status": "success"}, res)
}

func TestCloudClose(t *testing.T) {
	t.Parallel()

	c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())
	assert.NoError(t, c.Close(t.Context()))
}

// newCloudTestServer starts a plain HTTP test server (the redirect
// transport strips TLS) and returns its listener address.
func newCloudTestServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv.Listener.Addr().String()
}

// TestCloudNetworkBackedBehavior covers every code path that reaches the
// hardcoded Tesla Owner API URL (getSiteData / getSiteConfig / product
// lookup). None of its subtests call t.Parallel(): they all redirect the
// process-global http.DefaultTransport, so they must run one at a time.
//
//nolint:paralleltest // subtests share the process-global http.DefaultTransport; must run sequentially, see helper doc
func TestCloudNetworkBackedBehavior(t *testing.T) {
	t.Run("getSiteData powers meters aggregates and caches the result", func(t *testing.T) {
		var calls int
		handler := func(w http.ResponseWriter, r *http.Request) {
			calls++
			assert.Equal(t, "/api/1/energy_sites/"+testSiteID+"/live_status", r.URL.Path)
			_, _ = w.Write([]byte(
				`{"response":{"solar_power":100,"battery_power":-50,"load_power":150,"grid_power":10}}`,
			))
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		res, err := c.Poll(t.Context(), "/api/meters/aggregates", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		solar, ok := m["solar"].(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 100.0, solar["instant_power"], 0.001)

		_, err = c.Poll(t.Context(), "/api/meters/aggregates", false, false, false)
		require.NoError(t, err)
		assert.Equal(t, 1, calls, "the second call should be served from the SITE_DATA cache")
	})

	t.Run("getSiteData failure returns the zeroed stub", func(t *testing.T) {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		res, err := c.Poll(t.Context(), "/api/meters/aggregates", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		site, ok := m["site"].(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 0.0, site["instant_power"], 0.001)
	})

	t.Run("getAPIOperation reads config and falls back to defaults", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/1/energy_sites/"+testSiteID+"/site_info", r.URL.Path)
			_, _ = w.Write([]byte(`{"response":{"default_real_mode":"backup","backup_reserve_percent":55}}`))
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		res, err := c.Poll(t.Context(), "/api/operation", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "backup", m["real_mode"])
		assert.InDelta(t, 55.0, m["backup_reserve_percent"], 0.001)
	})

	t.Run("getAPIOperation defaults when config is unreachable", func(t *testing.T) {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		res, err := c.Poll(t.Context(), "/api/operation", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "self_consumption", m["real_mode"])
		assert.InDelta(t, 20.0, m["backup_reserve_percent"], 0.001)
	})

	t.Run("getAPISiteInfo and site name read from config", func(t *testing.T) {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"response":{"site_name":"CloudHouse","installation_time_zone":"UTC"}}`))
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		res, err := c.Poll(t.Context(), "/api/site_info", false, false, false)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"site_name": "CloudHouse", "timezone": "UTC"}, res)

		nameRes, err := c.Poll(t.Context(), "/api/site_info/site_name", false, false, false)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"site_name": "CloudHouse", "timezone": "UTC"}, nameRes)
	})

	t.Run("getAPIStatus reads din and version from config", func(t *testing.T) {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"response":{"id":"cloud-din","version":"1.2.3","installation_date":"2024-01-01"}}`))
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		res, err := c.Poll(t.Context(), "/api/status", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "cloud-din", m["din"])
		assert.Equal(t, "1.2.3", m["version"])
		// The fake git hash on the cloud status endpoint is a frozen parity value.
		assert.Equal(t, "27626f98a66cad5c665bbe1d4d788cdb3e94fd34", m["git_hash"])
	})

	t.Run("getAPIStatus defaults when config is unreachable", func(t *testing.T) {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		res, err := c.Poll(t.Context(), "/api/status", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, testSiteID, m["din"])
		assert.Equal(t, "23.28.2 27626f98", m["version"])
	})

	type gridStatusCase struct {
		name       string
		siteData   string
		wantStatus string
	}

	gridStatusCases := []gridStatusCase{
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
	for _, gc := range gridStatusCases {
		t.Run("getAPISystemStatusGridStatus/"+gc.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(gc.siteData))
			}
			withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
			c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

			res, err := c.Poll(t.Context(), "/api/system_status/grid_status", false, false, false)
			require.NoError(t, err)
			m, ok := res.(map[string]any)
			require.True(t, ok)
			assert.Equal(t, gc.wantStatus, m["grid_status"])
		})
	}

	t.Run("getAPISystemStatusSOE reads the live percentage", func(t *testing.T) {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"response":{"percentage_charged":66.6}}`))
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		res, err := c.Poll(t.Context(), "/api/system_status/soe", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 66.6, m["percentage"], 0.001)
	})

	t.Run("getAPISystemStatusSOE defaults to 100 when the site is unreachable", func(t *testing.T) {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		res, err := c.Poll(t.Context(), "/api/system_status/soe", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 100.0, m["percentage"], 0.001)
	})

	t.Run("GetGridCharging returns the configured value", func(t *testing.T) {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"response":{"grid_charging":true}}`))
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		got, err := c.GetGridCharging(t.Context())
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.True(t, *got)
	})

	t.Run("GetGridCharging returns ErrNotFound when the site is unreachable", func(t *testing.T) {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		_, err := c.GetGridCharging(t.Context())
		require.Error(t, err)
	})

	t.Run("GetGridExport returns the configured value", func(t *testing.T) {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"response":{"customer_preferred_export_rule":"pv_only"}}`))
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		got, err := c.GetGridExport(t.Context())
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "pv_only", *got)
	})

	t.Run("GetGridExport returns ErrNotFound when the field is absent", func(t *testing.T) {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"response":{}}`))
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		_, err := c.GetGridExport(t.Context())
		require.ErrorIs(t, err, backend.ErrNotFound)
	})

	t.Run("Power and FetchPower read through the meters aggregates endpoint", func(t *testing.T) {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"response":{"solar_power":5,"battery_power":6,"load_power":7,"grid_power":8}}`))
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		power, err := c.Power(t.Context())
		require.NoError(t, err)
		assert.InDelta(t, 7.0, power["load"], 0.001)

		verbose, err := c.FetchPower(t.Context(), "battery", true)
		require.NoError(t, err)
		m, ok := verbose.(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 6.0, m["instant_power"], 0.001)

		single, err := c.FetchPower(t.Context(), "battery", false)
		require.NoError(t, err)
		assert.InDelta(t, 6.0, single, 0.001)
	})

	t.Run("FetchPower verbose falls back to the zeroed stub when the site is unreachable", func(t *testing.T) {
		withRedirectedDefaultTransport(t, "127.0.0.1:1")
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		// getAPIMetersAggregates never returns an error, so a network
		// failure surfaces as the zeroed stub rather than as an error here.
		res, err := c.FetchPower(t.Context(), "battery", true)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 0.0, m["instant_power"], 0.001)
	})

	t.Run("Authenticate resolves the site id via the products API", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/1/products", r.URL.Path)
			_, _ = w.Write([]byte(`{"response":[{"energy_site_id":789}]}`))
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		dir := t.TempDir()
		writeAuthFile(t, dir, testEmail, "tok")
		c := cloud.New(testEmail, testCacheTTL, testTimeout, "", dir)

		require.NoError(t, c.Authenticate(t.Context()))

		writtenSiteID, err := os.ReadFile(filepath.Join(dir, cloud.SiteFile))
		require.NoError(t, err)
		assert.Equal(t, "789", string(writtenSiteID))
	})

	t.Run("Authenticate falls back to product id when energy_site_id is absent", func(t *testing.T) {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"response":[{"id":456}]}`))
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		dir := t.TempDir()
		writeAuthFile(t, dir, testEmail, "tok")
		c := cloud.New(testEmail, testCacheTTL, testTimeout, "", dir)

		require.NoError(t, c.Authenticate(t.Context()))
	})

	t.Run("Authenticate fails when no energy site is found", func(t *testing.T) {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"response":[]}`))
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		dir := t.TempDir()
		writeAuthFile(t, dir, testEmail, "tok")
		c := cloud.New(testEmail, testCacheTTL, testTimeout, "", dir)

		err := c.Authenticate(t.Context())
		require.ErrorIs(t, err, backend.ErrNoEnergySite)
	})

	t.Run("Authenticate propagates a network failure during site resolution", func(t *testing.T) {
		withRedirectedDefaultTransport(t, "127.0.0.1:1")
		dir := t.TempDir()
		writeAuthFile(t, dir, testEmail, "tok")
		c := cloud.New(testEmail, testCacheTTL, testTimeout, "", dir)

		require.Error(t, c.Authenticate(t.Context()))
	})
}
