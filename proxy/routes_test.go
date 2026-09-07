package proxy_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGETRoutesAgainstConnectedGateway exercises the proxy's read-only GET
// surface end-to-end against a fake local gateway, asserting concrete
// response bodies and field names since these routes are a parity contract
// with pypowerwall.
func TestGETRoutesAgainstConnectedGateway(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		path       string
		wantSubs   []string
		wantStatus int
	}

	for _, tc := range []testCase{
		{
			name:       "aggregates",
			path:       "/aggregates",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"instant_power"`, `"site"`, `"solar"`},
		},
		{
			name:       "api aggregates alias",
			path:       "/api/meters/aggregates",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"instant_power"`},
		},
		{
			name:       "soe",
			path:       "/soe",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"percentage"`},
		},
		{
			name:       "api soe",
			path:       "/api/system_status/soe",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"percentage"`},
		},
		{
			name:       "grid status",
			path:       "/api/system_status/grid_status",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"grid_status":"SystemGridConnected"`},
		},
		{
			name:       "csv v1 no headers",
			path:       "/csv",
			wantStatus: http.StatusOK,
			wantSubs:   []string{","},
		},
		{
			name:       "csv v1 with headers",
			path:       "/csv?headers=true",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"Grid,Home,Solar,Battery,BatteryLevel\n"},
		},
		{
			name:       "csv v2 no headers",
			path:       "/csv/v2",
			wantStatus: http.StatusOK,
			wantSubs:   []string{","},
		},
		{
			name:       "csv v2 with headers",
			path:       "/csv/v2?headers=true",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"Grid,Home,Solar,Battery,BatteryLevel,GridStatus,Reserve\n"},
		},
		{
			name:       "json composite",
			path:       "/json",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"grid"`, `"home"`, `"solar"`, `"battery"`, `"strings"`},
		},
		{
			name:       "freq",
			path:       "/freq",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"PW1_PINV_Fout", "grid_status"},
		},
		{
			name:       "pod",
			path:       "/pod",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"nominal_full_pack_energy", "PW1_POD_nom_energy_remaining"},
		},
		{
			name:       "vitals",
			path:       "/vitals",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"TEPINV--1", "PINV_Fout"},
		},
		{
			name:       "strings",
			path:       "/strings",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"strings"`},
		},
		{
			name:       "temps",
			path:       "/temps",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"temps"`, "TETHC--1"},
		},
		{
			name:       "temps pw",
			path:       "/temps/pw",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"PW1_temp"},
		},
		{
			// Device-level alerts (e.g. "StringFault" on the PVAC fixture
			// device) are not reflected here: gopowerwall's Alerts() type
			// -asserts each device's "alerts" field as []any, but the local
			// backend stores it as []string, so only the grid-status-derived
			// alert ever survives. See the reported finding in the proxy
			// test suite handoff.
			name:       "alerts",
			path:       "/alerts",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"SystemConnectedToGrid"},
		},
		{
			name:       "alerts pw",
			path:       "/alerts/pw",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"SystemConnectedToGrid"},
		},
		{
			name:       "version",
			path:       "/version",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"version"`, `"vint"`},
		},
		{
			name:       "help",
			path:       "/help",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"Documentation & API Reference"},
		},
		{
			name:       "stats",
			path:       "/stats",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"pypowerwall", "uptime"},
		},
		{
			name:       "stats clear",
			path:       "/stats/clear",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"pypowerwall"},
		},
		{
			name:       "health",
			path:       "/health",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"proxy_stats", "connection_health"},
		},
		{
			name:       "health reset",
			path:       "/health/reset",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"reset_complete"},
		},
		{
			name:       "troubleshooting problems always empty",
			path:       "/api/troubleshooting/problems",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`{"problems": []}`},
		},
		{
			name:       "allowlisted gateway route",
			path:       "/api/powerwalls",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"PackagePartNumber"},
		},
		{
			name:       "tedapi routes disabled without tedapi mode",
			path:       "/tedapi/config",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"TEDAPI not enabled"},
		},
		{
			name:       "pw level",
			path:       "/pw/level",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"level"},
		},
		{
			name:       "pw power",
			path:       "/pw/power",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"site", "solar", "battery", "load"},
		},
		{
			name:       "pw site verbose",
			path:       "/pw/site",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"instant_power":27`},
		},
		{
			name:       "pw solar verbose",
			path:       "/pw/solar",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"instant_power":1840`},
		},
		{
			name:       "pw battery verbose",
			path:       "/pw/battery",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"instant_power":-990`},
		},
		{
			name:       "pw load verbose",
			path:       "/pw/load",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"instant_power":866.25`},
		},
		{
			name:       "pw grid aliases site",
			path:       "/pw/grid",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"instant_power":27`},
		},
		{
			name:       "pw home aliases load",
			path:       "/pw/home",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"instant_power":866.25`},
		},
		{
			name:       "pw aggregates raw",
			path:       "/pw/aggregates",
			wantStatus: http.StatusOK,
			wantSubs:   []string{`"site"`, `"solar"`, `"battery"`, `"load"`},
		},
		{
			name:       "pw vitals",
			path:       "/pw/vitals",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"TEPINV--1"},
		},
		{
			name:       "pw temps",
			path:       "/pw/temps",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"TETHC--1"},
		},
		{
			name:       "pw strings",
			path:       "/pw/strings",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"strings"},
		},
		{
			name:       "pw din",
			path:       "/pw/din",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"din"},
		},
		{
			name:       "pw uptime",
			path:       "/pw/uptime",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"uptime"},
		},
		{
			name:       "pw version",
			path:       "/pw/version",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"version"},
		},
		{
			name:       "pw status",
			path:       "/pw/status",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"din"},
		},
		{
			name:       "pw system_status",
			path:       "/pw/system_status",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"nominal_full_pack_energy"},
		},
		{
			name:       "pw grid_status",
			path:       "/pw/grid_status",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"Connected"},
		},
		{
			name:       "pw site_name",
			path:       "/pw/site_name",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"Tesla Energy Gateway"},
		},
		{
			name:       "pw alerts",
			path:       "/pw/alerts",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"alerts"},
		},
		{
			name:       "pw is_connected",
			path:       "/pw/is_connected",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"is_connected"},
		},
		{
			name:       "pw get_reserve",
			path:       "/pw/get_reserve",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"reserve"},
		},
		{
			name:       "pw get_mode",
			path:       "/pw/get_mode",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"mode"},
		},
		{
			name:       "pw get_time_remaining",
			path:       "/pw/get_time_remaining",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"time_remaining"},
		},
		{
			name:       "pw battery_blocks",
			path:       "/pw/battery_blocks",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"TG123456789012"},
		},
		{
			name:       "pw invalid subroute",
			path:       "/pw/does-not-exist",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"Invalid Request"},
		},
		{
			name:       "control get reserve via GET route",
			path:       "/control/reserve",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"reserve"},
		},
		{
			name:       "control get mode via GET route",
			path:       "/control/mode",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"mode"},
		},
		{
			name:       "control get grid_charging via GET route",
			path:       "/control/grid_charging",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"grid_charging"},
		},
		{
			name:       "control get grid_export via GET route",
			path:       "/control/grid_export",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"grid_export"},
		},
		{
			name:       "control get max_backup requires tedapi",
			path:       "/control/max_backup",
			wantStatus: http.StatusOK,
			wantSubs:   []string{"max_backup requires v1r LAN transport"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Each case gets its own gateway/Powerwall/server: the local
			// backend's response cache keys raw and parsed reads of the same
			// endpoint together, so sharing one Powerwall across many
			// concurrent, differently-shaped requests is order-dependent.
			gw := newFakeGateway(t, nil)
			pw := newLocalPowerwall(t, gw)
			ts := newProxyServer(t, baseTestConfig(), pw)

			resp, err := ts.Client().Get(ts.URL + tc.path)
			require.NoError(t, err)
			defer resp.Body.Close()

			body, readErr := io.ReadAll(resp.Body)
			require.NoError(t, readErr)

			assert.Equal(t, tc.wantStatus, resp.StatusCode, "body: %s", body)
			for _, sub := range tc.wantSubs {
				assert.Contains(t, string(body), sub)
			}
		})
	}
}

func TestVersionRouteWithoutFirmware(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, map[string]http.HandlerFunc{
		"/api/status": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"din":"test"}`))
		},
	})
	pw := newLocalPowerwall(t, gw)
	ts := newProxyServer(t, baseTestConfig(), pw)

	resp, err := ts.Client().Get(ts.URL + "/version")
	require.NoError(t, err)
	defer resp.Body.Close()

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "SolarOnly", body["version"])
	assert.InDelta(t, 0.0, body["vint"], 0)
}

