package gopowerwall

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/gopowerwall/backend"
	"github.com/blackbirdworks/gopowerwall/backend/cloud"
	"github.com/blackbirdworks/gopowerwall/backend/fleetapi"
	"github.com/blackbirdworks/gopowerwall/backend/local"
	"github.com/blackbirdworks/gopowerwall/backend/tedapi"
	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/calc"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
	"github.com/blackbirdworks/gopowerwall/pkgs/version"
)

// Lookup safely traverses nested maps and slices using variadic path keys,
// returning nil the moment any key is missing or the current value is not a
// map, instead of panicking. It exists to dig values out of the map[string]any
// (or []any) payloads that the untyped accessors below - [Powerwall.Poll],
// [Powerwall.Status], [Powerwall.Site] and its siblings - return; callers
// using a typed accessor such as [Powerwall.SystemStatus] or
// [Powerwall.SiteInfo] get a concrete Go struct instead and do not need
// Lookup at all.
func Lookup(data any, keys ...string) any {
	return lookup.Lookup(data, keys...)
}

const (
	maxConnectRetries  = 3
	connectRetryWait   = 30 * time.Second
	defaultBackupDur   = 3600
	reserveThreshold80 = 80.0
)

// Powerwall is a facade over a Tesla Energy Gateway, holding at most one
// active backend connection (local, TEDAPI, cloud, or FleetAPI - see
// [ConnectionMode]) behind a mutex, and routing every method call to
// whichever backend is live. Construct one with [New]; it is safe for
// concurrent use by multiple goroutines. The zero value is not usable -
// there is no exported way to build a Powerwall other than New.
type Powerwall struct {
	config       *Config
	local        *local.PyPowerwallLocal
	tedapi       *tedapi.PyPowerwallTEDAPI
	cloud        *cloud.PyPowerwallCloud
	fleetapi     *fleetapi.PyPowerwallFleetAPI
	mode         ConnectionMode
	tedapiMode   TEDAPIMode
	mu           sync.RWMutex
	cloudmode    bool
	fleetapiFlag bool
	tedapiFlag   bool
}

// New builds a [Config] from [DefaultConfig] plus opts, validates it, and
// attempts to connect - matching pypowerwall's own "construct and connect"
// pattern rather than Go's usual "construct, then Dial/Connect separately"
// one.
//
// A non-nil error here means only that the [Config] itself was invalid (see
// [ValidateConfig]): a bad host/port, an invalid email in cloud mode, or an
// unwritable cache/auth directory. A failed *connection* attempt - wrong
// password, unreachable host, expired token file - is not returned as an
// error at all: New logs it and still returns a non-nil *Powerwall with a
// nil error. Callers must check [Powerwall.IsConnected] afterward to find
// out whether it actually has a live backend; every data-fetching method on
// a disconnected Powerwall degrades to a nil/zero-value result rather than
// panicking, but none of them will return real data either.
func New(ctx context.Context, opts ...Option) (*Powerwall, error) {
	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	pw := &Powerwall{
		config:     cfg,
		mode:       ModeUnknown,
		tedapiMode: TEDAPIOff,
	}

	if cfg.Host == "" {
		cfg.CloudMode = true
	}
	switch {
	case cfg.CloudMode && !cfg.FleetAPI:
		pw.mode = ModeCloud
		pw.cloudmode = true
	case cfg.CloudMode && cfg.FleetAPI:
		pw.mode = ModeFleetAPI
		pw.cloudmode = true
		pw.fleetapiFlag = true
	case !cfg.CloudMode && !cfg.FleetAPI:
		pw.mode = ModeLocal
	}

	if cfg.AutoSelect {
		pw.autoSelectMode(ctx, cfg)
	}

	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}

	if !pw.Connect(ctx, cfg.RetryModes) {
		logger.Load(ctx).
			ErrorContext(ctx, "unable to connect to Powerwall, verify host, credentials and network connectivity")
	}

	return pw, nil
}

func (p *Powerwall) autoSelectMode(ctx context.Context, cfg *Config) {
	if cfg.Host != "" && !cfg.CloudMode && !cfg.FleetAPI {
		logger.Load(ctx).DebugContext(ctx, "auto selecting local mode")
		p.mode = ModeLocal
		p.cloudmode = false
		p.fleetapiFlag = false
	} else if _, statErr := os.Stat(filepath.Join(cfg.AuthPath, fleetapi.ConfigFile)); statErr == nil {
		logger.Load(ctx).DebugContext(ctx, "auto selecting FleetAPI mode")
		p.mode = ModeFleetAPI
		p.cloudmode = true
		p.fleetapiFlag = true
	} else if _, cloudStatErr := os.Stat(filepath.Join(cfg.AuthPath, cloud.AuthFile)); cloudStatErr == nil {
		p.mode = ModeCloud
		p.cloudmode = true
		p.fleetapiFlag = false
		logger.Load(ctx).DebugContext(ctx, "auto selecting cloud mode")
	} else {
		logger.Load(ctx).DebugContext(ctx, "auto select failed, no usable local, cloud or fleetapi mode")
	}
}

// Mode returns the active [ConnectionMode]. This can differ from what the
// caller configured: [Powerwall.Connect]'s circular fallback may have moved
// p to a different mode than the one [New] started with, so check Mode
// rather than assuming it still matches the [Option] values passed to New.
func (p *Powerwall) Mode() ConnectionMode {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.mode
}

// TEDAPIMode returns the active [TEDAPIMode] sub-mode: [TEDAPIOff] unless a
// TEDAPI client is layered onto the connection, whether as the primary
// backend ([TEDAPIFull], [TEDAPIV1r]) or alongside an authenticated local
// session ([TEDAPIHybrid]).
func (p *Powerwall) TEDAPIMode() TEDAPIMode {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.tedapiMode
}

