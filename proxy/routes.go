package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/blackbirdworks/gopowerwall"
	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/version"
)

var (
	errNoData       = errors.New("no data")
	errNoSOE        = errors.New("no soe")
	errNoLevel      = errors.New("no level")
	errNoGridStatus = errors.New("no grid status")
	errNoVitals     = errors.New("no vitals")
	errNoStrings    = errors.New("no strings")
	errNoResponse   = errors.New("no response")
)

// aggregatesOptions builds the [gopowerwall.AggregatesOption] values
// carrying this server's configured corrections, so every route deriving
// power figures from meter data (aggregates, CSV, JSON) applies the same
// site-zero threshold and negative-solar correction.
func (s *Server) aggregatesOptions() []gopowerwall.AggregatesOption {
	return []gopowerwall.AggregatesOption{
		gopowerwall.WithSiteZeroThreshold(float64(s.Config.SiteZeroThreshold)),
		gopowerwall.WithNegativeSolarCorrection(!s.Config.NegSolar),
	}
}

func (s *Server) generateAggregates(ctx context.Context) (string, error) {
	agg, ok := s.safePWCall(ctx, "/aggregates", func() (any, error) {
		return s.PW.Aggregates(ctx, s.aggregatesOptions()...)
	})
	if !ok {
		return "", errNoData
	}

	b, err := json.Marshal(agg)

	return string(b), err
}

func (s *Server) formatV2CSVRow(
	ctx context.Context,
	includeHeaders bool,
	grid, home, solar, battery, batLevel float64,
) string {
	var sb strings.Builder
	if includeHeaders {
		sb.WriteString("Grid,Home,Solar,Battery,BatteryLevel,GridStatus,Reserve\n")
	}
	gridStatus, _ := s.PW.GridStatusNumeric(ctx)
	reserve, _ := s.PW.GetReserve(ctx)
	fmt.Fprintf(&sb, "%0.2f,%0.2f,%0.2f,%0.2f,%0.2f,%d,%d\n",
		grid, home, solar, battery, batLevel, gridStatus, int(reserve))

	return sb.String()
}

func (s *Server) formatV1CSVRow(
	_ context.Context,
	includeHeaders bool,
	grid, home, solar, battery, batLevel float64,
) string {
	var sb strings.Builder
	if includeHeaders {
		sb.WriteString("Grid,Home,Solar,Battery,BatteryLevel\n")
	}
	fmt.Fprintf(&sb, "%0.2f,%0.2f,%0.2f,%0.2f,%0.2f\n",
		grid, home, solar, battery, batLevel)

	return sb.String()
}

func (s *Server) generateCSV(ctx context.Context, isV2, includeHeaders bool) (string, error) {
	snap := s.PW.Snapshot(ctx, s.aggregatesOptions()...)
	batLevel, _ := s.PW.Level(ctx)

	if isV2 {
		return s.formatV2CSVRow(ctx, includeHeaders, snap.Grid, snap.Home, snap.Solar, snap.Battery, batLevel), nil
	}

	return s.formatV1CSVRow(ctx, includeHeaders, snap.Grid, snap.Home, snap.Solar, snap.Battery, batLevel), nil
}

