package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

var errNoResponse = errors.New("no response")

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
	case strings.HasPrefix(reqPath, "/cloud"):
		s.handleCloudRoute(ctx, w, reqPath)
	case strings.HasPrefix(reqPath, "/fleetapi"):
		s.handleFleetAPIRoute(ctx, w, reqPath)
	case strings.HasPrefix(reqPath, "/control/"):
		s.handleControlGetRoute(ctx, w, reqPath)
	case strings.HasPrefix(reqPath, "/pw/"):
		s.handlePWFacing(ctx, w, reqPath)
	case isDisabled(reqPath):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{keyStatus: "404 Response - API Disabled"})
		s.recordStats(ctx, reqPath, false, false)
	case isAllowlisted(reqPath):
		s.handleAllowlistRoute(ctx, w, reqPath)
	default:
		s.handleWeb(w, r, reqPath)
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
