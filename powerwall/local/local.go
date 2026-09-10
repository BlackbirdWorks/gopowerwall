package local

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/cache"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
	"github.com/blackbirdworks/gopowerwall/powerwall/proto/teslapower"
	"github.com/blackbirdworks/gopowerwall/powerwall/tedapi"

	"google.golang.org/protobuf/proto"
)

const (
	cookieAuth       = "AuthCookie"
	cookieUser       = "UserRecord"
	filePerm         = 0o600
	negCacheTTL      = 600 * time.Second
	cooldownTTL      = 300 * time.Second
	headerAuth       = "Authorization"
	headerBearer     = "Bearer "
	bearerParts      = 2
	apiDevicesVitals = "/api/devices/vitals"
)

// Client implements the gateway REST models.
type Client struct {
	cache        *cache.ResponseCache
	tedapiClient *tedapi.PyPowerwallTEDAPI
	client       *http.Client
	cachefile    string
	email        string
	host         string
	authToken    string
	authmode     models.AuthMode
	password     string
	gwPwd        string
	timezone     string
	authCookies  []*http.Cookie
	poolMaxSize  int
	timeout      time.Duration
	mu           sync.Mutex
	vitalsAPI    bool
	isPW3        bool
}

// PyPowerwallLocal is an alias for Client.
type PyPowerwallLocal = Client

