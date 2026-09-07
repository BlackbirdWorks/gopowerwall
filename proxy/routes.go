package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/blackbirdworks/gopowerwall"
	"github.com/blackbirdworks/gopowerwall/pkgs/version"
)

var (
	errNoData       = errors.New("no data")
	errInvalidJSON  = errors.New("invalid json")
	errNoSOE        = errors.New("no soe")
	errNoLevel      = errors.New("no level")
	errNoGridStatus = errors.New("no grid status")
	errNoVitals     = errors.New("no vitals")
	errNoStrings    = errors.New("no strings")
	errNoResponse   = errors.New("no response")
)

func (s *Server) generateAggregates(ctx context.Context) (string, error) {
	raw, ok := s.safePWCall(ctx, "/aggregates", func() (any, error) {
		res := s.PW.Poll(ctx, "/api/meters/aggregates")
		if res == nil {
			return nil, errNoData
		}

		return res, nil
	})
	if !ok || raw == nil {
		return "", errNoData
	}

	var agg map[string]any
	if m, isMap := raw.(map[string]any); isMap {
		agg = m
	} else if str, isStr := raw.(string); isStr {
		_ = json.Unmarshal([]byte(str), &agg)
	}
	if agg == nil {
		return "", errInvalidJSON
	}

	s.applySiteZeroThreshold(ctx, agg)
	s.applyNegativeSolarCorrection(ctx, agg)

	b, err := json.Marshal(agg)

	return string(b), err
}

func (s *Server) applySiteZeroThreshold(_ context.Context, agg map[string]any) {
	if s.Config.SiteZeroThreshold <= 0 {
		return
	}
	site, ok := agg["site"].(map[string]any)
	if !ok {
		return
	}
	ip, hasIP := site["instant_power"].(float64)
	thresh := float64(s.Config.SiteZeroThreshold)
	if hasIP && ip >= -thresh && ip <= thresh {
		site["instant_power"] = 0.0
	}
}

func (s *Server) applyNegativeSolarCorrection(_ context.Context, agg map[string]any) {
	if s.Config.NegSolar {
		return
	}
	solar, ok := agg["solar"].(map[string]any)
	if !ok {
		return
	}
	ip, hasIP := solar["instant_power"].(float64)
	if !hasIP || ip >= 0 {
		return
	}

	if load, hasLoad := agg["load"].(map[string]any); hasLoad {
		if lip, okL := load["instant_power"].(float64); okL {
			load["instant_power"] = lip - ip
		}
	}
	solar["instant_power"] = 0.0
}

func (s *Server) extractCSVMeters(_ context.Context, rawAgg any) (float64, float64, float64, float64) {
	agg, ok := rawAgg.(map[string]any)
	if !ok {
		return 0, 0, 0, 0
	}

	extractVal := func(key string) float64 {
		m, okM := agg[key].(map[string]any)
		if !okM {
			return 0
		}
		v, okV := m["instant_power"].(float64)
		if !okV {
			return 0
		}

		return v
	}

	return extractVal("site"), extractVal("solar"), extractVal("battery"), extractVal("load")
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
	gridStatus, _ := s.PW.GridStatus(ctx, gopowerwall.GridStatusNumeric).(int)
	reserve := 0.0
	if r := s.PW.GetReserve(ctx, false); r != nil {
		reserve = *r
	}
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
	rawAgg, _ := s.safePWCall(ctx, "/aggregates", func() (any, error) {
		res := s.PW.Poll(ctx, "/api/meters/aggregates")
		if res == nil {
			return nil, errNoData
		}

		return res, nil
	})

	grid, solar, battery, home := s.extractCSVMeters(ctx, rawAgg)

	if !s.Config.NegSolar && solar < 0 {
		home -= solar
		solar = 0
	}
	thresh := float64(s.Config.SiteZeroThreshold)
	if s.Config.SiteZeroThreshold > 0 && grid >= -thresh && grid <= thresh {
		grid = 0
	}

	batLevel := 0.0
	if lvl := s.PW.Level(ctx, false); lvl != nil {
		batLevel = *lvl
	}

	if isV2 {
		return s.formatV2CSVRow(ctx, includeHeaders, grid, home, solar, battery, batLevel), nil
	}

	return s.formatV1CSVRow(ctx, includeHeaders, grid, home, solar, battery, batLevel), nil
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

	rawVitals, _ := s.PW.Vitals(ctx)
	invIdx := 1
	for device, d := range rawVitals.Devices {
		if strings.HasPrefix(device, "TEPINV") {
			fcv[fmt.Sprintf("PW%d_name", invIdx)] = device
			fcv[fmt.Sprintf("PW%d_PINV_Fout", invIdx)] = d["PINV_Fout"]
			fcv[fmt.Sprintf("PW%d_PINV_VSplit1", invIdx)] = d["PINV_VSplit1"]
			fcv[fmt.Sprintf("PW%d_PINV_VSplit2", invIdx)] = d["PINV_VSplit2"]
			invIdx++
		}
		if strings.HasPrefix(device, "TESYNC") || strings.HasPrefix(device, "TEMSA") {
			for k, v := range d {
				if strings.HasPrefix(k, "ISLAND") || strings.HasPrefix(k, "METER") {
					fcv[k] = v
				}
			}
		}
	}
	fcv["grid_status"] = s.PW.GridStatus(ctx, gopowerwall.GridStatusNumeric)
	b, err := json.Marshal(fcv)

	return string(b), err
}

