//go:build integration

package integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/proxy"
)

// newProxyServer wires a proxy.Server to a local-mode Powerwall connected to
// sim, exposed over plain HTTP via httptest so route assertions do not also
// need to reason about the simulator's TLS.
func newProxyServer(t *testing.T, sim simulator) *httptest.Server {
	t.Helper()

	pw := newLocalPowerwall(t, sim)
	cfg := proxy.Config{
		HealthCheckEnabled:  true,
		GracefulDegradation: true,
		NegSolar:            true,
		CacheExpire:         1,
		CacheTTL:            1,
	}
	srv := proxy.NewServer(t.Context(), cfg, pw)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func getBody(t *testing.T, ts *httptest.Server, path string) (int, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+path, nil)
	require.NoError(t, err, "build request for %s", path)

	resp, err := ts.Client().Do(req)
	require.NoError(t, err, "GET %s", path)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read body for %s", path)

	return resp.StatusCode, body
}

// csvFields splits a single data row of the proxy's CSV output into its
// comma-separated fields, skipping a blank trailing line from the final \n.
func csvFields(t *testing.T, body []byte) []string {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	require.NotEmpty(t, lines)

	return strings.Split(lines[len(lines)-1], ",")
}

func csvFloat(t *testing.T, fields []string, idx int) float64 {
	t.Helper()

	v, err := strconv.ParseFloat(strings.TrimSpace(fields[idx]), 64)
	require.NoError(t, err, "parse CSV field %d (%q)", idx, fields[idx])

	return v
}

// TestProxyHTTPSurface asserts on the proxy's route paths, JSON field names,
// and value shapes against a real pwsimulator container - the parity
// contract described in .agent/rules/powerwall.md is about exactly these
// things, not merely "200 OK".
//
// Subtests only issue GETs against the simulator's static default state; the
// server and its underlying Powerwall are shared and requests are safe to
// run in parallel.
func TestProxyHTTPSurface(t *testing.T) {
	t.Parallel()

	sim := startSimulator(t)
	ts := newProxyServer(t, sim)

	type testCase struct {
		check func(t *testing.T, body []byte)
		name  string
		path  string
	}

	cases := []testCase{
		{
			name: "/aggregates mirrors the gateway meters JSON shape",
			path: "/aggregates",
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var agg map[string]map[string]any
				require.NoError(t, json.Unmarshal(body, &agg))
				require.Contains(t, agg, "site")
				require.Contains(t, agg, "solar")
				require.Contains(t, agg, "battery")
				require.Contains(t, agg, "load")
				assert.InDelta(t, -2100.0, agg["site"]["instant_power"], 0.01)
				assert.InDelta(t, 6500.0, agg["solar"]["instant_power"], 0.01)
				assert.InDelta(t, -3500.0, agg["battery"]["instant_power"], 0.01)
				assert.InDelta(t, 900.0, agg["load"]["instant_power"], 0.01)
			},
		},
		{
			name: "/soe passes through the raw, unscaled percentage",
			path: "/soe",
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var soe struct {
					Percentage float64 `json:"percentage"`
				}
				require.NoError(t, json.Unmarshal(body, &soe))
				assert.InDelta(t, 23.975388, soe.Percentage, 1e-6)
			},
		},
		{
			// Unlike /soe, this route scales the percentage via
			// Level(ctx, true), reversing the reserved-capacity offset the
			// simulator's battery-percentage test endpoint pre-applies
			// (see scenario_test.go). At the simulator's default,
			// unmodified percentage, the two need not match numerically -
			// this route documents that /soe and /api/system_status/soe
			// are intentionally different (raw vs scaled).
			name: "/api/system_status/soe reports the scaled percentage",
			path: "/api/system_status/soe",
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var soe struct {
					Percentage float64 `json:"percentage"`
				}
				require.NoError(t, json.Unmarshal(body, &soe))
				// scaled = (raw - 5) / 0.95 = (23.975388 - 5) / 0.95 = 19.974092...
				assert.InDelta(t, 19.974093, soe.Percentage, 1e-4)
			},
		},
		{
			name: "/api/system_status/grid_status passes through the raw gateway value",
			path: "/api/system_status/grid_status",
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var gs struct {
					GridStatus string `json:"grid_status"`
				}
				require.NoError(t, json.Unmarshal(body, &gs))
				assert.Equal(t, "SystemGridConnected", gs.GridStatus)
			},
		},
		{
			name: "/vitals decodes the simulator's protobuf vitals into named devices",
			path: "/vitals",
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var vitals struct {
					Devices map[string]map[string]any `json:"devices"`
				}
				require.NoError(t, json.Unmarshal(body, &vitals))
				assert.Contains(t, vitals.Devices, "TETHC--2012170-25-E--T0000000000000")
			},
		},
		{
			name: "/strings returns the solar-strings shape",
			path: "/strings",
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var strs struct {
					Strings map[string]struct {
						Connected bool    `json:"connected"`
						Voltage   float64 `json:"voltage"`
					} `json:"strings"`
				}
				require.NoError(t, json.Unmarshal(body, &strs))
				// Strings() keys on "<PVAC device>_<A|B|C|D>"; the simulator's
				// vitals sample has one PVAC inverter, so 4 string entries.
				assert.Len(t, strs.Strings, 4)
			},
		},
		{
			name: "/temps returns one ambient temperature per battery pack",
			path: "/temps",
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var temps struct {
					Temps map[string]float64 `json:"temps"`
				}
				require.NoError(t, json.Unmarshal(body, &temps))
				assert.Len(t, temps.Temps, 2)
			},
		},
		{
			name: "/alerts includes the grid-connected alert",
			path: "/alerts",
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var alerts struct {
					Alerts []string `json:"alerts"`
				}
				require.NoError(t, json.Unmarshal(body, &alerts))
				assert.Contains(t, alerts.Alerts, "SystemConnectedToGrid")
			},
		},
		{
			name: "/json composes power, soe and grid_status into one payload",
			path: "/json",
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var out map[string]any
				require.NoError(t, json.Unmarshal(body, &out))
				assert.InDelta(t, -2100.0, out["grid"], 0.01)
				assert.InDelta(t, 900.0, out["home"], 0.01)
				assert.InDelta(t, 6500.0, out["solar"], 0.01)
				assert.InDelta(t, -3500.0, out["battery"], 0.01)
				assert.InDelta(t, 23.975388, out["soe"], 1e-6)
				// generateJSON builds grid_status via
				// gopowerwall.GridStatus(ctx, GridStatusNumeric), which
				// returns 1 when the gateway reports SystemGridConnected -
				// the simulator's default state, as exercised here.
				assert.InDelta(t, 1.0, out["grid_status"], 0.01)
			},
		},
		{
			name: "/freq's grid_status (also built via GridStatusNumeric) is correct",
			path: "/freq",
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var out map[string]any
				require.NoError(t, json.Unmarshal(body, &out))
				assert.InDelta(t, 1.0, out["grid_status"], 0.01)
			},
		},
		{
			// /api/system_status is unimplemented by the simulator, so
			// BatteryBlocks is empty and /pod's per-block fields never
			// materialize; the top-level aggregate fields default to zero.
			name: "/pod degrades to zero aggregates when system_status is unavailable",
			path: "/pod",
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var out map[string]any
				require.NoError(t, json.Unmarshal(body, &out))
				assert.InDelta(t, 0.0, out["nominal_full_pack_energy"], 0.01)
				assert.NotContains(t, out, "PW1_name")
			},
		},
		{
			name: "/health reports local mode and connection health",
			path: "/health",
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var out map[string]any
				require.NoError(t, json.Unmarshal(body, &out))
				assert.Equal(t, "local", out["mode"])
				assert.Contains(t, out, "connection_health")
			},
		},
		{
			name: "/stats reports local mode and the simulator's site name",
			path: "/stats",
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var out map[string]any
				require.NoError(t, json.Unmarshal(body, &out))
				assert.Equal(t, "local", out["mode"])
				assert.Equal(t, "Tesla Energy Gateway", out["site_name"])
				assert.Equal(t, false, out["tedapi"])
			},
		},
		{
			name: "/version reports the simulator's firmware string",
			path: "/version",
			check: func(t *testing.T, body []byte) {
				t.Helper()
				var out map[string]any
				require.NoError(t, json.Unmarshal(body, &out))
				assert.Equal(t, "23.44.0 9064fc6a", out["version"])
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, body := getBody(t, ts, tc.path)
			require.Equal(t, http.StatusOK, status, "GET %s body=%s", tc.path, body)
			tc.check(t, body)
		})
	}
}

