//go:build integration

package integration_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireGridStatus asserts the gateway's grid_status against a freshly
// connected Powerwall. A fresh client is used deliberately - see the
// serialization comment on TestScenarios: PollRaw's default 5s response
// cache means reusing one client across several state transitions in the
// same subtest could observe a stale, pre-transition value rather than the
// simulator's current state.
func requireGridStatus(t *testing.T, sim simulator, want string) {
	t.Helper()

	pw := newLocalPowerwall(t, sim)
	resp, err := pw.GridStatusResponse(t.Context())
	require.NoError(t, err)
	assert.Equal(t, want, resp.GridStatus)
}

// TestScenarios drives pwsimulator's /test/scenario/*, /test/toggle-grid and
// /test/battery-percentage/<N> control endpoints
// (https://github.com/jasonacox/pypowerwall/blob/main/pwsimulator/stub.py)
// to put the simulator into a known state, then asserts gopowerwall reports
// that state correctly end to end.
//
// Every subtest here mutates the simulator's shared, package-level Python
// state (agg_solar/home/grid/powerwall, percentage, the cached
// grid_status API response) through one control endpoint on one container.
// Running them in parallel would let one subtest's scenario change bleed
// into another's assertions, so subtests are deliberately NOT t.Parallel();
// they run serially against a container dedicated to this test function.
// The top-level test itself is still t.Parallel() - it does not share that
// container with any other top-level test.
//
//nolint:paralleltest,tparallel // subtests deliberately serialize: see the doc above.
func TestScenarios(t *testing.T) {
	t.Parallel()

	sim := startSimulator(t)

	t.Run("grid up and down via toggle-grid", func(t *testing.T) {
		// Starting state per stub.py's static default.
		requireGridStatus(t, sim, "SystemGridConnected")

		sim.triggerControl(t, "/test/toggle-grid")
		requireGridStatus(t, sim, "SystemIslandedActive")

		pwDown := newLocalPowerwall(t, sim)
		downStr, err := pwDown.GridStatusString(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "Transition", downStr)
		downNum, err := pwDown.GridStatusNumeric(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 0, downNum)

		// Toggle back so later subtests in this file start from "grid up".
		sim.triggerControl(t, "/test/toggle-grid")
		requireGridStatus(t, sim, "SystemGridConnected")

		pwUp := newLocalPowerwall(t, sim)
		upStr, err := pwUp.GridStatusString(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "Connected", upStr)
		upNum, err := pwUp.GridStatusNumeric(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 1, upNum)
	})

	t.Run("sunny-day-outage: grid down, solar and battery covering load", func(t *testing.T) {
		sim.triggerControl(t, "/test/scenario/sunny-day-outage")

		pw := newLocalPowerwall(t, sim)
		p := pw.Power(t.Context())
		assert.InDelta(t, 0, p.Grid, 0.01)
		assert.InDelta(t, 4300, p.Solar, 0.01)
		assert.InDelta(t, -3100, p.Battery, 0.01, "battery discharging to cover the outage")
		assert.InDelta(t, 1200, p.Home, 0.01)

		requireGridStatus(t, sim, "SystemIslandedActive")
	})

	t.Run("battery-exporting vs grid-charging: Battery's sign flips with direction", func(t *testing.T) {
		sim.triggerControl(t, "/test/scenario/battery-exporting")
		exporting := newLocalPowerwall(t, sim).Power(t.Context())
		assert.Positive(t, exporting.Battery, "discharging/exporting battery reports positive power")
		assert.InDelta(t, 3000, exporting.Battery, 0.01)

		sim.triggerControl(t, "/test/scenario/grid-charging")
		charging := newLocalPowerwall(t, sim).Power(t.Context())
		assert.Negative(t, charging.Battery, "charging battery reports negative power")
		assert.InDelta(t, -300, charging.Battery, 0.01)

		// Both scenarios also exercise the grid_status reset every
		// /test/scenario/* handler performs, independent of toggle-grid.
		requireGridStatus(t, sim, "SystemGridConnected")
	})

	t.Run("battery-percentage flows through Level and SOE consistently", func(t *testing.T) {
		sim.triggerControl(t, "/test/battery-percentage/50")

		pw := newLocalPowerwall(t, sim)

		// stub.py pre-applies pypowerwall's reserved-capacity offset so
		// that the *scaled* reading round-trips back to exactly N:
		//   raw   = 0.95*N + 5       = 0.95*50 + 5 = 52.5
		//   scale(raw) = (raw-5)/0.95 = 47.5/0.95   = 50
		raw, err := pw.Level(t.Context())
		require.NoError(t, err)
		assert.InDelta(t, 52.5, raw, 0.001)

		scaled, err := pw.LevelScaled(t.Context())
		require.NoError(t, err)
		assert.InDelta(t, 50.0, scaled, 0.001)

		soe, err := pw.SOE(t.Context())
		require.NoError(t, err)
		assert.InDelta(t, 52.5, soe.Percentage, 0.001, "SOE() reports the raw, unscaled percentage like Level(false)")

		// The same percentage change, observed through the proxy's HTTP
		// surface: /soe passes the raw gateway value through unscaled, and
		// /csv's BatteryLevel column comes from Level(ctx, false) - also
		// unscaled - so both should agree with the direct SDK reads above.
		ts := newProxyServer(t, sim)

		_, soeBody := getBody(t, ts, "/soe")
		var proxySOE struct {
			Percentage float64 `json:"percentage"`
		}
		require.NoError(t, json.Unmarshal(soeBody, &proxySOE))
		assert.InDelta(t, 52.5, proxySOE.Percentage, 0.001)

		_, csvBody := getBody(t, ts, "/csv")
		fields := csvFields(t, csvBody)
		require.Len(t, fields, 5)
		assert.InDelta(t, 52.5, csvFloat(t, fields, 4), 0.02, "BatteryLevel column")
	})
}