func (s *Server) generatePOD(ctx context.Context) (string, error) {
	pod := make(map[string]any)
	rawSys, _ := s.PW.SystemStatus(ctx)
	for idx, block := range rawSys.BatteryBlocks {
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
	pod["nominal_full_pack_energy"] = rawSys.NominalFullPackEnergy
	pod["nominal_energy_remaining"] = rawSys.NominalEnergyRemaining
	pod["time_remaining_hours"] = s.PW.GetTimeRemaining(ctx)
	pod["backup_reserve_percent"] = s.PW.GetReserve(ctx, false)
	b, err := json.Marshal(pod)

	return string(b), err
}

func (s *Server) generateJSON(ctx context.Context) (string, error) {
	pwr := s.PW.Power(ctx)
	grid := pwr.Grid
	solar := pwr.Solar
	battery := pwr.Battery
	home := pwr.Home

	if !s.Config.NegSolar && solar < 0 {
		home -= solar
		solar = 0
	}
	thresh := float64(s.Config.SiteZeroThreshold)
	if s.Config.SiteZeroThreshold > 0 && grid >= -thresh && grid <= thresh {
		grid = 0
	}

	batLevel := 0.0
	if lvl := s.PW.Level(ctx, false); lvl != nil {
		batLevel = *lvl
	}
	gridStatus, _ := s.PW.GridStatus(ctx, gopowerwall.GridStatusNumeric).(int)
	reserve := 0.0
	if r := s.PW.GetReserve(ctx, false); r != nil {
		reserve = *r
	}
	timeRemaining := 0.0
	if tr := s.PW.GetTimeRemaining(ctx); tr != nil {
		timeRemaining = *tr
	}

	rawSys, _ := s.PW.SystemStatus(ctx)
	fullEnergy := rawSys.NominalFullPackEnergy
	energyRemaining := rawSys.NominalEnergyRemaining
	rawStrings := s.PW.Strings(ctx, false)

	out := map[string]any{
		"grid":                 grid,
		"home":                 home,
		"solar":                solar,
		"battery":              battery,
		"soe":                  batLevel,
		"grid_status":          gridStatus,
		keyReserve:             reserve,
		"time_remaining_hours": timeRemaining,
		"full_pack_energy":     fullEnergy,
		"energy_remaining":     energyRemaining,
		"strings":              rawStrings,
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
			lvl := s.PW.Level(ctx, true)
			if lvl == nil {
				return nil, errNoLevel
			}

			return fmt.Sprintf(`{"percentage": %v}`, *lvl), nil
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

func (s *Server) handleStrings(ctx context.Context, w http.ResponseWriter, reqPath string) {
	msg, ok := s.cachedRouteHandler(ctx, "/strings", func(ctx context.Context) (string, error) {
		raw, _ := s.safePWCall(ctx, "/strings", func() (any, error) {
			v := s.PW.Strings(ctx, true)
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

	default:
		return false
	}
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
	ver := s.PW.Version(ctx)
	if ver == nil || ver == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{
			keyVersion: "SolarOnly",
			"vint":     0,
		})

		return
	}
	verStr := fmt.Sprintf("%v", ver)
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
		res := s.PW.GetReserve(ctx, false)
		_ = json.NewEncoder(w).Encode(map[string]any{keyReserve: res})
	case strings.HasPrefix(reqPath, "/control/mode"):
		res := s.PW.GetMode(ctx)
		_ = json.NewEncoder(w).Encode(map[string]any{keyMode: res})
	case strings.HasPrefix(reqPath, "/control/grid_charging"):
		res := s.PW.GetGridCharging(ctx)
		_ = json.NewEncoder(w).Encode(map[string]any{keyGridCharging: res})
	case strings.HasPrefix(reqPath, "/control/grid_export"):
		res := s.PW.GetGridExport(ctx)
		_ = json.NewEncoder(w).Encode(map[string]any{keyGridExport: res})
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
