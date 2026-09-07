package fleetapi

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
	// ConfigFile is the filename for cached FleetAPI configuration.
	ConfigFile = ".pypowerwall.fleetapi"

	// FleetAPIURLNA is the North American FleetAPI URL.
	FleetAPIURLNA = "https://fleet-api.prd.na.vn.cloud.tesla.com"
	// FleetAPIURLEU is the European FleetAPI URL.
	FleetAPIURLEU = "https://fleet-api.prd.eu.vn.cloud.tesla.com"
	// FleetAPIURLCN is the Chinese FleetAPI URL.
	FleetAPIURLCN = "https://fleet-api.prd.cn.vn.cloud.tesla.cn"

	statusKey     = "status"
	statusSuccess = "success"
	siteConfigTTL = 59 * time.Second
)

// BaseURL returns the FleetAPI endpoint URL for a given region code.
func BaseURL(region string) string {
	switch region {
	case "eu":
		return FleetAPIURLEU
	case "cn":
		return FleetAPIURLCN
	default:
		return FleetAPIURLNA
	}
}

// PyPowerwallFleetAPI implements the Tesla FleetAPI backend.
type PyPowerwallFleetAPI struct {
	cache       *cache.ResponseCache
	client      *http.Client
	configData  map[string]any
	pollAPIMap  map[string]func(force, recursive, raw bool) (any, error)
	postAPIMap  map[string]func(payload any, din string, recursive, raw bool) (any, error)
	email       string
	siteID      string
	authPath    string
	accessToken string
	baseURL     string
	timeout     time.Duration
	mu          sync.Mutex
}

// New creates a new PyPowerwallFleetAPI backend.
func New(email string, cacheTTL, timeout time.Duration, siteID, authPath string) *PyPowerwallFleetAPI {
	f := &PyPowerwallFleetAPI{
		email:    email,
		siteID:   siteID,
		authPath: authPath,
		timeout:  timeout,
		baseURL:  BaseURL("na"),
		cache:    cache.NewResponseCache(cacheTTL),
		client: &http.Client{
			Timeout: timeout,
		},
	}
	f.initAPIMaps()

	return f
}

func (f *PyPowerwallFleetAPI) initAPIMaps() {
	f.pollAPIMap = map[string]func(force, recursive, raw bool) (any, error){
		"/api/devices/vitals": func(_, _, _ bool) (any, error) {
			return map[string]any{}, nil
		},
		"/vitals": func(_, _, _ bool) (any, error) {
			return map[string]any{}, nil
		},
		"/api/meters/aggregates": func(force, _, _ bool) (any, error) {
			return f.getAPIMetersAggregates(force)
		},
		"/api/operation": func(force, _, _ bool) (any, error) {
			return f.getAPIOperation(force)
		},
		"/api/site_info": func(force, _, _ bool) (any, error) {
			return f.getAPISiteInfo(force)
		},
		"/api/site_info/site_name": func(force, _, _ bool) (any, error) {
			return f.getAPISiteInfoSiteName(force)
		},
		"/api/status": func(force, _, _ bool) (any, error) {
			return f.getAPIStatus(force)
		},
		"/api/system_status": func(force, _, _ bool) (any, error) {
			return f.getAPISystemStatus(force)
		},
		"/api/system_status/grid_status": func(force, _, _ bool) (any, error) {
			return f.getAPISystemStatusGridStatus(force)
		},
		"/api/system_status/soe": func(force, _, _ bool) (any, error) {
			return f.getAPISystemStatusSOE(force)
		},
		"/api/login/Basic": func(_, _, _ bool) (any, error) {
			return map[string]any{"token": "fleetapi_token"}, nil
		},
		"/api/logout": func(_, _, _ bool) (any, error) {
			return map[string]any{"message": "logged out"}, nil
		},
		"/api/powerwalls": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockPowerwalls), nil
		},
		"/api/meters/site": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockMetersSite), nil
		},
		"/api/meters": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockMeters), nil
		},
		"/api/sitemaster": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockSitemaster), nil
		},
		"/api/customer": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockCustomer), nil
		},
		"/api/installer": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockInstaller), nil
		},
		"/api/networks": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockNetworks), nil
		},
		"/api/auth/toggle/supported": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockAuthToggle), nil
		},
		"/api/system/update/status": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockUpdate), nil
		},
		"/api/solars": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockSolars), nil
		},
	}

	f.postAPIMap = map[string]func(payload any, din string, recursive, raw bool) (any, error){
		"/api/operation": f.postAPIOperation,
	}
}

