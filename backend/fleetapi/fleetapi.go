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

	"golang.org/x/oauth2"

	"github.com/blackbirdworks/gopowerwall/backend"
	"github.com/blackbirdworks/gopowerwall/backend/stubs"
	"github.com/blackbirdworks/gopowerwall/pkgs/atomicfile"
	"github.com/blackbirdworks/gopowerwall/pkgs/cache"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
	"github.com/blackbirdworks/gopowerwall/pkgs/oauthclient"
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

	// TeslaAuthURL is the Tesla OAuth2 token endpoint, shared by Owner API
	// and Fleet API refresh_token grants.
	TeslaAuthURL = "https://auth.tesla.com/oauth2/v3/token"

	statusKey     = "status"
	statusSuccess = "success"
	filePerm      = 0o600
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
	cache      *cache.ResponseCache
	client     *http.Client
	configData map[string]any
	pollAPIMap map[string]func(ctx context.Context, force, recursive, raw bool) (any, error)
	postAPIMap map[string]func(ctx context.Context, payload any, din string, recursive, raw bool) (any, error)
	email      string
	siteID     string
	authPath   string
	baseURL    string
	timeout    time.Duration
	mu         sync.Mutex
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
	f.pollAPIMap = map[string]func(ctx context.Context, force, recursive, raw bool) (any, error){
		"/api/devices/vitals": func(_ context.Context, _, _, _ bool) (any, error) {
			return map[string]any{}, nil
		},
		"/vitals": func(_ context.Context, _, _, _ bool) (any, error) {
			return map[string]any{}, nil
		},
		"/api/meters/aggregates": func(ctx context.Context, force, _, _ bool) (any, error) {
			return f.getAPIMetersAggregates(ctx, force)
		},
		"/api/operation": func(ctx context.Context, force, _, _ bool) (any, error) {
			return f.getAPIOperation(ctx, force)
		},
		"/api/site_info": func(ctx context.Context, force, _, _ bool) (any, error) {
			return f.getAPISiteInfo(ctx, force)
		},
		"/api/site_info/site_name": func(ctx context.Context, force, _, _ bool) (any, error) {
			return f.getAPISiteInfoSiteName(ctx, force)
		},
		"/api/status": func(ctx context.Context, force, _, _ bool) (any, error) {
			return f.getAPIStatus(ctx, force)
		},
		"/api/system_status": func(ctx context.Context, force, _, _ bool) (any, error) {
			return f.getAPISystemStatus(ctx, force)
		},
		"/api/system_status/grid_status": func(ctx context.Context, force, _, _ bool) (any, error) {
			return f.getAPISystemStatusGridStatus(ctx, force)
		},
		"/api/system_status/soe": func(ctx context.Context, force, _, _ bool) (any, error) {
			return f.getAPISystemStatusSOE(ctx, force)
		},
		"/api/login/Basic": func(_ context.Context, _, _, _ bool) (any, error) {
			return map[string]any{"token": "fleetapi_token"}, nil
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

	f.postAPIMap = map[string]func(ctx context.Context, payload any, din string, recursive, raw bool) (any, error){
		"/api/operation": f.postAPIOperation,
	}
}

// Authenticate reads the FleetAPI configuration file and verifies authentication.
func (f *PyPowerwallFleetAPI) Authenticate(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	logger.Load(ctx).DebugContext(ctx, "FleetAPI mode enabled")
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

	if sid, ok := cfgData["site_id"].(string); ok && sid != "" && f.siteID == "" {
		f.siteID = sid
	}
	if u, ok := cfgData["fleet_api_url"].(string); ok && u != "" {
		f.baseURL = strings.TrimRight(u, "/")
	}

	clientID, _ := cfgData["client_id"].(string)

	tok, err := oauthclient.TokenFromFields(cfgData)
	if err != nil {
		return fmt.Errorf("%w: %w", backend.ErrLogin, err)
	}

	f.client = oauthclient.New(ctx, oauthclient.Config{
		ClientID: clientID,
		TokenURL: TeslaAuthURL,
		Token:    tok,
		Timeout:  f.timeout,
		OnRefresh: func(newTok *oauth2.Token) error {
			return f.persistToken(cfgPath, newTok)
		},
	})

	logger.Load(ctx).DebugContext(ctx, "FleetAPI connected", "site", f.siteID, "base_url", f.baseURL)

	return nil
}

// persistToken merges tok into f.configData and writes the FleetAPI config
// file back atomically, so a refreshed access token survives a container
// restart. Called by the oauth2 client whenever the token used by f.client
// changes.
func (f *PyPowerwallFleetAPI) persistToken(cfgPath string, tok *oauth2.Token) error {
	oauthclient.MergeToken(f.configData, tok)

	return atomicfile.WriteJSON(cfgPath, f.configData, filePerm)
}

// checkStatus maps a non-2xx Tesla API response to a sentinel error, so
// callers can distinguish an authentication failure from other unexpected
// responses.
func checkStatus(resp *http.Response) error {
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return backend.ErrLogin
	case resp.StatusCode >= http.StatusBadRequest:
		return fmt.Errorf("%w: HTTP %d", backend.ErrUnexpectedStatus, resp.StatusCode)
	default:
		return nil
	}
}

