package fleetapi_test

import (
	"encoding/json"
	"io"
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

// recordedRequest captures the parts of an inbound HTTP request the write-path
// tests below need to assert on: method, path, bearer token, and decoded JSON
// body.
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

	data := map[string]any{
		"access_token":  "tok-abc",
		"refresh_token": "rt-abc",
		"client_id":     "test-client",
	}
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
				require.NoError(
					t,
					os.WriteFile(
						filepath.Join(dir, fleetapi.ConfigFile),
						[]byte("{not-json"),
						0o600,
					),
				)
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
		// "/api/system_status" is intentionally not covered here: since the
		// live-data overlay fix, it requires getSiteData/getSiteConfig to
		// succeed and returns nil without a reachable site - see
		// TestFleetAPISystemStatusOverlay for its network-backed coverage.
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

// fleetAPIOperationPaths returns the three site-scoped URL paths the
// postAPIOperation write path can hit: backup reserve, operation mode, and
// site info.
func fleetAPIOperationPaths() (string, string, string) {
	base := "/api/1/energy_sites/" + testSiteID

	return base + "/backup", base + "/operation", base + "/site_info"
}

// TestFleetAPIPostAPIOperation exercises postAPIOperation's real HTTP calls:
// backup_reserve_percent goes to ".../backup" as {"backup_reserve_percent": <int>},
// real_mode goes to ".../operation" as {"default_real_mode": "<mode>"}, and
// both are sent independently when both fields are present in the payload -
// this is the fix for the parity gap where cloud/FleetAPI writes previously
// never reached Tesla at all (docs/parity-matrix.md §4).
func TestFleetAPIPostAPIOperation(t *testing.T) {
	t.Parallel()

	backupPath, operationPath, _ := fleetAPIOperationPaths()

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
			t.Parallel()

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
			srv := newTestServer(t, handler)
			f := newAuthenticatedBackend(t, srv)

			res, err := f.Post(t.Context(), "/api/operation", tc.payload, "", false, false)

			if tc.wantErrIs != nil {
				require.ErrorIs(t, err, tc.wantErrIs)
			} else {
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"status": "success"}, res)
			}

			if tc.wantBackupBody != nil {
				require.NotNil(t, backupReq, "expected a request to %s", backupPath)
				assert.Equal(t, http.MethodPost, backupReq.method)
				assert.Equal(t, "Bearer tok-abc", backupReq.auth)
				assert.Equal(t, tc.wantBackupBody, backupReq.body)
			}

			if tc.wantOpBody != nil {
				require.NotNil(t, opReq, "expected a request to %s", operationPath)
				assert.Equal(t, http.MethodPost, opReq.method)
				assert.Equal(t, "Bearer tok-abc", opReq.auth)
				assert.Equal(t, tc.wantOpBody, opReq.body)
			}
		})
	}
}

// TestFleetAPIPostAPIOperationInvalidatesCache confirms a successful write
// clears the cached SITE_CONFIG entry (which backs "/api/operation" reads
// and GetGridCharging/GetGridExport), so a subsequent read is not served
// stale data.
func TestFleetAPIPostAPIOperationInvalidatesCache(t *testing.T) {
	t.Parallel()

	backupPath, operationPath, siteInfoPath := fleetAPIOperationPaths()

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
	srv := newTestServer(t, handler)
	f := newAuthenticatedBackend(t, srv)

	_, err := f.Poll(t.Context(), "/api/operation", false, false, false)
	require.NoError(t, err)
	_, err = f.Poll(t.Context(), "/api/operation", false, false, false)
	require.NoError(t, err)
	require.Equal(t, 1, siteInfoCalls, "second read should be served from cache")

	_, err = f.Post(
		t.Context(),
		"/api/operation",
		map[string]any{"real_mode": "backup"},
		"",
		false,
		false,
	)
	require.NoError(t, err)

	_, err = f.Poll(t.Context(), "/api/operation", false, false, false)
	require.NoError(t, err)
	assert.Equal(
		t,
		2,
		siteInfoCalls,
		"the write should have invalidated the cached SITE_CONFIG entry",
	)
}

