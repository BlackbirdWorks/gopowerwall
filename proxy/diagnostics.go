package proxy

import (
	"context"
	"encoding/json"
	"net/http"
)

func (s *Server) handleTedapiRoute(ctx context.Context, w http.ResponseWriter, reqPath string) {
	w.Header().Set("Content-Type", "application/json")
	if !s.PW.IsTEDAPI() {
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "TEDAPI not enabled"})

		return
	}
	switch reqPath {
	case "/tedapi/config":
		cfg, _ := s.PW.GetFileStoreConfig(ctx)
		_ = json.NewEncoder(w).Encode(cfg)
	case "/tedapi/status":
		status, _ := s.PW.GetTEDAPIStatus(ctx)
		_ = json.NewEncoder(w).Encode(status)
	case "/tedapi/components":
		comps, _ := s.PW.GetTEDAPIComponents(ctx)
		_ = json.NewEncoder(w).Encode(comps)
	case "/tedapi/battery":
		blocks, _ := s.PW.GetTEDAPIBattery(ctx)
		_ = json.NewEncoder(w).Encode(blocks)
	case "/tedapi/controller":
		ctrl, _ := s.PW.GetTEDAPIDeviceController(ctx)
		_ = json.NewEncoder(w).Encode(ctrl)
	default:
		_ = json.NewEncoder(w).Encode(map[string]string{
			keyError: "Use /tedapi/config, /tedapi/status, /tedapi/components, /tedapi/battery, /tedapi/controller",
		})
	}
}

func (s *Server) handleCloudRoute(ctx context.Context, w http.ResponseWriter, reqPath string) {
	w.Header().Set("Content-Type", "application/json")
	if !s.PW.IsCloud() || s.PW.IsFleetAPI() {
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Cloud API not enabled"})

		return
	}
	switch reqPath {
	case "/cloud/battery":
		res, _ := s.PW.GetCloudBattery(ctx)
		_ = json.NewEncoder(w).Encode(res)
	case "/cloud/power":
		res, _ := s.PW.GetCloudPower(ctx)
		_ = json.NewEncoder(w).Encode(res)
	case "/cloud/config":
		res, _ := s.PW.GetCloudConfig(ctx)
		_ = json.NewEncoder(w).Encode(res)
	default:
		_ = json.NewEncoder(w).Encode(map[string]string{
			keyError: "Use /cloud/battery, /cloud/power, /cloud/config",
		})
	}
}

func (s *Server) handleFleetAPIRoute(ctx context.Context, w http.ResponseWriter, reqPath string) {
	w.Header().Set("Content-Type", "application/json")
	if !s.PW.IsFleetAPI() {
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "FleetAPI not enabled"})

		return
	}
	switch reqPath {
	case "/fleetapi/info":
		res, _ := s.PW.GetFleetAPIInfo(ctx)
		_ = json.NewEncoder(w).Encode(res)
	case "/fleetapi/status":
		res, _ := s.PW.GetFleetAPIStatus(ctx)
		_ = json.NewEncoder(w).Encode(res)
	default:
		_ = json.NewEncoder(w).Encode(map[string]string{
			keyError: "Use /fleetapi/info, /fleetapi/status",
		})
	}
}
