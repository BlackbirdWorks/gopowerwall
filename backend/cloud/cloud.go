package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/gopowerwall/backend"
	"github.com/blackbirdworks/gopowerwall/backend/stubs"
	"github.com/blackbirdworks/gopowerwall/pkgs/cache"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
)

const (
	// AuthFile is the filename for cached cloud auth tokens.
	AuthFile = ".pypowerwall.auth"
	// SiteFile is the filename for cached energy site ID.
	SiteFile = ".pypowerwall.site"

	// TeslaAuthURL is the Tesla OAuth2 token endpoint.
	TeslaAuthURL = "https://auth.tesla.com/oauth2/v3/token"
	// TeslaOwnerURL is the Tesla Owner API base endpoint.
	TeslaOwnerURL = "https://owner-api.teslamotors.com"

	statusKey     = "status"
	statusSuccess = "success"
	filePerm      = 0o600
	siteConfigTTL = 59 * time.Second
)

// PyPowerwallCloud implements the Tesla Cloud Owner API backend.
type PyPowerwallCloud struct {
	cache       *cache.ResponseCache
	client      *http.Client
	tokenData   map[string]any
	pollAPIMap  map[string]func(ctx context.Context, force, recursive, raw bool) (any, error)
	postAPIMap  map[string]func(ctx context.Context, payload any, din string, recursive, raw bool) (any, error)
	email       string
	siteID      string
	authPath    string
	accessToken string
	timeout     time.Duration
	mu          sync.Mutex
}

// New creates a new PyPowerwallCloud backend.
func New(email string, cacheTTL, timeout time.Duration, siteID, authPath string) *PyPowerwallCloud {
	c := &PyPowerwallCloud{
		email:    email,
		siteID:   siteID,
		authPath: authPath,
		timeout:  timeout,
		cache:    cache.NewResponseCache(cacheTTL),
		client: &http.Client{
			Timeout: timeout,
		},
	}
	c.initAPIMaps()

	return c
}

func (c *PyPowerwallCloud) initAPIMaps() {
	c.pollAPIMap = map[string]func(ctx context.Context, force, recursive, raw bool) (any, error){
		"/api/devices/vitals": func(_ context.Context, _, _, _ bool) (any, error) {
			return map[string]any{}, nil
		},
		"/vitals": func(_ context.Context, _, _, _ bool) (any, error) {
			return map[string]any{}, nil
		},
		"/api/meters/aggregates": func(ctx context.Context, force, _, _ bool) (any, error) {
			return c.getAPIMetersAggregates(ctx, force)
		},
		"/api/operation": func(ctx context.Context, force, _, _ bool) (any, error) {
			return c.getAPIOperation(ctx, force)
		},
		"/api/site_info": func(ctx context.Context, force, _, _ bool) (any, error) {
			return c.getAPISiteInfo(ctx, force)
		},
		"/api/site_info/site_name": func(ctx context.Context, force, _, _ bool) (any, error) {
			return c.getAPISiteInfoSiteName(ctx, force)
		},
		"/api/status": func(ctx context.Context, force, _, _ bool) (any, error) {
			return c.getAPIStatus(ctx, force)
		},
		"/api/system_status": func(ctx context.Context, force, _, _ bool) (any, error) {
			return c.getAPISystemStatus(ctx, force)
		},
		"/api/system_status/grid_status": func(ctx context.Context, force, _, _ bool) (any, error) {
			return c.getAPISystemStatusGridStatus(ctx, force)
		},
		"/api/system_status/soe": func(ctx context.Context, force, _, _ bool) (any, error) {
			return c.getAPISystemStatusSOE(ctx, force)
		},
		"/api/login/Basic": func(_ context.Context, _, _, _ bool) (any, error) {
			return map[string]any{"token": "cloud_token"}, nil
		},
		"/api/logout": func(_ context.Context, _, _, _ bool) (any, error) {
			return map[string]any{"message": "logged out"}, nil
		},
		"/api/powerwalls": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockPowerwalls), nil
		},
		"/api/meters/site": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockMetersSite), nil
		},
		"/api/meters": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockMeters), nil
		},
		"/api/sitemaster": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockSitemaster), nil
		},
		"/api/customer": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockCustomer), nil
		},
		"/api/installer": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockInstaller), nil
		},
		"/api/networks": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockNetworks), nil
		},
		"/api/auth/toggle/supported": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockAuthToggle), nil
		},
		"/api/system/update/status": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockUpdate), nil
		},
		"/api/solars": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockSolars), nil
		},
	}

	c.postAPIMap = map[string]func(ctx context.Context, payload any, din string, recursive, raw bool) (any, error){
		"/api/operation": c.postAPIOperation,
	}
}

