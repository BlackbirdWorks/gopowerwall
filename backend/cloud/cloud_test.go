package cloud_test

import (
	"encoding/json"
	"io"
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

// recordedRequest captures the parts of an inbound HTTP request the
// write-path tests below need to assert on: method, path, bearer token, and
// decoded JSON body.
type recordedRequest struct {
	body   map[string]any
	auth   string
	method string
	path   string
}

// recordRequest reads and JSON-decodes r's body (if any) into a
// recordedRequest, so a test handler can both answer the request and let the
// test assert on exactly what was sent.
func recordRequest(t *testing.T, r *http.Request) recordedRequest {
	t.Helper()

	b, err := io.ReadAll(r.Body)
	require.NoError(t, err)

	var body map[string]any
	if len(b) > 0 {
		require.NoError(t, json.Unmarshal(b, &body))
	}

	return recordedRequest{
		method: r.Method,
		path:   r.URL.Path,
		auth:   r.Header.Get("Authorization"),
		body:   body,
	}
}

// cloudOperationPaths returns the three site-scoped URL paths the
// postAPIOperation write path can hit: backup reserve, operation mode, and
// site info.
func cloudOperationPaths() (string, string, string) {
	base := "/api/1/energy_sites/" + testSiteID

	return base + "/backup", base + "/operation", base + "/site_info"
}

// gridImportExportPath returns the URL path shared by SetGridCharging and
// SetGridExport - both POST to the same Tesla endpoint with different body
// fields, per docs/parity-matrix.md §4.
func gridImportExportPath() string {
	return "/api/1/energy_sites/" + testSiteID + "/grid_import_export"
}

// newAuthenticatedCloudBackend redirects the process-global
// http.DefaultTransport to addr (as withRedirectedDefaultTransport does),
// writes a valid auth file and site file for testSiteID, and returns an
// authenticated cloud backend. Like withRedirectedDefaultTransport, callers
// must not run in parallel.
func newAuthenticatedCloudBackend(t *testing.T, addr string) *cloud.PyPowerwallCloud {
	t.Helper()

	withRedirectedDefaultTransport(t, addr)
	dir := t.TempDir()
	writeAuthFile(t, dir, testEmail, "tok-1")
	writeSiteFile(t, dir)
	c := cloud.New(testEmail, testCacheTTL, testTimeout, "", dir)
	require.NoError(t, c.Authenticate(t.Context()))

	return c
}

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
		email: map[string]any{
			"access_token":  accessToken,
			"refresh_token": "r1",
			"token_type":    "Bearer",
		},
	}
	b, err := json.Marshal(data)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, cloud.AuthFile), b, 0o600))
}