func (s *Server) generateFreq(ctx context.Context) (string, error) {
	fcv := make(map[string]any)
	rawSys, _ := s.PW.SystemStatus(ctx)
	for idx, block := range rawSys.BatteryBlocks {
		pNum := idx + 1
		fcv[fmt.Sprintf("PW%d_name", pNum)] = nil
		fcv[fmt.Sprintf("PW%d_PINV_Fout", pNum)] = block.FOut
		fcv[fmt.Sprintf("PW%d_PINV_VSplit1", pNum)] = nil
		fcv[fmt.Sprintf("PW%d_PINV_VSplit2", pNum)] = nil
		fcv[fmt.Sprintf("PW%d_PackagePartNumber", pNum)] = block.PackagePartNumber
		fcv[fmt.Sprintf("PW%d_PackageSerialNumber", pNum)] = block.PackageSerialNumber
		fcv[fmt.Sprintf("PW%d_p_out", pNum)] = block.POut
		fcv[fmt.Sprintf("PW%d_q_out", pNum)] = block.QOut
		fcv[fmt.Sprintf("PW%d_v_out", pNum)] = block.VOut
		fcv[fmt.Sprintf("PW%d_f_out", pNum)] = block.FOut
		fcv[fmt.Sprintf("PW%d_i_out", pNum)] = block.IOut
	}

	freq := s.PW.FrequencyView(ctx)
	for invIdx, inv := range freq.Inverters {
		pNum := invIdx + 1
		fcv[fmt.Sprintf("PW%d_name", pNum)] = inv.Device
		fcv[fmt.Sprintf("PW%d_PINV_Fout", pNum)] = inv.Fout
		fcv[fmt.Sprintf("PW%d_PINV_VSplit1", pNum)] = inv.VSplit1
		fcv[fmt.Sprintf("PW%d_PINV_VSplit2", pNum)] = inv.VSplit2
	}
	maps.Copy(fcv, freq.SyncMeterFields)

	gridStatus, _ := s.PW.GridStatusNumeric(ctx)
	fcv["grid_status"] = gridStatus
	b, err := json.Marshal(fcv)

	return string(b), err
}

// applyPODTEPODVitals overwrites pod's PW{idx}_* keys with each TEPOD
// vitals entry's data, mirroring pypowerwall's own second, independent
// /pod loop (server.py:2196-2244, "Augment with Vitals Data"): every TEPOD
// vitals device overwrites the keys the per-block loop seeded, at its own
// 1-based index derived from [gopowerwall.Powerwall.PODView]'s
// vitals-iteration order rather than the block loop's index - see
// PODView's own doc comment for why upstream (and this port) trust that
// ordering to line up instead of matching by DIN.
func applyPODTEPODVitals(pod map[string]any, entries []models.PODTEPODEntry) {
	for idx, entry := range entries {
		prefix := fmt.Sprintf("PW%d_", idx+1)
		pod[prefix+"name"] = entry.Device
		pod[prefix+"POD_ActiveHeating"] = entry.ActiveHeating
		pod[prefix+"POD_ChargeComplete"] = entry.ChargeComplete
		pod[prefix+"POD_ChargeRequest"] = entry.ChargeRequest
		pod[prefix+"POD_DischargeComplete"] = entry.DischargeComplete
		pod[prefix+"POD_PermanentlyFaulted"] = entry.PermanentlyFaulted
		pod[prefix+"POD_PersistentlyFaulted"] = entry.PersistentlyFaulted
		pod[prefix+"POD_enable_line"] = entry.EnableLine
		pod[prefix+"POD_available_charge_power"] = entry.AvailableChargePower
		pod[prefix+"POD_available_dischg_power"] = entry.AvailableDischargePower
		pod[prefix+"POD_nom_energy_remaining"] = entry.NomEnergyRemaining
		pod[prefix+"POD_nom_energy_to_be_charged"] = entry.NomEnergyToBeCharged
		pod[prefix+"POD_nom_full_pack_energy"] = entry.NomFullPackEnergy
	}
}