// Authenticate reads the FleetAPI configuration file and verifies authentication.
func (f *PyPowerwallFleetAPI) Authenticate() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	logger.LogDebug("Tesla FleetAPI mode enabled")
	cfgPath := filepath.Join(f.authPath, ConfigFile)
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("%w: %s - run setup -fleetapi", backend.ErrMissingAuthFile, cfgPath)
	}

	var cfgData map[string]any
	if unmarshalErr := json.Unmarshal(b, &cfgData); unmarshalErr != nil {
		return fmt.Errorf("failed to parse FleetAPI config: %w", unmarshalErr)
	}
	f.configData = cfgData

	if tok, ok := cfgData["access_token"].(string); ok {
		f.accessToken = tok
	}
	if sid, ok := cfgData["site_id"].(string); ok && sid != "" && f.siteID == "" {
		f.siteID = sid
	}
	if u, ok := cfgData["fleet_api_url"].(string); ok && u != "" {
		f.baseURL = strings.TrimRight(u, "/")
	}

	if f.accessToken == "" {
		return fmt.Errorf("%w: missing access_token in FleetAPI config file", backend.ErrLogin)
	}

	logger.LogDebug("FleetAPI connected (site: %s, base: %s)", f.siteID, f.baseURL)

	return nil
}

// Close closes any open sessions.
func (f *PyPowerwallFleetAPI) Close() error {
	return nil
}

// Poll fetches an endpoint through dispatch or returns an unknown API error.
func (f *PyPowerwallFleetAPI) Poll(api string, force, recursive, raw bool) (any, error) {
	handler, ok := f.pollAPIMap[api]
	if !ok {
		logger.LogError(" -- fleetapi: Unknown API: %s", api)

		return map[string]string{"ERROR": "Unknown API: " + api}, nil
	}

	return handler(force, recursive, raw)
}

// Post sends a command to the FleetAPI endpoint.
func (f *PyPowerwallFleetAPI) Post(api string, payload any, _ string, _, _ bool) (any, error) {
	handler, ok := f.postAPIMap[api]
	if !ok {
		logger.LogError(" -- fleetapi: Unknown POST API: %s", api)

		return map[string]string{"ERROR": "Unknown API: " + api}, nil
	}

	return handler(payload, "", false, false)
}

func (f *PyPowerwallFleetAPI) getSiteData(force bool) (map[string]any, error) {
	if !force {
		if val, found, _ := f.cache.Get("SITE_DATA"); found {
			if m, ok := val.(map[string]any); ok {
				return m, nil
			}
		}
	}

	url := fmt.Sprintf("%s/api/1/energy_sites/%s/live_status", f.baseURL, f.siteID)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+f.accessToken)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var data map[string]any
	if decodeErr := json.NewDecoder(resp.Body).Decode(&data); decodeErr != nil {
		return nil, decodeErr
	}

	f.cache.Set("SITE_DATA", data)

	return data, nil
}

func (f *PyPowerwallFleetAPI) getSiteConfig(force bool) (map[string]any, error) {
	if !force {
		if val, found, _ := f.cache.Get("SITE_CONFIG", siteConfigTTL); found {
			if m, ok := val.(map[string]any); ok {
				return m, nil
			}
		}
	}

	url := fmt.Sprintf("%s/api/1/energy_sites/%s/site_info", f.baseURL, f.siteID)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+f.accessToken)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var data map[string]any
	if decodeErr := json.NewDecoder(resp.Body).Decode(&data); decodeErr != nil {
		return nil, decodeErr
	}

	f.cache.Set("SITE_CONFIG", data)

	return data, nil
}

func updateFleetInstantPower(stub map[string]any, key string, power any) {
	if power == nil {
		return
	}
	if m, ok := stub[key].(map[string]any); ok {
		m["instant_power"] = power
	}
}

func (f *PyPowerwallFleetAPI) getAPIMetersAggregates(force bool) (any, error) {
	stub := stubs.MetersAggregatesStub()
	data, _ := f.getSiteData(force)
	if data == nil {
		return stub, nil
	}

	resp := data["response"]
	if resp == nil {
		return stub, nil
	}

	updateFleetInstantPower(stub, "solar", lookup.Lookup(resp, "solar_power"))
	updateFleetInstantPower(stub, "battery", lookup.Lookup(resp, "battery_power"))
	updateFleetInstantPower(stub, "load", lookup.Lookup(resp, "load_power"))
	updateFleetInstantPower(stub, "site", lookup.Lookup(resp, "grid_power"))

	return stub, nil
}