func TestFleetAPIVitals(t *testing.T) {
	t.Parallel()

	f := fleetapi.New(testEmail, testCacheTTL, testTimeout, testSiteID, t.TempDir())

	vitals, err := f.Vitals(t.Context())
	require.NoError(t, err)
	assert.Empty(t, vitals)
}

// TestFleetAPIGetTimeRemaining is the regression test for the confirmed
// hardcoded-0.0 bug (docs/parity-matrix.md §4): GetTimeRemaining used to
// return a hardcoded 0.0 with no network call whatsoever. It now queries
// Tesla's "api/1/energy_sites/{site_id}/backup_time_remaining" endpoint and
// returns the live response.time_remaining_hours value, falling back to
// 0.0 only when a well-formed response lacks that key - matching upstream's
// get_time_remaining (pypowerwall_fleetapi.py:372-380).
func TestFleetAPIGetTimeRemaining(t *testing.T) {
	t.Parallel()

	type testCase struct {
		wantErrIs  error
		name       string
		body       string
		wantHours  float64
		httpStatus int
	}

	cases := []testCase{
		{
			name:      "returns the live value from Tesla",
			body:      `{"response":{"time_remaining_hours":9.863332186566478}}`,
			wantHours: 9.863332186566478,
		},
		{
			name:      "falls back to 0.0 when time_remaining_hours is absent",
			body:      `{"response":{}}`,
			wantHours: 0.0,
		},
		{
			name:       "returns an error when the site is unreachable",
			httpStatus: http.StatusInternalServerError,
			wantErrIs:  backend.ErrUnexpectedStatus,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(
					t,
					"/api/1/energy_sites/"+testSiteID+"/backup_time_remaining",
					r.URL.Path,
				)
				if tc.httpStatus != 0 {
					w.WriteHeader(tc.httpStatus)

					return
				}
				_, _ = w.Write([]byte(tc.body))
			}
			srv := newTestServer(t, handler)
			f := newAuthenticatedBackend(t, srv)

			remaining, err := f.GetTimeRemaining(t.Context())

			if tc.wantErrIs != nil {
				require.ErrorIs(t, err, tc.wantErrIs)

				return
			}
			require.NoError(t, err)
			require.NotNil(t, remaining)
			assert.InDelta(t, tc.wantHours, *remaining, 0.0000001)
		})
	}
}

// gridImportExportPath returns the URL path shared by SetGridCharging and
// SetGridExport - both POST to the same Tesla endpoint with different body
// fields, per docs/parity-matrix.md §4.
func gridImportExportPath() string {
	return "/api/1/energy_sites/" + testSiteID + "/grid_import_export"
}