func (s *Server) generatePOD(ctx context.Context) (string, error) {
	view := s.PW.PODView(ctx)
	pod := make(map[string]any)
	for idx, block := range view.Blocks {
		prefix := fmt.Sprintf("PW%d_", idx+1)
		pod[prefix+"name"] = nil
		pod[prefix+"POD_ActiveHeating"] = nil
		pod[prefix+"POD_ChargeComplete"] = nil
		pod[prefix+"POD_ChargeRequest"] = nil
		pod[prefix+"POD_DischargeComplete"] = nil
		pod[prefix+"POD_PermanentlyFaulted"] = nil
		pod[prefix+"POD_PersistentlyFaulted"] = nil
		pod[prefix+"POD_enable_line"] = nil
		pod[prefix+"POD_available_charge_power"] = nil
		pod[prefix+"POD_available_dischg_power"] = nil
		pod[prefix+"POD_nom_energy_remaining"] = block.NominalEnergyRemaining
		pod[prefix+"POD_nom_energy_to_be_charged"] = nil
		pod[prefix+"POD_nom_full_pack_energy"] = block.NominalFullPackEnergy
		pod[prefix+"PackagePartNumber"] = block.PackagePartNumber
		pod[prefix+"PackageSerialNumber"] = block.PackageSerialNumber
		pod[prefix+"pinv_state"] = block.PinvState
		pod[prefix+"pinv_grid_state"] = block.PinvGridState
		pod[prefix+"p_out"] = block.POut
		pod[prefix+"q_out"] = block.QOut
		pod[prefix+"v_out"] = block.VOut
		pod[prefix+"f_out"] = block.FOut
		pod[prefix+"i_out"] = block.IOut
		pod[prefix+"energy_charged"] = block.EnergyCharged
		pod[prefix+"energy_discharged"] = block.EnergyDischarged
		pod[prefix+"off_grid"] = block.OffGrid
		pod[prefix+"vf_mode"] = block.VFMode
		pod[prefix+"wobble_detected"] = block.WobbleDetected
		pod[prefix+"charge_power_clamped"] = block.ChargePowerClamped
		pod[prefix+"backup_ready"] = block.BackupReady
		pod[prefix+"OpSeqState"] = block.OpSeqState
		pod[prefix+keyVersion] = block.Version
	}
	applyPODTEPODVitals(pod, view.TEPODEntries)
	pod["nominal_full_pack_energy"] = view.NominalFullPackEnergy
	pod["nominal_energy_remaining"] = view.NominalEnergyRemaining
	pod["time_remaining_hours"] = view.TimeRemainingHours
	pod["backup_reserve_percent"] = view.BackupReservePercent
	b, err := json.Marshal(pod)

	return string(b), err
}

func (s *Server) generateJSON(ctx context.Context) (string, error) {
	snap := s.PW.Snapshot(ctx, s.aggregatesOptions()...)

	gridStatusNumeric := 0
	if snap.GridConnected {
		gridStatusNumeric = 1
	}

	out := map[string]any{
		"grid":                 snap.Grid,
		"home":                 snap.Home,
		"solar":                snap.Solar,
		"battery":              snap.Battery,
		"soe":                  snap.BatteryLevel,
		"grid_status":          gridStatusNumeric,
		keyReserve:             snap.Reserve,
		"time_remaining_hours": snap.TimeRemaining.Hours(),
		"full_pack_energy":     snap.FullPackEnergy,
		"energy_remaining":     snap.EnergyRemaining,
		"strings":              solarStringsJSON(snap.Strings),
	}
	b, err := json.Marshal(out)

	return string(b), err
}

func (s *Server) handleCoreAPIRoutes(ctx context.Context, w http.ResponseWriter, reqPath string) bool {
	switch reqPath {
	case "/aggregates", "/api/meters/aggregates":
		msg, ok := s.cachedRouteHandler(ctx, "/aggregates", s.generateAggregates)
		s.respond(ctx, w, reqPath, "application/json", msg, ok)

		return true

	case "/soe":
		raw, ok := s.safePWCall(ctx, "/soe", func() (any, error) {
			str := s.PW.PollJSON(ctx, "/api/system_status/soe")
			if str == "" {
				return nil, errNoSOE
			}

			return str, nil
		})
		str, _ := raw.(string)
		s.respond(ctx, w, reqPath, "application/json", str, ok && str != "")

		return true

	case "/api/system_status/soe":
		raw, ok := s.safePWCall(ctx, "/api/system_status/soe", func() (any, error) {
			lvl, err := s.PW.LevelScaled(ctx)
			if err != nil {
				return nil, errNoLevel
			}

			return fmt.Sprintf(`{"percentage": %v}`, lvl), nil
		})
		str, _ := raw.(string)
		s.respond(ctx, w, reqPath, "application/json", str, ok && str != "")

		return true

	case "/api/system_status/grid_status":
		raw, ok := s.safePWCall(ctx, "/api/system_status/grid_status", func() (any, error) {
			str := s.PW.PollJSON(ctx, "/api/system_status/grid_status")
			if str == "" {
				return nil, errNoGridStatus
			}

			return str, nil
		})
		str, _ := raw.(string)
		s.respond(ctx, w, reqPath, "application/json", str, ok && str != "")

		return true

	default:
		return false
	}
}