func writeSiteFile(t *testing.T, dir string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(dir, cloud.SiteFile), []byte(testSiteID), 0o600))
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
				writeSiteFile(t, dir)
			},
		},
		{
			name:  "empty email resolves to the sole auth file entry",
			email: "",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeAuthFile(t, dir, "someone@else.com", "tok-2")
				writeSiteFile(t, dir)
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
				require.NoError(
					t,
					os.WriteFile(filepath.Join(dir, cloud.AuthFile), []byte("{not-json"), 0o600),
				)
			},
			wantErr: true,
		},
		{
			name:  "email not present in auth file",
			email: "missing@example.com",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeAuthFile(t, dir, testEmail, "tok-3")
				writeSiteFile(t, dir)
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

// TestCloudPostAPIOperation exercises postAPIOperation's real HTTP calls:
// backup_reserve_percent goes to ".../backup" as {"backup_reserve_percent": <int>},
// real_mode goes to ".../operation" as {"default_real_mode": "<mode>"}, and
// both are sent independently when both fields are present in the payload -
// this is the fix for the parity gap where cloud/FleetAPI writes previously
// never reached Tesla at all (docs/parity-matrix.md §4).
//
// Every subtest redirects the process-global http.DefaultTransport (cloud's
// Tesla Owner API URL is a hardcoded constant, unlike FleetAPI's
// configurable base URL), so none of them call t.Parallel() and they must
// run one at a time - same constraint as TestCloudNetworkBackedBehavior.
//
//nolint:paralleltest // subtests redirect the process-global http.DefaultTransport; must run sequentially
func TestCloudPostAPIOperation(t *testing.T) {
	backupPath, operationPath, _ := cloudOperationPaths()

	type testCase struct {
		wantErrIs      error
		payload        map[string]any
		wantBackupBody map[string]any
		wantOpBody     map[string]any
		name           string
		backupStatus   int
		opStatus       int
	}

	cases := []testCase{
		{
			name:           "backup_reserve_percent only",
			payload:        map[string]any{"backup_reserve_percent": 42.0},
			wantBackupBody: map[string]any{"backup_reserve_percent": float64(42)},
		},
		{
			name:       "real_mode only",
			payload:    map[string]any{"real_mode": "backup"},
			wantOpBody: map[string]any{"default_real_mode": "backup"},
		},
		{
			name: "both fields sent as two independent requests",
			payload: map[string]any{
				"backup_reserve_percent": 80.0,
				"real_mode":              "self_consumption",
			},
			wantBackupBody: map[string]any{"backup_reserve_percent": float64(80)},
			wantOpBody:     map[string]any{"default_real_mode": "self_consumption"},
		},
		{
			name:         "backup 401 maps to ErrLogin",
			payload:      map[string]any{"backup_reserve_percent": 10.0},
			backupStatus: http.StatusUnauthorized,
			wantErrIs:    backend.ErrLogin,
		},
		{
			name:         "backup 403 maps to ErrLogin",
			payload:      map[string]any{"backup_reserve_percent": 10.0},
			backupStatus: http.StatusForbidden,
			wantErrIs:    backend.ErrLogin,
		},
		{
			name:      "operation 404 maps to ErrNotFound",
			payload:   map[string]any{"real_mode": "backup"},
			opStatus:  http.StatusNotFound,
			wantErrIs: backend.ErrNotFound,
		},
		{
			name:         "backup 429 maps to ErrRateLimited",
			payload:      map[string]any{"backup_reserve_percent": 10.0},
			backupStatus: http.StatusTooManyRequests,
			wantErrIs:    backend.ErrRateLimited,
		},
		{
			name:      "operation 503 maps to ErrRateLimited",
			payload:   map[string]any{"real_mode": "backup"},
			opStatus:  http.StatusServiceUnavailable,
			wantErrIs: backend.ErrRateLimited,
		},
		{
			name:         "backup 500 maps to ErrUnexpectedStatus",
			payload:      map[string]any{"backup_reserve_percent": 10.0},
			backupStatus: http.StatusInternalServerError,
			wantErrIs:    backend.ErrUnexpectedStatus,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var backupReq, opReq *recordedRequest

			handler := func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case backupPath:
					rec := recordRequest(t, r)
					backupReq = &rec
					if tc.backupStatus != 0 {
						w.WriteHeader(tc.backupStatus)

						return
					}
					w.WriteHeader(http.StatusOK)
				case operationPath:
					rec := recordRequest(t, r)
					opReq = &rec
					if tc.opStatus != 0 {
						w.WriteHeader(tc.opStatus)

						return
					}
					w.WriteHeader(http.StatusOK)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}
			c := newAuthenticatedCloudBackend(t, newCloudTestServer(t, handler))

			res, err := c.Post(t.Context(), "/api/operation", tc.payload, "", false, false)

			if tc.wantErrIs != nil {
				require.ErrorIs(t, err, tc.wantErrIs)
			} else {
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"status": "success"}, res)
			}

			if tc.wantBackupBody != nil {
				require.NotNil(t, backupReq, "expected a request to %s", backupPath)
				assert.Equal(t, http.MethodPost, backupReq.method)
				assert.Equal(t, "Bearer tok-1", backupReq.auth)
				assert.Equal(t, tc.wantBackupBody, backupReq.body)
			}

			if tc.wantOpBody != nil {
				require.NotNil(t, opReq, "expected a request to %s", operationPath)
				assert.Equal(t, http.MethodPost, opReq.method)
				assert.Equal(t, "Bearer tok-1", opReq.auth)
				assert.Equal(t, tc.wantOpBody, opReq.body)
			}
		})
	}
}

