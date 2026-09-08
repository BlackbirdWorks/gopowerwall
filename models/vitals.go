package models

// StringMetric represents solar string voltage, current, power, and the
// gateway's own PV state string (e.g. "Pv_Active", "PV_Active_Parallel",
// "Pv_Standby" - the exact spelling is firmware/backend-dependent, see
// [Powerwall.Strings]).
type StringMetric struct {
	State     string  `json:"state"`
	Voltage   float64 `json:"voltage"`
	Current   float64 `json:"current"`
	Power     float64 `json:"power"`
	Connected bool    `json:"connected"`
}

// SolarStrings maps string IDs to their measurements.
type SolarStrings struct {
	Strings map[string]StringMetric `json:"strings"`
}

// PowerwallTemps maps device IDs to temperature in Celsius.
type PowerwallTemps struct {
	Temps map[string]float64 `json:"temps"`
}

// AlertsList contains active alert strings.
type AlertsList struct {
	Alerts []string `json:"alerts"`
}

// DeviceVital represents an individual device vital map.
type DeviceVital struct {
	Values     map[string]any `json:"values"`
	DeviceName string         `json:"device_name"`
}

// VitalsData represents full device vitals.
type VitalsData struct {
	Devices map[string]map[string]any `json:"devices"`
}

// FanSpeedEntry holds one PVAC device's cooling-fan speed readings in RPM,
// mirroring pypowerwall's extract_fan_speeds/get_fan_speeds
// (pypowerwall/tedapi/__init__.py:1879-1908). A field is nil - and, thanks
// to the omitempty tag, entirely absent from a marshaled /fans response -
// when the gateway's response did not carry that particular signal:
// upstream's own dict comprehension only ever inserts a signal name it
// found a non-null value for, so the JSON output omits a missing key
// rather than emitting an explicit null for it.
type FanSpeedEntry struct {
	ActualRPM *float64 `json:"PVAC_Fan_Speed_Actual_RPM,omitempty"`
	TargetRPM *float64 `json:"PVAC_Fan_Speed_Target_RPM,omitempty"`
}
