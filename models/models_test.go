package models_test

import (
	"encoding/json"
	"testing"

	"github.com/blackbirdworks/gopowerwall/models"
)

func TestModelsJSONSerialization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model    any
		name     string
		contains string
	}{
		{
			name: "MetersAggregates",
			model: models.MetersAggregates{
				Site: models.MeterReading{
					InstantPower: 1250.5,
				},
				Solar: models.MeterReading{
					InstantPower: 4200.0,
				},
			},
			contains: `"instant_power":1250.5`,
		},
		{
			name: "SOE",
			model: models.SOE{
				Percentage: 94.5,
			},
			contains: `"percentage":94.5`,
		},
		{
			name: "Operation",
			model: models.Operation{
				RealMode:             "self_consumption",
				BackupReservePercent: 20.0,
				GridCharging:         false,
			},
			contains: `"real_mode":"self_consumption"`,
		},
		{
			name: "SiteInfo",
			model: models.SiteInfo{
				SiteName: "My Powerwall",
				Timezone: "America/Los_Angeles",
			},
			contains: `"site_name":"My Powerwall"`,
		},
		{
			name: "FleetAPIConfig",
			model: models.FleetAPIConfig{
				ClientID: "client-123",
				Domain:   "example.com",
			},
			contains: `"client_id":"client-123"`,
		},
		{
			name: "DiscoveredDevice",
			model: models.DiscoveredDevice{
				IP:          "192.168.1.100",
				IsPowerwall: true,
			},
			contains: `"is_powerwall":true`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tt.model)
			if err != nil {
				t.Fatalf("json.Marshal failed: %v", err)
			}
			str := string(data)
			if !containsSubstring(str, tt.contains) {
				t.Errorf("got json %s, wanted substring %s", str, tt.contains)
			}
		})
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || stringContains(s, sub))
}

func stringContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}

	return false
}