func (s *Server) handleMetricsJSONRoutes(ctx context.Context, w http.ResponseWriter, reqPath string) bool {
	switch reqPath {
	case "/freq":
		msg, ok := s.cachedRouteHandler(ctx, "/freq", s.generateFreq)
		s.respond(ctx, w, reqPath, "application/json", msg, ok)

		return true

	case "/pod":
		msg, ok := s.cachedRouteHandler(ctx, "/pod", s.generatePOD)
		s.respond(ctx, w, reqPath, "application/json", msg, ok)

		return true

	case "/json":
		msg, ok := s.cachedRouteHandler(ctx, "/json", s.generateJSON)
		s.respond(ctx, w, reqPath, "application/json", msg, ok)

		return true

	default:
		return false
	}
}

func (s *Server) handleVitals(ctx context.Context, w http.ResponseWriter, reqPath string) {
	msg, ok := s.cachedRouteHandler(ctx, "/vitals", func(ctx context.Context) (string, error) {
		raw, _ := s.safePWCall(ctx, "/vitals", func() (any, error) {
			v, err := s.PW.Vitals(ctx)
			if err != nil {
				return nil, err
			}
			b, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}

			return string(b), nil
		})
		if str, okStr := raw.(string); okStr && str != "" {
			return str, nil
		}

		return "", errNoVitals
	})
	s.respond(ctx, w, reqPath, "application/json", msg, ok)
}

// solarStringsJSON converts the client's idiomatic [models.SolarStrings]
// into the flat shape pypowerwall's own /strings, /json ("strings" field),
// and /pw/strings routes emit: no top-level "strings" wrapper, and each
// entry's fields capitalized (Connected/Voltage/Current/Power/State)
// exactly as pypowerwall's strings() dict keys them
// (pypowerwall/__init__.py:497-549) - the proxy owns this parity
// serialization so the client can keep idiomatic Go field names
// (models.StringMetric's own lowercase json tags) for every other caller.
// The outer map key remains gopowerwall's own "<device>_<label>" scheme
// (see [gopowerwall.Powerwall.Strings]) rather than upstream's
// letter-plus-rotating-device-index keys, since Go's vitals map does not
// preserve the PVAC-device iteration order that scheme depends on.
func solarStringsJSON(ss models.SolarStrings) map[string]any {
	out := make(map[string]any, len(ss.Strings))
	for key, m := range ss.Strings {
		out[key] = map[string]any{
			"Connected": m.Connected,
			"Voltage":   m.Voltage,
			"Current":   m.Current,
			"Power":     m.Power,
			"State":     m.State,
		}
	}

	return out
}

func (s *Server) handleStrings(ctx context.Context, w http.ResponseWriter, reqPath string) {
	msg, ok := s.cachedRouteHandler(ctx, "/strings", func(ctx context.Context) (string, error) {
		raw, _ := s.safePWCall(ctx, "/strings", func() (any, error) {
			v := solarStringsJSON(s.PW.Strings(ctx))
			b, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}

			return string(b), nil
		})
		if str, okStr := raw.(string); okStr && str != "" {
			return str, nil
		}

		return "", errNoStrings
	})
	s.respond(ctx, w, reqPath, "application/json", msg, ok)
}

func (s *Server) handleMetricsStatusRoutes(ctx context.Context, w http.ResponseWriter, reqPath string) bool {
	switch reqPath {
	case "/vitals":
		s.handleVitals(ctx, w, reqPath)

		return true

	case "/strings":
		s.handleStrings(ctx, w, reqPath)

		return true

	case "/temps":
		raw := s.PW.Temps(ctx)
		b, _ := json.Marshal(raw)
		s.respond(ctx, w, reqPath, "application/json", string(b), true)

		return true

	case "/temps/pw":
		msg, ok := s.cachedRouteHandler(ctx, "/temps/pw", s.generatePWTemps)
		s.respond(ctx, w, reqPath, "application/json", msg, ok)

		return true

	case "/alerts":
		raw := s.PW.Alerts(ctx)
		b, _ := json.Marshal(raw)
		s.respond(ctx, w, reqPath, "application/json", string(b), true)

		return true

	case "/alerts/pw":
		msg, ok := s.cachedRouteHandler(ctx, "/alerts/pw", s.generatePWAlerts)
		s.respond(ctx, w, reqPath, "application/json", msg, ok)

		return true

	case "/fans":
		raw := s.PW.GetFanSpeeds(ctx)
		b, _ := json.Marshal(raw)
		s.respond(ctx, w, reqPath, "application/json", string(b), true)

		return true

	case "/fans/pw":
		raw := fanSpeedsPWJSON(s.PW.GetFanSpeeds(ctx))
		b, _ := json.Marshal(raw)
		s.respond(ctx, w, reqPath, "application/json", string(b), true)

		return true

	default:
		return false
	}
}

