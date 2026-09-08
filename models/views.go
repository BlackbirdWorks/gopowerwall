package models

import "time"

// Snapshot is the composite, corrected power-and-status view backing the
// gopowerwall proxy's /json route: current power flow for every channel,
// battery state of charge, grid connectivity, backup reserve, estimated
// backup time remaining, pack energy capacity, and per-string solar detail,
// all from one call. Like [Powerwall.Power], it degrades gracefully rather
// than failing outright - any field whose underlying read fails is left at
// its zero value instead of aborting the whole snapshot, mirroring
// pypowerwall's own /json endpoint, which does the same thing field by
// field.
type Snapshot struct {
	Strings         SolarStrings
	Grid            float64
	Home            float64
	Solar           float64
	Battery         float64
	BatteryLevel    float64
	Reserve         float64
	TimeRemaining   time.Duration
	FullPackEnergy  float64
	EnergyRemaining float64
	GridConnected   bool
}

// PODView is the per-battery-block operational view backing the gopowerwall
// proxy's /pod route. Like [Snapshot], it degrades gracefully: a
// disconnected Powerwall or a failed underlying read simply yields an empty
// Blocks slice and zero-valued totals rather than an error. TimeRemainingHours
// and BackupReservePercent are nil specifically when that one read fails,
// since pypowerwall's own /pod reports those two fields as JSON null rather
// than a zero number in that case - the rest of the view does not make the
// same null-vs-zero distinction.
type PODView struct {
	TimeRemainingHours     *float64
	BackupReservePercent   *float64
	Blocks                 []BatteryBlock
	TEPODEntries           []PODTEPODEntry
	NominalFullPackEnergy  float64
	NominalEnergyRemaining float64
}

// PODTEPODEntry is one "TEPOD"-prefixed vitals device's fields, backing the
// gopowerwall proxy's /pod route's second, vitals-derived augmentation pass
// (pypowerwall's server.py:2196-2244, generate_pod's "Augment with Vitals
// Data" loop - a battery-block-heating/POD-controller device pypowerwall's
// TEDAPI backend synthesizes at pypowerwall/tedapi/__init__.py:1018-1022, or
// that a local gateway reports natively). The boolean-shaped fields are ints
// (0/1, or whatever raw int the device reports), not bool, mirroring
// upstream's own int(get_value(v, key) or 0) coercion; the power/energy
// fields are nilable pointers because upstream's get_value returns raw None
// (JSON null) for a missing field rather than coercing it to zero.
type PODTEPODEntry struct {
	AvailableChargePower    *float64
	AvailableDischargePower *float64
	NomEnergyRemaining      *float64
	NomEnergyToBeCharged    *float64
	NomFullPackEnergy       *float64
	Device                  string
	ActiveHeating           int
	ChargeComplete          int
	ChargeRequest           int
	DischargeComplete       int
	PermanentlyFaulted      int
	PersistentlyFaulted     int
	EnableLine              int
}

// InverterFrequency holds one TEPINV (inverter) device's grid frequency and
// split-phase voltage readings, as reported by [Powerwall.Vitals].
type InverterFrequency struct {
	Device  string
	Fout    float64
	VSplit1 float64
	VSplit2 float64
}

// FrequencyView is the per-device frequency/voltage view backing the
// gopowerwall proxy's /freq route, built from [Powerwall.Vitals]. Inverters
// holds one entry per TEPINV device, sorted by device name for a
// deterministic order (Vitals itself is a map, so iteration order is
// otherwise unstable). SyncMeterFields carries every ISLAND*/METER*-prefixed
// field reported by a TESYNC or TEMSA (synchronizer/meter-aggregator)
// device, merged across every such device found. Its value type varies by
// field (float64, bool, or string depending on what the device reports), so
// - unlike the rest of this package's models - it is intentionally left
// untyped: there is no fixed schema to bind it to, the same reason
// [Powerwall.Vitals] itself reports each device as a map[string]any rather
// than a struct.
type FrequencyView struct {
	SyncMeterFields map[string]any
	Inverters       []InverterFrequency
}