func (p *Powerwall) connectLocal(ctx context.Context) bool {
	cfg := p.config

	if cfg.RSAKeyPath != "" {
		pwd := cfg.Password
		if pwd == "" && cfg.GwPwd != "" && len(cfg.GwPwd) >= 5 {
			pwd = cfg.GwPwd[len(cfg.GwPwd)-5:]
		}
		if pwd == "" {
			return false
		}
		v1r, err := tedapi.NewTEDAPIv1r(cfg.Host, pwd, cfg.RSAKeyPath, cfg.Timeout, cfg.PoolMaxSize)
		if err != nil {
			return false
		}
		tedClient := tedapi.NewClient(
			cfg.Host,
			cfg.GwPwd,
			cfg.Timeout,
			cfg.PWCacheExpire,
			cfg.PoolMaxSize,
			cfg.TEDAPIApiVersion,
			cfg.TEDAPIAuthMode,
		)
		tedClient.SetV1rTransport(v1r)
		p.tedapi = tedapi.NewBackend(tedClient, v1r)
		p.tedapiMode = TEDAPIV1r
		p.tedapiFlag = true
		// Pure v1r: no local HTTP backend exists (p.local stays nil), so the
		// facade's reads must dispatch on ModeV1r rather than the leftover
		// ModeLocal value New() assigned before Connect ran - see
		// pollInternal and friends' `case ModeTEDAPI, ModeV1r:` arms.
		p.mode = ModeV1r

		return true
	}

	if cfg.Password == "" && cfg.GwPwd != "" {
		tedClient := tedapi.NewClient(
			cfg.Host,
			cfg.GwPwd,
			cfg.Timeout,
			cfg.PWCacheExpire,
			cfg.PoolMaxSize,
			cfg.TEDAPIApiVersion,
			cfg.TEDAPIAuthMode,
		)
		p.tedapi = tedapi.NewBackend(tedClient, nil)
		p.tedapiMode = TEDAPIFull
		p.tedapiFlag = true
		// Pure TEDAPI: same reasoning as the v1r branch above - no local
		// backend, so p.mode must reflect TEDAPI or every mode-dispatching
		// read method silently falls through the dead ModeLocal branch.
		p.mode = ModeTEDAPI

		return true
	}

	localBackend := local.New(
		cfg.Host,
		cfg.Password,
		cfg.Email,
		cfg.Timezone,
		cfg.Timeout,
		cfg.PWCacheExpire,
		cfg.PoolMaxSize,
		cfg.AuthMode,
		cfg.CacheFile,
		cfg.GwPwd,
	)
	if cfg.GwPwd != "" && (cfg.Host == tedapi.DefaultGWIP || cfg.Host == tedapi.DefaultGWIP+":443") {
		tedClient := tedapi.NewClient(
			cfg.Host,
			cfg.GwPwd,
			cfg.Timeout,
			cfg.PWCacheExpire,
			cfg.PoolMaxSize,
			cfg.TEDAPIApiVersion,
			cfg.TEDAPIAuthMode,
		)
		if tedClient.Connect(ctx) {
			tedBackend := tedapi.NewBackend(tedClient, nil)
			localBackend.SetTEDAPIClient(tedBackend, false)
			p.tedapiMode = TEDAPIHybrid
			p.tedapiFlag = true
		}
	}

	if err := localBackend.Authenticate(ctx); err == nil {
		p.local = localBackend
		p.cloudmode = false
		p.fleetapiFlag = false

		return true
	}

	return false
}

func (p *Powerwall) connectFleetAPI(ctx context.Context) bool {
	fb := fleetapi.New(p.config.Email, p.config.PWCacheExpire, p.config.Timeout, p.config.SiteID, p.config.AuthPath)
	if err := fb.Authenticate(ctx); err == nil {
		p.fleetapi = fb
		p.cloudmode = true
		p.fleetapiFlag = true
		p.tedapiFlag = false
		p.tedapiMode = TEDAPIOff

		return true
	}

	return false
}

func (p *Powerwall) connectCloud(ctx context.Context) bool {
	cb := cloud.New(p.config.Email, p.config.PWCacheExpire, p.config.Timeout, p.config.SiteID, p.config.AuthPath)
	if err := cb.Authenticate(ctx); err == nil {
		p.cloud = cb
		p.cloudmode = true
		p.fleetapiFlag = false
		p.tedapiFlag = false
		p.tedapiMode = TEDAPIOff

		return true
	}

	return false
}

// Connect (re)establishes the backend connection for p's current
// [ConnectionMode], with circular fallback across modes: Local -> FleetAPI
// -> Cloud -> Local, up to three attempts total. It returns true as soon as
// any mode connects, and updates [Powerwall.Mode] to whichever mode that
// was - which may not be the mode p started with. If retry is true, Connect
// sleeps 30 seconds before its last attempt rather than failing immediately.
// [New] calls this once during construction; call it again to retry after a
// connection has dropped or after changing credentials on the underlying
// [Config] is not supported, so a fresh [New] is normally the simpler path
// for that case.
func (p *Powerwall) Connect(ctx context.Context, retry bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.mode == ModeUnknown {
		logger.Load(ctx).ErrorContext(ctx, "unable to determine mode to connect")

		return false
	}

	for attempt := range maxConnectRetries {
		if retry && attempt == maxConnectRetries-1 {
			logger.Load(ctx).
				WarnContext(ctx, "failed to connect with all modes, waiting to retry", "wait", connectRetryWait)
			time.Sleep(connectRetryWait)
		}

		switch p.mode {
		case ModeLocal:
			logger.Load(ctx).DebugContext(ctx, "trying local mode")
			if p.connectLocal(ctx) {
				return true
			}
			logger.Load(ctx).WarnContext(ctx, "local mode failed, trying fleetapi mode")
			p.mode = ModeFleetAPI

		case ModeFleetAPI:
			logger.Load(ctx).DebugContext(ctx, "trying FleetAPI mode")
			if p.connectFleetAPI(ctx) {
				return true
			}
			logger.Load(ctx).WarnContext(ctx, "FleetAPI mode failed, trying cloud mode")
			p.mode = ModeCloud

		case ModeCloud:
			logger.Load(ctx).DebugContext(ctx, "trying cloud mode")
			if p.connectCloud(ctx) {
				return true
			}
			logger.Load(ctx).WarnContext(ctx, "cloud mode failed, trying local mode")
			p.mode = ModeLocal
		default:
			// Remaining modes do not support this operation.
		}
	}

	return false
}

// IsConnected reports whether p currently holds a live backend client. This
// is the check callers must make after [New], since a failed connection
// attempt there does not surface as an error.
func (p *Powerwall) IsConnected() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.local != nil || p.tedapi != nil || p.cloud != nil || p.fleetapi != nil
}

// IsLocal reports whether [Powerwall.Mode] is [ModeLocal]. Note this checks
// the configured mode, not [Powerwall.IsConnected] - it can be true even
// while disconnected.
func (p *Powerwall) IsLocal() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.mode == ModeLocal
}

// IsCloud reports whether [Powerwall.Mode] is [ModeCloud]. Like
// [Powerwall.IsLocal], this checks the configured mode rather than
// [Powerwall.IsConnected].
func (p *Powerwall) IsCloud() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.mode == ModeCloud
}

// IsFleetAPI reports whether [Powerwall.Mode] is [ModeFleetAPI]. Like
// [Powerwall.IsLocal], this checks the configured mode rather than
// [Powerwall.IsConnected].
func (p *Powerwall) IsFleetAPI() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.mode == ModeFleetAPI
}

// IsTEDAPI reports whether [Powerwall.TEDAPIMode] is anything other than
// [TEDAPIOff] - true for a pure TEDAPI/v1r connection as well as a hybrid
// TEDAPI-over-local one.
func (p *Powerwall) IsTEDAPI() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.tedapiMode != TEDAPIOff
}

