package stubs

import "encoding/json"

const (
	keyLastCommTime         = "last_communication_time"
	keyInstantPower         = "instant_power"
	keyInstantReactivePower = "instant_reactive_power"
	keyInstantApparentPower = "instant_apparent_power"
	keyFrequency            = "frequency"
	keyEnergyExported       = "energy_exported"
	keyEnergyImported       = "energy_imported"
	keyInstantAvgVoltage    = "instant_average_voltage"
	keyInstantAvgCurrent    = "instant_average_current"
	keyIACurrent            = "i_a_current"
	keyIBCurrent            = "i_b_current"
	keyICCurrent            = "i_c_current"
	keyLastPhaseVoltageTime = "last_phase_voltage_communication_time"
	keyLastPhasePowerTime   = "last_phase_power_communication_time"
	keyLastPhaseEnergyTime  = "last_phase_energy_communication_time"
	keyTimeout              = "timeout"
	keyNumMetersAggregated  = "num_meters_aggregated"
	keyInstantTotalCurrent  = "instant_total_current"
	zeroTimeRFC3339         = "0001-01-01T00:00:00Z"
	meterTimeout1500ms      = 1500000000
	meterTimeout1000ms      = 1000000000
)

// MetersAggregatesStub returns a fresh copy of the /api/meters/aggregates template.
func MetersAggregatesStub() map[string]any {
	return map[string]any{
		"site": map[string]any{
			keyLastCommTime:         nil,
			keyInstantPower:         0.0,
			keyInstantReactivePower: 0.0,
			keyInstantApparentPower: 0.0,
			keyFrequency:            0.0,
			keyEnergyExported:       0.0,
			keyEnergyImported:       0.0,
			keyInstantAvgVoltage:    0.0,
			keyInstantAvgCurrent:    0.0,
			keyIACurrent:            0.0,
			keyIBCurrent:            0.0,
			keyICCurrent:            0.0,
			keyLastPhaseVoltageTime: zeroTimeRFC3339,
			keyLastPhasePowerTime:   zeroTimeRFC3339,
			keyLastPhaseEnergyTime:  zeroTimeRFC3339,
			keyTimeout:              meterTimeout1500ms,
			keyNumMetersAggregated:  1,
			keyInstantTotalCurrent:  nil,
		},
		"battery": map[string]any{
			keyLastCommTime:         nil,
			keyInstantPower:         0.0,
			keyInstantReactivePower: 0.0,
			keyInstantApparentPower: 0.0,
			keyFrequency:            0.0,
			keyEnergyExported:       0.0,
			keyEnergyImported:       0.0,
			keyInstantAvgVoltage:    0.0,
			keyInstantAvgCurrent:    0.0,
			keyIACurrent:            0.0,
			keyIBCurrent:            0.0,
			keyICCurrent:            0.0,
			keyLastPhaseVoltageTime: zeroTimeRFC3339,
			keyLastPhasePowerTime:   zeroTimeRFC3339,
			keyLastPhaseEnergyTime:  zeroTimeRFC3339,
			keyTimeout:              meterTimeout1500ms,
			keyNumMetersAggregated:  nil,
			keyInstantTotalCurrent:  0.0,
		},
		"load": map[string]any{
			keyLastCommTime:         nil,
			keyInstantPower:         0.0,
			keyInstantReactivePower: 0.0,
			keyInstantApparentPower: 0.0,
			keyFrequency:            0.0,
			keyEnergyExported:       0.0,
			keyEnergyImported:       0.0,
			keyInstantAvgVoltage:    0.0,
			keyInstantAvgCurrent:    0.0,
			keyIACurrent:            0.0,
			keyIBCurrent:            0.0,
			keyICCurrent:            0.0,
			keyLastPhaseVoltageTime: zeroTimeRFC3339,
			keyLastPhasePowerTime:   zeroTimeRFC3339,
			keyLastPhaseEnergyTime:  zeroTimeRFC3339,
			keyTimeout:              meterTimeout1500ms,
			keyInstantTotalCurrent:  0.0,
		},
		"solar": map[string]any{
			keyLastCommTime:         nil,
			keyInstantPower:         0.0,
			keyInstantReactivePower: 0.0,
			keyInstantApparentPower: 0.0,
			keyFrequency:            0.0,
			keyEnergyExported:       0.0,
			keyEnergyImported:       0.0,
			keyInstantAvgVoltage:    0.0,
			keyInstantAvgCurrent:    0.0,
			keyIACurrent:            0.0,
			keyIBCurrent:            0.0,
			keyICCurrent:            0.0,
			keyLastPhaseVoltageTime: zeroTimeRFC3339,
			keyLastPhasePowerTime:   zeroTimeRFC3339,
			keyLastPhaseEnergyTime:  zeroTimeRFC3339,
			keyTimeout:              meterTimeout1000ms,
			keyNumMetersAggregated:  nil,
			keyInstantTotalCurrent:  0.0,
		},
	}
}