// TestProxyCSVRoutes checks the proxy's CSV surface separately from the JSON
// routes above since it needs its own parsing and field-order assertions.
func TestProxyCSVRoutes(t *testing.T) {
	t.Parallel()

	sim := startSimulator(t)
	ts := newProxyServer(t, sim)

	t.Run("/csv reports Grid,Home,Solar,Battery,BatteryLevel", func(t *testing.T) {
		t.Parallel()
		status, body := getBody(t, ts, "/csv")
		require.Equal(t, http.StatusOK, status)
		fields := csvFields(t, body)
		require.Len(t, fields, 5)
		assert.InDelta(t, -2100.0, csvFloat(t, fields, 0), 0.02, "Grid")
		assert.InDelta(t, 900.0, csvFloat(t, fields, 1), 0.02, "Home")
		assert.InDelta(t, 6500.0, csvFloat(t, fields, 2), 0.02, "Solar")
		assert.InDelta(t, -3500.0, csvFloat(t, fields, 3), 0.02, "Battery")
		assert.InDelta(t, 23.975388, csvFloat(t, fields, 4), 0.02, "BatteryLevel")
	})

	t.Run("/csv/v2 appends GridStatus and Reserve columns", func(t *testing.T) {
		t.Parallel()
		status, body := getBody(t, ts, "/csv/v2")
		require.Equal(t, http.StatusOK, status)
		fields := csvFields(t, body)
		require.Len(t, fields, 7)
		assert.InDelta(t, -2100.0, csvFloat(t, fields, 0), 0.02, "Grid")
		// GridStatus is built via GridStatusNumeric, same as /json's
		// grid_status, so it reflects the simulator's default
		// SystemGridConnected state as 1.
		assert.InDelta(t, 1.0, csvFloat(t, fields, 5), 0.01, "GridStatus")
		// Reserve is 0 because the simulator does not implement
		// /api/operation, not because of a gopowerwall defect.
		assert.InDelta(t, 0.0, csvFloat(t, fields, 6), 0.01, "Reserve")
	})

	t.Run("/csv?headers includes the header row", func(t *testing.T) {
		t.Parallel()
		status, body := getBody(t, ts, "/csv?headers=1")
		require.Equal(t, http.StatusOK, status)
		assert.True(t, strings.HasPrefix(string(body), "Grid,Home,Solar,Battery,BatteryLevel"))
	})
}