func TestSiteZeroThresholdZeroesOutSmallSitePower(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, map[string]http.HandlerFunc{
		"/api/meters/aggregates": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"site":{"instant_power":3},"solar":{"instant_power":100},
				"battery":{"instant_power":0},"load":{"instant_power":103}}`))
		},
	})
	pw := newLocalPowerwall(t, gw)
	cfg := baseTestConfig()
	cfg.SiteZeroThreshold = 10
	ts := newProxyServer(t, cfg, pw)

	resp, err := ts.Client().Get(ts.URL + "/aggregates")
	require.NoError(t, err)
	defer resp.Body.Close()

	var agg map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&agg))
	site, ok := agg["site"].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 0.0, site["instant_power"], 0)
}

func TestNegSolarCorrectionMovesNegativeSolarIntoLoad(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, map[string]http.HandlerFunc{
		"/api/meters/aggregates": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"site":{"instant_power":100},"solar":{"instant_power":-50},
				"battery":{"instant_power":0},"load":{"instant_power":50}}`))
		},
	})
	pw := newLocalPowerwall(t, gw)
	cfg := baseTestConfig()
	cfg.NegSolar = false
	ts := newProxyServer(t, cfg, pw)

	resp, err := ts.Client().Get(ts.URL + "/aggregates")
	require.NoError(t, err)
	defer resp.Body.Close()

	var agg map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&agg))
	solar, ok := agg["solar"].(map[string]any)
	require.True(t, ok)
	load, ok := agg["load"].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 0.0, solar["instant_power"], 0)
	assert.InDelta(t, 100.0, load["instant_power"], 0.001)
}

