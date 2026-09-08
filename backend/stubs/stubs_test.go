package stubs_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/backend/stubs"
)

func TestMockConstantsValidJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		assertFn func(t *testing.T, val any)
		name     string
		rawJSON  string
	}{
		{
			name:    "MockPowerwalls",
			rawJSON: stubs.MockPowerwalls,
			assertFn: func(t *testing.T, val any) {
				t.Helper()
				m, ok := val.(map[string]any)
				require.True(t, ok)
				assert.Contains(t, m, "powerwalls")
				assert.Contains(t, m, "gateway_din")
			},
		},
		{
			name:    "MockMetersSite",
			rawJSON: stubs.MockMetersSite,
			assertFn: func(t *testing.T, val any) {
				t.Helper()
				slice, ok := val.([]any)
				require.True(t, ok)
				require.NotEmpty(t, slice)
			},
		},
		{
			name:    "MockMeters",
			rawJSON: stubs.MockMeters,
			assertFn: func(t *testing.T, val any) {
				t.Helper()
				slice, ok := val.([]any)
				require.True(t, ok)
				require.Len(t, slice, 3)
				first, ok := slice[0].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "VAH1234AB1234", first["serial"])
				assert.Equal(t, "neurio_w2_tcp", first["type"])
				assert.Equal(t, true, first["connected"])
			},
		},
		{
			name:    "MockSitemaster",
			rawJSON: stubs.MockSitemaster,
			assertFn: func(t *testing.T, val any) {
				t.Helper()
				m, ok := val.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "StatusUp", m["status"])
				assert.Equal(t, true, m["running"])
				assert.Equal(t, false, m["power_supply_mode"])
				assert.Equal(t, "Yes", m["can_reboot"])
			},
		},
		{
			name:    "MockCustomer",
			rawJSON: stubs.MockCustomer,
			assertFn: func(t *testing.T, val any) {
				t.Helper()
				m, ok := val.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, true, m["registered"])
			},
		},
		{
			name:    "MockInstaller",
			rawJSON: stubs.MockInstaller,
			assertFn: func(t *testing.T, val any) {
				t.Helper()
				m, ok := val.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "Tesla", m["company"])
				assert.Equal(t, "Whole Home", m["backup_configuration"])
				assert.Equal(t, true, m["run_sitemaster"])
			},
		},
		{
			name:    "MockNetworks",
			rawJSON: stubs.MockNetworks,
			assertFn: func(t *testing.T, val any) {
				t.Helper()
				slice, ok := val.([]any)
				require.True(t, ok)
				require.NotEmpty(t, slice)
			},
		},
		{
			name:    "MockAuthToggle",
			rawJSON: stubs.MockAuthToggle,
			assertFn: func(t *testing.T, val any) {
				t.Helper()
				m, ok := val.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, true, m["toggle_auth_supported"])
			},
		},
		{
			name:    "MockUpdate",
			rawJSON: stubs.MockUpdate,
			assertFn: func(t *testing.T, val any) {
				t.Helper()
				m, ok := val.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "/update_succeeded", m["state"])
				assert.Contains(t, m, "info")
				assert.Contains(t, m, "version")
			},
		},
		{
			name:    "MockSolars",
			rawJSON: stubs.MockSolars,
			assertFn: func(t *testing.T, val any) {
				t.Helper()
				slice, ok := val.([]any)
				require.True(t, ok)
				require.Len(t, slice, 1)
				first, ok := slice[0].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "Tesla", first["brand"])
				assert.Equal(t, "Solar Inverter 7.6", first["model"])
				assert.InDelta(t, 7600.0, first["power_rating_watts"], 0.001)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			parsed := stubs.ParseJSON(tt.rawJSON)
			require.NotNil(t, parsed)
			tt.assertFn(t, parsed)
		})
	}
}

func TestStubGenerators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		genFn    func() map[string]any
		assertFn func(t *testing.T, m map[string]any)
		name     string
	}{
		{
			name:  "MetersAggregatesStub",
			genFn: stubs.MetersAggregatesStub,
			assertFn: func(t *testing.T, m map[string]any) {
				t.Helper()
				assert.Contains(t, m, "site")
				assert.Contains(t, m, "battery")
				assert.Contains(t, m, "load")
				assert.Contains(t, m, "solar")
			},
		},
		{
			name:  "SystemStatusStub",
			genFn: stubs.SystemStatusStub,
			assertFn: func(t *testing.T, m map[string]any) {
				t.Helper()
				assert.Contains(t, m, "command_source")
				assert.Contains(t, m, "battery_blocks")
				assert.Contains(t, m, "system_island_state")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := tt.genFn()
			require.NotNil(t, m)
			tt.assertFn(t, m)

			// Confirm it marshals cleanly into JSON
			b, err := json.Marshal(m)
			require.NoError(t, err)
			assert.NotEmpty(t, b)
		})
	}
}