// TestCloudPostAPIOperationInvalidatesCache confirms a successful write
// clears the cached SITE_CONFIG entry (which backs "/api/operation" reads
// and GetGridCharging/GetGridExport), so a subsequent read is not served
// stale data.
//
//nolint:paralleltest // redirects the process-global http.DefaultTransport; must run sequentially
func TestCloudPostAPIOperationInvalidatesCache(t *testing.T) {
	backupPath, operationPath, siteInfoPath := cloudOperationPaths()

	var siteInfoCalls int
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case siteInfoPath:
			siteInfoCalls++
			_, _ = w.Write(
				[]byte(
					`{"response":{"default_real_mode":"self_consumption","backup_reserve_percent":20}}`,
				),
			)
		case backupPath, operationPath:
			_ = recordRequest(t, r)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
	c := newAuthenticatedCloudBackend(t, newCloudTestServer(t, handler))

	_, err := c.Poll(t.Context(), "/api/operation", false, false, false)
	require.NoError(t, err)
	_, err = c.Poll(t.Context(), "/api/operation", false, false, false)
	require.NoError(t, err)
	require.Equal(t, 1, siteInfoCalls, "second read should be served from cache")

	_, err = c.Post(
		t.Context(),
		"/api/operation",
		map[string]any{"real_mode": "backup"},
		"",
		false,
		false,
	)
	require.NoError(t, err)

	_, err = c.Poll(t.Context(), "/api/operation", false, false, false)
	require.NoError(t, err)
	assert.Equal(
		t,
		2,
		siteInfoCalls,
		"the write should have invalidated the cached SITE_CONFIG entry",
	)
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

// TestCloudSetGridCharging exercises SetGridCharging's real HTTP call: POST
// .../grid_import_export with
// {"disallow_charge_from_grid_with_solar_installed": <bool>} - note the
// field name Tesla actually reads is the "disallow" flag, not a
// "grid_charging" toggle; mode is forwarded to it verbatim.
//
//nolint:paralleltest // subtests redirect the process-global http.DefaultTransport; must run sequentially
func TestCloudSetGridCharging(t *testing.T) {
	gridPath := gridImportExportPath()

	type testCase struct {
		wantErrIs error
		name      string
		mode      bool
		status    int
	}

	cases := []testCase{
		{name: "enabling charging sends true", mode: true},
		{name: "disabling charging sends false", mode: false},
		{
			name:      "401 maps to ErrLogin",
			mode:      true,
			status:    http.StatusUnauthorized,
			wantErrIs: backend.ErrLogin,
		},
		{
			name:      "404 maps to ErrNotFound",
			mode:      true,
			status:    http.StatusNotFound,
			wantErrIs: backend.ErrNotFound,
		},
		{
			name: "429 maps to ErrRateLimited", mode: true,
			status: http.StatusTooManyRequests, wantErrIs: backend.ErrRateLimited,
		},
		{
			name: "500 maps to ErrUnexpectedStatus", mode: true,
			status: http.StatusInternalServerError, wantErrIs: backend.ErrUnexpectedStatus,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req *recordedRequest
			handler := func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, gridPath, r.URL.Path)
				rec := recordRequest(t, r)
				req = &rec
				if tc.status != 0 {
					w.WriteHeader(tc.status)

					return
				}
				w.WriteHeader(http.StatusOK)
			}
			c := newAuthenticatedCloudBackend(t, newCloudTestServer(t, handler))

			res, err := c.SetGridCharging(t.Context(), tc.mode)

			if tc.wantErrIs != nil {
				require.ErrorIs(t, err, tc.wantErrIs)
			} else {
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"status": "success"}, res)
			}

			require.NotNil(t, req)
			assert.Equal(t, http.MethodPost, req.method)
			assert.Equal(t, "Bearer tok-1", req.auth)
			assert.Equal(
				t,
				map[string]any{"disallow_charge_from_grid_with_solar_installed": tc.mode},
				req.body,
			)
		})
	}
}

