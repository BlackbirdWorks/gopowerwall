package proxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"maps"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/blackbirdworks/gopowerwall/pkgs/version"
)

const (
	secondsPerHour = 3600
	kiloByte       = 1024
)

func (s *Server) respond(ctx context.Context, w http.ResponseWriter, reqPath, contentType, body string, ok bool) {
	if !ok || body == "" {
		s.recordStats(ctx, reqPath, false, true)
		w.Header().Set("Content-Type", contentType)
		isAPI := strings.HasPrefix(reqPath, "/api/") || reqPath == "/aggregates" ||
			reqPath == "/soe" || reqPath == "/vitals" || reqPath == "/strings"
		if isAPI {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("null"))
		} else {
			w.WriteHeader(http.StatusGatewayTimeout)
			_, _ = w.Write([]byte("TIMEOUT!"))
		}

		return
	}

	s.recordStats(ctx, reqPath, false, false)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	//nolint:gosec // Content-Type is explicitly set and responses are serialized JSON or static templates
	_, _ = w.Write([]byte(body))
}

func (s *Server) lookupPWFacingSensor(ctx context.Context, sub string) (any, bool) {
	switch sub {
	case "level":
		return map[string]any{"level": orNil(s.PW.Level(ctx))}, true
	case "power":
		return s.PW.Power(ctx), true
	case "site":
		return orNil(s.PW.SiteReading(ctx)), true
	case "solar":
		return orNil(s.PW.SolarReading(ctx)), true
	case "battery":
		return orNil(s.PW.BatteryReading(ctx)), true
	case "battery_blocks":
		return s.PW.BatteryBlocks(ctx), true
	case "load":
		return orNil(s.PW.LoadReading(ctx)), true
	case "grid":
		return orNil(s.PW.GridReading(ctx)), true
	case "home":
		return orNil(s.PW.HomeReading(ctx)), true
	case "aggregates":
		return orNil(s.PW.Aggregates(ctx)), true
	default:
		return nil, false
	}
}

func (s *Server) lookupPWFacingSystem(ctx context.Context, sub string) (any, bool) {
	switch sub {
	case "vitals":
		res, _ := s.PW.Vitals(ctx)

		return res, true
	case "temps":
		return s.PW.Temps(ctx), true
	case "strings":
		return s.PW.Strings(ctx), true
	case "din":
		return map[string]any{"din": orNil(s.PW.Din(ctx))}, true
	case keyUptime:
		return map[string]any{keyUptime: orNil(s.PW.Uptime(ctx))}, true
	case keyVersion:
		return map[string]any{keyVersion: orNil(s.PW.Version(ctx))}, true
	case keyStatus:
		return orNil(s.PW.Status(ctx)), true
	case "system_status":
		res, _ := s.PW.SystemStatus(ctx)

		return res, true
	case "grid_status":
		gs, err := s.PW.GridStatusString(ctx)
		if err != nil {
			gs = "Unknown"
		}

		return gs, true
	default:
		return nil, false
	}
}

func (s *Server) lookupPWFacingControl(ctx context.Context, sub string) (any, bool) {
	switch sub {
	case keySiteName:
		return map[string]any{keySiteName: orNil(s.PW.SiteName(ctx))}, true
	case "alerts":
		return map[string]any{"alerts": s.PW.Alerts(ctx)}, true
	case "is_connected":
		return map[string]any{"is_connected": s.PW.IsConnected()}, true
	case "get_reserve":
		return map[string]any{keyReserve: orNil(s.PW.GetReserve(ctx))}, true
	case "get_mode":
		return map[string]any{keyMode: orNil(s.PW.GetMode(ctx))}, true
	case "get_time_remaining":
		tr, err := s.PW.GetTimeRemaining(ctx)

		return map[string]any{"time_remaining": orNil(tr.Hours(), err)}, true
	default:
		return nil, false
	}
}

