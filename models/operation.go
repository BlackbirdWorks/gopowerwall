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
	SiteName               string  `json:"site_name"`
	Timezone               string  `json:"timezone"`
	GridCode               string  `json:"grid_code"`
	MaxSystemEnergyKWH     float64 `json:"max_system_energy_kWh"`
	MaxSystemPowerKW       float64 `json:"max_system_power_kW"`
	NominalSystemEnergyKWH float64 `json:"nominal_system_energy_kWh"`
	NominalSystemPowerKW   float64 `json:"nominal_system_power_kW"`
	PanelMaxOutputW        float64 `json:"panel_max_output_W,omitempty"`
}