// TestFleetAPISetGridCharging exercises SetGridCharging's real HTTP call:
// POST .../grid_import_export with
// {"disallow_charge_from_grid_with_solar_installed": <bool>}. This is the
// regression test for the confirmed field-negation bug
// (docs/parity-matrix.md §1 item 30): the field Tesla actually reads is a
// *prohibition* ("disallow..."), not the enable flag the caller-facing
// "mode" name implies, so upstream negates mode before writing it
// (fleetapi.py:733-754) and gopowerwall must too. Before the fix,
// SetGridCharging(ctx, true) - "please enable grid charging" - sent
// disallow_charge_from_grid_with_solar_installed=true, which *disables* it
// on real hardware: the exact opposite of the caller's request. Each case
// below asserts the exact JSON body for both mode=true and mode=false so
// that inversion can never regress silently again.
func TestFleetAPISetGridCharging(t *testing.T) {
	t.Parallel()

	gridPath := gridImportExportPath()

	type testCase struct {
		wantErrIs    error
		name         string
		mode         bool
		wantDisallow bool
		status       int
	}

	cases := []testCase{
		{
			name:         "mode=true (enable) sends disallow_charge_from_grid_with_solar_installed=false",
			mode:         true,
			wantDisallow: false,
		},
		{
			name:         "mode=false (disable) sends disallow_charge_from_grid_with_solar_installed=true",
			mode:         false,
			wantDisallow: true,
		},
		{
			name:         "401 maps to ErrLogin",
			mode:         true,
			wantDisallow: false,
			status:       http.StatusUnauthorized,
			wantErrIs:    backend.ErrLogin,
		},
		{
			name:         "404 maps to ErrNotFound",
			mode:         true,
			wantDisallow: false,
			status:       http.StatusNotFound,
			wantErrIs:    backend.ErrNotFound,
		},
		{
			name: "429 maps to ErrRateLimited", mode: true, wantDisallow: false,
			status: http.StatusTooManyRequests, wantErrIs: backend.ErrRateLimited,
		},
		{
			name: "500 maps to ErrUnexpectedStatus", mode: true, wantDisallow: false,
			status: http.StatusInternalServerError, wantErrIs: backend.ErrUnexpectedStatus,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

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
			srv := newTestServer(t, handler)
			f := newAuthenticatedBackend(t, srv)

			res, err := f.SetGridCharging(t.Context(), tc.mode)

			if tc.wantErrIs != nil {
				require.ErrorIs(t, err, tc.wantErrIs)
			} else {
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"status": "success"}, res)
			}

			require.NotNil(t, req)
			assert.Equal(t, http.MethodPost, req.method)
			assert.Equal(t, "Bearer tok-abc", req.auth)
			assert.Equal(
				t,
				map[string]any{"disallow_charge_from_grid_with_solar_installed": tc.wantDisallow},
				req.body,
			)
		})
	}
}

// TestFleetAPISetGridChargingRoundTrip is the companion round-trip
// regression to TestFleetAPISetGridCharging's exact-body assertion: after
// SetGridCharging(true) ("enable"), a subsequent GetGridCharging read
// against a stateful mock of Tesla's site_info must report true, and
// likewise false after SetGridCharging(false) - proving the negation on
// the write side and the negation on the read side agree with each other,
// not just with the wire format in isolation.
//
//nolint:paralleltest,tparallel // steps mutate shared mock server state sequentially; must run in order
func TestFleetAPISetGridChargingRoundTrip(t *testing.T) {
	t.Parallel()

	type step struct {
		name        string
		mode        bool
		wantEnabled bool
	}

	steps := []step{
		{name: "enable", mode: true, wantEnabled: true},
		{name: "disable", mode: false, wantEnabled: false},
	}

	gridPath := gridImportExportPath()
	_, _, siteInfoPath := fleetAPIOperationPaths()

	var disallow bool // Tesla's stored state; starts "enabled" (disallow=false)
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case siteInfoPath:
			state := "false"
			if disallow {
				state = "true"
			}
			_, _ = w.Write([]byte(
				`{"response":{"components":{"disallow_charge_from_grid_with_solar_installed":` +
					state + `}}}`,
			))
		case gridPath:
			rec := recordRequest(t, r)
			disallow, _ = rec.body["disallow_charge_from_grid_with_solar_installed"].(bool)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
	srv := newTestServer(t, handler)
	f := newAuthenticatedBackend(t, srv)

	// Steps apply in order against the shared stateful mock above, so they
	// are not run with t.Parallel().
	for _, tc := range steps {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.SetGridCharging(t.Context(), tc.mode)
			require.NoError(t, err)
			got, err := f.GetGridCharging(t.Context())
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tc.wantEnabled, *got)
		})
	}
}