// Close releases the active backend's resources and clears it, leaving p
// disconnected ([Powerwall.IsConnected] false afterward). It always returns
// nil; the return value exists so Powerwall satisfies patterns expecting an
// io.Closer-shaped Close, not because closing can currently fail. Close does
// not stop a New from being usable again - call [Powerwall.Connect] to
// reconnect the same instance.
func (p *Powerwall) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.local != nil {
		_ = p.local.Close(ctx)
		p.local = nil
	}
	if p.tedapi != nil {
		_ = p.tedapi.Close(ctx)
		p.tedapi = nil
	}
	if p.cloud != nil {
		p.cloud = nil
	}
	if p.fleetapi != nil {
		p.fleetapi = nil
	}

	return nil
}

// PollOption customizes a single call to [Powerwall.Poll], [Powerwall.PollRaw],
// or [Powerwall.PollJSON]. Build one with [WithForce] or [WithRaw].
type PollOption func(*pollConfig)

type pollConfig struct {
	force     bool
	recursive bool
	raw       bool
}

// WithForce sets whether a poll bypasses [Config.PWCacheExpire]'s cache and
// re-fetches from the backend even if a cached value is still fresh. The
// default, when this option is omitted, is false (use the cache).
func WithForce(force bool) PollOption {
	return func(c *pollConfig) { c.force = force }
}

// WithRaw sets whether a poll returns the backend's raw, undecoded response
// body instead of a parsed value. [Powerwall.PollRaw] always behaves as
// though this is true regardless of what is passed here; it is meaningful
// only on [Powerwall.Poll] and [Powerwall.PollJSON]. The default, when this
// option is omitted, is false.
func WithRaw(raw bool) PollOption {
	return func(c *pollConfig) { c.raw = raw }
}

func (p *Powerwall) pollInternal(ctx context.Context, api string, force, recursive, raw bool) (any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	switch p.mode {
	case ModeLocal:
		if p.local != nil {
			return p.local.Poll(ctx, api, force, recursive, raw)
		}
	case ModeTEDAPI, ModeV1r:
		if p.tedapi != nil {
			return p.tedapi.Poll(ctx, api, force, recursive, raw)
		}
	case ModeCloud:
		if p.cloud != nil {
			return p.cloud.Poll(ctx, api, force, recursive, raw)
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			return p.fleetapi.Poll(ctx, api, force, recursive, raw)
		}
	default:
		// Remaining modes do not support this operation.
	}

	return nil, ErrNoClient
}

// Poll queries the gateway API endpoint named by api (e.g.
// "/api/system_status/soe") and returns its decoded response, or nil if the
// call failed for any reason - no client for the active [ConnectionMode],
// network error, non-2xx status, or a JSON decode failure. The error itself
// is discarded; there is no way to distinguish "unreachable" from "not
// found" from this return value alone. The dynamic type of a non-nil result
// is normally map[string]any for a JSON object endpoint (traverse it with
// [Lookup]), but is backend- and endpoint-dependent - callers that need a
// guaranteed shape should prefer a typed accessor such as
// [Powerwall.SystemStatus] or [Powerwall.SiteInfo] where one exists.
func (p *Powerwall) Poll(ctx context.Context, api string, opts ...PollOption) any {
	cfg := &pollConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	val, err := p.pollInternal(ctx, api, cfg.force, cfg.recursive, cfg.raw)
	if err != nil {
		return nil
	}

	return val
}

// PollRaw queries api like [Powerwall.Poll] but returns the response body
// undecoded, or nil on any failure (including one where the endpoint
// responded but with a body that was not itself a []byte, which should not
// happen for a real backend but is not distinguished from a network error
// here). The underlying error is discarded either way.
func (p *Powerwall) PollRaw(ctx context.Context, api string, opts ...PollOption) []byte {
	cfg := &pollConfig{raw: true}
	for _, opt := range opts {
		opt(cfg)
	}
	cfg.raw = true

	val, err := p.pollInternal(ctx, api, cfg.force, cfg.recursive, true)
	if err != nil {
		return nil
	}
	if b, ok := val.([]byte); ok {
		return b
	}

	return nil
}

// PollJSON queries api like [Powerwall.Poll] but re-encodes the result as a
// JSON string, returning "" if the underlying poll failed or the result
// could not be marshalled. An empty string is therefore ambiguous between
// "no data" and "the gateway returned literally an empty response" -
// callers that need to tell those apart should use [Powerwall.PollRaw]
// instead and check for a nil/empty byte slice explicitly alongside their
// own error handling.
func (p *Powerwall) PollJSON(ctx context.Context, api string, opts ...PollOption) string {
	res := p.Poll(ctx, api, opts...)
	if res == nil {
		return ""
	}
	if str, ok := res.(string); ok {
		return str
	}
	if b, ok := res.([]byte); ok {
		return string(b)
	}
	b, err := json.Marshal(res)
	if err != nil {
		return ""
	}

	return string(b)
}

