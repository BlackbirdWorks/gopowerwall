package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"maps"
	"net/http"
	"runtime/metrics"
	"time"

	"github.com/blackbirdworks/gopowerwall/pkgs/version"
)

var (
	errNoSOE        = errors.New("no soe")
	errNoLevel      = errors.New("no level")
	errNoGridStatus = errors.New("no grid status")
)

const (
	secondsPerHour   = 3600
	secondsPerMinute = 60
	kiloByte         = 1024
)

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
		w.Header().Set("Content-Type", "application/json")
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

func (s *Server) handleVersionRoute(ctx context.Context, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
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

func (s *Server) handleStats(ctx context.Context, w http.ResponseWriter) {
	s.statsMu.RLock()
	now := time.Now()
	delta := int(now.Sub(s.StartTime).Seconds())
	uptime := fmt.Sprintf(
		"%02d:%02d:%02d",
		delta/secondsPerHour,
		(delta%secondsPerHour)/secondsPerMinute,
		delta%secondsPerMinute,
	)

	uriCopy := maps.Clone(s.statsURI)
	gets := s.statsGets
	posts := s.statsPost
	errs := s.statsErr
	timeouts := s.statsTime
	startTS := s.StartTime.Unix()
	clearTS := s.ClearTime.Unix()
	s.statsMu.RUnlock()

	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	var memKB uint64
	if sample[0].Value.Kind() == metrics.KindUint64 {
		memKB = sample[0].Value.Uint64() / kiloByte
	}

	siteName, siteNameErr := s.PW.SiteName(ctx)

	stats := map[string]any{
		"pypowerwall": fmt.Sprintf("%s Proxy %s", version.Version, Build),
		"mode":        s.PW.Mode(),
		"gets":        gets,
		"posts":       posts,
		"errors":      errs,
		"timeout":     timeouts,
		"uri":         uriCopy,
		"ts":          now.Unix(),
		"start":       startTS,
		"clear":       clearTS,
		"uptime":      uptime,
		"mem":         memKB,
		keySiteName:   orNil(siteName, siteNameErr),
		"cloudmode":   s.PW.IsCloud(),
		"fleetapi":    s.PW.IsFleetAPI(),
		"tedapi":      s.PW.IsTEDAPI(),
		"config": map[string]any{
			"PW_BIND_ADDRESS":        s.Config.BindAddress,
			"PW_HOST":                s.Config.Host,
			"PW_EMAIL":               s.Config.Email,
			"PW_TIMEZONE":            s.Config.Timezone,
			"PW_PORT":                s.Config.Port,
			"PW_STYLE":               s.Config.Style,
			"PW_CACHE_EXPIRE":        s.Config.CacheExpire,
			"PW_CACHE_TTL":           s.Config.CacheTTL,
			"PW_NEG_SOLAR":           s.Config.NegSolar,
			"PW_SITE_ZERO_THRESHOLD": s.Config.SiteZeroThreshold,
		},
	}

	if s.Config.HealthCheckEnabled {
		stats["connection_health"] = s.Health.Snapshot()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleHealth(_ context.Context, w http.ResponseWriter) {
	s.statsMu.RLock()
	gets := s.statsGets
	posts := s.statsPost
	errs := s.statsErr
	timeouts := s.statsTime
	s.statsMu.RUnlock()

	health := map[string]any{
		"pypowerwall":                   fmt.Sprintf("%s Proxy %s", version.Version, Build),
		"mode":                          s.PW.Mode(),
		"pypowerwall_cache_expire":      s.Config.CacheExpire,
		"degradation_cache_ttl_seconds": s.Config.CacheTTL,
		"graceful_degradation":          s.Config.GracefulDegradation,
		"fail_fast_mode":                s.Config.FailFastMode,
		"health_check_enabled":          s.Config.HealthCheckEnabled,
		"startup_time":                  s.StartTime.Format(time.RFC3339),
		"current_time":                  time.Now().Format(time.RFC3339),
		"proxy_stats": map[string]any{
			"total_gets":     gets,
			"total_posts":    posts,
			"total_errors":   errs,
			"total_timeouts": timeouts,
		},
	}

	if s.Config.HealthCheckEnabled {
		health["connection_health"] = s.Health.Snapshot()
	}

	if s.Config.GracefulDegradation {
		size, snap := s.DegradedCache.Snapshot()
		health["cached_data"] = map[string]any{
			"cache_size": size,
			"endpoints":  snap,
		}
	}

	health["endpoint_statistics"] = s.EndpointStats.Snapshot()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(health)
}

func (s *Server) handleHelp(_ context.Context, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<html><head><title>pyPowerwall Proxy</title></head><body>
<h1>pyPowerwall [%s] Proxy [%s]</h1>
<p>Proxy running in mode: %s</p>
<p><a href="https://github.com/jasonacox/pypowerwall">Documentation & API Reference</a></p>
</body></html>`, html.EscapeString(version.Version), Build, html.EscapeString(string(s.PW.Mode())))
}
