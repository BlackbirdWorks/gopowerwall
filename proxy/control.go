package proxy

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	minControlParts = 2
)

var (
	errControlSecretNotSet = errors.New("control commands disabled - set PW_CONTROL_SECRET to enable")
	errInvalidRequest      = errors.New("control command error: invalid request")
	errInvalidToken        = errors.New("control command token invalid")
	errInvalidValue        = errors.New("control command value invalid")
	errInvalidMode         = errors.New("control command mode invalid")
)

func (s *Server) checkControlToken(_ context.Context, token string) bool {
	if s.Config.ControlSecret == "" || token == "" {
		return false
	}
	tokHash := sha256.Sum256([]byte(token))
	secHash := sha256.Sum256([]byte(s.Config.ControlSecret))

	return subtle.ConstantTimeCompare(tokHash[:], secHash[:]) == 1
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request, reqPath string) {
	ctx := r.Context()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if !strings.HasPrefix(reqPath, "/control") {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Invalid Request"})
		s.recordStats(ctx, reqPath, true, false)

		return
	}

	if s.Config.ControlSecret == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: errControlSecretNotSet.Error()})
		s.recordStats(ctx, reqPath, true, false)

		return
	}

	if r.ContentLength > MaxPostBody {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: errInvalidRequest.Error()})
		s.recordStats(ctx, reqPath, true, false)

		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, MaxPostBody))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: errInvalidRequest.Error()})
		s.recordStats(ctx, reqPath, true, false)

		return
	}

	vals, err := url.ParseQuery(string(body))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: errInvalidRequest.Error()})
		s.recordStats(ctx, reqPath, true, false)

		return
	}

	token := vals.Get("token")
	if !s.checkControlToken(ctx, token) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"unauthorized": errInvalidToken.Error()})
		s.recordStats(ctx, reqPath, true, false)

		return
	}

	s.statsMu.Lock()
	s.statsPost++
	s.statsMu.Unlock()

	parts := strings.Split(strings.Trim(reqPath, "/"), "/")
	action := ""
	if len(parts) >= minControlParts {
		action = parts[1]
	}

	s.dispatchControl(ctx, w, action, vals)
}

func (s *Server) dispatchControl(ctx context.Context, w http.ResponseWriter, action string, vals url.Values) {
	value := vals.Get("value")

	switch action {
	case keyReserve:
		s.handleControlReserve(ctx, w, value, vals.Get("mode"))
	case keyMode:
		s.handleControlMode(ctx, w, value, vals.Get("level"))
	case keyGridCharging:
		s.handleControlGridCharging(ctx, w, value)
	case keyGridExport:
		s.handleControlGridExport(ctx, w, value)
	case keyMaxBackup:
		s.handleControlMaxBackup(ctx, w, value)
	default:
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Invalid Command Action"})
	}
}

func (s *Server) handleControlReserve(ctx context.Context, w http.ResponseWriter, value, mode string) {
	if value == "" {
		res, err := s.PW.GetReserve(ctx)
		_ = json.NewEncoder(w).Encode(map[string]any{keyReserve: orNil(res, err)})

		return
	}

	intVal, err := strconv.Atoi(value)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: errInvalidValue.Error()})

		return
	}

	if mode != "" {
		if mode != "self_consumption" && mode != "backup" && mode != "autonomous" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{keyError: errInvalidMode.Error()})

			return
		}
		fVal := float64(intVal)
		res, setErr := s.PW.SetOperation(ctx, &fVal, &mode)
		if setErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to set reserve+mode"})

			return
		}
		_ = json.NewEncoder(w).Encode(res)

		return
	}

	res, setErr := s.PW.SetReserve(ctx, float64(intVal))
	if setErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to set reserve"})

		return
	}
	_ = json.NewEncoder(w).Encode(res)
}

func (s *Server) handleControlMode(ctx context.Context, w http.ResponseWriter, value, levelStr string) {
	if value == "" {
		res, err := s.PW.GetMode(ctx)
		_ = json.NewEncoder(w).Encode(map[string]any{keyMode: orNil(res, err)})

		return
	}

	if value != "self_consumption" && value != "backup" && value != "autonomous" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: errInvalidValue.Error()})

		return
	}

	if levelStr != "" {
		lvl, err := strconv.Atoi(levelStr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Control Command Level Invalid"})

			return
		}
		fLvl := float64(lvl)
		res, setErr := s.PW.SetOperation(ctx, &fLvl, &value)
		if setErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to set reserve+mode"})

			return
		}
		_ = json.NewEncoder(w).Encode(res)

		return
	}

	res, setErr := s.PW.SetMode(ctx, value)
	if setErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to set mode"})

		return
	}
	_ = json.NewEncoder(w).Encode(res)
}

func (s *Server) handleControlGridCharging(ctx context.Context, w http.ResponseWriter, value string) {
	if value == "" {
		gc, err := s.PW.GetGridCharging(ctx)
		_ = json.NewEncoder(w).Encode(map[string]any{keyGridCharging: orNil(gc, err)})

		return
	}

	lower := strings.ToLower(value)
	if lower != valTrue && lower != valFalse {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: errInvalidValue.Error()})

		return
	}

	bVal := lower == valTrue
	_, setErr := s.PW.SetGridCharging(ctx, bVal)
	if setErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to set grid_charging"})

		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{keyGridCharging: "Set Successfully"})
}

func (s *Server) handleControlGridExport(ctx context.Context, w http.ResponseWriter, value string) {
	if value == "" {
		ge, err := s.PW.GetGridExport(ctx)
		_ = json.NewEncoder(w).Encode(map[string]any{keyGridExport: orNil(ge, err)})

		return
	}

	lower := strings.ToLower(value)
	if lower != "battery_ok" && lower != "pv_only" && lower != "never" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: errInvalidValue.Error()})

		return
	}

	_, setErr := s.PW.SetGridExport(ctx, lower)
	if setErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to set grid_export"})

		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{keyGridExport: "Set Successfully"})
}

func (s *Server) handleControlMaxBackup(ctx context.Context, w http.ResponseWriter, value string) {
	if !s.PW.IsTEDAPI() {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "max_backup requires v1r LAN transport"})

		return
	}

	if value == "" {
		events, err := s.PW.GetBackupEvents(ctx)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to get backup events"})

			return
		}
		_ = json.NewEncoder(w).Encode(events)

		return
	}

	if strings.EqualFold(value, "cancel") {
		_, err := s.PW.CancelMaxBackup(ctx)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to cancel max backup"})

			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{keyMaxBackup: "Cancelled"})

		return
	}

	sec, err := strconv.Atoi(value)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).
			Encode(map[string]string{keyError: "Control Command Value Invalid - use seconds or cancel"})

		return
	}

	_, err = s.PW.ScheduleMaxBackup(ctx, sec)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to schedule max backup"})

		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{keyMaxBackup: fmt.Sprintf("Scheduled for %d seconds", sec)})
}

func (s *Server) handleControlGetRoute(ctx context.Context, w http.ResponseWriter, reqPath string) {
	w.Header().Set("Content-Type", "application/json")
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