// Post sends payload to the gateway control endpoint named by api,
// returning the decoded response or nil if the call failed - no client for
// the active mode, network error, non-2xx status, or decode failure, with
// the underlying error discarded just as in [Powerwall.Poll]. din, if
// given, is the gateway's device identification number required by some
// control endpoints; only din[0] is ever used, so passing more than one
// value has no additional effect. Most callers should prefer a typed
// method such as [Powerwall.SetReserve] or [Powerwall.SetMode] instead of
// calling Post directly.
func (p *Powerwall) Post(ctx context.Context, api string, payload any, din ...string) any {
	dinStr := ""
	if len(din) > 0 {
		dinStr = din[0]
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	var (
		res any
		err error
	)

	switch p.mode {
	case ModeLocal:
		if p.local != nil {
			res, err = p.local.Post(ctx, api, payload, dinStr, false, false)
		}
	case ModeTEDAPI, ModeV1r:
		if p.tedapi != nil {
			res, err = p.tedapi.Post(ctx, api, payload, dinStr, false, false)
		}
	case ModeCloud:
		if p.cloud != nil {
			res, err = p.cloud.Post(ctx, api, payload, dinStr, false, false)
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			res, err = p.fleetapi.Post(ctx, api, payload, dinStr, false, false)
		}
	default:
		// Remaining modes do not support this operation.
	}

	if err != nil {
		return nil
	}

	return res
}

// Level returns the battery's state-of-charge percentage, or nil if the
// value could not be retrieved - no connection, a network error, or a
// missing/unexpected field in the gateway's response are all reported the
// same way, with the underlying cause discarded. scale, if given, only its
// first element is read: false (the default when omitted) returns the raw
// percentage as the gateway reports it; true rescales it with
// [github.com/blackbirdworks/gopowerwall/pkgs/calc.ScaleBatteryLevel] to
// account for the reserved capacity Tesla does not expose, matching
// pypowerwall's "scale" behavior and the gopowerwall CLI's default display.
func (p *Powerwall) Level(ctx context.Context, scale ...bool) *float64 {
	doScale := false
	if len(scale) > 0 {
		doScale = scale[0]
	}

	data := p.Poll(ctx, "/api/system_status/soe")
	if data == nil {
		return nil
	}

	pct := Lookup(data, "percentage")
	if pct == nil {
		return nil
	}

	var val float64
	switch v := pct.(type) {
	case float64:
		val = v
	case int:
		val = float64(v)
	}

	if doScale {
		val = calc.ScaleBatteryLevel(val)
	}

	return &val
}

// Power returns instant power, in Watts, for the site (grid), solar,
// battery, and load channels as a [models.PowerSummary]. Unlike most
// accessors on Powerwall, Power never returns nil or an error to the
// caller: if the active backend has no client or the underlying poll
// fails, it returns a zero-value PowerSummary (all fields 0) rather than
// distinguishing "no data" from "genuinely zero power" - check
// [Powerwall.IsConnected] first if that distinction matters.
func (p *Powerwall) Power(ctx context.Context) models.PowerSummary {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var (
		res map[string]float64
		err error
	)

	switch p.mode {
	case ModeLocal:
		if p.local != nil {
			res, err = p.local.Power(ctx)
		}
	case ModeTEDAPI, ModeV1r:
		if p.tedapi != nil {
			res, err = p.tedapi.Power(ctx)
		}
	case ModeCloud:
		if p.cloud != nil {
			res, err = p.cloud.Power(ctx)
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			res, err = p.fleetapi.Power(ctx)
		}
	default:
		// Remaining modes do not support this operation.
	}

	if err != nil || res == nil {
		return models.PowerSummary{}
	}

	return models.PowerSummary{
		Site:    res["site"],
		Solar:   res["solar"],
		Battery: res["battery"],
		Load:    res["load"],
		Grid:    res["site"],
		Home:    res["load"],
	}
}

// Site returns site (grid) meter power. verbose, if given, only its first
// element is read: false (the default when omitted) returns a plain
// float64 in Watts - the instant_power field, via [Powerwall.Power]'s
// zero-on-failure PowerSummary.Site; true instead returns the sensor's
// entire reading (voltage, current, cumulative energy, and so on) via
// [Lookup] against "/api/meters/aggregates" - whatever map[string]any (or
// nil, on failure) the gateway's JSON decodes to for that sensor, not a
// fixed Go type. Prefer [Powerwall.Power] when only the Watts figure is
// needed, since it returns a typed
// [github.com/blackbirdworks/gopowerwall/models.PowerSummary] rather than
// any.
func (p *Powerwall) Site(ctx context.Context, verbose ...bool) any {
	return p.fetchSensor(ctx, "site", verbose...)
}

// Solar returns solar power. See [Powerwall.Site] for the meaning of
// verbose and the any return's dynamic type in each case.
func (p *Powerwall) Solar(ctx context.Context, verbose ...bool) any {
	return p.fetchSensor(ctx, "solar", verbose...)
}

// Battery returns battery power (negative while charging, matching
// pypowerwall's sign convention). See [Powerwall.Site] for the meaning of
// verbose and the any return's dynamic type in each case.
func (p *Powerwall) Battery(ctx context.Context, verbose ...bool) any {
	return p.fetchSensor(ctx, "battery", verbose...)
}

// Load returns home load power. See [Powerwall.Site] for the meaning of
// verbose and the any return's dynamic type in each case.
func (p *Powerwall) Load(ctx context.Context, verbose ...bool) any {
	return p.fetchSensor(ctx, "load", verbose...)
}

// Grid is an alias for [Powerwall.Site]: the site meter reading is the grid
// reading. See Site for the meaning of verbose and the any return's dynamic
// type in each case.
func (p *Powerwall) Grid(ctx context.Context, verbose ...bool) any { return p.Site(ctx, verbose...) }

// Home is an alias for [Powerwall.Load]: home load is what Load reports.
// See [Powerwall.Site] for the meaning of verbose and the any return's
// dynamic type in each case.
func (p *Powerwall) Home(ctx context.Context, verbose ...bool) any { return p.Load(ctx, verbose...) }

// fetchSensor dispatches Site/Solar/Battery/Load: verbose[0] (false if
// absent) selects between the sensor's full reading from
// "/api/meters/aggregates" and just its instant_power figure from Power.
func (p *Powerwall) fetchSensor(ctx context.Context, sensor string, verbose ...bool) any {
	isVerbose := false
	if len(verbose) > 0 {
		isVerbose = verbose[0]
	}

	if isVerbose {
		data := p.Poll(ctx, "/api/meters/aggregates")
		if data != nil {
			return Lookup(data, sensor)
		}

		return nil
	}

	summary := p.Power(ctx)
	switch sensor {
	case "site", "grid":
		return summary.Site
	case "solar":
		return summary.Solar
	case "battery":
		return summary.Battery
	case "load", "home":
		return summary.Load
	}

	return 0.0
}

// SiteName returns the configured site name, or nil if it could not be
// retrieved - no connection, a network error, or a missing field are all
// reported the same way, with the underlying cause discarded.
func (p *Powerwall) SiteName(ctx context.Context) *string {
	data := p.Poll(ctx, "/api/site_info/site_name")
	if data == nil {
		return nil
	}
	name := Lookup(data, "site_name")
	if name == nil {
		return nil
	}
	s := fmt.Sprintf("%v", name)

	return &s
}

// Status returns the gateway's "/api/status" response, or nil if it could
// not be retrieved. With no param, the dynamic type of a non-nil result is
// map[string]any (the full decoded status document); with param[0] set to
// a field name (e.g. "version", "din"), Status instead returns just that
// field via [Lookup] - any type the field's JSON value decodes to, or nil
// if the field is absent. Only param[0] is read; passing more than one
// value has no additional effect.
func (p *Powerwall) Status(ctx context.Context, param ...string) any {
	data := p.Poll(ctx, "/api/status")
	if data == nil {
		return nil
	}
	if len(param) > 0 && param[0] != "" {
		return Lookup(data, param[0])
	}

	return data
}

// Version returns the gateway firmware version, or nil if it could not be
// retrieved. intValue, if given, only its first element is read: false (the
// default when omitted) returns the version as a string; true instead
// returns an int from
// [github.com/blackbirdworks/gopowerwall/pkgs/version.ParseVersion] -
// major*10000 + minor*100 + patch from the string's leading dotted-numeric
// run (e.g. "23.44.10" becomes 234410), or 0 if no such run is found.
// Callers should type-assert the result to string or int depending on
// which intValue they passed.
func (p *Powerwall) Version(ctx context.Context, intValue ...bool) any {
	s := p.Status(ctx, "version")
	if s == nil {
		return nil
	}
	strVal := fmt.Sprintf("%v", s)
	if len(intValue) > 0 && intValue[0] {
		return version.ParseVersion(strVal)
	}

	return strVal
}

// Uptime returns the gateway's reported uptime in seconds, formatted as a
// string, or nil if it could not be retrieved - no connection, a network
// error, or a missing field are all reported the same way, with the
// underlying cause discarded.
func (p *Powerwall) Uptime(ctx context.Context) *string {
	s := p.Status(ctx, "up_time_seconds")
	if s == nil {
		return nil
	}
	strVal := fmt.Sprintf("%v", s)

	return &strVal
}

// Din returns the gateway's device identification number, or nil if it
// could not be retrieved - no connection, a network error, or a missing
// field are all reported the same way, with the underlying cause
// discarded.
func (p *Powerwall) Din(ctx context.Context) *string {
	s := p.Status(ctx, "din")
	if s == nil {
		return nil
	}
	strVal := fmt.Sprintf("%v", s)

	return &strVal
}

// Vitals returns per-device vitals keyed by device name (e.g. "TETHC--1",
// "PVAC--2"), each device's own fields keyed by field name (e.g.
// "THC_AmbientTemp", "PVAC_Vsolar0"). Only [ModeLocal], [ModeTEDAPI], and
// [ModeV1r] support this; cloud and FleetAPI connections silently return an
// empty [models.VitalsData] with a nil error, since Tesla's cloud APIs do
// not expose per-device vitals - a nil error does not mean the site
// genuinely has no devices, so check [Powerwall.Mode] first if that
// distinction matters. An error is returned only when the active backend
// does have a client but that client's own Vitals call failed.
func (p *Powerwall) Vitals(ctx context.Context) (models.VitalsData, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var (
		res map[string]any
		err error
	)

	switch p.mode {
	case ModeLocal:
		if p.local != nil {
			res, err = p.local.Vitals(ctx)
		}
	case ModeTEDAPI, ModeV1r:
		if p.tedapi != nil {
			res, err = p.tedapi.Vitals(ctx)
		}
	default:
		// Remaining modes do not support this operation.
	}

	if err != nil {
		return models.VitalsData{}, err
	}

	devices := make(map[string]map[string]any)
	for k, v := range res {
		if devMap, ok := v.(map[string]any); ok {
			devices[k] = devMap
		}
	}

	return models.VitalsData{Devices: devices}, nil
}

// Temps returns each Powerwall's ambient temperature in Celsius, keyed by
// device name, read from the "THC_AmbientTemp" field of every device whose
// name starts with "TETHC" in [Powerwall.Vitals]. It returns an empty
// [models.PowerwallTemps] - never an error - if Vitals fails or the site
// has no TETHC devices; the two cases are not distinguishable from the
// result.
func (p *Powerwall) Temps(ctx context.Context) models.PowerwallTemps {
	temps := make(map[string]float64)
	vitals, err := p.Vitals(ctx)
	if err != nil || len(vitals.Devices) == 0 {
		return models.PowerwallTemps{Temps: temps}
	}

	for dev, data := range vitals.Devices {
		if strings.HasPrefix(dev, "TETHC") {
			if t, ok := data["THC_AmbientTemp"].(float64); ok {
				temps[dev] = t
			}
		}
	}

	return models.PowerwallTemps{Temps: temps}
}

// Alerts returns the sorted, de-duplicated union of every device's alert
// list from [Powerwall.Vitals] plus a synthesized grid-status alert
// ("GridServicesActive" or the raw grid status string). It accepts a
// variadic bool for signature parity with pypowerwall's alerts(), but the
// parameter is ignored entirely - there is no way to filter or otherwise
// change Alerts' behavior by passing one. Errors from the underlying Vitals
// and Poll calls are silently discarded; a disconnected Powerwall returns
// an empty (but non-nil) [models.AlertsList] rather than an error.
func (p *Powerwall) Alerts(ctx context.Context, _ ...bool) models.AlertsList {
	alertSet := make(map[string]struct{})

	vitals, _ := p.Vitals(ctx)
	for _, data := range vitals.Devices {
		switch rawAlerts := data["alerts"].(type) {
		case []any:
			// A JSON-decoded backend (e.g. cloud or fleetapi) yields []any.
			for _, a := range rawAlerts {
				alertSet[fmt.Sprintf("%v", a)] = struct{}{}
			}
		case []string:
			// The local backend stores the protobuf accessor's []string result
			// directly (see backend/local.go's devMap["alerts"] assignment).
			for _, a := range rawAlerts {
				alertSet[a] = struct{}{}
			}
		}
	}

	gridStatus := p.Poll(ctx, "/api/system_status/grid_status")
	if gridStatus != nil {
		if Lookup(gridStatus, "grid_services_active") == true {
			alertSet["GridServicesActive"] = struct{}{}
		} else if gStatus := Lookup(gridStatus, "grid_status"); gStatus != nil {
			alertSet[fmt.Sprintf("%v", gStatus)] = struct{}{}
		}
	}

	list := make([]string, 0, len(alertSet))
	for a := range alertSet {
		norm := strings.ReplaceAll(a, "SystemGridConnected", "SystemConnectedToGrid")
		list = append(list, norm)
	}
	sort.Strings(list)

	return models.AlertsList{Alerts: list}
}

// Strings returns per-solar-string measurements (voltage, current, power),
// keyed by "<PVAC device name>_<label>" (label is one of A/B/C/D) so that a
// site with more than one PVAC inverter keeps each device's four strings
// distinct rather than colliding. It accepts a variadic bool for signature
// parity with pypowerwall's strings(), but the parameter is ignored
// entirely - there is no way to change Strings' behavior by passing one.
// Only [ModeLocal], [ModeTEDAPI], and [ModeV1r] populate this (see
// [Powerwall.Vitals]); other modes, and any failure of the underlying
// Vitals call, silently return an empty (but non-nil)
// [models.SolarStrings].
func (p *Powerwall) Strings(ctx context.Context, _ ...bool) models.SolarStrings {
	strMap := make(map[string]models.StringMetric)
	vitals, _ := p.Vitals(ctx)

	for dev, data := range vitals.Devices {
		if !strings.HasPrefix(dev, "PVAC") {
			continue
		}
		for _, label := range []string{"A", "B", "C", "D"} {
			// Key on the originating PVAC device name plus the string label so
			// that a site with more than one PVAC inverter does not have one
			// device's strings silently overwrite another's. pypowerwall's own
			// upstream /strings implementation keys on a different,
			// firmware-version-specific field naming scheme
			// (PVAC_PVMeasuredVoltage/Current/Power) that has no equivalent for
			// the PVAC_Vsolar<label> fields used here, so there is no directly
			// analogous upstream key to mirror; "<device>_<label>" is the
			// simplest non-colliding choice that still preserves device identity.
			key := dev + "_" + label
			strMap[key] = models.StringMetric{
				Connected: true,
				Voltage:   LookupFloat(data, "PVAC_Vsolar"+label),
				Current:   LookupFloat(data, "PVAC_Isolar"+label),
				Power:     LookupFloat(data, "PVAC_Psolar"+label),
			}
		}
	}

	return models.SolarStrings{Strings: strMap}
}

// BatteryBlocks returns per-battery-module data from "/api/system_status",
// keyed by package serial number. It never returns an error: a
// disconnected Powerwall, a poll failure, a missing "battery_blocks" field,
// or a block with an empty serial number (silently dropped rather than
// added under an empty key) all just shrink or empty the returned map.
func (p *Powerwall) BatteryBlocks(ctx context.Context) map[string]models.BatteryBlock {
	res := make(map[string]models.BatteryBlock)
	sys := p.Poll(ctx, "/api/system_status")
	if sys == nil {
		return res
	}

	blocks, ok := Lookup(sys, "battery_blocks").([]any)
	if !ok {
		return res
	}

	for _, b := range blocks {
		raw, err := json.Marshal(b)
		if err != nil {
			continue
		}
		var block models.BatteryBlock
		if unmarshalErr := json.Unmarshal(raw, &block); unmarshalErr == nil && block.PackageSerialNumber != "" {
			res[block.PackageSerialNumber] = block
		}
	}

	return res
}

// SystemStatus returns the full decoded "/api/system_status" response as a
// [models.SystemStatus]. It returns [ErrNotFound] if the underlying poll
// produced no data (which also covers "no connection" and any network
// failure, since [Powerwall.PollRaw] does not distinguish those from a
// genuine 404), or a wrapped JSON error if the response could not be
// decoded into the struct.
func (p *Powerwall) SystemStatus(ctx context.Context) (models.SystemStatus, error) {
	raw := p.PollRaw(ctx, "/api/system_status")
	if len(raw) == 0 {
		return models.SystemStatus{}, ErrNotFound
	}
	var res models.SystemStatus
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.SystemStatus{}, fmt.Errorf("unmarshal system status: %w", err)
	}

	return res, nil
}

