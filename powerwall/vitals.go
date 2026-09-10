package powerwall

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
)

func (p *Powerwall) Vitals(ctx context.Context) (models.VitalsData, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return models.VitalsData{}, ErrNoClient
	}

	res, err := p.client.Vitals(ctx)

	if err != nil {
		return models.VitalsData{}, err
	}

	devices := make(map[string]map[string]any, len(res))
	for k, v := range res {
		if devMap, ok := v.(map[string]any); ok {
			devices[k] = devMap
		}
	}

	return models.VitalsData{Devices: devices}, nil
}

// Temps returns each Powerwall's ambient temperature in Celsius, keyed by
// device name, read from the "THC_AmbientTemp" field of every device whose
// name starts with "TETHC" in [Powerwall.Vitals]. It returns an empty
// [models.PowerwallTemps] - never an error - if Vitals fails or the site
// has no TETHC devices; the two cases are not distinguishable from the
// result.
func (p *Powerwall) Temps(ctx context.Context) models.PowerwallTemps {
	vitals, err := p.Vitals(ctx)
	if err != nil || len(vitals.Devices) == 0 {
		return models.PowerwallTemps{Temps: make(map[string]float64)}
	}

	temps := make(map[string]float64, len(vitals.Devices))
	for dev, data := range vitals.Devices {
		if strings.HasPrefix(dev, "TETHC") {
			if t, ok := data["THC_AmbientTemp"].(float64); ok {
				temps[dev] = t
			}
		}
	}

	return models.PowerwallTemps{Temps: temps}
}

func collectDeviceAlerts(devices map[string]map[string]any, alertSet map[string]struct{}) {
	for _, data := range devices {
		switch rawAlerts := data["alerts"].(type) {
		case []any:
			// A JSON-decoded backend (e.g. cloud or fleetapi) yields []any.
			for _, a := range rawAlerts {
				if s, ok := a.(string); ok {
					alertSet[s] = struct{}{}
				} else {
					alertSet[fmt.Sprint(a)] = struct{}{}
				}
			}
		case []string:
			// The local backend stores the protobuf accessor's []string result
			// directly (see backend/local.go's devMap["alerts"] assignment).
			for _, a := range rawAlerts {
				alertSet[a] = struct{}{}
			}
		}
	}
}

func collectGridStatusAlert(gridStatus any, alertSet map[string]struct{}) {
	if gridStatus == nil {
		return
	}
	if lookup.Lookup(gridStatus, "grid_services_active") == true {
		alertSet["GridServicesActive"] = struct{}{}

		return
	}
	gStatus := lookup.Lookup(gridStatus, "grid_status")
	if gStatus == nil {
		return
	}
	if s, ok := gStatus.(string); ok {
		alertSet[s] = struct{}{}

		return
	}
	alertSet[fmt.Sprint(gStatus)] = struct{}{}
}

// Alerts returns the sorted, de-duplicated union of every device's alert
// list from [Powerwall.Vitals] plus a synthesized grid-status alert
// ("GridServicesActive" or the raw grid status string). Errors from the
// underlying Vitals and Poll calls are silently discarded; a disconnected
// Powerwall returns an empty (but non-nil) [models.AlertsList] rather than
// an error.
func (p *Powerwall) Alerts(ctx context.Context) models.AlertsList {
	alertSet := make(map[string]struct{})

	vitals, _ := p.Vitals(ctx)
	collectDeviceAlerts(vitals.Devices, alertSet)

	gridStatus := p.Poll(ctx, "/api/system_status/grid_status")
	collectGridStatusAlert(gridStatus, alertSet)

	list := make([]string, 0, len(alertSet))
	for a := range alertSet {
		norm := strings.ReplaceAll(a, "SystemGridConnected", "SystemConnectedToGrid")
		list = append(list, norm)
	}
	slices.Sort(list)

	return models.AlertsList{Alerts: list}
}

