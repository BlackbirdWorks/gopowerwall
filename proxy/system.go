package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"maps"
	"net/http"
	"runtime/metrics"
	"time"

	"github.com/blackbirdworks/gopowerwall/pkgs/version"
	"github.com/blackbirdworks/gopowerwall/powerwall"
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
	keyVint          = "vint"
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

	case "/metrics":
		s.handleMetrics(ctx, w)

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
	if s.PW == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{
			keyVersion: "SolarOnly",
			keyVint:    0,
		})

		return
	}
	verStr, err := s.PW.Version(ctx)
	if err != nil || verStr == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{
			keyVersion: "SolarOnly",
			keyVint:    0,
		})

		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		keyVersion: verStr,
		keyVint:    version.ParseVersion(verStr),
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

	var mode powerwall.ConnectionMode
	var isCloud, isFleetAPI, isTEDAPI bool
	var siteName any
	if s.PW != nil {
		mode = s.PW.Mode()
		isCloud = s.PW.IsCloud()
		isFleetAPI = s.PW.IsFleetAPI()
		isTEDAPI = s.PW.IsTEDAPI()
		sn, snErr := s.PW.SiteName(ctx)
		siteName = orNil(sn, snErr)
	}

	stats := map[string]any{
		"pypowerwall": fmt.Sprintf("%s Proxy %s", version.Version, Build),
		"mode":        mode,
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
		keySiteName:   siteName,
		"cloudmode":   isCloud,
		"fleetapi":    isFleetAPI,
		"tedapi":      isTEDAPI,
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

type helpData struct {
	mode     string
	uptime   string
	solar    float64
	battery  float64
	grid     float64
	load     float64
	soe      float64
	gets     int
	posts    int
	errs     int
	timeouts int
}

func (s *Server) collectHelpData(ctx context.Context) helpData {
	s.statsMu.RLock()
	delta := int(time.Since(s.StartTime).Seconds())
	uptime := fmt.Sprintf(
		"%02d:%02d:%02d",
		delta/secondsPerHour,
		(delta%secondsPerHour)/secondsPerMinute,
		delta%secondsPerMinute,
	)
	gets := s.statsGets
	posts := s.statsPost
	errs := s.statsErr
	timeouts := s.statsTime
	s.statsMu.RUnlock()

	mode := "unknown"
	var solar, battery, grid, load, soe float64
	if s.PW != nil {
		mode = string(s.PW.Mode())
		snap := s.PW.Snapshot(ctx)
		solar = snap.Solar
		battery = snap.Battery
		grid = snap.Grid
		load = snap.Home
		soe = snap.BatteryLevel
	}

	return helpData{
		solar:    solar,
		battery:  battery,
		grid:     grid,
		load:     load,
		soe:      soe,
		gets:     gets,
		posts:    posts,
		errs:     errs,
		timeouts: timeouts,
		mode:     mode,
		uptime:   uptime,
	}
}

const helpHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>pyPowerwall Proxy</title>
<style>
:root {
  color-scheme: light dark;
  --bg: #f8fafc; --card-bg: #ffffff; --text: #0f172a; --text-muted: #64748b;
  --border: #e2e8f0; --accent: #2563eb; --green: #16a34a; --amber: #d97706;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #0f172a; --card-bg: #1e293b; --text: #f8fafc; --text-muted: #94a3b8;
    --border: #334155; --accent: #3b82f6; --green: #22c55e; --amber: #f59e0b;
  }
}
body {
  font-family: system-ui, -apple-system, sans-serif;
  background: var(--bg);
  color: var(--text);
  margin: 0;
  padding: 24px;
  line-height: 1.5;
}
.container { max-width: 960px; margin: 0 auto; }
header { margin-bottom: 24px; }
h1 { margin: 0 0 8px 0; font-size: 1.75rem; }
.badge {
  display: inline-block;
  padding: 4px 10px;
  border-radius: 9999px;
  background: var(--accent);
  color: #fff;
  font-size: 0.8rem;
  font-weight: 600;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 16px;
  margin-bottom: 24px;
}
.card {
  background: var(--card-bg);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 18px;
}
.card-title {
  font-size: 0.875rem;
  color: var(--text-muted);
  text-transform: uppercase;
  font-weight: 600;
  margin-bottom: 6px;
}
.card-value { font-size: 1.75rem; font-weight: 700; }
.links a {
  display: inline-block;
  margin: 4px 8px 4px 0;
  padding: 6px 12px;
  background: var(--card-bg);
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--accent);
  text-decoration: none;
  font-size: 0.9rem;
}
.links a:hover { border-color: var(--accent); }
footer { margin-top: 32px; font-size: 0.85rem; color: var(--text-muted); }
</style>
</head>
<body>
<div class="container">
<header>
  <h1>pyPowerwall Proxy <span class="badge">%s</span></h1>
  <p style="color:var(--text-muted);margin:0;">
    Version %s &bull; Mode: <strong>%s</strong> &bull; Uptime: <strong>%s</strong>
  </p>
</header>

<div class="grid">
  <div class="card">
    <div class="card-title">Solar</div>
    <div class="card-value" style="color:var(--amber);">%.1f W</div>
  </div>
  <div class="card">
    <div class="card-title">Battery</div>
    <div class="card-value" style="color:var(--green);">%.1f W (%.1f%%)</div>
  </div>
  <div class="card">
    <div class="card-title">Grid</div>
    <div class="card-value">%.1f W</div>
  </div>
  <div class="card">
    <div class="card-title">Home Load</div>
    <div class="card-value">%.1f W</div>
  </div>
</div>

<div class="card" style="margin-bottom:24px;">
  <div class="card-title">Proxy Statistics</div>
  <p style="margin:4px 0;">
    <strong>Gets:</strong> %d &bull;
    <strong>Posts:</strong> %d &bull;
    <strong>Errors:</strong> %d &bull;
    <strong>Timeouts:</strong> %d
  </p>
</div>

<div class="card links">
  <div class="card-title">Endpoints</div>
  <a href="/aggregates">/aggregates</a>
  <a href="/soe">/soe</a>
  <a href="/vitals">/vitals</a>
  <a href="/pod">/pod</a>
  <a href="/freq">/freq</a>
  <a href="/metrics">/metrics (Prometheus)</a>
  <a href="/stats">/stats</a>
  <a href="/health">/health</a>
  <a href="/csv">/csv</a>
  <a href="/version">/version</a>
</div>

<footer>
  <p>
    <a href="https://github.com/jasonacox/pypowerwall" style="color:var(--accent);">
      Documentation & API Reference
    </a>
  </p>
</footer>
</div>
</body>
</html>`

func renderHelpHTML(w io.Writer, d helpData) {
	fmt.Fprintf(w, helpHTMLTemplate,
		Build,
		html.EscapeString(version.Version),
		html.EscapeString(d.mode),
		html.EscapeString(d.uptime),
		d.solar,
		d.battery,
		d.soe,
		d.grid,
		d.load,
		d.gets,
		d.posts,
		d.errs,
		d.timeouts,
	)
}

func (s *Server) handleHelp(ctx context.Context, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := s.collectHelpData(ctx)
	renderHelpHTML(w, data)
}
