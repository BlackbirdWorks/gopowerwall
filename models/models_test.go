package models_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/models"
)

// readFixture returns the raw contents of a recorded gateway response under
// proxy/web/bogus. Tests prefer these fixtures over hand-written JSON
// literals, per project convention, so the DTOs are exercised against
// realistic gateway payload shapes.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "proxy", "web", "bogus", name))
	require.NoError(t, err)

	return data
}

func TestCoerceTEDAPIApiVersion(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name  string
		input string
		want  models.TEDAPIApiVersion
	}

	for _, tc := range []testCase{
		{name: "exact V2026_06", input: "V2026_06", want: models.TEDAPIVersion2026_06},
		{name: "bare 2026_06", input: "2026_06", want: models.TEDAPIVersion2026_06},
		{name: "short V2026", input: "V2026", want: models.TEDAPIVersion2026_06},
		{name: "lowercase v2026_06", input: "v2026_06", want: models.TEDAPIVersion2026_06},
		{name: "padded with whitespace", input: "  V2026_06  ", want: models.TEDAPIVersion2026_06},
		{name: "mixed case with whitespace", input: " v2026 ", want: models.TEDAPIVersion2026_06},
		{name: "explicit V2024_06 maps to default", input: "V2024_06", want: models.TEDAPIVersion2024_06},
		{name: "unrecognised value falls back to default", input: "bogus", want: models.TEDAPIVersion2024_06},
		{name: "empty string falls back to default", input: "", want: models.TEDAPIVersion2024_06},
		{name: "whitespace only falls back to default", input: "   ", want: models.TEDAPIVersion2024_06},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, models.CoerceTEDAPIApiVersion(tc.input))
		})
	}
}

// TestFixtureRoundTrip decodes recorded gateway responses into their
// corresponding models DTO and spot-checks the fields that are known to
// line up between the wire shape and the Go struct tags.
func TestFixtureRoundTrip(t *testing.T) {
	t.Parallel()

	type testCase struct {
		verify  func(t *testing.T, raw []byte)
		name    string
		fixture string
	}

	for _, tc := range []testCase{
		{
			name:    "GatewayStatus decodes /api/status",
			fixture: "api.status.json",
			verify: func(t *testing.T, raw []byte) {
				t.Helper()

				var status models.GatewayStatus
				require.NoError(t, json.Unmarshal(raw, &status))
				assert.Equal(t, "1232100-00-E--TG1234567890G1", status.DIN)
				assert.Equal(t, "23.28.2 27626f98", status.Version)
				assert.Equal(t, "teg", status.DeviceType)
				assert.False(t, status.IsNew)
			},
		},
		{
			name:    "Operation decodes /api/operation",
			fixture: "api.operation.json",
			verify: func(t *testing.T, raw []byte) {
				t.Helper()

				var op models.Operation
				require.NoError(t, json.Unmarshal(raw, &op))
				assert.Equal(t, "self_consumption", op.RealMode)
				assert.InDelta(t, 81.0, op.BackupReservePercent, 0.001)
			},
		},
		{
			name:    "GridStatusResponse decodes /api/system_status/grid_status",
			fixture: "api.system_status.grid_status.json",
			verify: func(t *testing.T, raw []byte) {
				t.Helper()

				var gs models.GridStatusResponse
				require.NoError(t, json.Unmarshal(raw, &gs))
				assert.Equal(t, "SystemGridConnected", gs.GridStatus)
			},
		},
		{
			name:    "SOE decodes /api/system_status/soe",
			fixture: "api.system_status.soe.json",
			verify: func(t *testing.T, raw []byte) {
				t.Helper()

				var soe models.SOE
				require.NoError(t, json.Unmarshal(raw, &soe))
				assert.InDelta(t, 20.109166592431226, soe.Percentage, 1e-9)
			},
		},
		{
			name:    "SiteInfo decodes the site_name subset of /api/site_info",
			fixture: "api.site_info.site_name.json",
			verify: func(t *testing.T, raw []byte) {
				t.Helper()

				var si models.SiteInfo
				require.NoError(t, json.Unmarshal(raw, &si))
				assert.Equal(t, "Tesla Energy Gateway", si.SiteName)
				assert.Equal(t, "America/Los_Angeles", si.Timezone)
			},
		},
		{
			name:    "SystemStatus decodes /api/system_status nested battery blocks",
			fixture: "api.system_status.json",
			verify: func(t *testing.T, raw []byte) {
				t.Helper()

				var ss models.SystemStatus
				require.NoError(t, json.Unmarshal(raw, &ss))
				assert.Equal(t, "Configuration", ss.CommandSource)
				assert.InDelta(t, -3866.6666666666665, ss.BatteryTargetPower, 0.001)
				assert.InDelta(t, float64(25995), ss.NominalFullPackEnergy, 0.001)
				assert.Empty(t, ss.GridFaults)
				require.Len(t, ss.BatteryBlocks, 2)
				assert.Equal(t, "TG123456789012", ss.BatteryBlocks[0].PackageSerialNumber)
				assert.Equal(t, "2012170-25-E", ss.BatteryBlocks[0].PackagePartNumber)
				assert.True(t, ss.BatteryBlocks[0].BackupReady)
				assert.False(t, ss.BatteryBlocks[0].OffGrid)

				// Known gap, see the package-level bug report in
				// TestKnownFixtureMismatches's doc comment: the recorded
				// gateway reports grid connectivity under
				// "system_island_state" and avail-power limits under
				// "max_charge_power"/"max_discharge_power", not the
				// "grid_status"/"inverter_type"/"max_avail_charge_power"/
				// "max_avail_discharge_power" keys SystemStatus binds to, so
				// these four fields silently decode as zero values against a
				// real gateway payload.
				assert.Empty(t, ss.GridStatus)
				assert.Empty(t, ss.InverterType)
				assert.Zero(t, ss.MaxAvailChargePower)
				assert.Zero(t, ss.MaxAvailDischargePower)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.verify(t, readFixture(t, tc.fixture))
		})
	}
}

