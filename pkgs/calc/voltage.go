package calc

import "math"

const (
	significantVoltage = 100.0
	numPhaseValues     = 3.0
	twoPhases          = 2
)

// ComputeLLVoltage calculates the line-to-line voltage for single, split, or three-phase systems.
// Matches pypowerwall.tedapi.pypowerwall_tedapi.compute_LL_voltage.
func ComputeLLVoltage(v1n, v2n, v3n float64) float64 {
	var active []float64
	for _, v := range []float64{v1n, v2n, v3n} {
		if math.Abs(v) > significantVoltage {
			active = append(active, v)
		}
	}

	if len(active) == 0 {
		return v1n + v2n + v3n
	}
	if len(active) == 1 {
		return active[0]
	}
	if len(active) == twoPhases {
		return active[0] + active[1]
	}

	// Three-phase: 120 degrees out of phase
	v12 := math.Sqrt(v1n*v1n + v2n*v2n + v1n*v2n)
	v23 := math.Sqrt(v2n*v2n + v3n*v3n + v2n*v3n)
	v31 := math.Sqrt(v3n*v3n + v1n*v1n + v3n*v1n)

	return (v12 + v23 + v31) / numPhaseValues
}

const (
	scaleUsableFraction = 0.95
	scaleReservedFloor  = 5.0
)

// ScaleBatteryLevel converts a raw gateway battery percentage into the
// user-facing percentage, mapping the gateway's usable 5-100 range onto 0-100.
// Matches pypowerwall's (level / 0.95) - (5 / 0.95).
func ScaleBatteryLevel(level float64) float64 {
	return (level / scaleUsableFraction) - (scaleReservedFloor / scaleUsableFraction)
}

// UnscaleBatteryLevel converts a user-facing battery percentage (such as
// percentage_charged from Tesla Cloud / FleetAPI) into the raw gateway
// battery percentage, mapping the 0-100 app range back onto the gateway's
// usable 5-100 range.
// Matches pypowerwall's (level + (5 / 0.95)) * 0.95.
func UnscaleBatteryLevel(level float64) float64 {
	return (level + (scaleReservedFloor / scaleUsableFraction)) * scaleUsableFraction
}