// New creates a new Client backend instance.
func New(
	host, password, email, timezone string,
	timeout, cacheTTL time.Duration,
	poolMaxSize int,
	authmode models.AuthMode,
	cachefile, gwPwd string,
) *Client {
	if authmode != models.AuthModeToken {
		authmode = models.AuthModeCookie
	}

	transport := &http.Transport{
		//nolint:gosec // Local Powerwall gateway uses self-signed HTTPS certificate.
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		MaxIdleConns:        poolMaxSize,
		MaxIdleConnsPerHost: poolMaxSize,
		DisableKeepAlives:   poolMaxSize == 0,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	return &Client{
		host:        host,
		password:    password,
		email:       email,
		timezone:    timezone,
		timeout:     timeout,
		poolMaxSize: poolMaxSize,
		authmode:    authmode,
		cachefile:   cachefile,
		gwPwd:       gwPwd,
		cache:       cache.NewResponseCache(cacheTTL),
		client:      client,
		vitalsAPI:   true,
	}
}

// SetTEDAPIClient sets the embedded hybrid TEDAPI client.
func (l *Client) SetTEDAPIClient(c *tedapi.PyPowerwallTEDAPI, isPW3 bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tedapiClient = c
	l.isPW3 = isPW3
}

// Authenticate attempts to load cached credentials or log in to the gateway.
func (l *Client) Authenticate(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	logger.Load(ctx).DebugContext(ctx, "local mode enabled")

	// Try loading cached auth session
	if l.loadAuthCache() {
		logger.Load(ctx).DebugContext(ctx, "loaded auth from cache", "file", l.cachefile, "authmode", l.authmode)

		return nil
	}

	// Login and create a new session
	return l.loginLocked(ctx)
}

func (l *Client) loadTokenCache(data map[string]string) bool {
	auth, ok := data[headerAuth]
	if !ok {
		return false
	}
	parts := strings.Split(auth, " ")
	if len(parts) == bearerParts {
		l.authToken = parts[1]

		return true
	}

	return false
}

func (l *Client) loadCookieCache(data map[string]string) bool {
	cookieVal, hasCookie := data[cookieAuth]
	userVal, hasUser := data[cookieUser]
	if hasCookie && hasUser {
		l.authCookies = []*http.Cookie{
			{Name: cookieAuth, Value: cookieVal, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode},
			{Name: cookieUser, Value: userVal, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode},
		}

		return true
	}

	return false
}

func (l *Client) loadAuthCache() bool {
	if l.cachefile == "" {
		return false
	}
	f, err := os.Open(l.cachefile)
	if err != nil {
		return false
	}
	defer f.Close()

	var data map[string]string
	if decodeErr := json.NewDecoder(f).Decode(&data); decodeErr != nil {
		return false
	}

	if l.authmode == models.AuthModeToken {
		return l.loadTokenCache(data)
	}

	return l.loadCookieCache(data)
}

func (l *Client) loginLocked(ctx context.Context) error {
	url := fmt.Sprintf("https://%s/api/login/Basic", l.host)
	payload := map[string]any{
		"username": "customer",
		"password": l.password,
		"email":    l.email,
		"clientInfo": map[string]string{
			"timezone": l.timezone,
		},
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.client.Do(req)
	if err != nil {
		logger.Load(ctx).ErrorContext(ctx, "unable to connect to Powerwall", "host", l.host, "error", err)

		return err
	}
	defer resp.Body.Close()

	logger.Load(ctx).DebugContext(ctx, "login response", "status", resp.StatusCode)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: invalid password for %s", models.ErrLogin, l.host)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: login failed for %s (HTTP %d)", models.ErrLogin, l.host, resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if l.authmode == models.AuthModeToken {
		var res models.LoginResponse
		if unmarshalErr := json.Unmarshal(respBody, &res); unmarshalErr == nil && res.Token != "" {
			l.authToken = res.Token
			l.saveAuthCache(ctx, map[string]string{
				headerAuth: headerBearer + res.Token,
			})

			return nil
		}
	}

	// Default: cookie mode
	authCookie, userRecord := extractAuthCookies(resp.Cookies())
	if authCookie != "" && userRecord != "" {
		l.authCookies = []*http.Cookie{
			{Name: cookieAuth, Value: authCookie, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode},
			{Name: cookieUser, Value: userRecord, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode},
		}
		l.saveAuthCache(ctx, map[string]string{
			cookieAuth: authCookie,
			cookieUser: userRecord,
		})

		return nil
	}

	return fmt.Errorf("%w: missing auth tokens/cookies in login response", models.ErrLogin)
}

func extractAuthCookies(cookies []*http.Cookie) (string, string) {
	var authCookie, userRecord string
	for _, c := range cookies {
		switch c.Name {
		case cookieAuth:
			authCookie = c.Value
		case cookieUser:
			userRecord = c.Value
		}
	}

	return authCookie, userRecord
}

func (l *Client) saveAuthCache(ctx context.Context, data map[string]string) {
	if l.cachefile == "" {
		return
	}
	f, err := os.OpenFile(l.cachefile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePerm)
	if err != nil {
		logger.Load(ctx).DebugContext(ctx, "unable to cache auth session", "error", err)

		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(data)
}

// Close logs out and releases connections.
func (l *Client) Close(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	url := fmt.Sprintf("https://%s/api/logout", l.host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err == nil {
		l.applyAuth(req)
		resp, doErr := l.client.Do(req)
		if doErr == nil {
			_ = resp.Body.Close()
		}
	}

	l.authCookies = nil
	l.authToken = ""
	if l.tedapiClient != nil {
		_ = l.tedapiClient.Close(ctx)
	}

	return nil
}

func (l *Client) applyAuth(req *http.Request) {
	if l.authmode == models.AuthModeToken && l.authToken != "" {
		req.Header.Set(headerAuth, headerBearer+l.authToken)
	} else {
		for _, c := range l.authCookies {
			req.AddCookie(c)
		}
	}
}

func (l *Client) handleHTTPStatus(
	ctx context.Context,
	statusCode int,
	api, url string,
	force, recursive, raw bool,
) (any, bool, error) {
	switch {
	case statusCode == http.StatusNotFound:
		logger.Load(ctx).ErrorContext(ctx, "Powerwall API not found", "url", url, "status", statusCode)
		if api == apiDevicesVitals {
			l.vitalsAPI = false
		}
		l.setEndpointNegative(api, negCacheTTL)

		return nil, true, models.ErrNotFound
	case statusCode == http.StatusTooManyRequests:
		logger.Load(ctx).ErrorContext(ctx, "rate limited by Powerwall API, activating cooldown",
			"url", url, "status", statusCode, "cooldown", cooldownTTL)
		l.cache.SetCooldown(cooldownTTL)

		return nil, true, models.ErrRateLimited
	case statusCode == http.StatusServiceUnavailable:
		logger.Load(ctx).ErrorContext(ctx, "Powerwall API unavailable, activating cooldown",
			"url", url, "status", statusCode, "cooldown", cooldownTTL)
		l.cache.SetCooldown(cooldownTTL)
		l.setEndpointNegative(api, cooldownTTL)

		return nil, true, models.ErrRateLimited
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		if !recursive {
			logger.Load(ctx).DebugContext(ctx, "session expired, requesting a new one")
			l.mu.Lock()
			loginErr := l.loginLocked(ctx)
			l.mu.Unlock()
			if loginErr == nil {
				res, pollErr := l.Poll(ctx, api, force, true, raw)

				return res, true, pollErr
			}
		}
		l.setEndpointNegative(api, negCacheTTL)

		return nil, true, models.ErrLogin
	case statusCode >= http.StatusBadRequest:
		logger.Load(ctx).ErrorContext(ctx, "unexpected HTTP response", "status", statusCode, "url", url)

		return nil, true, fmt.Errorf("%w: HTTP %d", models.ErrUnexpectedStatus, statusCode)
	default:
		return nil, false, nil
	}
}

// setEndpointNegative records a negative cache entry for api under both the
// raw and parsed keyspaces. Whether an endpoint is missing, rate limited, or
// requires re-authentication is a property of the endpoint itself, not of
// the representation a caller asked for, so a negative result observed on a
// raw poll must suppress a subsequent parsed poll of the same endpoint, and
// vice versa. Caching the two representations under distinct keys (see
// [cache.RawKey]) means that sharing has to be done explicitly here rather
// than falling out of the key scheme automatically.
func (l *Client) setEndpointNegative(api string, ttl time.Duration) {
	l.cache.SetNegative(api, ttl)
	l.cache.SetNegative(cache.RawKey(api), ttl)
}

// Poll queries the Powerwall Gateway API and caches the response. Raw and
// parsed reads of the same api are cached under distinct keys (see
// [cache.RawKey]) so that a parsed poll can never hand a raw caller a
// map[string]any (or vice versa) - see the [Client.setEndpointNegative]
// doc for how negative results stay consistent across the two keyspaces
// regardless.
func (l *Client) Poll(ctx context.Context, api string, force, recursive, raw bool) (any, error) {
	// /api/devices/vitals is always served as a raw protobuf payload - decide
	// this before computing the cache key below so the lookup, the eventual
	// fetch, and the resulting cache write all agree on the same keyspace.
	if api == apiDevicesVitals {
		raw = true
	}

	if !force {
		key := api
		if raw {
			key = cache.RawKey(api)
		}
		val, found, isNegative := l.cache.Get(key)
		if found {
			if isNegative {
				logger.Load(ctx).DebugContext(ctx, "returning cached negative result", "api", api)

				return nil, models.ErrNotFound
			}
			logger.Load(ctx).DebugContext(ctx, "returning cached result", "api", api)

			return val, nil
		}
	}

	if l.cache.InCooldown() {
		logger.Load(ctx).DebugContext(ctx, "rate limit cooldown active, pausing API calls")

		return nil, models.ErrRateLimited
	}

	if api == apiDevicesVitals && !l.vitalsAPI {
		return nil, models.ErrNotFound
	}

	url := fmt.Sprintf("https://%s%s", l.host, api)
	logger.Load(ctx).DebugContext(ctx, "requesting Powerwall API", "api", api)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	l.applyAuth(req)

	resp, err := l.client.Do(req)
	if err != nil {
		logger.Load(ctx).ErrorContext(ctx, "error connecting to Powerwall", "url", url, "error", err)

		return nil, err
	}
	defer resp.Body.Close()

	if res, handled, statusErr := l.handleHTTPStatus(ctx, resp.StatusCode, api, url, force, recursive, raw); handled {
		return res, statusErr
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if raw {
		l.cache.Set(cache.RawKey(api), data)

		return data, nil
	}

	// Try unmarshaling JSON
	var parsed any
	if unmarshalErr := json.Unmarshal(data, &parsed); unmarshalErr == nil {
		l.cache.Set(api, parsed)

		return parsed, nil
	}

	// Fall back to raw string
	strVal := string(data)
	l.cache.Set(api, strVal)

	return strVal, nil
}

// Post sends a command to the Powerwall Gateway.
func (l *Client) Post(
	ctx context.Context,
	api string,
	payload any,
	din string,
	recursive, raw bool,
) (any, error) {
	url := fmt.Sprintf("https://%s%s", l.host, api)

	var reqBody io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	l.applyAuth(req)

	resp, err := l.client.Do(req)
	if err != nil {
		logger.Load(ctx).ErrorContext(ctx, "error connecting to Powerwall", "url", url, "error", err)

		return nil, err
	}
	defer resp.Body.Close()

	if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && !recursive {
		l.mu.Lock()
		loginErr := l.loginLocked(ctx)
		l.mu.Unlock()
		if loginErr == nil {
			return l.Post(ctx, api, payload, din, true, raw)
		}
	}

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Invalidate associated cached read endpoints
	l.cache.Invalidate(api)

	if raw {
		return respData, nil
	}

	var parsed any
	if unmarshalErr := json.Unmarshal(respData, &parsed); unmarshalErr == nil {
		return parsed, nil
	}

	return string(respData), nil
}

func extractDeviceMetadata(device *teslapower.Device) map[string]any {
	devMap := make(map[string]any)
	if din := device.GetComponentParentDin(); din != nil {
		devMap["componentParentDin"] = din.GetValue()
	}
	if pn := device.GetPartNumber(); pn != nil {
		devMap["partNumber"] = pn.GetValue()
	}
	if sn := device.GetSerialNumber(); sn != nil {
		devMap["serialNumber"] = sn.GetValue()
	}
	if mfg := device.GetManufacturer(); mfg != nil {
		devMap["manufacturer"] = mfg.GetValue()
	}
	if fw := device.GetFirmwareVersion(); fw != nil {
		devMap["firmwareVersion"] = fw.GetValue()
	}
	if fc := device.GetFirstCommunicationTime(); fc != nil {
		devMap["firstCommunicationTime"] = fc.GetSeconds()
	}
	if lc := device.GetLastCommunicationTime(); lc != nil {
		devMap["lastCommunicationTime"] = lc.GetSeconds()
	}

	return devMap
}

func extractDeviceAttributes(attrs *teslapower.DeviceAttributes) map[string]any {
	attrMap := make(map[string]any)
	if ecu := attrs.GetTeslaEnergyEcuAttributes(); ecu != nil {
		attrMap["teslaEnergyEcuAttributes"] = map[string]any{
			"ecuType": int64(ecu.GetEcuType()),
		}
	}
	if gen := attrs.GetGeneratorAttributes(); gen != nil {
		attrMap["generatorAttributes"] = map[string]any{
			"nameplateRealPowerW":      gen.GetNameplateRealPowerW(),
			"nameplateApparentPowerVa": gen.GetNameplateApparentPowerVa(),
		}
	}
	if inv := attrs.GetPvInverterAttributes(); inv != nil {
		attrMap["pvInverterAttributes"] = map[string]any{
			"nameplateRealPowerW": inv.GetNameplateRealPowerW(),
		}
	}
	if meter := attrs.GetMeterAttributes(); meter != nil {
		locs := make([]int64, 0, len(meter.GetMeterLocation()))
		for _, loc := range meter.GetMeterLocation() {
			locs = append(locs, int64(loc))
		}
		attrMap["meterAttributes"] = map[string]any{
			"meterLocation": locs,
		}
	}

	return attrMap
}

func extractDeviceVitals(devMap map[string]any, vitals []*teslapower.DeviceVital) {
	for _, v := range vitals {
		vName := v.GetName()
		if vName == "" {
			continue
		}
		switch val := v.GetValue().(type) {
		case *teslapower.DeviceVital_IntValue:
			devMap[vName] = val.IntValue
		case *teslapower.DeviceVital_FloatValue:
			devMap[vName] = val.FloatValue
		case *teslapower.DeviceVital_StringValue:
			devMap[vName] = val.StringValue
		case *teslapower.DeviceVital_BoolValue:
			devMap[vName] = val.BoolValue
		}
	}
}

// Vitals decodes device vitals via TEDAPI (hybrid) or protobuf /api/devices/vitals.
func (l *Client) Vitals(ctx context.Context) (map[string]any, error) {
	l.mu.Lock()
	tedapi := l.tedapiClient
	l.mu.Unlock()

	if tedapi != nil {
		return tedapi.Vitals(ctx)
	}

	raw, err := l.Poll(ctx, apiDevicesVitals, false, false, true)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, models.ErrNotFound
	}

	data, ok := raw.([]byte)
	if !ok {
		return nil, models.ErrNotFound
	}

	var pb teslapower.DevicesWithVitals
	if unmarshalErr := proto.Unmarshal(data, &pb); unmarshalErr != nil {
		logger.Load(ctx).ErrorContext(ctx, "failed to decode DevicesWithVitals protobuf", "error", unmarshalErr)

		return nil, unmarshalErr
	}

	devs := pb.GetDevices()
	output := make(map[string]any, len(devs))
	for _, devItem := range devs {
		device := devItem.GetDevice().GetDevice()
		if device == nil {
			continue
		}
		din := device.GetDin()
		if din == nil || din.GetValue() == "" {
			continue
		}
		name := din.GetValue()

		devMap, exists := output[name].(map[string]any)
		if !exists {
			devMap = extractDeviceMetadata(device)
			output[name] = devMap
		}

		if attrs := device.GetDeviceAttributes(); attrs != nil {
			maps.Copy(devMap, extractDeviceAttributes(attrs))
		}

		extractDeviceVitals(devMap, devItem.GetVitals())

		if len(devItem.GetAlerts()) > 0 {
			devMap["alerts"] = devItem.GetAlerts()
		}
	}

	return output, nil
}

// GetTimeRemaining calculates backup time remaining based on nominal energy and load.
func (l *Client) GetTimeRemaining(ctx context.Context) (*float64, error) {
	d, err := l.Poll(ctx, "/api/system_status", false, false, false)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, models.ErrNotFound
	}

	rem := lookup.Lookup(d, "nominal_energy_remaining")
	if rem == nil {
		return nil, models.ErrNotFound
	}

	var remVal float64
	switch v := rem.(type) {
	case float64:
		remVal = v
	case int:
		remVal = float64(v)
	case string:
		f, parseErr := strconv.ParseFloat(v, 64)
		if parseErr == nil {
			remVal = f
		}
	}

	pwr, pwrErr := l.Power(ctx)
	if pwrErr == nil && pwr["load"] > 0 {
		hours := remVal / pwr["load"]

		return &hours, nil
	}

	return nil, models.ErrNotFound
}

// Power returns the instant power for site, solar, battery, load.
func (l *Client) Power(ctx context.Context) (map[string]float64, error) {
	site, solar, battery, load := 0.0, 0.0, 0.0, 0.0
	payload, err := l.Poll(ctx, "/api/meters/aggregates", false, false, false)
	if err == nil && payload != nil {
		site = getInstantPower(lookup.Lookup(payload, "site"))
		solar = getInstantPower(lookup.Lookup(payload, "solar"))
		battery = getInstantPower(lookup.Lookup(payload, "battery"))
		load = getInstantPower(lookup.Lookup(payload, "load"))
	}

	return map[string]float64{
		"site":    site,
		"solar":   solar,
		"battery": battery,
		"load":    load,
	}, nil
}

// FetchPower returns single sensor power or aggregate structure if verbose is true.
func (l *Client) FetchPower(ctx context.Context, sensor string, verbose bool) (any, error) {
	if verbose {
		payload, err := l.Poll(ctx, "/api/meters/aggregates", false, false, false)
		if err == nil && payload != nil {
			return lookup.Lookup(payload, sensor), nil
		}

		return nil, err
	}
	p, err := l.Power(ctx)
	if err != nil {
		return 0.0, err
	}

	return p[sensor], nil
}

// HasTEDAPI returns whether hybrid TEDAPI is active.
func (l *Client) HasTEDAPI() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.tedapiClient != nil
}

// IsPW3 returns whether PW3 was detected in hybrid mode.
func (l *Client) IsPW3() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.isPW3
}

func getInstantPower(val any) float64 {
	if val == nil {
		return 0.0
	}
	inst := lookup.Lookup(val, "instant_power")
	if inst == nil {
		return 0.0
	}
	switch v := inst.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case int32:
		return float64(v)
	case uint:
		return float64(v)
	case uint64:
		return float64(v)
	case uint32:
		return float64(v)
	default:
		return 0.0
	}
}
