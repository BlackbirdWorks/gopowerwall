package tedapi_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/powerwall/tedapi"
)

func TestGetQuery(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		role      string
		version   models.TEDAPIApiVersion
		wantNil   bool
		wantBCode bool
	}

	cases := []testCase{
		{
			name:      "V2024_06 device controller basic",
			role:      tedapi.QueryRoleDeviceControllerBasic,
			version:   models.TEDAPIVersion2024_06,
			wantBCode: true,
		},
		{
			name:      "V2024_06 device controller full",
			role:      tedapi.QueryRoleDeviceControllerFull,
			version:   models.TEDAPIVersion2024_06,
			wantBCode: true,
		},
		{
			name:      "V2024_06 components",
			role:      tedapi.QueryRoleComponents,
			version:   models.TEDAPIVersion2024_06,
			wantBCode: true,
		},
		{
			name:    "V2024_06 unknown role returns nil",
			role:    "not_a_real_role",
			version: models.TEDAPIVersion2024_06,
			wantNil: true,
		},
		{
			name:      "V2026_06 device controller basic maps to DeviceControllerQuery",
			role:      tedapi.QueryRoleDeviceControllerBasic,
			version:   models.TEDAPIVersion2026_06,
			wantBCode: true,
		},
		{
			name:      "V2026_06 device controller full maps to DeviceControllerQuery",
			role:      tedapi.QueryRoleDeviceControllerFull,
			version:   models.TEDAPIVersion2026_06,
			wantBCode: true,
		},
		{
			name:      "V2026_06 components maps to PW3Query",
			role:      tedapi.QueryRoleComponents,
			version:   models.TEDAPIVersion2026_06,
			wantBCode: true,
		},
		{
			name:    "V2026_06 unrecognised role passes through unchanged and misses",
			role:    "not_a_real_role",
			version: models.TEDAPIVersion2026_06,
			wantNil: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			q := tedapi.GetQuery(tc.role, tc.version)
			if tc.wantNil {
				assert.Nil(t, q)

				return
			}
			require.NotNil(t, q)
			assert.NotEmpty(t, q.Text)
			if tc.wantBCode {
				assert.NotEmpty(t, q.BValue)
			}
		})
	}
}

func TestGetQueryByName(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		opName  string
		wantNil bool
	}

	cases := []testCase{
		{name: "known V2026_06 operation name", opName: "PW3Query"},
		{name: "another known V2026_06 operation name", opName: "GridCodesQuery"},
		{name: "unknown operation name returns nil", opName: "DoesNotExist", wantNil: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			q := tedapi.GetQueryByName(tc.opName)
			if tc.wantNil {
				assert.Nil(t, q)

				return
			}
			require.NotNil(t, q)
			assert.NotEmpty(t, q.Text)
		})
	}
}

func TestExtraSignals(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		role       string
		wantSignal string
	}

	cases := []testCase{
		{
			name:       "components includes PCH_AmbientTemp",
			role:       tedapi.QueryRoleComponents,
			wantSignal: "PCH_AmbientTemp",
		},
		{
			name:       "components includes PCH_FanSpeed_A",
			role:       tedapi.QueryRoleComponents,
			wantSignal: "PCH_FanSpeed_A",
		},
		{
			name:       "components includes HVP_PackTempMax",
			role:       tedapi.QueryRoleComponents,
			wantSignal: "HVP_PackTempMax",
		},
		{
			name:       "components includes BMS_kwhDischargeTotal",
			role:       tedapi.QueryRoleComponents,
			wantSignal: "BMS_kwhDischargeTotal",
		},
		{
			name:       "device controller full includes SYNC_Uptime",
			role:       tedapi.QueryRoleDeviceControllerFull,
			wantSignal: "SYNC_Uptime",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			q := tedapi.GetQuery(tc.role, models.TEDAPIVersion2024_06)
			require.NotNil(t, q)
			assert.Contains(t, q.BValue, tc.wantSignal)
		})
	}
}