// SOE returns the decoded "/api/system_status/soe" response (state-of-energy
// percentage) as a [models.SOE]. Like [Powerwall.SystemStatus], it returns
// [ErrNotFound] for "no data" (which includes "no connection") and a
// wrapped JSON error on a decode failure. Prefer this over
// [Powerwall.Level] when an error return is more useful than a nil
// *float64.
func (p *Powerwall) SOE(ctx context.Context) (models.SOE, error) {
	raw := p.PollRaw(ctx, "/api/system_status/soe")
	if len(raw) == 0 {
		return models.SOE{}, ErrNotFound
	}
	var res models.SOE
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.SOE{}, fmt.Errorf("unmarshal soe: %w", err)
	}

	return res, nil
}

// GridStatus reports whether the site is connected to the grid, formatted
// according to outputType (only outputType[0] is read; the default when
// omitted is [GridStatusString]). The dynamic type of the result depends on
// which GridStatusOutput was requested: [GridStatusString] (default) and
// [GridStatusJSON] both return a string ("Connected"/"Transition", or a
// JSON-encoded [models.GridStatusResponse]/"{}" on failure);
// [GridStatusNumeric] returns an int, 1 or 0. If the underlying
// [Powerwall.GridStatusResponse] call fails, GridStatus reports "Unknown"
// (or "{}" for JSON) rather than surfacing the error - use
// GridStatusResponse directly when the failure needs to be distinguished
// from a genuine "Transition" state.
func (p *Powerwall) GridStatus(ctx context.Context, outputType ...GridStatusOutput) any {
	t := GridStatusString
	if len(outputType) > 0 {
		t = outputType[0]
	}

	resp, err := p.GridStatusResponse(ctx)
	if err != nil {
		if t == GridStatusJSON {
			return "{}"
		}

		return "Unknown"
	}

	switch t {
	case GridStatusJSON:
		b, _ := json.Marshal(resp)

		return string(b)
	case GridStatusNumeric:
		if resp.GridStatus == "SystemGridConnected" || resp.GridStatus == "SystemConnectedToGrid" {
			return 1
		}

		return 0
	default:
		if resp.GridStatus == "SystemGridConnected" || resp.GridStatus == "SystemConnectedToGrid" {
			return "Connected"
		}

		return "Transition"
	}
}