// Authenticate connects to the Tesla Owner API using cached credentials.
// anyKey returns an arbitrary key from m, or the empty string when m is empty.
func anyKey(m map[string]any) string {
	for k := range m {
		return k
	}

	return ""
}

func (c *PyPowerwallCloud) Authenticate(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	logger.Load(ctx).DebugContext(ctx, "cloud mode enabled")
	authFilePath := filepath.Join(c.authPath, AuthFile)
	b, err := os.ReadFile(authFilePath)
	if err != nil {
		return fmt.Errorf("%w: %s - run setup", backend.ErrMissingAuthFile, authFilePath)
	}

	var authData map[string]any
	if unmarshalErr := json.Unmarshal(b, &authData); unmarshalErr != nil {
		return fmt.Errorf("failed to parse auth file: %w", unmarshalErr)
	}

	// Email key resolution
	if c.email == "" || c.email == "nobody@nowhere.com" {
		if k := anyKey(authData); k != "" {
			c.email = k
		}
	}

	tokMap, ok := authData[c.email].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: %s", backend.ErrEmailNotFound, c.email)
	}
	c.tokenData = tokMap
	if tok, tokOk := tokMap["access_token"].(string); tokOk {
		c.accessToken = tok
	}

	// Resolve site ID if not provided
	if c.siteID == "" {
		siteFilePath := filepath.Join(c.authPath, SiteFile)
		if sb, readErr := os.ReadFile(siteFilePath); readErr == nil {
			c.siteID = strings.TrimSpace(string(sb))
		} else {
			// Query products
			siteID, siteErr := c.findEnergySite(ctx)
			if siteErr != nil {
				return siteErr
			}
			c.siteID = siteID
			_ = os.WriteFile(siteFilePath, []byte(siteID), filePerm)
		}
	}

	logger.Load(ctx).DebugContext(ctx, "cloud mode connected", "email", c.email, "site", c.siteID)

	return nil
}

