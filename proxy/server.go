package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/cache"
	"github.com/blackbirdworks/gopowerwall/pkgs/influx"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/powerwall"
)

const (
	Build                = "t101"
	MaxPostBody          = 4096
	URIStatsMax          = 100
	defaultServerTimeout = 5 * time.Second
)

// isAllowlisted reports whether the proxy forwards reqPath to the gateway.
// A switch keeps the route set constant and allocation-free per request.
//
// This is a parity surface (see .agent/rules/powerwall.md): the set below
// is exactly pypowerwall's own ALLOWLIST (server.py:173-199, 26 entries),
// reconciled against a prior version of this list that had drifted by 13
// entries (docs/parity-matrix.md's allowlist row and correction #8).
// Reconciliation decisions, made deliberately rather than defaulting to
// "keep everything":
//
//   - Added "/api/system/networks" and "/api/synchrometer/ct_voltage_references":
//     both are on upstream's ALLOWLIST and were missing here entirely, so a
//     client requesting either was silently falling through to the static-
//     file handler and 404ing instead of being proxied.
//   - Removed "/api/system/networks/conn_tests": not on upstream's
//     ALLOWLIST at all, and its path strongly suggests it triggers an
//     active network connectivity test on the gateway rather than reading
//     passive state - forwarding a bare GET to it is a meaningfully
//     different risk profile than the read-only informational routes
//     upstream actually allows, and it was almost certainly meant to *be*
//     "/api/system/networks" (added above) rather than a deliberate,
//     distinct addition.
//   - Removed "/api/system_status/soe": already served by its own dedicated
//     handler (handleCoreAPIRoutes, checked before this function ever runs)
//     with different, correct-for-parity output; the allowlist entry was
//     unreachable dead code, so removing it is a pure no-op that resolves
//     the divergence rather than a behavior change.
//   - Removed the remaining 8 ("/api/diagnostics", "/api/generators",
//     "/api/generators/actions", "/api/syncon/vitals", "/api/syncon/actions",
//     "/api/inverters", "/api/inverters/status", "/api/meters/status",
//     "/api/powerwalls/status"): none has any test, documentation, or
//     comment anywhere in this repository evidencing a deliberate reason
//     for the addition, and two are actions-suffixed paths whose passive-GET
//     semantics on a real gateway are unknown. Per the parity contract,
//     forwarding a path pypowerwall's own allowlist refuses is a real
//     behavioral difference, not a cosmetic one; absent a documented reason
//     to diverge, this reconciliation restores exact parity with upstream's
//     26-entry list rather than preserving undocumented scope creep.
func isAllowlisted(reqPath string) bool {
	switch reqPath {
	case "/api/status",
		"/api/site_info/site_name",
		"/api/meters/site",
		"/api/meters/solar",
		"/api/sitemaster",
		"/api/powerwalls",
		"/api/customer/registration",
		"/api/system_status",
		"/api/system_status/grid_status",
		"/api/system/update/status",
		"/api/site_info",
		"/api/system_status/grid_faults",
		"/api/operation",
		"/api/site_info/grid_codes",
		"/api/solars",
		"/api/solars/brands",
		"/api/customer",
		"/api/meters",
		"/api/installer",
		"/api/networks",
		"/api/system/networks",
		"/api/meters/readings",
		"/api/synchrometer/ct_voltage_references",
		"/api/troubleshooting/problems",
		"/api/auth/toggle/supported",
		"/api/solar_powerwall":
		return true
	default:
		return false
	}
}

// isDisabled reports whether the proxy refuses reqPath outright.
func isDisabled(reqPath string) bool {
	switch reqPath {
	case "/api/customer/registration":
		return true
	default:
		return false
	}
}

// Server is the HTTP proxy server for Powerwall.
type Server struct {
	StartTime     time.Time
	ClearTime     time.Time
	statsURI      map[string]int
	RateLimiter   *cache.RateLimiter
	EndpointStats *EndpointStatsTracker
	Health        *ConnectionHealth
	PW            *powerwall.Powerwall
	PerfCache     *cache.PerformanceCache
	DegradedCache *cache.DegradationCache
	InfluxClient  *influx.Client
	WebRoot       string
	Config        Config
	statsTime     int
	statsErr      int
	statsPost     int
	statsGets     int
	statsMu       sync.RWMutex
}