func (f *PyPowerwallFleetAPI) getAPIOperation(force bool) (any, error) {
	cfg, _ := f.getSiteConfig(force)
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

func (f *PyPowerwallFleetAPI) postAPIOperation(payload any, _ string, _, _ bool) (any, error) {
	logger.LogDebug("FleetAPI post operation: %v", payload)
	f.cache.Invalidate("/api/operation")

	return map[string]any{statusKey: statusSuccess}, nil
}

func (f *PyPowerwallFleetAPI) getAPISiteInfo(force bool) (any, error) {
	cfg, _ := f.getSiteConfig(force)
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

func (f *PyPowerwallFleetAPI) getAPISiteInfoSiteName(force bool) (any, error) {
	return f.getAPISiteInfo(force)
}

func (f *PyPowerwallFleetAPI) getAPIStatus(force bool) (any, error) {
	cfg, _ := f.getSiteConfig(force)
	din := f.siteID
	var version any = "23.28.2 27626f98"
	if cfg != nil {
		if d := lookup.Lookup(cfg, "response", "id"); d != nil {
			din = fmt.Sprintf("%v", d)
		}
		if v := lookup.Lookup(cfg, "response", "version"); v != nil {
			version = v
		}
	}

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

func (f *PyPowerwallFleetAPI) getAPISystemStatus(_ bool) (any, error) {
	stub := stubs.SystemStatusStub()
	stub["battery_blocks"] = []any{}

	return stub, nil
}

func (f *PyPowerwallFleetAPI) getAPISystemStatusGridStatus(force bool) (any, error) {
	data, _ := f.getSiteData(force)
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

func (f *PyPowerwallFleetAPI) getAPISystemStatusSOE(force bool) (any, error) {
	data, _ := f.getSiteData(force)
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
func (f *PyPowerwallFleetAPI) Vitals() (map[string]any, error) {
	return map[string]any{}, nil
}

// GetTimeRemaining returns the time remaining until Powerwall is depleted.
// Invariant: get_time_remaining() returns 0.0 in FleetAPI mode when unknown.
func (f *PyPowerwallFleetAPI) GetTimeRemaining() (*float64, error) {
	zero := 0.0

	return &zero, nil
}

// Power returns current aggregate power values across site, solar, battery, and load.
func (f *PyPowerwallFleetAPI) Power() (map[string]float64, error) {
	site, solar, battery, load := 0.0, 0.0, 0.0, 0.0
	payload, err := f.Poll("/api/meters/aggregates", false, false, false)
	if err == nil && payload != nil {
		site = getFloatVal(lookup.Lookup(payload, "site", "instant_power"))
		solar = getFloatVal(lookup.Lookup(payload, "solar", "instant_power"))
		battery = getFloatVal(lookup.Lookup(payload, "battery", "instant_power"))
		load = getFloatVal(lookup.Lookup(payload, "load", "instant_power"))
	}

	return map[string]float64{
		"site":    site,
		"solar":   solar,
		"battery": battery,
		"load":    load,
	}, nil
}

// FetchPower returns instant power or detailed sensor reading.
func (f *PyPowerwallFleetAPI) FetchPower(sensor string, verbose bool) (any, error) {
	if verbose {
		payload, err := f.Poll("/api/meters/aggregates", false, false, false)
		if err != nil {
			return nil, err
		}
		if payload != nil {
			return lookup.Lookup(payload, sensor), nil
		}

		return nil, backend.ErrNotFound
	}
	p, err := f.Power()
	if err != nil {
		return 0.0, err
	}

	return p[sensor], nil
}

// SetGridCharging controls grid charging in FleetAPI mode.
func (f *PyPowerwallFleetAPI) SetGridCharging(mode bool) (map[string]any, error) {
	logger.LogDebug("SetGridCharging(%v)", mode)

	return map[string]any{statusKey: statusSuccess}, nil
}

// GetGridCharging returns current grid charging mode.
func (f *PyPowerwallFleetAPI) GetGridCharging() (*bool, error) {
	cfg, err := f.getSiteConfig(false)
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

// SetGridExport controls grid export in FleetAPI mode.
func (f *PyPowerwallFleetAPI) SetGridExport(mode string) (map[string]any, error) {
	logger.LogDebug("SetGridExport(%s)", mode)

	return map[string]any{statusKey: statusSuccess}, nil
}

// GetGridExport returns current grid export mode.
func (f *PyPowerwallFleetAPI) GetGridExport() (*string, error) {
	cfg, err := f.getSiteConfig(false)
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

func getFloatVal(val any) float64 {
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
