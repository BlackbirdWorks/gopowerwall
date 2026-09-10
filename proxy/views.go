package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"net/http"
	"strconv"

	"github.com/blackbirdworks/gopowerwall/models"
)

const (
	freqExtraCap = 2
	podExtraCap  = 5
	jsonCap      = 11

	batteryFieldCount  = 11
	inverterFieldCount = 4
	podBlockFieldCount = 29
	podTepodFieldCount = 6
)

var errCapacityOverflow = errors.New("capacity computation overflowed")

// safeCapAdd returns base + count*perItem, or errCapacityOverflow if the
// multiplication or addition would overflow int - guarding the map capacity
// hints below against CodeQL's "size computation for allocation may
// overflow" finding.
func safeCapAdd(base, count, perItem int) (int, error) {
	if count != 0 && perItem > (math.MaxInt-base)/count {
		return 0, errCapacityOverflow
	}

	return base + count*perItem, nil
}

func pwPrefix(num int) string {
	return "PW" + strconv.Itoa(num) + "_"
}

func (s *Server) generateFreq(ctx context.Context) (string, error) {
	rawSys, _ := s.PW.SystemStatus(ctx)
	freq := s.PW.FrequencyView(ctx)

	capHint, err := safeCapAdd(freqExtraCap, len(rawSys.BatteryBlocks), batteryFieldCount)
	if err != nil {
		return "", err
	}

	capHint, err = safeCapAdd(capHint, len(freq.Inverters), inverterFieldCount)
	if err != nil {
		return "", err
	}

	capHint, err = safeCapAdd(capHint, len(freq.SyncMeterFields), 1)
	if err != nil {
		return "", err
	}

	fcv := make(map[string]any, capHint)
	for idx, block := range rawSys.BatteryBlocks {
		pNum := idx + 1
		pfx := pwPrefix(pNum)
		fcv[pfx+"name"] = nil
		fcv[pfx+"PINV_Fout"] = block.FOut
		fcv[pfx+"PINV_VSplit1"] = nil
		fcv[pfx+"PINV_VSplit2"] = nil
		fcv[pfx+"PackagePartNumber"] = block.PackagePartNumber
		fcv[pfx+"PackageSerialNumber"] = block.PackageSerialNumber
		fcv[pfx+"p_out"] = block.POut
		fcv[pfx+"q_out"] = block.QOut
		fcv[pfx+"v_out"] = block.VOut
		fcv[pfx+"f_out"] = block.FOut
		fcv[pfx+"i_out"] = block.IOut
	}

	for invIdx, inv := range freq.Inverters {
		pNum := invIdx + 1
		pfx := pwPrefix(pNum)
		fcv[pfx+"name"] = inv.Device
		fcv[pfx+"PINV_Fout"] = inv.Fout
		fcv[pfx+"PINV_VSplit1"] = inv.VSplit1
		fcv[pfx+"PINV_VSplit2"] = inv.VSplit2
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
// 1-based index derived from [powerwall.Powerwall.PODView]'s
// vitals-iteration order rather than the block loop's index - see
// PODView's own doc comment for why upstream (and this port) trust that
// ordering to line up instead of matching by DIN.
func applyPODTEPODVitals(pod map[string]any, entries []models.PODTEPODEntry) {
	for idx, entry := range entries {
		prefix := pwPrefix(idx + 1)
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

	capHint, err := safeCapAdd(podExtraCap, len(view.Blocks), podBlockFieldCount)
	if err != nil {
		return "", err
	}

	capHint, err = safeCapAdd(capHint, len(view.TEPODEntries), podTepodFieldCount)
	if err != nil {
		return "", err
	}

	pod := make(map[string]any, capHint)
	for idx, block := range view.Blocks {
		prefix := pwPrefix(idx + 1)
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

	out := make(map[string]any, jsonCap)
	out["grid"] = snap.Grid
	out["home"] = snap.Home
	out["solar"] = snap.Solar
	out["battery"] = snap.Battery
	out["soe"] = snap.BatteryLevel
	out["grid_status"] = gridStatusNumeric
	out[keyReserve] = snap.Reserve
	out["time_remaining_hours"] = snap.TimeRemaining.Hours()
	out["full_pack_energy"] = snap.FullPackEnergy
	out["energy_remaining"] = snap.EnergyRemaining
	out["strings"] = solarStringsJSON(snap.Strings)
	b, err := json.Marshal(out)

	return string(b), err
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
