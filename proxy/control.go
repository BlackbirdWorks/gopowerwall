package proxy

import (
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

func (s *Server) checkControlToken(token string) bool {
	if s.Config.ControlSecret == "" || token == "" {
		return false
	}
	tokHash := sha256.Sum256([]byte(token))
	secHash := sha256.Sum256([]byte(s.Config.ControlSecret))

	return subtle.ConstantTimeCompare(tokHash[:], secHash[:]) == 1
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request, reqPath string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if !strings.HasPrefix(reqPath, "/control") {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Invalid Request"})
		s.recordStats(reqPath, true, false)

		return
	}

	if s.Config.ControlSecret == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: errControlSecretNotSet.Error()})
		s.recordStats(reqPath, true, false)

		return
	}

	if r.ContentLength > MaxPostBody {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: errInvalidRequest.Error()})
		s.recordStats(reqPath, true, false)

		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, MaxPostBody))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: errInvalidRequest.Error()})
		s.recordStats(reqPath, true, false)

		return
	}

	vals, err := url.ParseQuery(string(body))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: errInvalidRequest.Error()})
		s.recordStats(reqPath, true, false)

		return
	}

	token := vals.Get("token")
	if !s.checkControlToken(token) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"unauthorized": errInvalidToken.Error()})
		s.recordStats(reqPath, true, false)

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

	s.dispatchControl(w, action, vals)
}

func (s *Server) dispatchControl(w http.ResponseWriter, action string, vals url.Values) {
	value := vals.Get("value")

	switch action {
	case keyReserve:
		s.handleControlReserve(w, value, vals.Get("mode"))
	case keyMode:
		s.handleControlMode(w, value, vals.Get("level"))
	case keyGridCharging:
		s.handleControlGridCharging(w, value)
	case keyGridExport:
		s.handleControlGridExport(w, value)
	case keyMaxBackup:
		s.handleControlMaxBackup(w, value)
	default:
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Invalid Command Action"})
	}
}

func (s *Server) handleControlReserve(w http.ResponseWriter, value, mode string) {
	if value == "" {
		res := s.PW.GetReserve(false, false)
		_ = json.NewEncoder(w).Encode(map[string]any{keyReserve: res})

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
		res, setErr := s.PW.SetOperation(&fVal, &mode)
		if setErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to set reserve+mode"})

			return
		}
		_ = json.NewEncoder(w).Encode(res)

		return
	}

	res, setErr := s.PW.SetReserve(float64(intVal))
	if setErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to set reserve"})

		return
	}
	_ = json.NewEncoder(w).Encode(res)
}

func (s *Server) handleControlMode(w http.ResponseWriter, value, levelStr string) {
	if value == "" {
		res := s.PW.GetMode()
		_ = json.NewEncoder(w).Encode(map[string]any{keyMode: res})

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
		res, setErr := s.PW.SetOperation(&fLvl, &value)
		if setErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to set reserve+mode"})

			return
		}
		_ = json.NewEncoder(w).Encode(res)

		return
	}

	res, setErr := s.PW.SetMode(value)
	if setErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to set mode"})

		return
	}
	_ = json.NewEncoder(w).Encode(res)
}

func (s *Server) handleControlGridCharging(w http.ResponseWriter, value string) {
	if value == "" {
		gc := s.PW.GetGridCharging()
		_ = json.NewEncoder(w).Encode(map[string]any{keyGridCharging: gc})

		return
	}

	lower := strings.ToLower(value)
	if lower != valTrue && lower != valFalse {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: errInvalidValue.Error()})

		return
	}

	bVal := lower == valTrue
	_, setErr := s.PW.SetGridCharging(bVal)
	if setErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to set grid_charging"})

		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{keyGridCharging: "Set Successfully"})
}

func (s *Server) handleControlGridExport(w http.ResponseWriter, value string) {
	if value == "" {
		ge := s.PW.GetGridExport()
		_ = json.NewEncoder(w).Encode(map[string]any{keyGridExport: ge})

		return
	}

	lower := strings.ToLower(value)
	if lower != "battery_ok" && lower != "pv_only" && lower != "never" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: errInvalidValue.Error()})

		return
	}

	_, setErr := s.PW.SetGridExport(lower)
	if setErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to set grid_export"})

		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{keyGridExport: "Set Successfully"})
}

func (s *Server) handleControlMaxBackup(w http.ResponseWriter, value string) {
	if !s.PW.IsTEDAPI() {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "max_backup requires v1r LAN transport"})

		return
	}

	if value == "" {
		events, err := s.PW.GetBackupEvents()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to get backup events"})

			return
		}
		_ = json.NewEncoder(w).Encode(events)

		return
	}

	if strings.EqualFold(value, "cancel") {
		_, err := s.PW.CancelMaxBackup()
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

	_, err = s.PW.ScheduleMaxBackup(sec)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{keyError: "Failed to schedule max backup"})

		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{keyMaxBackup: fmt.Sprintf("Scheduled for %d seconds", sec)})
}