// TestCloudSetGridExport exercises SetGridExport's real HTTP call: POST
// .../grid_import_export with {"customer_preferred_export_rule": "<mode>"}.
//
//nolint:paralleltest // subtests redirect the process-global http.DefaultTransport; must run sequentially
func TestCloudSetGridExport(t *testing.T) {
	gridPath := gridImportExportPath()

	type testCase struct {
		wantErrIs error
		name      string
		mode      string
		status    int
	}

	cases := []testCase{
		{name: "battery_ok mode", mode: "battery_ok"},
		{name: "pv_only mode", mode: "pv_only"},
		{
			name:      "401 maps to ErrLogin",
			mode:      "battery_ok",
			status:    http.StatusUnauthorized,
			wantErrIs: backend.ErrLogin,
		},
		{
			name:      "404 maps to ErrNotFound",
			mode:      "battery_ok",
			status:    http.StatusNotFound,
			wantErrIs: backend.ErrNotFound,
		},
		{
			name: "503 maps to ErrRateLimited", mode: "battery_ok",
			status: http.StatusServiceUnavailable, wantErrIs: backend.ErrRateLimited,
		},
		{
			name: "500 maps to ErrUnexpectedStatus", mode: "battery_ok",
			status: http.StatusInternalServerError, wantErrIs: backend.ErrUnexpectedStatus,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req *recordedRequest
			handler := func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, gridPath, r.URL.Path)
				rec := recordRequest(t, r)
				req = &rec
				if tc.status != 0 {
					w.WriteHeader(tc.status)

					return
				}
				w.WriteHeader(http.StatusOK)
			}
			c := newAuthenticatedCloudBackend(t, newCloudTestServer(t, handler))

			res, err := c.SetGridExport(t.Context(), tc.mode)

			if tc.wantErrIs != nil {
				require.ErrorIs(t, err, tc.wantErrIs)
			} else {
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"status": "success"}, res)
			}

			require.NotNil(t, req)
			assert.Equal(t, http.MethodPost, req.method)
			assert.Equal(t, "Bearer tok-1", req.auth)
			assert.Equal(t, map[string]any{"customer_preferred_export_rule": tc.mode}, req.body)
		})
	}
}