func TestDisconnectedPowerwallDegradesGracefully(t *testing.T) {
	t.Parallel()

	pw := newDisconnectedPowerwall(t)
	cfg := baseTestConfig()
	ts := newProxyServer(t, cfg, pw)

	resp, err := ts.Client().Get(ts.URL + "/aggregates")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "null", string(body))
}

func TestGracefulDegradationServesLastKnownGoodValue(t *testing.T) {
	t.Parallel()

	failing := false
	gw := newFakeGateway(t, map[string]http.HandlerFunc{
		"/api/system_status/soe": func(w http.ResponseWriter, _ *http.Request) {
			if failing {
				w.WriteHeader(http.StatusServiceUnavailable)

				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(readFixture(t, "api.system_status.soe.json"))
		},
	})
	pw := newLocalPowerwall(t, gw)
	cfg := baseTestConfig()
	cfg.CacheExpire = 0 // disable perf cache so the second request re-polls
	ts := newProxyServer(t, cfg, pw)
	client := ts.Client()

	resp1, err := client.Get(ts.URL + "/soe")
	require.NoError(t, err)
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	require.Contains(t, string(body1), "percentage")

	failing = true
	resp2, err := client.Get(ts.URL + "/soe")
	require.NoError(t, err)
	defer resp2.Body.Close()

	body2, err := io.ReadAll(resp2.Body)
	require.NoError(t, err)
	assert.Equal(t, string(body1), string(body2), "expected degraded cache to serve last known good response")
}

func TestReverseProxyPassthroughToLocalGateway(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, map[string]http.HandlerFunc{
		"/grafana/dashboard": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("grafana dashboard content"))
		},
	})
	pw := newLocalPowerwall(t, gw)
	cfg := baseTestConfig()
	cfg.Host = strings.TrimPrefix(gw.URL, "https://")
	ts := newProxyServer(t, cfg, pw)

	resp, err := ts.Client().Get(ts.URL + "/grafana/dashboard")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "grafana dashboard content", string(body))
}