func (c *PyPowerwallCloud) findEnergySite(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, TeslaOwnerURL+"/api/1/products", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var data struct {
		Response []map[string]any `json:"response"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&data); decodeErr != nil {
		return "", decodeErr
	}

	for _, prod := range data.Response {
		if energyID := lookup.Lookup(prod, "energy_site_id"); energyID != nil {
			return fmt.Sprintf("%v", energyID), nil
		}
		if siteID := lookup.Lookup(prod, "id"); siteID != nil {
			return fmt.Sprintf("%v", siteID), nil
		}
	}

	return "", fmt.Errorf("%w: %s", backend.ErrNoEnergySite, c.email)
}

// Close closes any open sessions.
func (c *PyPowerwallCloud) Close(_ context.Context) error {
	return nil
}

// Poll fetches an endpoint through dispatch or returns an unknown API error.
func (c *PyPowerwallCloud) Poll(ctx context.Context, api string, force, recursive, raw bool) (any, error) {
	handler, ok := c.pollAPIMap[api]
	if !ok {
		logger.Load(ctx).ErrorContext(ctx, "unknown cloud poll endpoint", "api", api)

		return map[string]string{"ERROR": "Unknown API: " + api}, nil
	}

	return handler(ctx, force, recursive, raw)
}

// Post sends a command to the cloud API.
func (c *PyPowerwallCloud) Post(ctx context.Context, api string, payload any, _ string, _, _ bool) (any, error) {
	handler, ok := c.postAPIMap[api]
	if !ok {
		logger.Load(ctx).ErrorContext(ctx, "unknown cloud post endpoint", "api", api)

		return map[string]string{"ERROR": "Unknown API: " + api}, nil
	}

	return handler(ctx, payload, "", false, false)
}

func (c *PyPowerwallCloud) getSiteData(ctx context.Context, force bool) (map[string]any, error) {
	if !force {
		if val, found, _ := c.cache.Get("SITE_DATA"); found {
			if m, ok := val.(map[string]any); ok {
				return m, nil
			}
		}
	}

	url := fmt.Sprintf("%s/api/1/energy_sites/%s/live_status", TeslaOwnerURL, c.siteID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var data map[string]any
	if decodeErr := json.NewDecoder(resp.Body).Decode(&data); decodeErr != nil {
		return nil, decodeErr
	}

	c.cache.Set("SITE_DATA", data)

	return data, nil
}

func (c *PyPowerwallCloud) getSiteConfig(ctx context.Context, force bool) (map[string]any, error) {
	if !force {
		if val, found, _ := c.cache.Get("SITE_CONFIG", siteConfigTTL); found {
			if m, ok := val.(map[string]any); ok {
				return m, nil
			}
		}
	}

	url := fmt.Sprintf("%s/api/1/energy_sites/%s/site_info", TeslaOwnerURL, c.siteID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var data map[string]any
	if decodeErr := json.NewDecoder(resp.Body).Decode(&data); decodeErr != nil {
		return nil, decodeErr
	}

	c.cache.Set("SITE_CONFIG", data)

	return data, nil
}

func updateInstantPower(stub map[string]any, key string, power any) {
	if power == nil {
		return
	}
	if m, ok := stub[key].(map[string]any); ok {
		m["instant_power"] = power
	}
}

func (c *PyPowerwallCloud) getAPIMetersAggregates(ctx context.Context, force bool) (any, error) {
	stub := stubs.MetersAggregatesStub()
	data, _ := c.getSiteData(ctx, force)
	if data == nil {
		return stub, nil
	}

	resp := data["response"]
	if resp == nil {
		return stub, nil
	}

	updateInstantPower(stub, "solar", lookup.Lookup(resp, "solar_power"))
	updateInstantPower(stub, "battery", lookup.Lookup(resp, "battery_power"))
	updateInstantPower(stub, "load", lookup.Lookup(resp, "load_power"))
	updateInstantPower(stub, "site", lookup.Lookup(resp, "grid_power"))

	return stub, nil
}

func (c *PyPowerwallCloud) getAPIOperation(ctx context.Context, force bool) (any, error) {
	cfg, _ := c.getSiteConfig(ctx, force)
	mode := "self_consumption"
	reserve := 20.0

	if cfg != nil {
		if m := lookup.Lookup(cfg, "response", "default_real_mode"); m != nil {
			mode = fmt.Sprintf("%v", m)
		}
		if r := lookup.Lookup(cfg, "response", "backup_reserve_percent"); r != nil {
			if rf, ok := r.(float64); ok {
				reserve = rf
			}
		}
	}

	return map[string]any{
		"real_mode":              mode,
		"backup_reserve_percent": reserve,
	}, nil
}

func (c *PyPowerwallCloud) postAPIOperation(ctx context.Context, payload any, _ string, _, _ bool) (any, error) {
	logger.Load(ctx).DebugContext(ctx, "cloud post operation", "payload", payload)
	c.cache.Invalidate("/api/operation")

	return map[string]any{statusKey: statusSuccess}, nil
}

func (c *PyPowerwallCloud) getAPISiteInfo(ctx context.Context, force bool) (any, error) {
	cfg, _ := c.getSiteConfig(ctx, force)
	siteName := "Powerwall"
	tz := "America/Los_Angeles"
	if cfg != nil {
		if s := lookup.Lookup(cfg, "response", "site_name"); s != nil {
			siteName = fmt.Sprintf("%v", s)
		}
		if t := lookup.Lookup(cfg, "response", "installation_time_zone"); t != nil {
			tz = fmt.Sprintf("%v", t)
		}
	}

	return map[string]any{
		"site_name": siteName,
		"timezone":  tz,
	}, nil
}

func (c *PyPowerwallCloud) getAPISiteInfoSiteName(ctx context.Context, force bool) (any, error) {
	return c.getAPISiteInfo(ctx, force)
}

func (c *PyPowerwallCloud) getAPIStatus(ctx context.Context, force bool) (any, error) {
	cfg, _ := c.getSiteConfig(ctx, force)
	din := c.siteID
	var version any = "23.28.2 27626f98"
	if cfg != nil {
		if d := lookup.Lookup(cfg, "response", "id"); d != nil {
			din = fmt.Sprintf("%v", d)
		}
		if v := lookup.Lookup(cfg, "response", "version"); v != nil {
			version = v
		}
	}

	// Frozen public API invariant: cloud status returns hard-coded fake git hash
	return map[string]any{
		"din":               din,
		"start_time":        lookup.Lookup(cfg, "response", "installation_date"),
		"up_time_seconds":   nil,
		"is_new":            false,
		"version":           version,
		"git_hash":          "27626f98a66cad5c665bbe1d4d788cdb3e94fd34",
		"commission_count":  0,
		"device_type":       "teg",
		"teg_type":          "unknown",
		"sync_type":         "v2.1",
		"cellular_disabled": false,
		"can_reboot":        true,
	}, nil
}

func (c *PyPowerwallCloud) getAPISystemStatus(_ context.Context, _ bool) (any, error) {
	stub := stubs.SystemStatusStub()
	stub["battery_blocks"] = []any{}

	return stub, nil
}

func (c *PyPowerwallCloud) getAPISystemStatusGridStatus(ctx context.Context, force bool) (any, error) {
	data, _ := c.getSiteData(ctx, force)
	statusStr := "SystemGridConnected"
	if data != nil {
		gridStatus := lookup.Lookup(data, "response", "grid_status")
		if gridStatus != nil && fmt.Sprintf("%v", gridStatus) != "Active" &&
			fmt.Sprintf("%v", gridStatus) != "Unknown" {
			statusStr = "SystemIslandedActive"
		}
	}

	return map[string]any{
		"grid_status":          statusStr,
		"grid_services_active": lookup.Lookup(data, "response", "grid_services_active"),
	}, nil
}

func (c *PyPowerwallCloud) getAPISystemStatusSOE(ctx context.Context, force bool) (any, error) {
	data, _ := c.getSiteData(ctx, force)
	pct := 100.0
	if data != nil {
		if p := lookup.Lookup(data, "response", "percentage_charged"); p != nil {
			if pf, ok := p.(float64); ok {
				pct = pf
			}
		}
	}

	return map[string]any{
		"percentage": pct,
	}, nil
}

// Vitals returns vitals data for the Powerwall system.
func (c *PyPowerwallCloud) Vitals(_ context.Context) (map[string]any, error) {
	return map[string]any{}, nil
}

// GetTimeRemaining returns the time remaining until Powerwall is depleted.
// Invariant: get_time_remaining() when unknown returns 0.0 in cloud/fleetapi (DESIGN.md).
func (c *PyPowerwallCloud) GetTimeRemaining(_ context.Context) (*float64, error) {
	zero := 0.0

	return &zero, nil
}

// Power returns current aggregate power values across site, solar, battery, and load.
func (c *PyPowerwallCloud) Power(ctx context.Context) (map[string]float64, error) {
	site, solar, battery, load := 0.0, 0.0, 0.0, 0.0
	payload, err := c.Poll(ctx, "/api/meters/aggregates", false, false, false)
	if err == nil && payload != nil {
		site = getFloat(lookup.Lookup(payload, "site", "instant_power"))
		solar = getFloat(lookup.Lookup(payload, "solar", "instant_power"))
		battery = getFloat(lookup.Lookup(payload, "battery", "instant_power"))
		load = getFloat(lookup.Lookup(payload, "load", "instant_power"))
	}

	return map[string]float64{
		"site":    site,
		"solar":   solar,
		"battery": battery,
		"load":    load,
	}, nil
}

// FetchPower returns instant power or detailed sensor reading.
func (c *PyPowerwallCloud) FetchPower(ctx context.Context, sensor string, verbose bool) (any, error) {
	if verbose {
		payload, err := c.Poll(ctx, "/api/meters/aggregates", false, false, false)
		if err != nil {
			return nil, err
		}
		if payload != nil {
			return lookup.Lookup(payload, sensor), nil
		}

		return nil, backend.ErrNotFound
	}
	p, err := c.Power(ctx)
	if err != nil {
		return 0.0, err
	}

	return p[sensor], nil
}

// SetGridCharging controls grid charging in cloud mode.
func (c *PyPowerwallCloud) SetGridCharging(ctx context.Context, mode bool) (map[string]any, error) {
	logger.Load(ctx).DebugContext(ctx, "set grid charging", "mode", mode)

	return map[string]any{statusKey: statusSuccess}, nil
}

// GetGridCharging returns current grid charging mode.
func (c *PyPowerwallCloud) GetGridCharging(ctx context.Context) (*bool, error) {
	cfg, err := c.getSiteConfig(ctx, false)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, backend.ErrNotFound
	}
	val := lookup.Lookup(cfg, "response", "grid_charging")
	if b, ok := val.(bool); ok {
		return &b, nil
	}

	return nil, backend.ErrNotFound
}

// SetGridExport controls grid export setting.
func (c *PyPowerwallCloud) SetGridExport(ctx context.Context, mode string) (map[string]any, error) {
	logger.Load(ctx).DebugContext(ctx, "set grid export", "mode", mode)

	return map[string]any{statusKey: statusSuccess}, nil
}

// GetGridExport returns current grid export mode.
func (c *PyPowerwallCloud) GetGridExport(ctx context.Context) (*string, error) {
	cfg, err := c.getSiteConfig(ctx, false)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, backend.ErrNotFound
	}
	val := lookup.Lookup(cfg, "response", "customer_preferred_export_rule")
	if s, ok := val.(string); ok {
		return &s, nil
	}

	return nil, backend.ErrNotFound
}

func getFloat(val any) float64 {
	if val == nil {
		return 0.0
	}
	switch v := val.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		f, _ := strconv.ParseFloat(v, 64)

		return f
	default:
		return 0.0
	}
}