func (s *Server) handlePWFacing(ctx context.Context, w http.ResponseWriter, reqPath string) {
	sub := strings.TrimPrefix(reqPath, "/pw/")

	res, found := s.lookupPWFacingSensor(ctx, sub)
	if !found {
		res, found = s.lookupPWFacingSystem(ctx, sub)
	}
	if !found {
		res, found = s.lookupPWFacingControl(ctx, sub)
	}
	if !found {
		res = map[string]string{keyError: "Invalid Request"}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
	s.recordStats(ctx, reqPath, false, false)
}

func (s *Server) renderIndexHTML(ctx context.Context, content []byte) []byte {
	htmlStr := string(content)
	ver, hash := "", ""
	if st, err := s.PW.Status(ctx); err == nil {
		ver = st.Version
		hash = st.GitHash
	}
	htmlStr = strings.ReplaceAll(htmlStr, "{VERSION}", ver)
	htmlStr = strings.ReplaceAll(htmlStr, "{HASH}", hash)
	htmlStr = strings.ReplaceAll(htmlStr, "{EMAIL}", s.Config.Email)
	assetPrefix := s.Config.APIBaseURL + "viz-static/"
	htmlStr = strings.ReplaceAll(htmlStr, "{STYLE}", assetPrefix+s.Config.Style)
	htmlStr = strings.ReplaceAll(htmlStr, "{ASSET_PREFIX}", assetPrefix)
	htmlStr = strings.ReplaceAll(htmlStr, "{API_BASE_URL}", s.Config.APIBaseURL+"api")

	return InjectJS([]byte(htmlStr), s.Config.Style)
}

func (s *Server) proxyLocalGateway(ctx context.Context, w http.ResponseWriter, reqPath string) bool {
	if !s.PW.IsLocal() || s.Config.Host == "" || strings.HasPrefix(reqPath, "/api/") {
		return false
	}

	gwURL := fmt.Sprintf("https://%s%s", s.Config.Host, reqPath)
	tr := &http.Transport{
		//nolint:gosec // Local gateway connects via self-signed HTTPS by design
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr, Timeout: s.Config.TimeoutDuration()}
	//nolint:gosec // Local gateway reverse proxy target is user-configured host
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, gwURL, nil)
	if reqErr != nil {
		return false
	}

	//nolint:gosec // Intended reverse-proxy to the local Powerwall Gateway
	gwResp, gwErr := client.Do(req)
	if gwErr != nil {
		return false
	}
	defer gwResp.Body.Close()

	maps.Copy(w.Header(), gwResp.Header)
	w.WriteHeader(gwResp.StatusCode)
	_, _ = io.Copy(w, gwResp.Body)
	s.recordStats(ctx, reqPath, false, false)

	return true
}

func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request, reqPath string) {
	ctx := r.Context()
	cookieSuffix := "path=/;"
	if s.Config.HTTPSMode == "yes" || s.Config.HTTPSMode == "http" {
		cookieSuffix = "path=/;SameSite=None;Secure;"
	}
	// Add, not Set: both cookies must be sent. A second Set-Cookie header
	// written via Header().Set would silently overwrite the first.
	w.Header().Add("Set-Cookie", "AuthCookie=1234567890;"+cookieSuffix)
	w.Header().Add("Set-Cookie", "UserRecord=1234567890;"+cookieSuffix)

	targetFile := reqPath
	if targetFile == "/" || targetFile == "" {
		targetFile = "/index.html"
	}

	content, mime, err := GetStatic(s.WebRoot, targetFile)
	if err == nil && content != nil {
		if targetFile == "/index.html" {
			content = s.renderIndexHTML(ctx, content)
		}

		if s.Config.BrowserCache > 0 &&
			(mime == "text/css" || mime == "application/javascript" || mime == "image/png") {
			w.Header().Set("Cache-Control", fmt.Sprintf("max-age=%d", s.Config.BrowserCache))
		} else {
			w.Header().Set("Cache-Control", "no-cache, no-store")
		}

		w.Header().Set("Content-Type", mime)
		w.WriteHeader(http.StatusOK)
		//nolint:gosec // Static file contents served directly
		_, _ = w.Write(content)
		s.recordStats(ctx, reqPath, false, false)

		return
	}

	if s.proxyLocalGateway(ctx, w, reqPath) {
		return
	}

	http.NotFound(w, r)
	s.recordStats(ctx, reqPath, true, false)
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

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

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
		"mem":         m.Alloc / kiloByte,
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