func TestUnknownPathWithoutGatewayReturns404(t *testing.T) {
	t.Parallel()

	pw := newDisconnectedPowerwall(t)
	ts := newProxyServer(t, baseTestConfig(), pw)

	resp, err := ts.Client().Get(ts.URL + "/totally-unknown-path")
	require.NoError(t, err)
	defer resp.Body.Close()

	// Not local (no gateway host reachable), not a static file: falls
	// through to plain 404.
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAllowlistRouteReturns502StyleTimeoutWhenGatewayFails(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, map[string]http.HandlerFunc{
		"/api/powerwalls": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		},
	})
	pw := newLocalPowerwall(t, gw)
	cfg := baseTestConfig()
	cfg.GracefulDegradation = false
	ts := newProxyServer(t, cfg, pw)

	resp, err := ts.Client().Get(ts.URL + "/api/powerwalls")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "null", string(body))
}

// TestJSONAndCSVv2GridStatusReflectsRealGridState is a regression test for a
// bug where /json and /csv/v2 hardcoded their grid_status field to 0
// regardless of the gateway's actual grid state: both handlers compared
// PW.GridStatus(ctx, gopowerwall.GridStatusString) against the literal "UP",
// but GridStatusString only ever returns "Connected", "Transition", or
// "Unknown" - so the comparison was always false. /freq (generateFreq) got
// this right next to the buggy code by using GridStatusNumeric directly;
// /json and /csv/v2 now do the same.
func TestJSONAndCSVv2GridStatusReflectsRealGridState(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name           string
		gridStatusJSON string
		wantGridStatus int
	}

	for _, tc := range []testCase{
		{
			name:           "grid connected reports 1",
			gridStatusJSON: `{"grid_status":"SystemGridConnected","grid_services_active":false}`,
			wantGridStatus: 1,
		},
		{
			name:           "grid in transition reports 0",
			gridStatusJSON: `{"grid_status":"SystemTransitionToGrid","grid_services_active":false}`,
			wantGridStatus: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gw := newFakeGateway(t, map[string]http.HandlerFunc{
				"/api/system_status/grid_status": func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(tc.gridStatusJSON))
				},
			})
			pw := newLocalPowerwall(t, gw)
			ts := newProxyServer(t, baseTestConfig(), pw)

			jsonResp, err := ts.Client().Get(ts.URL + "/json")
			require.NoError(t, err)
			defer jsonResp.Body.Close()

			var jsonBody map[string]any
			require.NoError(t, json.NewDecoder(jsonResp.Body).Decode(&jsonBody))
			assert.InDelta(t, float64(tc.wantGridStatus), jsonBody["grid_status"], 0, "/json grid_status")

			csvResp, err := ts.Client().Get(ts.URL + "/csv/v2")
			require.NoError(t, err)
			defer csvResp.Body.Close()

			csvBody, err := io.ReadAll(csvResp.Body)
			require.NoError(t, err)
			fields := strings.Split(strings.TrimSpace(string(csvBody)), ",")
			require.Len(t, fields, 7, "csv: %s", csvBody)
			assert.Equal(t, strconv.Itoa(tc.wantGridStatus), fields[5], "/csv/v2 GridStatus column")
		})
	}
}