// NewServer creates a new Server instance.
func NewServer(ctx context.Context, cfg Config, pw *powerwall.Powerwall) *Server {
	if pw == nil {
		authMode := powerwall.AuthMode(cfg.AuthMode)
		if authMode == "" {
			authMode = powerwall.AuthModeCookie
		}
		var err error
		pw, err = powerwall.New(
			ctx,
			powerwall.WithHost(cfg.Host),
			powerwall.WithPassword(cfg.Password),
			powerwall.WithEmail(cfg.Email),
			powerwall.WithAuthPath(cfg.AuthPath),
			powerwall.WithAuthMode(authMode),
			powerwall.WithGwPwd(cfg.GwPwd),
			powerwall.WithRSAKeyPath(cfg.RsaKeyPath),
			powerwall.WithWiFiHost(cfg.WifiHost),
			powerwall.WithTimeout(cfg.TimeoutDuration()),
			powerwall.WithCacheFile(cfg.CacheFile),
		)
		if err != nil {
			logger.Load(ctx).
				ErrorContext(ctx, "failed to initialize Powerwall client", "error", err)
		}
	}

	var influxClient *influx.Client
	if influxCfg, ok := cfg.InfluxConfig(); ok {
		var err error
		influxClient, err = influx.New(influxCfg)
		if err != nil {
			logger.Load(ctx).
				ErrorContext(ctx, "failed to initialize InfluxDB exporter", "error", err)
		}
	}

	now := time.Now()

	return &Server{
		Config:        cfg,
		PW:            pw,
		PerfCache:     cache.NewPerformanceCache(cfg.CacheExpireDuration()),
		DegradedCache: cache.NewDegradationCache(cfg.CacheTTLDuration()),
		Health:        NewConnectionHealth(),
		EndpointStats: NewEndpointStatsTracker(),
		RateLimiter:   cache.NewRateLimiter(),
		InfluxClient:  influxClient,
		StartTime:     now,
		ClearTime:     now,
		statsURI:      make(map[string]int),
	}
}

func (s *Server) recordStats(_ context.Context, uri string, isErr, isTimeout bool) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if isTimeout {
		s.statsTime++

		return
	}
	if isErr {
		s.statsErr++

		return
	}
	s.statsGets++
	if len(s.statsURI) < URIStatsMax {
		s.statsURI[uri]++
	}
}

func (s *Server) safePWCall(
	_ context.Context,
	endpoint string,
	fn func() (any, error),
) (any, bool) {
	if s.Config.FailFastMode && s.Health.IsDegraded {
		if val, ok, _ := s.DegradedCache.Get(endpoint); ok {
			return val, true
		}
	}

	res, err := fn()
	success := err == nil && res != nil
	s.EndpointStats.Record(endpoint, success)

	if success {
		s.Health.RecordSuccess()
		if s.Config.GracefulDegradation {
			str := fmt.Sprintf("%v", res)
			s.DegradedCache.Set(endpoint, str)
		}

		return res, true
	}

	s.Health.RecordFailure(s.Config.NetworkErrorRateLimit)
	if s.Config.GracefulDegradation {
		if val, ok, _ := s.DegradedCache.Get(endpoint); ok {
			return val, true
		}
	}

	return nil, false
}

func (s *Server) cachedRouteHandler(
	ctx context.Context,
	endpoint string,
	generator func(context.Context) (string, error),
) (string, bool) {
	if s.Config.CacheExpire > 0 {
		if val, ok := s.PerfCache.Get(endpoint); ok {
			return val, true
		}
	}

	val, err := generator(ctx)
	if err != nil || val == "" {
		if s.Config.GracefulDegradation {
			if degVal, ok, _ := s.DegradedCache.Get(endpoint); ok {
				return degVal, true
			}
		}

		return "", false
	}

	if s.Config.CacheExpire > 0 {
		s.PerfCache.Set(endpoint, val)
	}

	return val, true
}

// ServeHTTP dispatches incoming HTTP requests to handlers.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqPath := r.URL.Path

	// Security: Block path traversal
	if strings.Contains(reqPath, "..") {
		http.Error(w, `{"error": "Invalid Path"}`, http.StatusBadRequest)
		s.recordStats(ctx, reqPath, true, false)

		return
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.handleGet(w, r, reqPath)
	case http.MethodPost:
		s.handlePost(w, r, reqPath)
	default:
		http.Error(w, `{"error": "Method Not Allowed"}`, http.StatusMethodNotAllowed)
	}
}

// StartExporter starts the background InfluxDB exporter loop if configured.
func (s *Server) StartExporter(ctx context.Context) {
	if s.InfluxClient != nil && s.PW != nil {
		go s.runInfluxExporter(ctx)
	}
}

// Start runs the HTTP server listening on the configured address.
func (s *Server) Start(ctx context.Context) error {
	s.StartExporter(ctx)

	addr := fmt.Sprintf("%s:%d", s.Config.BindAddress, s.Config.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           s,
		ReadHeaderTimeout: defaultServerTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultServerTimeout)
		defer cancel()

		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) runInfluxExporter(ctx context.Context) {
	defer s.InfluxClient.Close()
	logger.Load(ctx).InfoContext(
		ctx,
		"influx exporter started",
		"url", s.Config.InfluxURL,
		"bucket", s.Config.InfluxBucket,
		"interval", s.Config.InfluxInterval,
	)
	collector := func(collectCtx context.Context) (models.Snapshot, models.MetersAggregates, error) {
		snap := s.PW.Snapshot(collectCtx)
		agg, err := s.PW.Aggregates(collectCtx)

		return snap, agg, err
	}
	if err := s.InfluxClient.Run(ctx, collector); err != nil && !errors.Is(err, context.Canceled) {
		logger.Load(ctx).ErrorContext(ctx, "influx exporter stopped", "error", err)
	}
}