// TestKnownFixtureMismatches is a bug report in test form. Two DTOs disagree
// with the shape of a real recorded gateway response:
//
//   - models.SiteInfo.GridCode is typed string, but /api/site_info nests a
//     full grid-code descriptor object under "grid_code" (see
//     proxy/web/bogus/api.site_info.json). Powerwall.SiteInfo(ctx) always
//     fails to unmarshal against a real gateway as a result, returning an
//     error on every call.
//   - models.MeterReading.Timeout is typed bool, but /api/meters/aggregates
//     reports a numeric duration in nanoseconds (e.g. 1500000000). This is
//     latent rather than actively broken today: MetersAggregates is only
//     referenced by the unused models.CompositeJSON type, so nothing in
//     production currently unmarshals a live payload into it. It would fail
//     the same way the moment it is wired up.
//
// Per project convention these are reported here rather than silently
// patched; fixing them is a product decision (SiteInfo.GridCode would need
// to become a struct, which is a breaking change to an exported type).
func TestKnownFixtureMismatches(t *testing.T) {
	t.Parallel()

	type testCase struct {
		target      any
		name        string
		fixture     string
		wantErrText string
	}

	for _, tc := range []testCase{
		{
			name:        "SiteInfo.GridCode cannot decode the real grid_code object",
			fixture:     "api.site_info.json",
			target:      &models.SiteInfo{},
			wantErrText: "grid_code",
		},
		{
			name:        "MetersAggregates.Timeout cannot decode the real numeric timeout",
			fixture:     "api.meters.aggregates.json",
			target:      &models.MetersAggregates{},
			wantErrText: "timeout",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := json.Unmarshal(readFixture(t, tc.fixture), tc.target)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErrText)
		})
	}
}