// fanSpeedsPWJSON flattens the client's [models.FanSpeedEntry] map into the
// simplified FAN{i}_actual/FAN{i}_target shape pypowerwall's /fans/pw route
// emits (server.py:2488-2500): 1-based, ordered by sorting the fan-speed
// map's own device-name keys - not by any inherent index, since the raw
// map carries no ordering of its own on either side. A field is JSON null
// (rather than absent) when the corresponding device never reported that
// particular signal, matching upstream's `value.get(...)` returning None.
func fanSpeedsPWJSON(speeds map[string]models.FanSpeedEntry) map[string]any {
	keys := make([]string, 0, len(speeds))
	for k := range speeds {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]any, len(keys)*2) //nolint:mnd // two output keys (_actual/_target) per device.
	for i, k := range keys {
		entry := speeds[k]
		prefix := fmt.Sprintf("FAN%d", i+1)
		out[prefix+"_actual"] = entry.ActualRPM
		out[prefix+"_target"] = entry.TargetRPM
	}

	return out
}

func (s *Server) handleSystemManagementRoutes(ctx context.Context, w http.ResponseWriter, reqPath string) bool {
	switch reqPath {
	case "/stats":
		s.handleStats(ctx, w)

		return true

	case "/stats/clear":
		s.statsMu.Lock()
		s.statsGets = 0
		s.statsErr = 0
		s.statsURI = make(map[string]int)
		s.ClearTime = time.Now()
		s.statsMu.Unlock()
		s.handleStats(ctx, w)

		return true

	case "/health":
		s.handleHealth(ctx, w)

		return true

	case "/health/reset":
		s.Health.Reset()
		cleared := s.DegradedCache.Clear()
		epCleared := s.EndpointStats.Reset()
		_ = json.NewEncoder(w).Encode(map[string]any{
			keyStatus:                "reset_complete",
			"health_counters_reset":  s.Config.HealthCheckEnabled,
			"cache_cleared":          s.Config.GracefulDegradation,
			"cache_entries_removed":  cleared,
			"endpoint_stats_cleared": epCleared,
		})

		return true

	case "/version":
		s.handleVersionRoute(ctx, w)

		return true

	case "/help":
		s.handleHelp(ctx, w)

		return true

	case "/api/troubleshooting/problems":
		s.respond(ctx, w, reqPath, "application/json", `{"problems": []}`, true)

		return true

	default:
		return false
	}
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request, reqPath string) {
	ctx := r.Context()
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if s.handleCoreAPIRoutes(ctx, w, reqPath) ||
		s.handleMetricsStatusRoutes(ctx, w, reqPath) ||
		s.handleMetricsJSONRoutes(ctx, w, reqPath) ||
		s.handleSystemManagementRoutes(ctx, w, reqPath) {
		return
	}

	switch {
	case strings.HasPrefix(reqPath, "/csv"):
		s.handleCSVRoute(w, r, reqPath)
	case strings.HasPrefix(reqPath, "/tedapi"):
		s.handleTedapiRoute(ctx, w, reqPath)
	case strings.HasPrefix(reqPath, "/control/"):
		s.handleControlGetRoute(ctx, w, reqPath)
	case strings.HasPrefix(reqPath, "/pw/"):
		s.handlePWFacing(ctx, w, reqPath)
	case isDisabled(reqPath):
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{keyStatus: "404 Response - API Disabled"})
		s.recordStats(ctx, reqPath, false, false)
	case isAllowlisted(reqPath):
		s.handleAllowlistRoute(ctx, w, reqPath)
	default:
		s.handleWeb(w, r, reqPath)
	}
}