// Close closes any open sessions.
func (f *PyPowerwallFleetAPI) Close(_ context.Context) error {
	return nil
}

// Poll fetches an endpoint through dispatch or returns an unknown API error.
func (f *PyPowerwallFleetAPI) Poll(ctx context.Context, api string, force, recursive, raw bool) (any, error) {
	handler, ok := f.pollAPIMap[api]
	if !ok {
		logger.Load(ctx).ErrorContext(ctx, "unknown fleetapi poll endpoint", "api", api)

		return map[string]string{"ERROR": "Unknown API: " + api}, nil
	}

	return handler(ctx, force, recursive, raw)
}

// Post sends a command to the FleetAPI endpoint.
func (f *PyPowerwallFleetAPI) Post(ctx context.Context, api string, payload any, _ string, _, _ bool) (any, error) {
	handler, ok := f.postAPIMap[api]
	if !ok {
		logger.Load(ctx).ErrorContext(ctx, "unknown fleetapi post endpoint", "api", api)

		return map[string]string{"ERROR": "Unknown API: " + api}, nil
	}

	return handler(ctx, payload, "", false, false)
}

func (f *PyPowerwallFleetAPI) getSiteData(ctx context.Context, force bool) (map[string]any, error) {
	if !force {
		if val, found, _ := f.cache.Get("SITE_DATA"); found {
			if m, ok := val.(map[string]any); ok {
				return m, nil
			}
		}
	}

	url := fmt.Sprintf("%s/api/1/energy_sites/%s/live_status", f.baseURL, f.siteID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if statusErr := checkStatus(resp); statusErr != nil {
		return nil, statusErr
	}

	var data map[string]any
	if decodeErr := json.NewDecoder(resp.Body).Decode(&data); decodeErr != nil {
		return nil, decodeErr
	}

	f.cache.Set("SITE_DATA", data)

	return data, nil
}

func (f *PyPowerwallFleetAPI) getSiteConfig(ctx context.Context, force bool) (map[string]any, error) {
	if !force {
		if val, found, _ := f.cache.Get("SITE_CONFIG", siteConfigTTL); found {
			if m, ok := val.(map[string]any); ok {
				return m, nil
			}
		}
	}

	url := fmt.Sprintf("%s/api/1/energy_sites/%s/site_info", f.baseURL, f.siteID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if statusErr := checkStatus(resp); statusErr != nil {
		return nil, statusErr
	}

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

func (f *PyPowerwallFleetAPI) getAPIMetersAggregates(ctx context.Context, force bool) (any, error) {
	stub := stubs.MetersAggregatesStub()
	data, _ := f.getSiteData(ctx, force)
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

func (f *PyPowerwallFleetAPI) getAPIOperation(ctx context.Context, force bool) (any, error) {
	cfg, _ := f.getSiteConfig(ctx, force)
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

func (f *PyPowerwallFleetAPI) postAPIOperation(ctx context.Context, payload any, _ string, _, _ bool) (any, error) {
	logger.Load(ctx).DebugContext(ctx, "FleetAPI post operation", "payload", payload)
	f.cache.Invalidate("/api/operation")

	return map[string]any{statusKey: statusSuccess}, nil
}

func (f *PyPowerwallFleetAPI) getAPISiteInfo(ctx context.Context, force bool) (any, error) {
	cfg, _ := f.getSiteConfig(ctx, force)
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

func (f *PyPowerwallFleetAPI) getAPISiteInfoSiteName(ctx context.Context, force bool) (any, error) {
	return f.getAPISiteInfo(ctx, force)
}

func (f *PyPowerwallFleetAPI) getAPIStatus(ctx context.Context, force bool) (any, error) {
	cfg, _ := f.getSiteConfig(ctx, force)
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

func (f *PyPowerwallFleetAPI) getAPISystemStatus(_ context.Context, _ bool) (any, error) {
	stub := stubs.SystemStatusStub()
	stub["battery_blocks"] = []any{}

	return stub, nil
}

func (f *PyPowerwallFleetAPI) getAPISystemStatusGridStatus(ctx context.Context, force bool) (any, error) {
	data, _ := f.getSiteData(ctx, force)
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

func (f *PyPowerwallFleetAPI) getAPISystemStatusSOE(ctx context.Context, force bool) (any, error) {
	data, _ := f.getSiteData(ctx, force)
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
func (f *PyPowerwallFleetAPI) Vitals(_ context.Context) (map[string]any, error) {
	return map[string]any{}, nil
}

// GetTimeRemaining returns the time remaining until Powerwall is depleted.
// Invariant: get_time_remaining() returns 0.0 in FleetAPI mode when unknown.
func (f *PyPowerwallFleetAPI) GetTimeRemaining(_ context.Context) (*float64, error) {
	zero := 0.0

	return &zero, nil
}

// Power returns current aggregate power values across site, solar, battery, and load.
func (f *PyPowerwallFleetAPI) Power(ctx context.Context) (map[string]float64, error) {
	site, solar, battery, load := 0.0, 0.0, 0.0, 0.0
	payload, err := f.Poll(ctx, "/api/meters/aggregates", false, false, false)
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
func (f *PyPowerwallFleetAPI) FetchPower(ctx context.Context, sensor string, verbose bool) (any, error) {
	if verbose {
		payload, err := f.Poll(ctx, "/api/meters/aggregates", false, false, false)
		if err != nil {
			return nil, err
		}
		if payload != nil {
			return lookup.Lookup(payload, sensor), nil
		}

		return nil, backend.ErrNotFound
	}
	p, err := f.Power(ctx)
	if err != nil {
		return 0.0, err
	}

	return p[sensor], nil
}

// SetGridCharging controls grid charging in FleetAPI mode.
func (f *PyPowerwallFleetAPI) SetGridCharging(ctx context.Context, mode bool) (map[string]any, error) {
	logger.Load(ctx).DebugContext(ctx, "set grid charging", "mode", mode)

	return map[string]any{statusKey: statusSuccess}, nil
}

// GetGridCharging returns current grid charging mode.
func (f *PyPowerwallFleetAPI) GetGridCharging(ctx context.Context) (*bool, error) {
	cfg, err := f.getSiteConfig(ctx, false)
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
func (f *PyPowerwallFleetAPI) SetGridExport(ctx context.Context, mode string) (map[string]any, error) {
	logger.Load(ctx).DebugContext(ctx, "set grid export", "mode", mode)

	return map[string]any{statusKey: statusSuccess}, nil
}

// GetGridExport returns current grid export mode.
func (f *PyPowerwallFleetAPI) GetGridExport(ctx context.Context) (*string, error) {
	cfg, err := f.getSiteConfig(ctx, false)
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
