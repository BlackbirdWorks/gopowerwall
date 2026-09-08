//go:build integration

package integration_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall"
)

// newLocalPowerwall connects a Powerwall to sim in local mode using the
// credentials pwsimulator's own test.sh authenticates with.
func newLocalPowerwall(t *testing.T, sim simulator) *gopowerwall.Powerwall {
	t.Helper()

	pw, err := gopowerwall.New(
		t.Context(),
		gopowerwall.WithHost(sim.HostPort),
		gopowerwall.WithPassword(simulatorPassword),
		gopowerwall.WithEmail(simulatorEmail),
		gopowerwall.WithTimezone(simulatorTimezone),
		gopowerwall.WithCloudMode(false),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pw.Close(t.Context()) })
	require.True(t, pw.IsConnected(), "expected local mode to authenticate against the simulator")
	require.True(t, pw.IsLocal())

	return pw
}

// TestLocalModeReadSurface exercises gopowerwall's local-mode read surface
// end to end against a real pwsimulator container, asserting on the exact
// static values stub.py serves (https://github.com/jasonacox/pypowerwall/
// blob/main/pwsimulator/stub.py) rather than just checking for "no error".
//
// Subtests only read - none call the simulator's mutating /test/* control
// endpoints - so they are safe to run in parallel against one shared
// container and Powerwall client.
func TestLocalModeReadSurface(t *testing.T) {
	t.Parallel()

	sim := startSimulator(t)
	pw := newLocalPowerwall(t, sim)

	type testCase struct {
		run  func(t *testing.T, pw *gopowerwall.Powerwall)
		name string
	}

	cases := []testCase{
		{
			name: "Poll /api/status returns the gateway identity",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				data := pw.Poll(t.Context(), "/api/status")
				require.NotNil(t, data)
				assert.Equal(t, "1232100-00-E--TG123456789ABC", gopowerwall.Lookup(data, "din"))
				assert.Equal(t, "23.44.0 9064fc6a", gopowerwall.Lookup(data, "version"))
			},
		},
		{
			name: "Power reports the simulator's static aggregate meters",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				p := pw.Power(t.Context())
				// stub.py's initial state: agg_solar=6500, agg_home=900,
				// agg_grid=-2100, agg_powerwall=-3500 (battery charging).
				assert.InDelta(t, -2100, p.Site, 0.01)
				assert.InDelta(t, -2100, p.Grid, 0.01)
				assert.InDelta(t, 6500, p.Solar, 0.01)
				assert.InDelta(t, -3500, p.Battery, 0.01)
				assert.InDelta(t, 900, p.Load, 0.01)
				assert.InDelta(t, 900, p.Home, 0.01)
			},
		},
		{
			name: "Level reports the simulator's static SOE",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				lvl := pw.Level(t.Context(), false)
				require.NotNil(t, lvl)
				// stub.py: percentage = 23.975388097174584, serialized with
				// Python's "%f" (6 decimal places) as the wire value.
				assert.InDelta(t, 23.975388, *lvl, 1e-6)
			},
		},
		{
			name: "SOE returns a strongly-typed percentage",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				soe, err := pw.SOE(t.Context())
				require.NoError(t, err)
				assert.InDelta(t, 23.975388, soe.Percentage, 1e-6)
			},
		},
		{
			name: "GridStatus reports connected in every output form",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Equal(t, "Connected", pw.GridStatus(t.Context(), gopowerwall.GridStatusString))
				assert.Equal(t, 1, pw.GridStatus(t.Context(), gopowerwall.GridStatusNumeric))

				resp, err := pw.GridStatusResponse(t.Context())
				require.NoError(t, err)
				assert.Equal(t, "SystemGridConnected", resp.GridStatus)
			},
		},
		{
			// The simulator does not implement /api/operation at all (it is
			// absent from stub.py's static `api` map), so an authenticated
			// GET falls through to the "unknown API" branch, which returns
			// 200 with an empty body. That is indistinguishable from "not
			// found" to PollRaw, so Operation surfaces ErrNotFound - this
			// documents that gap rather than a gopowerwall defect.
			name: "Operation is unsupported by the simulator",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				_, err := pw.Operation(t.Context())
				assert.ErrorIs(t, err, gopowerwall.ErrNotFound)
			},
		},
		{
			// Regression test: the simulator's real, recorded /api/site_info
			// response - reproduced byte-for-byte by
			// proxy/web/bogus/api.site_info.json in this repo - nests
			// grid_code as an object:
			//   "grid_code":{"grid_code":"...","grid_voltage_setting":240,...}
			// models.SiteInfo.GridCode used to be typed as a plain `string`,
			// so json.Unmarshal failed on every real gateway's /api/site_info
			// response and Powerwall.SiteInfo always returned an error
			// rather than populated site info. GridCode is now
			// models.GridCodeInfo, mirroring the nested object, so the call
			// succeeds and the nested fields decode correctly.
			name: "SiteInfo decodes the simulator's real grid_code object",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				info, err := pw.SiteInfo(t.Context())
				require.NoError(t, err)
				assert.Equal(t, "Tesla Energy Gateway", info.SiteName)
				assert.Equal(t, "60Hz_240V_s_UL1741SA:2019_California", info.GridCode.GridCode)
				assert.InDelta(t, 240.0, info.GridCode.GridVoltageSetting, 0.001)
				assert.InDelta(t, 60.0, info.GridCode.GridFreqSetting, 0.001)
				assert.Equal(t, "Split", info.GridCode.GridPhaseSetting)
				assert.Equal(t, "United States", info.GridCode.Country)
				assert.Equal(t, "California", info.GridCode.State)
				assert.Equal(t, "Southern California Edison", info.GridCode.Utility)
			},
		},
		{
			name: "SiteName reads the un-nested site name endpoint",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				name := pw.SiteName(t.Context())
				require.NotNil(t, name)
				assert.Equal(t, "Tesla Energy Gateway", *name)
			},
		},
		{
			name: "Status/Version/Uptime/Din read from the same gateway status payload",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Equal(t, "23.44.0 9064fc6a", pw.Version(t.Context()))
				uptime := pw.Uptime(t.Context())
				require.NotNil(t, uptime)
				assert.Equal(t, "127h34m16.275122187s", *uptime)
				din := pw.Din(t.Context())
				require.NotNil(t, din)
				assert.Equal(t, "1232100-00-E--TG123456789ABC", *din)
			},
		},
		{
			name: "Vitals decodes the simulator's real protobuf payload",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				vitals, err := pw.Vitals(t.Context())
				require.NoError(t, err)
				require.NotEmpty(t, vitals.Devices, "expected at least one decoded vitals device")
				// stub.py's sample protobuf includes two THC (thermal
				// controller) devices, one per simulated battery pack.
				assert.Contains(t, vitals.Devices, "TETHC--2012170-25-E--T0000000000000")
				assert.Contains(t, vitals.Devices, "TETHC--3012170-05-B--T0000000000000")
			},
		},
		{
			name: "Temps extracts ambient temperature from TETHC vitals devices",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				temps := pw.Temps(t.Context())
				assert.Len(t, temps.Temps, 2, "one ambient temperature per simulated battery pack")
			},
		},
		{
			name: "Alerts includes the grid-connected status alert",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				alerts := pw.Alerts(t.Context())
				// The simulator's default grid_status ("SystemGridConnected")
				// is normalized to pypowerwall's "SystemConnectedToGrid"
				// alert name; see Powerwall.Alerts.
				assert.Contains(t, alerts.Alerts, "SystemConnectedToGrid")
			},
		},
		{
			// Like Operation, BatteryBlocks depends on the full
			// /api/system_status endpoint, which the simulator does not
			// implement - it only implements specific system_status/*
			// leaves (soe, grid_status, grid_faults). BatteryBlocks
			// degrades to an empty map rather than erroring.
			name: "BatteryBlocks is empty because /api/system_status is unimplemented",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				blocks := pw.BatteryBlocks(t.Context())
				assert.Empty(t, blocks)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, pw)
		})
	}
}
