package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/gopowerwall"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
)

const (
	Build                = "t101"
	MaxPostBody          = 4096
	URIStatsMax          = 100
	defaultServerTimeout = 5 * time.Second
)

//nolint:gochecknoglobals // exported allowlist map for proxy routing
var Allowlist = map[string]bool{
	"/api/status":                     true,
	"/api/site_info/site_name":        true,
	"/api/meters/site":                true,
	"/api/meters/solar":               true,
	"/api/sitemaster":                 true,
	"/api/powerwalls":                 true,
	"/api/customer/registration":      true,
	"/api/system_status":              true,
	"/api/system_status/grid_status":  true,
	"/api/system/update/status":       true,
	"/api/site_info":                  true,
	"/api/system_status/grid_faults":  true,
	"/api/operation":                  true,
	"/api/site_info/grid_codes":       true,
	"/api/solars":                     true,
	"/api/solars/brands":              true,
	"/api/customer":                   true,
	"/api/meters":                     true,
	"/api/installer":                  true,
	"/api/networks":                   true,
	"/api/system/networks/conn_tests": true,
	"/api/auth/toggle/supported":      true,
	"/api/solar_powerwall":            true,
	"/api/troubleshooting/problems":   true,
	"/api/diagnostics":                true,
	"/api/generators":                 true,
	"/api/generators/actions":         true,
	"/api/syncon/vitals":              true,
	"/api/syncon/actions":             true,
	"/api/inverters":                  true,
	"/api/inverters/status":           true,
	"/api/meters/readings":            true,
	"/api/meters/status":              true,
	"/api/powerwalls/status":          true,
	"/api/system_status/soe":          true,
}

//nolint:gochecknoglobals // exported disabled map for proxy routing
var Disabled = map[string]bool{
	"/api/customer/registration": true,
}

// Server is the HTTP proxy server for Powerwall.
type Server struct {
	StartTime     time.Time
	ClearTime     time.Time
	statsURI      map[string]int
	RateLimiter   *RateLimiter
	EndpointStats *EndpointStatsTracker
	Health        *ConnectionHealth
	PW            *gopowerwall.Powerwall
	PerfCache     *PerformanceCache
	DegradedCache *DegradationCache
	WebRoot       string
	Config        Config
	statsTime     int
	statsErr      int
	statsPost     int
	statsGets     int
	statsMu       sync.RWMutex
}

// NewServer creates a new Server instance.
func NewServer(cfg Config, pw *gopowerwall.Powerwall) *Server {
	if pw == nil {
		authMode := gopowerwall.AuthMode(cfg.AuthMode)
		if authMode == "" {
			authMode = gopowerwall.AuthModeCookie
		}
		var err error
		pw, err = gopowerwall.New(
			gopowerwall.WithHost(cfg.Host),
			gopowerwall.WithPassword(cfg.Password),
			gopowerwall.WithEmail(cfg.Email),
			gopowerwall.WithAuthPath(cfg.AuthPath),
			gopowerwall.WithAuthMode(authMode),
			gopowerwall.WithGwPwd(cfg.GwPwd),
			gopowerwall.WithRSAKeyPath(cfg.RsaKeyPath),
			gopowerwall.WithWiFiHost(cfg.WifiHost),
			gopowerwall.WithTimeout(cfg.TimeoutDuration()),
		)
		if err != nil {
			logger.LogError("Failed to initialize Powerwall client: %v", err)
		}
	}

	now := time.Now()

	return &Server{
		Config:        cfg,
		PW:            pw,
		PerfCache:     NewPerformanceCache(cfg.CacheExpireDuration()),
		DegradedCache: NewDegradationCache(cfg.CacheTTLDuration()),
		Health:        NewConnectionHealth(),
		EndpointStats: NewEndpointStatsTracker(),
		RateLimiter:   NewRateLimiter(),
		StartTime:     now,
		ClearTime:     now,
		statsURI:      make(map[string]int),
	}
}

func (s *Server) recordStats(uri string, isErr, isTimeout bool) {
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

func (s *Server) safePWCall(endpoint string, fn func() (any, error)) (any, bool) {
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

func (s *Server) cachedRouteHandler(endpoint string, generator func() (string, error)) (string, bool) {
	if s.Config.CacheExpire > 0 {
		if val, ok := s.PerfCache.Get(endpoint); ok {
			return val, true
		}
	}

	val, err := generator()
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
	reqPath := r.URL.Path

	// Security: Block path traversal
	if strings.Contains(reqPath, "..") {
		http.Error(w, `{"error": "Invalid Path"}`, http.StatusBadRequest)
		s.recordStats(reqPath, true, false)

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

// Start runs the HTTP server listening on the configured address.
func (s *Server) Start(ctx context.Context) error {
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
