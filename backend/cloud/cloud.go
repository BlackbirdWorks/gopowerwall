package cloud

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
	// AuthFile is the filename for cached cloud auth tokens.
	AuthFile = ".pypowerwall.auth"
	// SiteFile is the filename for cached energy site ID.
	SiteFile = ".pypowerwall.site"

	// TeslaAuthURL is the Tesla OAuth2 token endpoint.
	TeslaAuthURL = "https://auth.tesla.com/oauth2/v3/token"
	// TeslaOwnerURL is the Tesla Owner API base endpoint.
	TeslaOwnerURL = "https://owner-api.teslamotors.com"

	// teslaOwnerAPIClientID is the public OAuth2 client id Tesla's own apps
	// use to refresh Owner API tokens. It requires no client secret.
	teslaOwnerAPIClientID = "ownerapi"

	statusKey     = "status"
	statusSuccess = "success"
	filePerm      = 0o600
	siteConfigTTL = 59 * time.Second

	gridStatusConnected = "SystemGridConnected"
	gridStatusIslanded  = "SystemIslandedActive"
)

// PyPowerwallCloud implements the Tesla Cloud Owner API backend.
type PyPowerwallCloud struct {
	cache      *cache.ResponseCache
	client     *http.Client
	tokenData  map[string]any
	pollAPIMap map[string]func(ctx context.Context, force, recursive, raw bool) (any, error)
	postAPIMap map[string]func(ctx context.Context, payload any, din string, recursive, raw bool) (any, error)
	email      string
	siteID     string
	authPath   string
	timeout    time.Duration
	mu         sync.Mutex
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

	authData, err := c.loadOrBootstrapAuthFile(ctx, authFilePath)
	if err != nil {
		return err
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

	tok, err := oauthclient.TokenFromFields(tokMap)
	if err != nil {
		return fmt.Errorf("%w: %w", backend.ErrLogin, err)
	}

	c.client = oauthclient.New(ctx, oauthclient.Config{
		ClientID: teslaOwnerAPIClientID,
		TokenURL: TeslaAuthURL,
		Token:    tok,
		Timeout:  c.timeout,
		OnRefresh: func(newTok *oauth2.Token) error {
			return c.persistToken(authFilePath, authData, newTok)
		},
	})

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

// loadOrBootstrapAuthFile reads authFilePath, or - when it does not exist
// and the TESLA_REFRESH_TOKEN environment variable is set - bootstraps it
// from the refresh token (and optional access token) obtained via
// tesla_auth, so a container's first run can start from a .env file alone
// rather than a pre-existing auth file.
func (c *PyPowerwallCloud) loadOrBootstrapAuthFile(ctx context.Context, authFilePath string) (map[string]any, error) {
	b, err := os.ReadFile(authFilePath)
	if err == nil {
		var authData map[string]any
		if unmarshalErr := json.Unmarshal(b, &authData); unmarshalErr != nil {
			return nil, fmt.Errorf("failed to parse auth file: %w", unmarshalErr)
		}

		return authData, nil
	}

	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s - run setup", backend.ErrMissingAuthFile, authFilePath)
	}

	const envVarName = "TESLA_REFRESH_TOKEN"

	refreshToken := os.Getenv(envVarName)
	if refreshToken == "" {
		return nil, fmt.Errorf("%w: %s - run setup", backend.ErrMissingAuthFile, authFilePath)
	}

	email := c.email
	if email == "" {
		email = "nobody@nowhere.com"
	}

	logger.Load(ctx).InfoContext(ctx, "bootstrapping cloud auth file from "+envVarName,
		"path", authFilePath, "email", email)

	authData := map[string]any{
		email: map[string]any{
			"access_token":  os.Getenv("TESLA_ACCESS_TOKEN"),
			"refresh_token": refreshToken,
			"token_type":    "Bearer",
		},
	}

	if writeErr := atomicfile.WriteJSON(authFilePath, authData, filePerm); writeErr != nil {
		return nil, fmt.Errorf("bootstrap auth file %s: %w", authFilePath, writeErr)
	}

	return authData, nil
}

// persistToken merges tok into authData's entry for c.email and writes the
// whole auth file back atomically, so a refreshed access token survives a
// container restart. Called by the oauth2 client whenever the token used by
// c.client changes.
func (c *PyPowerwallCloud) persistToken(authFilePath string, authData map[string]any, tok *oauth2.Token) error {
	entry, _ := authData[c.email].(map[string]any)
	authData[c.email] = oauthclient.MergeToken(entry, tok)

	return atomicfile.WriteJSON(authFilePath, authData, filePerm)
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

// postJSON POSTs body as JSON to url using c.client, which - once
// Authenticate has run - attaches and transparently refreshes the caller's
// OAuth2 bearer token via pkgs/oauthclient. It never sets the Authorization
// header itself: doing so here would bypass that automatic refresh. A
// non-2xx response is mapped to a backend sentinel error via checkStatus.
func (c *PyPowerwallCloud) postJSON(ctx context.Context, url string, body map[string]any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
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

func (c *PyPowerwallCloud) findEnergySite(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, TeslaOwnerURL+"/api/1/products", nil)
	if err != nil {
		return "", err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if statusErr := checkStatus(resp); statusErr != nil {
		return "", statusErr
	}

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

// fetchSiteJSON GETs url and decodes it as JSON, serving from the cache
// under cacheKey (with an optional custom ttl, like getSiteConfig's shorter
// siteConfigTTL) when force is false and a fresh entry exists. It is the
// shared implementation behind getSiteData/getSiteConfig/getSiteBattery/
// getBackupTimeRemaining, which differ only in cache key, URL, and TTL.
func (c *PyPowerwallCloud) fetchSiteJSON(
	ctx context.Context, cacheKey, url string, force bool, ttl ...time.Duration,
) (map[string]any, error) {
	if !force {
		if val, found, _ := c.cache.Get(cacheKey, ttl...); found {
			if m, ok := val.(map[string]any); ok {
				return m, nil
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
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

	c.cache.Set(cacheKey, data)

	return data, nil
}

func (c *PyPowerwallCloud) getSiteData(ctx context.Context, force bool) (map[string]any, error) {
	url := fmt.Sprintf("%s/api/1/energy_sites/%s/live_status", TeslaOwnerURL, c.siteID)

	return c.fetchSiteJSON(ctx, "SITE_DATA", url, force)
}

func (c *PyPowerwallCloud) getSiteConfig(ctx context.Context, force bool) (map[string]any, error) {
	url := fmt.Sprintf("%s/api/1/energy_sites/%s/site_info", TeslaOwnerURL, c.siteID)

	return c.fetchSiteJSON(ctx, "SITE_CONFIG", url, force, siteConfigTTL)
}

// getSiteBattery fetches Tesla's "api/1/energy_sites/{site_id}/site_status"
// endpoint - upstream's "SITE_SUMMARY" API call (pypowerwall_cloud.py:465-472,
// get_battery()) - which is the only source for total_pack_energy/energy_left
// used by getAPISystemStatus's live overlay (see docs/parity-matrix.md §3.2).
// Cached under the "SITE_SUMMARY" key with the default cache TTL, matching
// upstream's self.pwcacheexpire.
func (c *PyPowerwallCloud) getSiteBattery(ctx context.Context, force bool) (map[string]any, error) {
	url := fmt.Sprintf("%s/api/1/energy_sites/%s/site_status", TeslaOwnerURL, c.siteID)

	return c.fetchSiteJSON(ctx, "SITE_SUMMARY", url, force)
}

// getBackupTimeRemaining fetches Tesla's
// "api/1/energy_sites/{site_id}/backup_time_remaining" endpoint - upstream's
// "ENERGY_SITE_BACKUP_TIME_REMAINING" API call
// (pypowerwall_cloud.py:596-611) - cached under the
// "BACKUP_TIME_REMAINING" key with the default cache TTL, matching
// upstream's self.pwcacheexpire.
func (c *PyPowerwallCloud) getBackupTimeRemaining(ctx context.Context, force bool) (map[string]any, error) {
	url := fmt.Sprintf("%s/api/1/energy_sites/%s/backup_time_remaining", TeslaOwnerURL, c.siteID)

	return c.fetchSiteJSON(ctx, "BACKUP_TIME_REMAINING", url, force)
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

// postAPIOperation POSTs whichever of backup_reserve_percent / real_mode is
// present in payload to Tesla's Owner API - the former to
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
func (c *PyPowerwallCloud) postAPIOperation(ctx context.Context, payload any, _ string, _, _ bool) (any, error) {
	logger.Load(ctx).DebugContext(ctx, "cloud post operation", "payload", payload)

	fields, _ := payload.(map[string]any)

	var errs []error

	wroteReserve := c.postBackupReserve(ctx, fields, &errs)
	wroteMode := c.postOperationMode(ctx, fields, &errs)

	if wroteReserve || wroteMode {
		c.cache.Invalidate("/api/operation")
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return map[string]any{statusKey: statusSuccess}, nil
}

// postBackupReserve sends the backup_reserve_percent field of fields, if
// present, to Tesla's backup-reserve endpoint, appending any failure to
// errs. It reports whether the write succeeded.
func (c *PyPowerwallCloud) postBackupReserve(ctx context.Context, fields map[string]any, errs *[]error) bool {
	raw, ok := fields["backup_reserve_percent"]
	if !ok {
		return false
	}

	reserve, convOK := toBackupReservePercent(raw)
	if !convOK {
		*errs = append(*errs, fmt.Errorf("%w: backup_reserve_percent", errInvalidOperationPayload))

		return false
	}

	url := fmt.Sprintf("%s/api/1/energy_sites/%s/backup", TeslaOwnerURL, c.siteID)
	if err := c.postJSON(ctx, url, map[string]any{"backup_reserve_percent": reserve}); err != nil {
		*errs = append(*errs, err)

		return false
	}

	return true
}

// postOperationMode sends the real_mode field of fields, if present, to
// Tesla's operation-mode endpoint, appending any failure to errs. It
// reports whether the write succeeded.
func (c *PyPowerwallCloud) postOperationMode(ctx context.Context, fields map[string]any, errs *[]error) bool {
	raw, ok := fields["real_mode"]
	if !ok {
		return false
	}

	mode, convOK := raw.(string)
	if !convOK || mode == "" {
		*errs = append(*errs, fmt.Errorf("%w: real_mode", errInvalidOperationPayload))

		return false
	}

	url := fmt.Sprintf("%s/api/1/energy_sites/%s/operation", TeslaOwnerURL, c.siteID)
	if err := c.postJSON(ctx, url, map[string]any{"default_real_mode": mode}); err != nil {
		*errs = append(*errs, err)

		return false
	}

	return true
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

// getAPISystemStatus overlays nine live, site-specific values onto
// stubs.SystemStatusStub(), matching upstream's get_api_system_status
// (pypowerwall_cloud.py:835-874): nominal_full_pack_energy/
// nominal_energy_remaining (from the site's total_pack_energy/energy_left),
// max_charge_power/max_discharge_power/max_apparent_power (from
// nameplate_power), grid_services_power, system_island_state (derived from
// island_status/grid_status), available_blocks/blocks_controlled (from
// battery_count), and solar_real_power_limit (from solar_power). Like
// upstream, it requires all three of getSiteData/getSiteConfig/
// getSiteBattery to succeed and returns [backend.ErrNotFound] - upstream's
// None - otherwise, rather than the raw placeholder stub (see
// docs/parity-matrix.md §3.2).
func (c *PyPowerwallCloud) getAPISystemStatus(ctx context.Context, force bool) (any, error) {
	power, _ := c.getSiteData(ctx, force)
	cfg, _ := c.getSiteConfig(ctx, force)
	battery, _ := c.getSiteBattery(ctx, force)
	if power == nil || cfg == nil || battery == nil {
		return nil, backend.ErrNotFound
	}

	gridState := gridStatusIslanded
	rawGridStatus := fmt.Sprintf("%v", lookup.Lookup(power, "response", "grid_status"))
	onGrid := fmt.Sprintf("%v", lookup.Lookup(power, "response", "island_status")) == "on_grid"

	if onGrid || rawGridStatus == "Active" || rawGridStatus == "Unknown" {
		gridState = gridStatusConnected
	}

	nameplatePower := lookup.Lookup(cfg, "response", "nameplate_power")
	batteryCount := lookup.Lookup(cfg, "response", "battery_count")

	stub := stubs.SystemStatusStub()
	stub["nominal_full_pack_energy"] = lookup.Lookup(battery, "response", "total_pack_energy")
	stub["nominal_energy_remaining"] = lookup.Lookup(battery, "response", "energy_left")
	stub["max_charge_power"] = nameplatePower
	stub["max_discharge_power"] = nameplatePower
	stub["max_apparent_power"] = nameplatePower
	stub["grid_services_power"] = lookup.Lookup(power, "response", "grid_services_power")
	stub["system_island_state"] = gridState
	stub["available_blocks"] = batteryCount
	stub["blocks_controlled"] = batteryCount
	stub["solar_real_power_limit"] = lookup.Lookup(power, "response", "solar_power")

	return stub, nil
}

func (c *PyPowerwallCloud) getAPISystemStatusGridStatus(ctx context.Context, force bool) (any, error) {
	data, _ := c.getSiteData(ctx, force)
	statusStr := gridStatusConnected
	if data != nil {
		gridStatus := lookup.Lookup(data, "response", "grid_status")
		if gridStatus != nil && fmt.Sprintf("%v", gridStatus) != "Active" &&
			fmt.Sprintf("%v", gridStatus) != "Unknown" {
			statusStr = gridStatusIslanded
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

// GetTimeRemaining returns the time remaining until Powerwall is depleted,
// querying Tesla's "api/1/energy_sites/{site_id}/backup_time_remaining"
// endpoint for the live response.time_remaining_hours value, matching
// upstream's get_time_remaining (pypowerwall_cloud.py:596-611). It returns
// 0.0 only as upstream's narrow fallback for a well-formed response that
// lacks the time_remaining_hours key; a network failure or non-2xx status
// is surfaced as an error, unlike upstream's silent None (see
// docs/parity-matrix.md §4).
func (c *PyPowerwallCloud) GetTimeRemaining(ctx context.Context) (*float64, error) {
	data, err := c.getBackupTimeRemaining(ctx, false)
	if err != nil {
		return nil, err
	}

	if raw := lookup.Lookup(data, "response", "time_remaining_hours"); raw != nil {
		hours := getFloat(raw)

		return &hours, nil
	}

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

// SetGridCharging controls grid charging in cloud mode by POSTing to
// Tesla's "api/1/energy_sites/{site_id}/grid_import_export" endpoint. The
// field Tesla actually reads,
// "disallow_charge_from_grid_with_solar_installed", is phrased as a
// prohibition, not as the enable flag the caller-facing "mode" name
// implies: upstream's own set_grid_charging negates the caller's boolean
// before writing it (pypowerwall_cloud.py:878-891 - "mode = False" when the
// caller asked to enable, "mode = True" when the caller asked to disable),
// so gopowerwall negates it too. mode=true ("enable grid charging") sends
// disallow_charge_from_grid_with_solar_installed=false, and mode=false
// sends =true (see docs/parity-matrix.md §1 item 30 and §4).
func (c *PyPowerwallCloud) SetGridCharging(ctx context.Context, mode bool) (map[string]any, error) {
	logger.Load(ctx).DebugContext(ctx, "set grid charging", "mode", mode)

	url := fmt.Sprintf("%s/api/1/energy_sites/%s/grid_import_export", TeslaOwnerURL, c.siteID)
	if err := c.postJSON(ctx, url, map[string]any{
		"disallow_charge_from_grid_with_solar_installed": !mode,
	}); err != nil {
		return nil, err
	}

	c.cache.Invalidate("SITE_CONFIG")

	return map[string]any{statusKey: statusSuccess}, nil
}

// GetGridCharging returns current grid charging mode: the logical negation
// of "response.components.disallow_charge_from_grid_with_solar_installed"
// in the site_info response, matching upstream's get_grid_charging
// (pypowerwall_cloud.py:920-923). The field defaults to "enabled" (true)
// when absent, exactly as "not state" does in Python when
// components.get(...) returns None.
func (c *PyPowerwallCloud) GetGridCharging(ctx context.Context) (*bool, error) {
	cfg, err := c.getSiteConfig(ctx, false)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, backend.ErrNotFound
	}
	disallow, _ := lookup.Lookup(cfg, "response", "components", "disallow_charge_from_grid_with_solar_installed").(bool)
	enabled := !disallow

	return &enabled, nil
}

// SetGridExport controls grid export setting by POSTing to Tesla's
// "api/1/energy_sites/{site_id}/grid_import_export" endpoint with
// {"customer_preferred_export_rule": mode} (see docs/parity-matrix.md §4).
// Valid mode values are validated one layer up, by Powerwall.SetGridExport
// (backend.ErrInvalidGridExportMode) - this method forwards mode verbatim.
func (c *PyPowerwallCloud) SetGridExport(ctx context.Context, mode string) (map[string]any, error) {
	logger.Load(ctx).DebugContext(ctx, "set grid export", "mode", mode)

	url := fmt.Sprintf("%s/api/1/energy_sites/%s/grid_import_export", TeslaOwnerURL, c.siteID)
	if err := c.postJSON(ctx, url, map[string]any{
		"customer_preferred_export_rule": mode,
	}); err != nil {
		return nil, err
	}

	c.cache.Invalidate("SITE_CONFIG")

	return map[string]any{statusKey: statusSuccess}, nil
}

// GetGridExport returns current grid export mode: "never" when
// "response.components.non_export_configured" is set, else
// "response.components.customer_preferred_export_rule", defaulting to
// "battery_ok" when absent - matching upstream's get_grid_export
// (pypowerwall_cloud.py:926-933). The previous implementation read a
// nonexistent top-level "response.customer_preferred_export_rule" field -
// the real field lives under "components", exactly like
// disallow_charge_from_grid_with_solar_installed (see GetGridCharging) -
// and had neither the non_export_configured special case nor the
// "battery_ok" default, reporting ErrNotFound wherever upstream would
// return a normal string.
func (c *PyPowerwallCloud) GetGridExport(ctx context.Context) (*string, error) {
	cfg, err := c.getSiteConfig(ctx, false)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, backend.ErrNotFound
	}

	components, _ := lookup.Lookup(cfg, "response", "components").(map[string]any)

	if nonExport, _ := components["non_export_configured"].(bool); nonExport {
		never := "never"

		return &never, nil
	}

	mode, ok := components["customer_preferred_export_rule"].(string)
	if !ok || mode == "" {
		mode = "battery_ok"
	}

	return &mode, nil
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