// GridStatusResponse returns the decoded "/api/system_status/grid_status"
// response as a [models.GridStatusResponse]. Like [Powerwall.SystemStatus],
// it returns [ErrNotFound] for "no data" (which includes "no connection")
// and a wrapped JSON error on a decode failure. [Powerwall.GridStatus]
// builds its formatted output on top of this call.
func (p *Powerwall) GridStatusResponse(ctx context.Context) (models.GridStatusResponse, error) {
	raw := p.PollRaw(ctx, "/api/system_status/grid_status")
	if len(raw) == 0 {
		return models.GridStatusResponse{}, ErrNotFound
	}
	var res models.GridStatusResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.GridStatusResponse{}, fmt.Errorf("unmarshal grid status: %w", err)
	}

	return res, nil
}

// Operation returns the decoded "/api/operation" response - the current
// real mode and backup reserve percentage - as a [models.Operation], using
// the poll cache. Like [Powerwall.SystemStatus], it returns [ErrNotFound]
// for "no data" (which includes "no connection") and a wrapped JSON error
// on a decode failure.
func (p *Powerwall) Operation(ctx context.Context) (models.Operation, error) {
	return p.readOperation(ctx, false)
}

// readOperation fetches /api/operation, optionally bypassing the poll cache.
// force must be true whenever a stale cached value would be unsafe to use -
// e.g. SetOperation's local-mode back-fill, or confirming the value a
// cloud/FleetAPI write actually applied.
func (p *Powerwall) readOperation(ctx context.Context, force bool) (models.Operation, error) {
	opts := make([]PollOption, 0, 1)
	if force {
		opts = append(opts, WithForce(true))
	}
	raw := p.PollRaw(ctx, "/api/operation", opts...)
	if len(raw) == 0 {
		return models.Operation{}, ErrNotFound
	}
	var res models.Operation
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.Operation{}, fmt.Errorf("unmarshal operation: %w", err)
	}

	return res, nil
}

// SiteInfo returns the decoded "/api/site_info" response - site
// configuration parameters such as timezone, grid code, and nominal system
// energy/power - as a [models.SiteInfo]. Like [Powerwall.SystemStatus], it
// returns [ErrNotFound] for "no data" (which includes "no connection") and
// a wrapped JSON error on a decode failure.
func (p *Powerwall) SiteInfo(ctx context.Context) (models.SiteInfo, error) {
	raw := p.PollRaw(ctx, "/api/site_info")
	if len(raw) == 0 {
		return models.SiteInfo{}, ErrNotFound
	}
	var res models.SiteInfo
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.SiteInfo{}, fmt.Errorf("unmarshal site info: %w", err)
	}

	return res, nil
}

// GetReserve returns the current backup reserve percentage, or nil if it
// could not be retrieved - this wraps [Powerwall.Operation], so "no
// connection" and any error from that call both collapse to nil, with the
// underlying error discarded. scale, if given, only its first element is
// read: false (the default when omitted) returns the raw percentage; true
// rescales it with
// [github.com/blackbirdworks/gopowerwall/pkgs/calc.ScaleBatteryLevel], the
// same scaling [Powerwall.Level] applies. GetReserve uses the poll cache;
// see [Powerwall.GetReserveForced] for an uncached read.
func (p *Powerwall) GetReserve(ctx context.Context, scale ...bool) *float64 {
	op, err := p.Operation(ctx)
	if err != nil {
		return nil
	}
	val := op.BackupReservePercent
	if len(scale) > 0 && scale[0] {
		val = calc.ScaleBatteryLevel(val)
	}

	return &val
}