func (s *Server) handleCSVRoute(w http.ResponseWriter, r *http.Request, reqPath string) {
	ctx := r.Context()
	isV2 := strings.HasPrefix(reqPath, "/csv/v2")
	includeHeaders := strings.Contains(r.URL.RawQuery, "headers") || strings.Contains(reqPath, "headers")
	cacheKey := "/csv"
	if isV2 {
		cacheKey = "/csv/v2"
	}
	if includeHeaders {
		cacheKey += "_headers"
	}

	msg, ok := s.cachedRouteHandler(ctx, cacheKey, func(ctx context.Context) (string, error) {
		return s.generateCSV(ctx, isV2, includeHeaders)
	})
	s.respond(ctx, w, reqPath, "text/plain; charset=utf-8", msg, ok)
}

func (s *Server) generatePWTemps(ctx context.Context) (string, error) {
	raw := s.PW.Temps(ctx)
	pwtemp := make(map[string]any)
	idx := 1
	keys := make([]string, 0, len(raw.Temps))
	for k := range raw.Temps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		pwtemp[fmt.Sprintf("PW%d_temp", idx)] = raw.Temps[k]
		idx++
	}
	b, err := json.Marshal(pwtemp)

	return string(b), err
}

func (s *Server) generatePWAlerts(ctx context.Context) (string, error) {
	raw := s.PW.Alerts(ctx)
	pwalerts := make(map[string]int)
	for _, a := range raw.Alerts {
		pwalerts[a] = 1
	}
	b, err := json.Marshal(pwalerts)

	return string(b), err
}

func (s *Server) handleVersionRoute(ctx context.Context, w http.ResponseWriter) {
	verStr, err := s.PW.Version(ctx)
	if err != nil || verStr == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{
			keyVersion: "SolarOnly",
			"vint":     0,
		})

		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		keyVersion: verStr,
		"vint":     version.ParseVersion(verStr),
	})
}

func (s *Server) handleTedapiRoute(ctx context.Context, w http.ResponseWriter, reqPath string) {
	if !s.PW.IsTEDAPI() {
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "TEDAPI not enabled"})

		return
	}
	switch reqPath {
	case "/tedapi/config":
		cfg, _ := s.PW.GetFileStoreConfig(ctx)
		_ = json.NewEncoder(w).Encode(cfg)
	default:
		_ = json.NewEncoder(w).Encode(map[string]string{
			keyError: "Use /tedapi/config, /tedapi/status, /tedapi/components, /tedapi/battery, /tedapi/controller",
		})
	}
}

func (s *Server) handleControlGetRoute(ctx context.Context, w http.ResponseWriter, reqPath string) {
	switch {
	case strings.HasPrefix(reqPath, "/control/reserve"):
		res, err := s.PW.GetReserve(ctx)
		_ = json.NewEncoder(w).Encode(map[string]any{keyReserve: orNil(res, err)})
	case strings.HasPrefix(reqPath, "/control/mode"):
		res, err := s.PW.GetMode(ctx)
		_ = json.NewEncoder(w).Encode(map[string]any{keyMode: orNil(res, err)})
	case strings.HasPrefix(reqPath, "/control/grid_charging"):
		res, err := s.PW.GetGridCharging(ctx)
		_ = json.NewEncoder(w).Encode(map[string]any{keyGridCharging: orNil(res, err)})
	case strings.HasPrefix(reqPath, "/control/grid_export"):
		res, err := s.PW.GetGridExport(ctx)
		_ = json.NewEncoder(w).Encode(map[string]any{keyGridExport: orNil(res, err)})
	case strings.HasPrefix(reqPath, "/control/max_backup"):
		if !s.PW.IsTEDAPI() {
			_ = json.NewEncoder(w).Encode(map[string]string{keyError: "max_backup requires v1r LAN transport"})

			return
		}
		events, err := s.PW.GetBackupEvents(ctx)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to get backup events"})

			return
		}
		_ = json.NewEncoder(w).Encode(events)
	}
}

func (s *Server) handleAllowlistRoute(ctx context.Context, w http.ResponseWriter, reqPath string) {
	raw, ok := s.safePWCall(ctx, reqPath, func() (any, error) {
		str := s.PW.PollJSON(ctx, reqPath)
		if str == "" {
			return nil, errNoResponse
		}

		return str, nil
	})
	str, _ := raw.(string)
	s.respond(ctx, w, reqPath, "application/json", str, ok && str != "")
}
