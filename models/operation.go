package models

// Operation represents /api/operation mode and reserve data.
type Operation struct {
	RealMode             string  `json:"real_mode"`
	GridExport           string  `json:"grid_export,omitempty"`
	BackupReservePercent float64 `json:"backup_reserve_percent"`
	TimeRemainingHours   float64 `json:"time_remaining_hours,omitempty"`
	GridCharging         bool    `json:"grid_charging"`
	StormMode            bool    `json:"storm_mode,omitempty"`
}

// SiteInfo represents /api/site_info site details.
type SiteInfo struct {
	SiteName               string       `json:"site_name"`
	Timezone               string       `json:"timezone"`
	GridCode               GridCodeInfo `json:"grid_code"`
	MaxSystemEnergyKWH     float64      `json:"max_system_energy_kWh"`
	MaxSystemPowerKW       float64      `json:"max_system_power_kW"`
	NominalSystemEnergyKWH float64      `json:"nominal_system_energy_kWh"`
	NominalSystemPowerKW   float64      `json:"nominal_system_power_kW"`
	PanelMaxOutputW        float64      `json:"panel_max_output_W,omitempty"`
}

// GridCodeInfo describes the grid interconnection standard the gateway is
// configured for, as nested under /api/site_info's "grid_code" key. On a
// real gateway this is an object, not a bare code string - see
// proxy/web/bogus/api.site_info.json for a recorded example.
type GridCodeInfo struct {
	// GridCode is the interconnection standard identifier, e.g.
	// "60Hz_240V_s_UL1741SA:2019_California".
	GridCode string `json:"grid_code"`
	// GridPhaseSetting is the configured phase arrangement, e.g. "Split".
	GridPhaseSetting string `json:"grid_phase_setting,omitempty"`
	// Country is the country the grid code applies to.
	Country string `json:"country,omitempty"`
	// State is the state or region the grid code applies to.
	State string `json:"state,omitempty"`
	// Utility is the name of the serving utility.
	Utility string `json:"utility,omitempty"`
	// GridVoltageSetting is the configured nominal grid voltage, in volts.
	GridVoltageSetting float64 `json:"grid_voltage_setting"`
	// GridFreqSetting is the configured nominal grid frequency, in hertz.
	GridFreqSetting float64 `json:"grid_freq_setting"`
}