// TestCloudGridImportExportInvalidatesCache confirms both SetGridCharging
// and SetGridExport clear the cached SITE_CONFIG entry that GetGridCharging
// and GetGridExport read through, so a re-read after a write is not served
// stale data.
//
//nolint:paralleltest // redirects the process-global http.DefaultTransport; must run sequentially
func TestCloudGridImportExportInvalidatesCache(t *testing.T) {
	gridPath := gridImportExportPath()
	_, _, siteInfoPath := cloudOperationPaths()

	var siteInfoCalls int
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case siteInfoPath:
			siteInfoCalls++
			_, _ = w.Write([]byte(`{"response":{"grid_charging":false}}`))
		case gridPath:
			_ = recordRequest(t, r)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
	c := newAuthenticatedCloudBackend(t, newCloudTestServer(t, handler))

	_, err := c.GetGridCharging(t.Context())
	require.NoError(t, err)
	_, err = c.GetGridCharging(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, siteInfoCalls, "second read should be served from cache")

	_, err = c.SetGridCharging(t.Context(), true)
	require.NoError(t, err)

	_, err = c.GetGridCharging(t.Context())
	require.NoError(t, err)
	assert.Equal(
		t,
		2,
		siteInfoCalls,
		"SetGridCharging should have invalidated the cached SITE_CONFIG entry",
	)
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
			_, _ = w.Write(
				[]byte(`{"response":{"default_real_mode":"backup","backup_reserve_percent":55}}`),
			)
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
			_, _ = w.Write(
				[]byte(`{"response":{"site_name":"CloudHouse","installation_time_zone":"UTC"}}`),
			)
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
			_, _ = w.Write(
				[]byte(
					`{"response":{"id":"cloud-din","version":"1.2.3","installation_date":"2024-01-01"}}`,
				),
			)
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
			_, _ = w.Write(
				[]byte(
					`{"response":{"solar_power":5,"battery_power":6,"load_power":7,"grid_power":8}}`,
				),
			)
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

	t.Run(
		"FetchPower verbose falls back to the zeroed stub when the site is unreachable",
		func(t *testing.T) {
			withRedirectedDefaultTransport(t, "127.0.0.1:1")
			c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

			// getAPIMetersAggregates never returns an error, so a network
			// failure surfaces as the zeroed stub rather than as an error here.
			res, err := c.FetchPower(t.Context(), "battery", true)
			require.NoError(t, err)
			m, ok := res.(map[string]any)
			require.True(t, ok)
			assert.InDelta(t, 0.0, m["instant_power"], 0.001)
		},
	)

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

	t.Run(
		"Authenticate falls back to product id when energy_site_id is absent",
		func(t *testing.T) {
			handler := func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"response":[{"id":456}]}`))
			}
			withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
			dir := t.TempDir()
			writeAuthFile(t, dir, testEmail, "tok")
			c := cloud.New(testEmail, testCacheTTL, testTimeout, "", dir)

			require.NoError(t, c.Authenticate(t.Context()))
		},
	)

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

	t.Run(
		"Authenticate refreshes an empty access token exactly once and persists it with 0600",
		func(t *testing.T) {
			var tokenCalls int
			handler := func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/oauth2/v3/token":
					tokenCalls++
					assert.NoError(t, r.ParseForm())
					assert.Equal(t, "refresh_token", r.PostForm.Get("grant_type"))
					assert.Equal(t, "r1", r.PostForm.Get("refresh_token"))
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"access_token":  "at-fresh",
						"refresh_token": "r1",
						"token_type":    "Bearer",
						"expires_in":    28800,
					})
				case "/api/1/products":
					_, _ = w.Write([]byte(`{"response":[{"energy_site_id":789}]}`))
				case "/api/1/energy_sites/789/live_status":
					_, _ = w.Write([]byte(`{"response":{}}`))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}
			withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
			dir := t.TempDir()
			writeAuthFile(t, dir, testEmail, "") // empty access token forces an immediate refresh
			c := cloud.New(testEmail, testCacheTTL, testTimeout, "", dir)

			require.NoError(t, c.Authenticate(t.Context()))
			assert.Equal(t, 1, tokenCalls, "the token endpoint should be hit exactly once")

			authPath := filepath.Join(dir, cloud.AuthFile)
			info, statErr := os.Stat(authPath)
			require.NoError(t, statErr)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

			authBytes, readErr := os.ReadFile(authPath)
			require.NoError(t, readErr)
			var authData map[string]map[string]any
			require.NoError(t, json.Unmarshal(authBytes, &authData))
			assert.Equal(t, "at-fresh", authData[testEmail]["access_token"])
			assert.NotEmpty(t, authData[testEmail]["expires_at"])

			// A second call should reuse the now-valid cached token.
			_, pollErr := c.Poll(t.Context(), "/api/meters/aggregates", true, false, false)
			require.NoError(t, pollErr)
			assert.Equal(t, 1, tokenCalls, "a valid cached token must not be refreshed again")
		},
	)

	t.Run("GetGridCharging returns ErrLogin on a 401 response", func(t *testing.T) {
		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}
		withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

		_, err := c.GetGridCharging(t.Context())
		require.ErrorIs(t, err, backend.ErrLogin)
	})
}

// TestCloudAuthenticateBootstrapsFromEnv covers bootstrapping AuthFile from
// TESLA_REFRESH_TOKEN when no auth file exists yet, so a container's first
// run can start from a .env file alone. It uses t.Setenv, so - like
// TestCloudNetworkBackedBehavior - it must not run in parallel.
//
//nolint:paralleltest // subtests use t.Setenv, which panics if combined with t.Parallel(); must run sequentially
func TestCloudAuthenticateBootstrapsFromEnv(t *testing.T) {
	t.Run(
		"bootstraps the auth file from TESLA_REFRESH_TOKEN and refreshes immediately",
		func(t *testing.T) {
			t.Setenv("TESLA_REFRESH_TOKEN", "env-refresh-token")

			var tokenCalls int
			handler := func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/oauth2/v3/token":
					tokenCalls++
					assert.NoError(t, r.ParseForm())
					assert.Equal(t, "env-refresh-token", r.PostForm.Get("refresh_token"))
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"access_token":  "at-bootstrapped",
						"refresh_token": "env-refresh-token",
						"token_type":    "Bearer",
						"expires_in":    28800,
					})
				case "/api/1/products":
					_, _ = w.Write([]byte(`{"response":[{"energy_site_id":789}]}`))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}
			withRedirectedDefaultTransport(t, newCloudTestServer(t, handler))

			dir := t.TempDir()
			c := cloud.New(testEmail, testCacheTTL, testTimeout, "", dir)

			require.NoError(t, c.Authenticate(t.Context()))
			assert.Equal(t, 1, tokenCalls)

			authPath := filepath.Join(dir, cloud.AuthFile)
			info, statErr := os.Stat(authPath)
			require.NoError(t, statErr)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

			authBytes, readErr := os.ReadFile(authPath)
			require.NoError(t, readErr)
			var authData map[string]map[string]any
			require.NoError(t, json.Unmarshal(authBytes, &authData))
			assert.Equal(t, "at-bootstrapped", authData[testEmail]["access_token"])
		},
	)

	t.Run("missing auth file and no env refresh token still fails", func(t *testing.T) {
		dir := t.TempDir()
		c := cloud.New(testEmail, testCacheTTL, testTimeout, testSiteID, dir)

		err := c.Authenticate(t.Context())
		require.ErrorIs(t, err, backend.ErrMissingAuthFile)
	})
}
