package models

// StringMetric represents solar string voltage, current, and power.
type StringMetric struct {
	Connected bool    `json:"connected"`
	Voltage   float64 `json:"voltage"`
	Current   float64 `json:"current"`
	Power     float64 `json:"power"`
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