// TestFleetAPISetGridExport exercises SetGridExport's real HTTP call: POST
// .../grid_import_export with {"customer_preferred_export_rule": "<mode>"}.
func TestFleetAPISetGridExport(t *testing.T) {
	t.Parallel()

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
			t.Parallel()

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
			srv := newTestServer(t, handler)
			f := newAuthenticatedBackend(t, srv)

			res, err := f.SetGridExport(t.Context(), tc.mode)

			if tc.wantErrIs != nil {
				require.ErrorIs(t, err, tc.wantErrIs)
			} else {
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"status": "success"}, res)
			}

			require.NotNil(t, req)
			assert.Equal(t, http.MethodPost, req.method)
			assert.Equal(t, "Bearer tok-abc", req.auth)
			assert.Equal(t, map[string]any{"customer_preferred_export_rule": tc.mode}, req.body)
		})
	}
}

// TestFleetAPIGridImportExportInvalidatesCache confirms both SetGridCharging
// and SetGridExport clear the cached SITE_CONFIG entry that GetGridCharging
// and GetGridExport read through, so a re-read after a write is not served
// stale data.
func TestFleetAPIGridImportExportInvalidatesCache(t *testing.T) {
	t.Parallel()

	gridPath := gridImportExportPath()
	_, _, siteInfoPath := fleetAPIOperationPaths()

	var siteInfoCalls int
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case siteInfoPath:
			siteInfoCalls++
			_, _ = w.Write([]byte(
				`{"response":{"components":{"disallow_charge_from_grid_with_solar_installed":true}}}`,
			))
		case gridPath:
			_ = recordRequest(t, r)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
	srv := newTestServer(t, handler)
	f := newAuthenticatedBackend(t, srv)

	got, err := f.GetGridCharging(t.Context())
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.False(t, *got, "disallow=true means grid charging is disabled")
	_, err = f.GetGridCharging(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, siteInfoCalls, "second read should be served from cache")

	_, err = f.SetGridCharging(t.Context(), true)
	require.NoError(t, err)

	_, err = f.GetGridCharging(t.Context())
	require.NoError(t, err)
	assert.Equal(
		t,
		2,
		siteInfoCalls,
		"SetGridCharging should have invalidated the cached SITE_CONFIG entry",
	)
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
			_, _ = w.Write(
				[]byte(`{"response":{"default_real_mode":"backup","backup_reserve_percent":55}}`),
			)
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
		_, _ = w.Write(
			[]byte(`{"response":{"site_name":"FleetHouse","installation_time_zone":"UTC"}}`),
		)
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
	assert.Equal(
		t,
		map[string]any{"site_name": "Powerwall", "timezone": "America/Los_Angeles"},
		res,
	)
}