// TestHandwrittenRoundTrip covers the DTOs with no corresponding recorded
// gateway fixture (internal proxy/CLI types, or types built from protobuf
// vitals rather than raw JSON) with a generic marshal/unmarshal round trip.
func TestHandwrittenRoundTrip(t *testing.T) {
	t.Parallel()

	type testCase struct {
		model any
		name  string
	}

	for _, tc := range []testCase{
		{
			name: "DeviceVital",
			model: models.DeviceVital{
				Values:     map[string]any{"THC_AmbientTemp": 28.5},
				DeviceName: "TETHC--1",
			},
		},
		{
			name: "VitalsData",
			model: models.VitalsData{
				Devices: map[string]map[string]any{"TETHC--1": {"THC_AmbientTemp": 28.5}},
			},
		},
		{
			name:  "AlertsList",
			model: models.AlertsList{Alerts: []string{"SystemConnectedToGrid"}},
		},
		{
			name:  "StringMetric",
			model: models.StringMetric{Connected: true, Voltage: 245.5, Current: 8.2, Power: 2013},
		},
		{
			name: "SolarStrings",
			model: models.SolarStrings{
				Strings: map[string]models.StringMetric{
					"PVAC--1_A": {Connected: true, Voltage: 245.5, Current: 8.2, Power: 2013},
				},
			},
		},
		{
			name:  "PowerwallTemps",
			model: models.PowerwallTemps{Temps: map[string]float64{"TETHC--1": 28.5}},
		},
		{
			name: "ProxyStats",
			model: models.ProxyStats{
				SiteName:   "Tesla Energy Gateway",
				Config:     map[string]any{"host": "127.0.0.1"},
				Uptime:     "1h2m3s",
				TotalGets:  10,
				TotalPosts: 2,
				CloudMode:  true,
			},
		},
		{
			name: "CompositeJSON",
			model: models.CompositeJSON{
				SiteName: "Tesla Energy Gateway",
				Firmware: "23.28.2",
				DIN:      "1232100-00-E--TG1234567890G1",
				SOE:      models.SOE{Percentage: 50},
			},
		},
		{
			name: "PODMetrics",
			model: models.PODMetrics{
				Voltage:   243.7,
				Current:   40.9,
				Frequency: 60.0,
				Power:     -1990,
			},
		},
		{
			name:  "FrequencyMetrics",
			model: models.FrequencyMetrics{Grid: 60.01, Home: 60.02},
		},
		{
			name:  "PowerSummary",
			model: models.PowerSummary{Site: 27, Solar: 1840, Battery: -990, Load: 866.25},
		},
		{
			name:  "LocalAuthCredentials",
			model: models.LocalAuthCredentials{AuthCookie: "cookie-value", UserRecord: "user-value"},
		},
		{
			name: "TokenData",
			model: models.TokenData{
				AccessToken:  "access",
				RefreshToken: "refresh",
				TokenType:    "Bearer",
				ExpiresIn:    300,
			},
		},
		{
			name:  "CloudAccountEntry",
			model: models.CloudAccountEntry{SSO: models.TokenData{AccessToken: "access"}},
		},
		{
			name: "FleetAPIConfig",
			model: models.FleetAPIConfig{
				ClientID:     "client-123",
				ClientSecret: "secret",
				Domain:       "example.com",
				SiteID:       "site-1",
			},
		},
		{
			name:  "DiscoveredDevice",
			model: models.DiscoveredDevice{IP: "192.168.1.100", DIN: "abc", IsPowerwall: true},
		},
		{
			name:  "GridFault",
			model: models.GridFault{AlertName: "fault", Active: true},
		},
		{
			name: "BatteryBlock",
			model: models.BatteryBlock{
				PackageSerialNumber: "TG1",
				OpSeqState:          "Active",
				BackupReady:         true,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tc.model)
			require.NoError(t, err)

			out := reflect.New(reflect.TypeOf(tc.model))
			require.NoError(t, json.Unmarshal(data, out.Interface()))
			assert.Equal(t, tc.model, out.Elem().Interface())
		})
	}
}
