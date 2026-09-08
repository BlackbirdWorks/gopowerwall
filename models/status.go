package models

// GridFault represents a grid fault event.
type GridFault struct {
	AlertName string `json:"alert_name"`
	Active    bool   `json:"active"`
}

// BatteryBlock represents an individual Powerwall battery module.
type BatteryBlock struct {
	OpSeqState             string  `json:"OpSeqState"`
	PackageSerialNumber    string  `json:"PackageSerialNumber"`
	PinvGridState          string  `json:"pinv_grid_state,omitempty"`
	PinvState              string  `json:"pinv_state,omitempty"`
	Version                string  `json:"version"`
	PackagePartNumber      string  `json:"PackagePartNumber"`
	FOut                   float64 `json:"f_out"`
	VOut                   float64 `json:"v_out"`
	EnergyDischarged       float64 `json:"energy_discharged"`
	POut                   float64 `json:"p_out"`
	EnergyCharged          float64 `json:"energy_charged"`
	QOut                   float64 `json:"q_out"`
	NominalFullPackEnergy  float64 `json:"nominal_full_pack_energy"`
	NominalEnergyRemaining float64 `json:"nominal_energy_remaining"`
	IOut                   float64 `json:"i_out"`
	VFMode                 bool    `json:"vf_mode"`
	BackupReady            bool    `json:"backup_ready"`
	ChargePowerClamped     bool    `json:"charge_power_clamped"`
	WobbleDetected         bool    `json:"wobble_detected"`
	OffGrid                bool    `json:"off_grid"`
}

// SystemStatus represents /api/system_status.
type SystemStatus struct {
	CommandSource              string         `json:"command_source"`
	GridStatus                 string         `json:"grid_status"`
	InverterType               string         `json:"inverter_type"`
	GridFaults                 []GridFault    `json:"grid_faults"`
	BatteryBlocks              []BatteryBlock `json:"battery_blocks"`
	BatteryTargetPower         float64        `json:"battery_target_power"`
	BatteryTargetReactivePower float64        `json:"battery_target_reactive_power"`
	NominalFullPackEnergy      float64        `json:"nominal_full_pack_energy"`
	NominalEnergyRemaining     float64        `json:"nominal_energy_remaining"`
	MaxAvailChargePower        float64        `json:"max_avail_charge_power"`
	MaxAvailDischargePower     float64        `json:"max_avail_discharge_power"`
}

// SOE represents /api/system_status/soe.
type SOE struct {
	Percentage float64 `json:"percentage"`
}

// GridStatusResponse represents /api/system_status/grid_status.
type GridStatusResponse struct {
	GridStatus string `json:"grid_status"`
}

// GatewayStatus represents /api/status.
type GatewayStatus struct {
	DIN              string `json:"din"`
	StartTime        string `json:"start_time"`
	UpTimeSeconds    string `json:"up_time_seconds"`
	Version          string `json:"version"`
	GitHash          string `json:"git_hash"`
	DeviceType       string `json:"device_type"`
	TEGType          string `json:"teg_type"`
	SyncType         string `json:"sync_type"`
	CommissionCount  int    `json:"commission_count"`
	IsNew            bool   `json:"is_new"`
	CellularDisabled bool   `json:"cellular_disabled"`
	CanReboot        bool   `json:"can_reboot"`
}