// GetReserveForced returns the current backup reserve percentage (always
// scaled, unlike [Powerwall.GetReserve]), bypassing the poll cache, or nil
// if it could not be retrieved with the underlying error discarded.
// Callers use this right after a reserve write to confirm the value Tesla
// actually applied, since cloud/FleetAPI silently cap the requested reserve
// (e.g. to 80%) rather than rejecting the write.
func (p *Powerwall) GetReserveForced(ctx context.Context) *float64 {
	op, err := p.readOperation(ctx, true)
	if err != nil {
		return nil
	}
	val := calc.ScaleBatteryLevel(op.BackupReservePercent)

	return &val
}

// GetMode returns the current real operating mode (e.g.
// "self_consumption"), or nil if it could not be retrieved or was empty -
// "no connection", an error from the underlying [Powerwall.Operation] call,
// and "the gateway reported an empty mode string" are all reported the
// same way, with any underlying error discarded.
func (p *Powerwall) GetMode(ctx context.Context) *string {
	op, err := p.Operation(ctx)
	if err != nil || op.RealMode == "" {
		return nil
	}

	return &op.RealMode
}

// SetReserve sets the battery backup reserve to level, a percentage in
// [0, 100]. It is a thin wrapper over [Powerwall.SetOperation] with mode
// left nil; see SetOperation's doc comment for the local-mode back-fill
// behavior this triggers and for what the returned [models.Operation]
// actually contains.
func (p *Powerwall) SetReserve(ctx context.Context, level float64) (models.Operation, error) {
	return p.SetOperation(ctx, &level, nil)
}

// SetMode sets the battery's real operating mode (e.g.
// "self_consumption"). It is a thin wrapper over [Powerwall.SetOperation]
// with level left nil; see SetOperation's doc comment for the local-mode
// back-fill behavior this triggers and for what the returned
// [models.Operation] actually contains.
func (p *Powerwall) SetMode(ctx context.Context, mode string) (models.Operation, error) {
	return p.SetOperation(ctx, nil, &mode)
}

// backfillLocalOperation fills in whichever of level/mode the caller omitted
// using the CURRENT gateway state, but only when running in local mode - see
// SetOperation's doc comment for why cloud/FleetAPI/TEDAPI must never receive
// a back-filled payload. The read bypasses the poll cache: a stale cached
// value here would defeat the safeguard. If that read fails, an error is
// returned so the caller can refuse the write outright rather than fall back
// to a partial (and potentially destructive) payload.
func (p *Powerwall) backfillLocalOperation(
	ctx context.Context,
	level *float64,
	mode *string,
) (*float64, *string, error) {
	haveLevel := level != nil
	haveMode := mode != nil && *mode != ""
	if !p.IsLocal() || haveLevel == haveMode {
		return level, mode, nil
	}

	current, err := p.readOperation(ctx, true)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", backend.ErrOperationBackfillFailed, err)
	}
	if !haveLevel {
		backfillLevel := current.BackupReservePercent
		level = &backfillLevel
	}
	if !haveMode {
		backfillMode := current.RealMode
		mode = &backfillMode
	}

	return level, mode, nil
}

// SetOperation sets the battery's backup reserve percentage and/or
// operating mode; pass nil for whichever of level/mode should be left
// unchanged. It returns [backend.ErrReserveOutOfRange] if level is outside
// [0, 100] without attempting the write. On success, the returned
// [models.Operation] simply echoes back the level/mode values SetOperation
// sent (after any local-mode back-fill) - it is not a fresh read-back
// confirming what the gateway actually applied; call
// [Powerwall.GetReserveForced] or [Powerwall.Operation] afterward if that
// confirmation matters (notably, cloud/FleetAPI silently cap the requested
// reserve rather than rejecting an out-of-range write of their own).
//
// The local gateway's /api/operation endpoint is a full overwrite: any field
// omitted from the POST body is reset by the gateway rather than left
// unchanged. So when running in local mode and the caller supplies only one
// of level/mode, backfillLocalOperation reads back the current value of the
// other field and merges it into the payload before it is sent.
//
// Cloud, FleetAPI and TEDAPI apply BACKUP_RESERVE and OPERATION_MODE as two
// independent, asynchronous commands, so a partial payload must reach them
// unchanged: back-filling the omitted field there would race the other
// write. The back-fill therefore only ever applies in local mode.
func (p *Powerwall) SetOperation(ctx context.Context, level *float64, mode *string) (models.Operation, error) {
	if level != nil && (*level < 0 || *level > 100) {
		return models.Operation{}, backend.ErrReserveOutOfRange
	}

	level, mode, err := p.backfillLocalOperation(ctx, level, mode)
	if err != nil {
		return models.Operation{}, err
	}

	payload := make(map[string]any)
	if level != nil {
		payload["backup_reserve_percent"] = *level
	}
	if mode != nil && *mode != "" {
		payload["real_mode"] = *mode
	}

	dinStr := ""
	if d := p.Din(ctx); d != nil {
		dinStr = *d
	}

	res := p.Post(ctx, "/api/operation", payload, dinStr)
	if res == nil {
		return models.Operation{}, backend.ErrSetOperationFailed
	}

	result := models.Operation{}
	if level != nil {
		result.BackupReservePercent = *level
	}
	if mode != nil {
		result.RealMode = *mode
	}

	return result, nil
}

// GetTimeRemaining returns estimated backup time remaining in hours, or nil
// if it could not be retrieved - no client for the active mode or an error
// from the backend's own call are both reported the same way, with the
// underlying error discarded.
func (p *Powerwall) GetTimeRemaining(ctx context.Context) *float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	switch p.mode {
	case ModeLocal:
		if p.local != nil {
			t, _ := p.local.GetTimeRemaining(ctx)

			return t
		}
	case ModeTEDAPI, ModeV1r:
		if p.tedapi != nil {
			t, _ := p.tedapi.GetTimeRemaining(ctx)

			return t
		}
	case ModeCloud:
		if p.cloud != nil {
			t, _ := p.cloud.GetTimeRemaining(ctx)

			return t
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			t, _ := p.fleetapi.GetTimeRemaining(ctx)

			return t
		}
	default:
		// Remaining modes do not support this operation.
	}

	return nil
}

// SetGridCharging enables or disables charging the battery from the grid.
// Only [ModeCloud] and [ModeFleetAPI] support this; other modes return
// [ErrUnsupported]. On both success and backend failure the returned
// [models.Operation] simply echoes mode back in its GridCharging field - it
// is not a read-back of what the gateway actually applied, so a non-nil
// error must still be checked even though the result value looks
// "correct".
func (p *Powerwall) SetGridCharging(ctx context.Context, mode bool) (models.Operation, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	switch p.mode {
	case ModeCloud:
		if p.cloud != nil {
			_, err := p.cloud.SetGridCharging(ctx, mode)

			return models.Operation{GridCharging: mode}, err
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			_, err := p.fleetapi.SetGridCharging(ctx, mode)

			return models.Operation{GridCharging: mode}, err
		}
	default:
		// Remaining modes do not support this operation.
	}

	return models.Operation{}, ErrUnsupported
}