// SystemStatusStub returns a fresh copy of the /api/system_status template.
func SystemStatusStub() map[string]any {
	return map[string]any{
		"command_source":                    "Configuration",
		"battery_target_power":              0.0,
		"battery_target_reactive_power":     0.0,
		"nominal_full_pack_energy":          nil,
		"nominal_energy_remaining":          nil,
		"max_power_energy_remaining":        0.0,
		"max_power_energy_to_be_charged":    0.0,
		"max_charge_power":                  nil,
		"max_discharge_power":               nil,
		"max_apparent_power":                nil,
		"instantaneous_max_discharge_power": 0.0,
		"instantaneous_max_charge_power":    0.0,
		"grid_services_power":               0.0,
		"system_island_state":               "SystemGridConnected",
		"available_blocks":                  1,
		"battery_blocks":                    []any{},
		"ff_nps":                            0.0,
		"generator_inpower":                 0.0,
		"generator_energy_supplied":         0.0,
		"grid_faults":                       []any{},
	}
}

// Canned mock responses for endpoints not supported in cloud/tedapi modes.
const (
	MockPowerwalls = `{"enumerating": false, "updating": false, ` +
		`"checking_if_offgrid": false, "running_phase_detection": false, ` +
		`"powerwalls": [{"PackagePartNumber": "2012170-25-E", ` +
		`"PackageSerialNumber": "TG1234567890G1", "type": "SolarPowerwall", ` +
		`"grid_state": "Grid_Uncompliant"}], "gateway_din": "1232100-00-E--TG1234567890G1"}`

	MockMetersSite = `[{"id":0,"location":"site","type":"synchrometerX",` +
		`"connection":{"short_id":"1232100-00-E--TG123456789E4G",` +
		`"device_serial":"JBL12345Y1F012synchrometerX"},` +
		`"Cached_readings":{"instant_power":0,"frequency":0,` +
		`"instant_average_voltage":210.89,"instant_average_current":0}}]`

	MockMeters = `[{"id": 0, "location": "site", "type": "synchrometerX"}, ` +
		`{"id": 1, "location": "load", "type": "synchrometerX"}, ` +
		`{"id": 2, "location": "solar", "type": "synchrometerX"}]`

	MockSitemaster = `{"status": "StatusUp", "running": true, "connected_to_tesla": true}`
	MockCustomer   = `{"registered": true}`
	MockInstaller  = `{"ready_for_customer": true}`
	MockNetworks   = `[{"network_name": "Tesla_Guest", "enabled": true}]`
	MockAuthToggle = `{"toggle_supported": false}`
	MockUpdate     = `{"status": "idle", "percentage": 0.0}`
	MockSolars     = `{"brand": "Tesla", "model": "Solar Inverter"}`
)

// ParseJSON parses a canned JSON string into an any.
func ParseJSON(jsonStr string) any {
	var out any
	_ = json.Unmarshal([]byte(jsonStr), &out)

	return out
}