func TestFleetAPIStatus(t *testing.T) {
	t.Parallel()

	t.Run("reads din and version from config", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(
				[]byte(
					`{"response":{"id":"fleet-din","version":"2.0.0","installation_date":"2024-02-02"}}`,
				),
			)
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

// TestFleetAPISystemStatusOverlay is the regression test for the confirmed
// missing live-data overlay (docs/parity-matrix.md §3.2): getAPISystemStatus
// used to return stubs.SystemStatusStub() completely unmodified. It now
// overlays nine live values, matching upstream's get_api_system_status
// (pypowerwall_fleetapi.py:597-629).
func TestFleetAPISystemStatusOverlay(t *testing.T) {
	t.Parallel()

	liveStatusPath := "/api/1/energy_sites/" + testSiteID + "/live_status"
	siteInfoPath := "/api/1/energy_sites/" + testSiteID + "/site_info"
	siteStatusPath := "/api/1/energy_sites/" + testSiteID + "/site_status"

	type testCase struct {
		wantErrIs  error
		check      func(t *testing.T, m map[string]any)
		name       string
		liveStatus string
		siteInfo   string
		siteStatus string
	}

	cases := []testCase{
		{
			name: "overlays live data onto the stub",
			liveStatus: `{"response":{"solar_power":1500,"grid_services_power":25,` +
				`"island_status":"on_grid","grid_status":"Active"}}`,
			siteInfo:   `{"response":{"battery_count":2,"nameplate_power":10800}}`,
			siteStatus: `{"response":{"total_pack_energy":25939,"energy_left":21276.5}}`,
			check: func(t *testing.T, m map[string]any) {
				t.Helper()
				assert.InDelta(t, 25939.0, m["nominal_full_pack_energy"], 0.001)
				assert.InDelta(t, 21276.5, m["nominal_energy_remaining"], 0.001)
				assert.InDelta(t, 10800.0, m["max_charge_power"], 0.001)
				assert.InDelta(t, 10800.0, m["max_discharge_power"], 0.001)
				assert.InDelta(t, 10800.0, m["max_apparent_power"], 0.001)
				assert.InDelta(t, 25.0, m["grid_services_power"], 0.001)
				assert.Equal(t, "SystemGridConnected", m["system_island_state"])
				assert.InDelta(t, 2.0, m["available_blocks"], 0.001)
				assert.InDelta(t, 2.0, m["blocks_controlled"], 0.001)
				assert.InDelta(t, 1500.0, m["solar_real_power_limit"], 0.001)
			},
		},
		{
			name:       "reports SystemIslandedActive off-grid",
			liveStatus: `{"response":{"island_status":"off_grid","grid_status":"Down"}}`,
			siteInfo:   `{"response":{"battery_count":1,"nameplate_power":5000}}`,
			siteStatus: `{"response":{"total_pack_energy":13000,"energy_left":6000}}`,
			check: func(t *testing.T, m map[string]any) {
				t.Helper()
				assert.Equal(t, "SystemIslandedActive", m["system_island_state"])
			},
		},
		{
			name:      "returns ErrNotFound when live_status/site_info are unreachable",
			wantErrIs: backend.ErrNotFound,
		},
		// This asymmetry - gating on power/config but not on the battery
		// (site_status) call - is confirmed in upstream itself
		// (pypowerwall_fleetapi.py:597-629 only checks "power is None or
		// config is None", unlike the cloud backend's three-way check), so
		// gopowerwall's FleetAPI overlay must reproduce it rather than
		// "fixing" it to match the cloud backend. An empty siteStatus
		// below means "site_status returns 500" (see the handler), while
		// liveStatus/siteInfo are populated so the overlay still runs.
		{
			name:       "still overlays when only site_status is unreachable",
			liveStatus: `{"response":{"solar_power":42,"island_status":"on_grid"}}`,
			siteInfo:   `{"response":{"battery_count":3,"nameplate_power":9000}}`,
			check: func(t *testing.T, m map[string]any) {
				t.Helper()
				assert.Nil(t, m["nominal_full_pack_energy"])
				assert.Nil(t, m["nominal_energy_remaining"])
				assert.InDelta(t, 9000.0, m["max_charge_power"], 0.001)
				assert.InDelta(t, 3.0, m["available_blocks"], 0.001)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case liveStatusPath:
					if tc.liveStatus == "" {
						w.WriteHeader(http.StatusInternalServerError)

						return
					}
					_, _ = w.Write([]byte(tc.liveStatus))
				case siteInfoPath:
					if tc.siteInfo == "" {
						w.WriteHeader(http.StatusInternalServerError)

						return
					}
					_, _ = w.Write([]byte(tc.siteInfo))
				case siteStatusPath:
					if tc.siteStatus == "" {
						w.WriteHeader(http.StatusInternalServerError)

						return
					}
					_, _ = w.Write([]byte(tc.siteStatus))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}
			srv := newTestServer(t, handler)
			f := newAuthenticatedBackend(t, srv)

			res, err := f.Poll(t.Context(), "/api/system_status", false, false, false)

			if tc.wantErrIs != nil {
				require.ErrorIs(t, err, tc.wantErrIs)
				assert.Nil(t, res)

				return
			}
			require.NoError(t, err)
			m, ok := res.(map[string]any)
			require.True(t, ok)
			tc.check(t, m)
		})
	}
}

func TestFleetAPIGridChargingAndExport(t *testing.T) {
	t.Parallel()

	// getGridChargingCases is the regression coverage for the confirmed
	// field-read bug (docs/parity-matrix.md §4): the previous
	// implementation read a nonexistent top-level "response.grid_charging"
	// field and so always returned ErrNotFound; the fix reads
	// "response.components.disallow_charge_from_grid_with_solar_installed"
	// and negates it, defaulting to "enabled" when the field is absent -
	// matching fleetapi.py:694-698's "return not state".
	type getGridChargingCase struct {
		name        string
		siteInfo    string
		wantEnabled bool
	}

	getGridChargingCases := []getGridChargingCase{
		{
			name:        "disallow=false means grid charging is enabled",
			siteInfo:    `{"response":{"components":{"disallow_charge_from_grid_with_solar_installed":false}}}`,
			wantEnabled: true,
		},
		{
			name:        "disallow=true means grid charging is disabled",
			siteInfo:    `{"response":{"components":{"disallow_charge_from_grid_with_solar_installed":true}}}`,
			wantEnabled: false,
		},
		{
			name:        "absent field defaults to enabled",
			siteInfo:    `{"response":{"components":{}}}`,
			wantEnabled: true,
		},
	}

	for _, tc := range getGridChargingCases {
		t.Run("GetGridCharging/"+tc.name, func(t *testing.T) {
			t.Parallel()

			handler := func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.siteInfo))
			}
			srv := newTestServer(t, handler)
			f := newAuthenticatedBackend(t, srv)

			got, err := f.GetGridCharging(t.Context())
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tc.wantEnabled, *got)
		})
	}

	t.Run("GetGridCharging returns an error when unreachable", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}
		srv := newTestServer(t, handler)
		f := newAuthenticatedBackend(t, srv)

		_, err := f.GetGridCharging(t.Context())
		require.Error(t, err)
	})

	// getGridExportCases is the regression coverage for the confirmed
	// field-read bug found while verifying SetGridExport's neighbor
	// against upstream: the previous implementation read a nonexistent
	// top-level "response.customer_preferred_export_rule" field (the real
	// field lives under "components", like grid charging's disallow flag)
	// and had neither the non_export_configured special case nor the
	// "battery_ok" default - matching fleetapi.py:700-707.
	type getGridExportCase struct {
		name       string
		siteInfo   string
		wantExport string
	}

	getGridExportCases := []getGridExportCase{
		{
			name:       "configured value is read from components",
			siteInfo:   `{"response":{"components":{"customer_preferred_export_rule":"pv_only"}}}`,
			wantExport: "pv_only",
		},
		{
			name:       "non_export_configured overrides to never",
			siteInfo:   `{"response":{"components":{"non_export_configured":true,"customer_preferred_export_rule":"pv_only"}}}`,
			wantExport: "never",
		},
		{
			name:       "absent field defaults to battery_ok",
			siteInfo:   `{"response":{"components":{}}}`,
			wantExport: "battery_ok",
		},
	}

	for _, tc := range getGridExportCases {
		t.Run("GetGridExport/"+tc.name, func(t *testing.T) {
			t.Parallel()

			handler := func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.siteInfo))
			}
			srv := newTestServer(t, handler)
			f := newAuthenticatedBackend(t, srv)

			got, err := f.GetGridExport(t.Context())
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tc.wantExport, *got)
		})
	}

	t.Run("GetGridExport returns an error when unreachable", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}
		srv := newTestServer(t, handler)
		f := newAuthenticatedBackend(t, srv)

		_, err := f.GetGridExport(t.Context())
		require.Error(t, err)
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
		_, _ = w.Write(
			[]byte(
				`{"response":{"solar_power":5,"battery_power":6,"load_power":7,"grid_power":8}}`,
			),
		)
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
	t.Run(
		"refreshes an empty access token exactly once and persists it with 0600",
		func(t *testing.T) {
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
		},
	)
}
