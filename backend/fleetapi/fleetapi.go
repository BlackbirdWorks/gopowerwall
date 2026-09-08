package fleetapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// errInvalidOperationPayload indicates postAPIOperation received a
// caller-supplied payload whose backup_reserve_percent or real_mode field
// was present but not the expected type. This is a programming error on the
// caller's side (every in-tree caller builds this payload itself - see
// Powerwall.SetOperation), never a Tesla API response, so the malformed
// field is never sent as a request.
var errInvalidOperationPayload = errors.New("invalid operation payload field")

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
// callers can distinguish an authentication failure, a rate limit, and a
// missing resource from other unexpected responses.
func checkStatus(resp *http.Response) error {
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return backend.ErrLogin
	case resp.StatusCode == http.StatusNotFound:
		return backend.ErrNotFound
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable:
		return backend.ErrRateLimited
	case resp.StatusCode >= http.StatusBadRequest:
		return fmt.Errorf("%w: HTTP %d", backend.ErrUnexpectedStatus, resp.StatusCode)
	default:
		return nil
	}
}

// postJSON POSTs body as JSON to url using f.client, which - once
// Authenticate has run - attaches and transparently refreshes the caller's
// OAuth2 bearer token via pkgs/oauthclient. It never sets the Authorization
// header itself: doing so here would bypass that automatic refresh. A
// non-2xx response is mapped to a backend sentinel error via checkStatus.
func (f *PyPowerwallFleetAPI) postJSON(ctx context.Context, url string, body map[string]any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	return checkStatus(resp)
}

// toBackupReservePercent extracts an integer backup reserve percentage from
// the numeric value Powerwall.SetOperation places under
// "backup_reserve_percent" in its payload map (always a float64, since
// SetOperation builds it from a *float64), tolerating int/int64 too for any
// other caller of Post.
func toBackupReservePercent(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	default:
		return 0, false
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

// postAPIOperation POSTs whichever of backup_reserve_percent / real_mode is
// present in payload to Tesla's Fleet API - the former to
// "api/1/energy_sites/{site_id}/backup" as {"backup_reserve_percent": <int>},
// the latter to "api/1/energy_sites/{site_id}/operation" as
// {"default_real_mode": "<mode>"} (see docs/parity-matrix.md §4).
//
// Tesla applies these as two independent, asynchronous commands, so when
// both fields are present this sends two separate POSTs rather than
// merging them into one payload - and always attempts both regardless of
// whether the first failed, since a failure on one must not silently skip
// the other. Tesla is also documented to cap backup_reserve_percent at 80%
// for cloud/FleetAPI accounts; this function does not attempt to detect or
// pre-empt that cap - it reports success once Tesla has accepted the
// request, and it is the caller's responsibility to re-poll
// "/api/operation" (force=true) afterward to learn the value Tesla actually
// applied, exactly as commands/set.go already does via GetReserveForced.
func (f *PyPowerwallFleetAPI) postAPIOperation(ctx context.Context, payload any, _ string, _, _ bool) (any, error) {
	logger.Load(ctx).DebugContext(ctx, "FleetAPI post operation", "payload", payload)

	fields, _ := payload.(map[string]any)

	var errs []error

	wroteReserve := f.postBackupReserve(ctx, fields, &errs)
	wroteMode := f.postOperationMode(ctx, fields, &errs)

	if wroteReserve || wroteMode {
		f.cache.Invalidate("/api/operation")
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return map[string]any{statusKey: statusSuccess}, nil
}

// postBackupReserve sends the backup_reserve_percent field of fields, if
// present, to Tesla's backup-reserve endpoint, appending any failure to
// errs. It reports whether the write succeeded.
func (f *PyPowerwallFleetAPI) postBackupReserve(ctx context.Context, fields map[string]any, errs *[]error) bool {
	raw, ok := fields["backup_reserve_percent"]
	if !ok {
		return false
	}

	reserve, convOK := toBackupReservePercent(raw)
	if !convOK {
		*errs = append(*errs, fmt.Errorf("%w: backup_reserve_percent", errInvalidOperationPayload))

		return false
	}

	url := fmt.Sprintf("%s/api/1/energy_sites/%s/backup", f.baseURL, f.siteID)
	if err := f.postJSON(ctx, url, map[string]any{"backup_reserve_percent": reserve}); err != nil {
		*errs = append(*errs, err)

		return false
	}

	return true
}

// postOperationMode sends the real_mode field of fields, if present, to
// Tesla's operation-mode endpoint, appending any failure to errs. It
// reports whether the write succeeded.
func (f *PyPowerwallFleetAPI) postOperationMode(ctx context.Context, fields map[string]any, errs *[]error) bool {
	raw, ok := fields["real_mode"]
	if !ok {
		return false
	}

	mode, convOK := raw.(string)
	if !convOK || mode == "" {
		*errs = append(*errs, fmt.Errorf("%w: real_mode", errInvalidOperationPayload))

		return false
	}

	url := fmt.Sprintf("%s/api/1/energy_sites/%s/operation", f.baseURL, f.siteID)
	if err := f.postJSON(ctx, url, map[string]any{"default_real_mode": mode}); err != nil {
		*errs = append(*errs, err)

		return false
	}

	return true
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

// SetGridCharging controls grid charging in FleetAPI mode by POSTing to
// Tesla's "api/1/energy_sites/{site_id}/grid_import_export" endpoint. The
// field Tesla actually reads is
// "disallow_charge_from_grid_with_solar_installed" - mode is forwarded to
// it verbatim, matching pypowerwall_fleetapi.py's own set_grid_charging
// (see docs/parity-matrix.md §4); this is not a "grid charging enabled"
// flag under a different name; it is that flag's caller-facing name mapped
// onto whichever raw field Tesla defines for it.
func (f *PyPowerwallFleetAPI) SetGridCharging(ctx context.Context, mode bool) (map[string]any, error) {
	logger.Load(ctx).DebugContext(ctx, "set grid charging", "mode", mode)

	url := fmt.Sprintf("%s/api/1/energy_sites/%s/grid_import_export", f.baseURL, f.siteID)
	if err := f.postJSON(ctx, url, map[string]any{
		"disallow_charge_from_grid_with_solar_installed": mode,
	}); err != nil {
		return nil, err
	}

	f.cache.Invalidate("SITE_CONFIG")

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

// SetGridExport controls grid export in FleetAPI mode by POSTing to
// Tesla's "api/1/energy_sites/{site_id}/grid_import_export" endpoint with
// {"customer_preferred_export_rule": mode} (see docs/parity-matrix.md §4).
// Valid mode values are validated one layer up, by Powerwall.SetGridExport
// (backend.ErrInvalidGridExportMode) - this method forwards mode verbatim.
func (f *PyPowerwallFleetAPI) SetGridExport(ctx context.Context, mode string) (map[string]any, error) {
	logger.Load(ctx).DebugContext(ctx, "set grid export", "mode", mode)

	url := fmt.Sprintf("%s/api/1/energy_sites/%s/grid_import_export", f.baseURL, f.siteID)
	if err := f.postJSON(ctx, url, map[string]any{
		"customer_preferred_export_rule": mode,
	}); err != nil {
		return nil, err
	}

	f.cache.Invalidate("SITE_CONFIG")

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
