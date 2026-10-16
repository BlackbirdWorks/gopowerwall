package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/blackbirdworks/gopowerwall/models"
)

var (
	errNoVitals  = errors.New("no vitals")
	errNoStrings = errors.New("no strings")
)

func fanPrefix(num int) string {
	return "FAN" + strconv.Itoa(num)
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
// (see [powerwall.Powerwall.Strings]) rather than upstream's
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

// fanSpeedsPWJSON flattens the client's [models.FanSpeedEntry] map into the
// simplified FAN{i}_actual/FAN{i}_target/FAN{i}_duty shape pypowerwall's /fans/pw route
// emits (server.py:2498-2522): Powerwall 2/+ fans first sorted by key, then two fans
// (A and B) per PW3 inverter.
func fanSpeedsPWJSON(speeds map[string]models.FanSpeedEntry) map[string]any {
	var pvacKeys []string
	var tepinvKeys []string
	for k := range speeds {
		if strings.HasPrefix(k, "TEPINV--") {
			tepinvKeys = append(tepinvKeys, k)
		} else {
			pvacKeys = append(pvacKeys, k)
		}
	}
	slices.Sort(pvacKeys)
	slices.Sort(tepinvKeys)

	out := make(map[string]any, len(pvacKeys)*2+len(tepinvKeys)*6)
	for i, k := range pvacKeys {
		entry := speeds[k]
		prefix := fanPrefix(i + 1)
		out[prefix+"_actual"] = entry.ActualRPM
		out[prefix+"_target"] = entry.TargetRPM
	}

	n := len(pvacKeys)
	for _, k := range tepinvKeys {
		entry := speeds[k]
		// Fan A
		n++
		prefixA := fanPrefix(n)
		out[prefixA+"_actual"] = entry.FanSpeedA
		out[prefixA+"_target"] = nil
		out[prefixA+"_duty"] = entry.FanDutyA

		// Fan B
		n++
		prefixB := fanPrefix(n)
		out[prefixB+"_actual"] = entry.FanSpeedB
		out[prefixB+"_target"] = nil
		out[prefixB+"_duty"] = entry.FanDutyB
	}

	return out
}

func (s *Server) generatePWTemps(ctx context.Context) (string, error) {
	raw := s.PW.Temps(ctx)
	pwtemp := make(map[string]any, len(raw.Temps))
	idx := 1
	keys := slices.Sorted(maps.Keys(raw.Temps))
	for _, k := range keys {
		pwtemp[pwPrefix(idx)+"temp"] = raw.Temps[k]
		idx++
	}
	b, err := json.Marshal(pwtemp)

	return string(b), err
}

func (s *Server) generatePWAlerts(ctx context.Context) (string, error) {
	raw := s.PW.Alerts(ctx)
	pwalerts := make(map[string]int, len(raw.Alerts))
	for _, a := range raw.Alerts {
		pwalerts[a] = 1
	}
	b, err := json.Marshal(pwalerts)

	return string(b), err
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
