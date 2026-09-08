package models

import "time"

// MeterReading represents individual meter data from /api/meters/aggregates.
type MeterReading struct {
	LastCommunicationTime             string  `json:"last_communication_time,omitempty"`
	LastPhaseVoltageCommunicationTime string  `json:"last_phase_voltage_communication_time,omitempty"`
	LastPhasePowerCommunicationTime   string  `json:"last_phase_power_communication_time,omitempty"`
	LastPhaseEnergyCommunicationTime  string  `json:"last_phase_energy_communication_time,omitempty"`
	InstantPower                      float64 `json:"instant_power"`
	InstantReactivePower              float64 `json:"instant_reactive_power,omitempty"`
	InstantApparentPower              float64 `json:"instant_apparent_power,omitempty"`
	Frequency                         float64 `json:"frequency,omitempty"`
	EnergyExported                    float64 `json:"energy_exported,omitempty"`
	EnergyImported                    float64 `json:"energy_imported,omitempty"`
	InstantAverageVoltage             float64 `json:"instant_average_voltage,omitempty"`
	InstantAverageCurrent             float64 `json:"instant_average_current,omitempty"`
	InstantTotalCurrent               float64 `json:"instant_total_current,omitempty"`
	IACCurrent                        float64 `json:"i_a_current,omitempty"`
	IBCCurrent                        float64 `json:"i_b_current,omitempty"`
	ICCCurrent                        float64 `json:"i_c_current,omitempty"`
	VL1N                              float64 `json:"v_l1n,omitempty"`
	VL2N                              float64 `json:"v_l2n,omitempty"`
	NumMetersAggregated               int     `json:"num_meters_aggregated,omitempty"`
	// Timeout is the meter's staleness threshold, reported by the gateway
	// as a nanosecond duration (e.g. 1500000000, i.e. 1.5s) - not the bool
	// its field name might suggest. time.Duration decodes a bare JSON
	// number directly since its underlying type is int64, so no custom
	// unmarshaling is needed.
	Timeout time.Duration `json:"timeout,omitempty"`
}

// MetersAggregates represents /api/meters/aggregates.
type MetersAggregates struct {
	Generator *MeterReading `json:"generator,omitempty"`
	Site      MeterReading  `json:"site"`
	Battery   MeterReading  `json:"battery"`
	Load      MeterReading  `json:"load"`
	Solar     MeterReading  `json:"solar"`
}

// PowerSummary holds combined scalar power metrics in Watts.
type PowerSummary struct {
	Site    float64 `json:"site"`
	Solar   float64 `json:"solar"`
	Battery float64 `json:"battery"`
	Load    float64 `json:"load"`
	Grid    float64 `json:"grid,omitempty"`
	Home    float64 `json:"home,omitempty"`
}