// GetGridCharging returns whether charging the battery from the grid is
// currently enabled, or nil if it could not be retrieved. Only [ModeCloud]
// and [ModeFleetAPI] support this; other modes, as well as any error from
// the backend's own call, are both reported as nil with the underlying
// error discarded.
func (p *Powerwall) GetGridCharging(ctx context.Context) *bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	switch p.mode {
	case ModeCloud:
		if p.cloud != nil {
			b, _ := p.cloud.GetGridCharging(ctx)

			return b
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			b, _ := p.fleetapi.GetGridCharging(ctx)

			return b
		}
	default:
		// Remaining modes do not support this operation.
	}

	return nil
}

// SetGridExport sets the grid export mode, which must be one of
// "battery_ok", "pv_only", or "never" - any other value returns
// [backend.ErrInvalidGridExportMode] without attempting the write. Only
// [ModeCloud] and [ModeFleetAPI] support this; other modes return
// [ErrUnsupported]. On both success and backend failure the returned
// [models.Operation] simply echoes mode back in its GridExport field - it
// is not a read-back of what the gateway actually applied, so a non-nil
// error must still be checked even though the result value looks
// "correct".
func (p *Powerwall) SetGridExport(ctx context.Context, mode string) (models.Operation, error) {
	if mode != "battery_ok" && mode != "pv_only" && mode != "never" {
		return models.Operation{}, fmt.Errorf(
			"%w: %s (must be battery_ok, pv_only, or never)",
			backend.ErrInvalidGridExportMode,
			mode,
		)
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	switch p.mode {
	case ModeCloud:
		if p.cloud != nil {
			_, err := p.cloud.SetGridExport(ctx, mode)

			return models.Operation{GridExport: mode}, err
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			_, err := p.fleetapi.SetGridExport(ctx, mode)

			return models.Operation{GridExport: mode}, err
		}
	default:
		// Remaining modes do not support this operation.
	}

	return models.Operation{}, ErrUnsupported
}

// GetGridExport returns the current grid export mode ("battery_ok",
// "pv_only", or "never"), or nil if it could not be retrieved. Only
// [ModeCloud] and [ModeFleetAPI] support this; other modes, as well as any
// error from the backend's own call, are both reported as nil with the
// underlying error discarded.
func (p *Powerwall) GetGridExport(ctx context.Context) *string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	switch p.mode {
	case ModeCloud:
		if p.cloud != nil {
			s, _ := p.cloud.GetGridExport(ctx)

			return s
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			s, _ := p.fleetapi.GetGridExport(ctx)

			return s
		}
	default:
		// Remaining modes do not support this operation.
	}

	return nil
}

// ScheduleMaxBackup schedules a maximum backup event lasting
// durationSeconds (only durationSeconds[0] is read; the default when
// omitted is 3600, one hour). Only a TEDAPI-based connection ([ModeTEDAPI]
// or [ModeV1r]) supports this; other modes return [ErrUnsupported]. The
// returned [models.Operation] is always the zero value even on success -
// it carries no information about the scheduled event.
func (p *Powerwall) ScheduleMaxBackup(ctx context.Context, durationSeconds ...int) (models.Operation, error) {
	dur := defaultBackupDur
	if len(durationSeconds) > 0 {
		dur = durationSeconds[0]
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		_, err := p.tedapi.ScheduleMaxBackup(ctx, dur)

		return models.Operation{}, err
	}

	return models.Operation{}, ErrUnsupported
}

// CancelMaxBackup cancels a previously scheduled maximum backup event
// ([Powerwall.ScheduleMaxBackup]). Only a TEDAPI-based connection
// ([ModeTEDAPI] or [ModeV1r]) supports this; other modes return
// [ErrUnsupported]. The returned [models.Operation] is always the zero
// value even on success.
func (p *Powerwall) CancelMaxBackup(ctx context.Context) (models.Operation, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		_, err := p.tedapi.CancelMaxBackup(ctx)

		return models.Operation{}, err
	}

	return models.Operation{}, ErrUnsupported
}

// GetBackupEvents returns the gateway's backup event history as a
// map[string]any decoded from the TEDAPI response - there is no typed model
// for this data yet. Only a TEDAPI-based connection ([ModeTEDAPI] or
// [ModeV1r]) supports this; other modes return [ErrUnsupported].
func (p *Powerwall) GetBackupEvents(ctx context.Context) (map[string]any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		return p.tedapi.GetBackupEvents(ctx)
	}

	return nil, ErrUnsupported
}

// GoOffGrid disconnects the system from the grid, deliberately taking the
// site off-grid. confirm must be true or GoOffGrid refuses the request with
// [ErrOffGridConfirm] rather than acting on it - this guard exists because
// the operation is disruptive and hard to reverse instantly. Only a
// TEDAPI-based connection ([ModeTEDAPI] or [ModeV1r]) supports this; other
// modes return [ErrUnsupported]. The returned [models.Operation] is always
// the zero value even on success.
func (p *Powerwall) GoOffGrid(ctx context.Context, confirm bool) (models.Operation, error) {
	if !confirm {
		return models.Operation{}, ErrOffGridConfirm
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		_, err := p.tedapi.GoOffGrid(ctx)

		return models.Operation{}, err
	}

	return models.Operation{}, ErrUnsupported
}

// ReconnectGrid reconnects the system to the grid after
// [Powerwall.GoOffGrid]. Only a TEDAPI-based connection ([ModeTEDAPI] or
// [ModeV1r]) supports this; other modes return [ErrUnsupported]. The
// returned [models.Operation] is always the zero value even on success.
func (p *Powerwall) ReconnectGrid(ctx context.Context) (models.Operation, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		_, err := p.tedapi.ReconnectGrid(ctx)

		return models.Operation{}, err
	}

	return models.Operation{}, ErrUnsupported
}

// GetFileStoreConfig returns the TEDAPI FileStore configuration as a
// map[string]any - there is no typed model for this data yet. Only a
// TEDAPI-based connection ([ModeTEDAPI] or [ModeV1r]) supports this; other
// modes return [ErrUnsupported].
func (p *Powerwall) GetFileStoreConfig(ctx context.Context) (map[string]any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		return p.tedapi.GetConfig(ctx), nil
	}

	return nil, ErrUnsupported
}

// LookupFloat retrieves m[key] as a float64, returning 0.0 if the key is
// absent or its value is neither a float64 nor an int - a missing key and a
// genuinely zero-valued field are therefore indistinguishable in the
// result. It exists to decode numeric fields out of the map[string]any
// device data [Powerwall.Vitals] returns.
func LookupFloat(m map[string]any, key string) float64 {
	if v, ok := m[key]; ok {
		switch num := v.(type) {
		case float64:
			return num
		case int:
			return float64(num)
		}
	}

	return 0.0
}