// lookupFloat retrieves m[key] as a float64, returning 0 if the key is
// absent or its value is neither a float64 nor an int - a missing key and a
// genuinely zero-valued field are therefore indistinguishable in the
// result. It exists to decode numeric fields out of the map[string]any
// per-device data [Powerwall.Vitals] returns.
func lookupFloat(m map[string]any, key string) float64 {
	v, _ := floatFromAny(m[key])

	return v
}

// lookupFloatPtr retrieves m[key] as a *float64, returning nil if the key is
// absent or its value is neither a float64 nor an int - unlike lookupFloat,
// it preserves the missing/zero distinction, matching pypowerwall's
// get_value(m, key), which returns raw None (JSON null) for a missing field
// rather than coercing it to zero (server.py:1132-1137, used throughout
// generate_pod's vitals-augmentation pass for the power/energy fields).
func lookupFloatPtr(m map[string]any, key string) *float64 {
	v, err := floatFromAny(m[key])
	if err != nil {
		return nil
	}

	return &v
}

// intOrZero coerces m[key] to an int, mirroring pypowerwall's
// int(get_value(v, key) or 0) coercion used throughout generate_pod's
// TEPOD vitals-augmentation pass (server.py:2196-2244): a missing, nil,
// false, or zero-valued field all yield 0, matching Python's falsy-or-0
// fallback; any other value is coerced to its int equivalent rather than
// clamped to 1, since upstream's "or 0" only substitutes a default and
// otherwise passes the field through as-is.
func intOrZero(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case bool:
		if v {
			return 1
		}

		return 0
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

// solarStringLabels are the possible PV string labels vitals fields carry.
// PW3 gateways report up to six strings, A-F; PW2 gateways report at most
// four, A-D. See pypowerwall's tedapi vitals synthesis
// (pypowerwall/tedapi/__init__.py:1030, "PW3 has 6 strings A-F") and its
// device_controller esCan.bus.PVAC fields (A-D only). Iterating the full A-F
// superset is safe for both: a label a given device does not report is
// simply absent from its vitals map and skipped (see hasStringLabel below).
//
//nolint:gochecknoglobals // Read-only constant table, not mutated.
var solarStringLabels = []string{"A", "B", "C", "D", "E", "F"}

// hasStringLabel reports whether data carries any per-string vitals field
// for label, so [Powerwall.Strings] does not fabricate an all-zero entry for
// a label a device never reported (e.g. E/F on a 4-string PW2 gateway).
func hasStringLabel(data map[string]any, label string) bool {
	suffixes := []string{
		"PVAC_PVMeasuredVoltage_",
		"PVAC_PVCurrent_",
		"PVAC_PVMeasuredPower_",
		"PVAC_PvState_",
	}
	for _, suffix := range suffixes {
		if _, ok := data[suffix+label]; ok {
			return true
		}
	}

	return false
}

// Strings returns per-solar-string measurements (voltage, current, power,
// state, and connected status), keyed by "<PVAC device name>_<label>" so
// that a site with more than one PVAC inverter keeps each device's strings
// distinct rather than colliding - pypowerwall's own upstream strings()
// keys on a letter derived from the field name plus a rotating per-device
// index instead (pypowerwall/__init__.py:495-549), a scheme this package
// has no direct equivalent for since Go's vitals map does not preserve
// PVAC-device iteration order; "<device>_<label>" is the simplest
// non-colliding choice that still preserves device identity.
//
// Each field is read from the real gateway vitals field names upstream
// produces: PVAC_PVMeasuredVoltage_<label>, PVAC_PVCurrent_<label>, and
// PVAC_PVMeasuredPower_<label> for voltage/current/power,
// PVAC_PvState_<label> for the raw PV state string, and
// PVS_String<label>_Connected - read from the sibling "PVS" device sharing
// the same device-name suffix as the PVAC device - for Connected. That
// mirrors pypowerwall/__init__.py:497-549's own field scan (it merges the
// PVS device's "*String*" fields into the PVAC device's dict before
// scanning) and pypowerwall/tedapi/__init__.py:1032-1069's TEDAPI-mode
// synthesis of those same field names from raw PCH_Pv* signals. Connected
// is read verbatim from that field rather than re-derived from State: on
// TEDAPI it was itself derived from state ("Pv_Active" in state) by the
// backend's own vitals synthesis, but on local firmware it is an
// independent hardware reading, so Powerwall.Strings must not recompute it.
//
// Only [ModeLocal], [ModeTEDAPI], and [ModeV1r] populate this (see
// [Powerwall.Vitals]); other modes, and any failure of the underlying
// Vitals call, silently return an empty (but non-nil) [models.SolarStrings].
func (p *Powerwall) Strings(ctx context.Context) models.SolarStrings {
	strMap := make(map[string]models.StringMetric)
	vitals, _ := p.Vitals(ctx)

	for dev, data := range vitals.Devices {
		if !strings.HasPrefix(dev, "PVAC") {
			continue
		}

		// The sibling PVS device shares everything after the "PVAC" prefix
		// in its own name (e.g. "PVAC--1" / "PVS--1"), mirroring
		// pypowerwall/__init__.py:509's `"PVS" + str(device)[4:]`.
		pvsData := vitals.Devices["PVS"+dev[len("PVAC"):]]

		for _, label := range solarStringLabels {
			if !hasStringLabel(data, label) {
				continue
			}

			var connected bool
			if pvsData != nil {
				connected, _ = pvsData["PVS_String"+label+"_Connected"].(bool)
			}

			state, _ := data["PVAC_PvState_"+label].(string)

			key := dev + "_" + label
			strMap[key] = models.StringMetric{
				Connected: connected,
				Voltage:   lookupFloat(data, "PVAC_PVMeasuredVoltage_"+label),
				Current:   lookupFloat(data, "PVAC_PVCurrent_"+label),
				Power:     lookupFloat(data, "PVAC_PVMeasuredPower_"+label),
				State:     state,
			}
		}
	}

	return models.SolarStrings{Strings: strMap}
}

// BatteryBlocks returns per-battery-module data from "/api/system_status",
// keyed by package serial number. It never returns an error: a
// disconnected Powerwall, a poll failure, a missing "battery_blocks" field,
// or a block with an empty serial number (silently dropped rather than
// added under an empty key) all just shrink or empty the returned map.
func (p *Powerwall) BatteryBlocks(ctx context.Context) map[string]models.BatteryBlock {
	sys := p.Poll(ctx, "/api/system_status")
	if sys == nil {
		return make(map[string]models.BatteryBlock)
	}

	blocks, ok := lookup.Lookup(sys, "battery_blocks").([]any)
	if !ok {
		return make(map[string]models.BatteryBlock)
	}

	res := make(map[string]models.BatteryBlock, len(blocks))

	for _, b := range blocks {
		raw, err := json.Marshal(b)
		if err != nil {
			continue
		}
		var block models.BatteryBlock
		if unmarshalErr := json.Unmarshal(raw, &block); unmarshalErr == nil && block.PackageSerialNumber != "" {
			res[block.PackageSerialNumber] = block
		}
	}

	return res
}

// GetFanSpeeds returns the raw cooling-fan speed readings the active
// TEDAPI/v1r client reports, keyed by synthesized PVAC device name -
// backing the gopowerwall proxy's /fans and /fans/pw routes, mirroring
// pypowerwall's `pw.tedapi.get_fan_speeds() if pw.tedapi else {}`
// (server.py:2483-2500). It is empty for every other connection mode,
// matching upstream's own `pw.tedapi` falsy gate. See
// [github.com/blackbirdworks/gopowerwall/powerwall/tedapi.ExtractFanSpeeds]'s
// doc comment for a static ambiguity in upstream's own query text that
// means this may legitimately return empty even in TEDAPI/v1r mode on real
// hardware - this method faithfully reproduces that upstream behavior
// rather than working around it.
func (p *Powerwall) GetFanSpeeds(ctx context.Context) map[string]models.FanSpeedEntry {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi == nil {
		return map[string]models.FanSpeedEntry{}
	}

	return p.tedapi.GetFanSpeeds(ctx, false)
}
