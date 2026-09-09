package powerwall

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
// A non-nil error here is one of two things. Most commonly it means the
// [Config] itself was invalid (see [ValidateConfig]): a bad host/port, an
// invalid email in cloud mode, or an unwritable cache/auth directory - in
// that case the returned *Powerwall is nil. Otherwise, the Config validated
// fine but the initial connection attempt failed across every applicable
// mode; New still returns a non-nil, usable *Powerwall in that case,
// wrapping the failure as a [ConnectError] rather than discarding it.
// Callers that only care about "do I have a live backend" can skip the
// distinction entirely and check [Powerwall.IsConnected]; every
// data-fetching method on a disconnected Powerwall degrades to a
// zero-value/error result rather than panicking, but none of them will
// return real data either. Callers that want to know *why* the connection
// failed can use [errors.As] against the returned error for a *ConnectError.
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

		return pw, &backend.ConnectError{Mode: string(pw.Mode())}
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

// Level returns the battery's raw state-of-charge percentage, as the gateway
// reports it, or an error if the value could not be retrieved - no
// connection, a network error, and a missing/unexpected field in the
// gateway's response are distinguished via [ErrNoClient], the poll's own
// error, and [ErrFieldMissing] respectively. See [Powerwall.LevelScaled] for
// the rescaled percentage pypowerwall calls "scale=True" and the gopowerwall
// CLI displays by default.
func (p *Powerwall) Level(ctx context.Context) (float64, error) {
	data, err := p.pollInternal(ctx, "/api/system_status/soe", false, false, false)
	if err != nil {
		return 0, err
	}

	pct := lookup.Lookup(data, "percentage")

	return floatFromAny(pct)
}

// LevelScaled returns the battery's state-of-charge percentage rescaled with
// [github.com/blackbirdworks/gopowerwall/pkgs/calc.ScaleBatteryLevel] to
// account for the reserved capacity Tesla does not expose, matching
// pypowerwall's "scale=True" behavior and the gopowerwall CLI's default
// display. See [Powerwall.Level] for the error cases and the unscaled
// percentage.
func (p *Powerwall) LevelScaled(ctx context.Context) (float64, error) {
	val, err := p.Level(ctx)
	if err != nil {
		return 0, err
	}

	return calc.ScaleBatteryLevel(val), nil
}

// floatFromAny converts a decoded JSON numeric value (float64 or int) to
// float64, returning [ErrFieldMissing] for nil or any other dynamic type -
// the shared tail end of every typed accessor built on [lookup.Lookup]
// against an untyped poll result.
func floatFromAny(v any) (float64, error) {
	switch val := v.(type) {
	case float64:
		return val, nil
	case int:
		return float64(val), nil
	default:
		return 0, ErrFieldMissing
	}
}

// powerSummary is the shared implementation behind [Powerwall.Power] and the
// per-channel Site/Solar/Battery/Load/Grid/Home accessors: it returns a
// PowerSummary and, unlike Power's own public contract, does not discard the
// underlying error.
func (p *Powerwall) powerSummary(ctx context.Context) (models.PowerSummary, error) {
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
		} else {
			err = ErrNoClient
		}
	case ModeTEDAPI, ModeV1r:
		if p.tedapi != nil {
			res, err = p.tedapi.Power(ctx)
		} else {
			err = ErrNoClient
		}
	case ModeCloud:
		if p.cloud != nil {
			res, err = p.cloud.Power(ctx)
		} else {
			err = ErrNoClient
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			res, err = p.fleetapi.Power(ctx)
		} else {
			err = ErrNoClient
		}
	default:
		err = ErrUnsupported
	}

	if err != nil {
		return models.PowerSummary{}, err
	}
	if res == nil {
		return models.PowerSummary{}, ErrFieldMissing
	}

	return models.PowerSummary{
		Site:    res["site"],
		Solar:   res["solar"],
		Battery: res["battery"],
		Load:    res["load"],
		Grid:    res["site"],
		Home:    res["load"],
	}, nil
}

// Power returns instant power, in Watts, for the site (grid), solar,
// battery, and load channels as a [models.PowerSummary]. Unlike most
// accessors on Powerwall, Power never returns an error to the caller: if the
// active backend has no client or the underlying poll fails, it returns a
// zero-value PowerSummary (all fields 0) rather than distinguishing "no
// data" from "genuinely zero power" - check [Powerwall.IsConnected] first if
// that distinction matters, or use [Powerwall.Site] and its siblings for the
// same figures with an error return.
func (p *Powerwall) Power(ctx context.Context) models.PowerSummary {
	res, _ := p.powerSummary(ctx)

	return res
}

// Site returns site (grid) meter power in Watts - the instant_power field,
// via [Powerwall.Power]'s underlying poll - or an error if it could not be
// retrieved. See [Powerwall.SiteReading] for the sensor's entire reading
// (voltage, current, cumulative energy, and so on) rather than just this
// scalar figure.
func (p *Powerwall) Site(ctx context.Context) (float64, error) {
	res, err := p.powerSummary(ctx)

	return res.Site, err
}

// Solar returns solar power in Watts. See [Powerwall.Site] for the error
// cases, and [Powerwall.SolarReading] for the sensor's full reading.
func (p *Powerwall) Solar(ctx context.Context) (float64, error) {
	res, err := p.powerSummary(ctx)

	return res.Solar, err
}

// Battery returns battery power in Watts (negative while charging, matching
// pypowerwall's sign convention). See [Powerwall.Site] for the error cases,
// and [Powerwall.BatteryReading] for the sensor's full reading.
func (p *Powerwall) Battery(ctx context.Context) (float64, error) {
	res, err := p.powerSummary(ctx)

	return res.Battery, err
}

// Load returns home load power in Watts. See [Powerwall.Site] for the error
// cases, and [Powerwall.LoadReading] for the sensor's full reading.
func (p *Powerwall) Load(ctx context.Context) (float64, error) {
	res, err := p.powerSummary(ctx)

	return res.Load, err
}

// Grid is an alias for [Powerwall.Site]: the site meter reading is the grid
// reading.
func (p *Powerwall) Grid(ctx context.Context) (float64, error) { return p.Site(ctx) }

// Home is an alias for [Powerwall.Load]: home load is what Load reports.
func (p *Powerwall) Home(ctx context.Context) (float64, error) { return p.Load(ctx) }

// SiteReading returns the site (grid) meter's entire reading - voltage,
// current, cumulative energy, and so on, not just its instant_power figure -
// as a [models.MeterReading] decoded from "/api/meters/aggregates". It
// applies no [AggregatesOption] corrections; see [Powerwall.Aggregates] for
// the full corrected multi-sensor view.
func (p *Powerwall) SiteReading(ctx context.Context) (models.MeterReading, error) {
	agg, err := p.Aggregates(ctx)

	return agg.Site, err
}

// SolarReading returns the solar sensor's entire reading. See
// [Powerwall.SiteReading] for the shared behavior.
func (p *Powerwall) SolarReading(ctx context.Context) (models.MeterReading, error) {
	agg, err := p.Aggregates(ctx)

	return agg.Solar, err
}

// BatteryReading returns the battery sensor's entire reading. See
// [Powerwall.SiteReading] for the shared behavior.
func (p *Powerwall) BatteryReading(ctx context.Context) (models.MeterReading, error) {
	agg, err := p.Aggregates(ctx)

	return agg.Battery, err
}

// LoadReading returns the load sensor's entire reading. See
// [Powerwall.SiteReading] for the shared behavior.
func (p *Powerwall) LoadReading(ctx context.Context) (models.MeterReading, error) {
	agg, err := p.Aggregates(ctx)

	return agg.Load, err
}

// GridReading is an alias for [Powerwall.SiteReading].
func (p *Powerwall) GridReading(ctx context.Context) (models.MeterReading, error) {
	return p.SiteReading(ctx)
}

// HomeReading is an alias for [Powerwall.LoadReading].
func (p *Powerwall) HomeReading(ctx context.Context) (models.MeterReading, error) {
	return p.LoadReading(ctx)
}

// SiteName returns the configured site name, or an error if it could not be
// retrieved.
func (p *Powerwall) SiteName(ctx context.Context) (string, error) {
	data, err := p.pollInternal(ctx, "/api/site_info/site_name", false, false, false)
	if err != nil {
		return "", err
	}
	name := lookup.Lookup(data, "site_name")
	if name == nil {
		return "", ErrFieldMissing
	}

	return fmt.Sprintf("%v", name), nil
}

// Status returns the gateway's decoded "/api/status" response as a
// [models.GatewayStatus], or an error if it could not be retrieved. Use
// [Powerwall.Version], [Powerwall.Uptime], or [Powerwall.Din] for a single
// field rather than fetching and decoding the whole document.
func (p *Powerwall) Status(ctx context.Context) (models.GatewayStatus, error) {
	raw := p.PollRaw(ctx, "/api/status")
	if len(raw) == 0 {
		return models.GatewayStatus{}, ErrNotFound
	}
	var res models.GatewayStatus
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.GatewayStatus{}, fmt.Errorf("unmarshal status: %w", err)
	}

	return res, nil
}

// Version returns the gateway firmware version string (e.g.
// "23.44.10 abc12345"), or an error if it could not be retrieved. See
// [Powerwall.VersionNumeric] for the same value parsed into a comparable
// int.
func (p *Powerwall) Version(ctx context.Context) (string, error) {
	st, err := p.Status(ctx)
	if err != nil {
		return "", err
	}
	if st.Version == "" {
		return "", ErrFieldMissing
	}

	return st.Version, nil
}

// VersionNumeric returns the gateway firmware version parsed by
// [github.com/blackbirdworks/gopowerwall/pkgs/version.ParseVersion] -
// major*10000 + minor*100 + patch from the version string's leading
// dotted-numeric run (e.g. "23.44.10" becomes 234410) - or an error if the
// version itself could not be retrieved. A version string with no
// recognisable dotted-numeric run parses to 0 without an error, matching
// ParseVersion's own zero-on-no-match contract.
func (p *Powerwall) VersionNumeric(ctx context.Context) (int, error) {
	s, err := p.Version(ctx)
	if err != nil {
		return 0, err
	}

	return version.ParseVersion(s), nil
}

// Uptime returns the gateway's reported uptime, or an error if it could not
// be retrieved or the gateway's "up_time_seconds" string could not be parsed
// as a [time.Duration] (it is already formatted like one, e.g.
// "1541h38m20.998412744s", despite the field's name).
func (p *Powerwall) Uptime(ctx context.Context) (time.Duration, error) {
	st, err := p.Status(ctx)
	if err != nil {
		return 0, err
	}
	if st.UpTimeSeconds == "" {
		return 0, ErrFieldMissing
	}
	d, parseErr := time.ParseDuration(st.UpTimeSeconds)
	if parseErr != nil {
		return 0, fmt.Errorf("parse uptime %q: %w", st.UpTimeSeconds, parseErr)
	}

	return d, nil
}

// Din returns the gateway's device identification number, or an error if it
// could not be retrieved.
func (p *Powerwall) Din(ctx context.Context) (string, error) {
	st, err := p.Status(ctx)
	if err != nil {
		return "", err
	}
	if st.DIN == "" {
		return "", ErrFieldMissing
	}

	return st.DIN, nil
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

	devices := make(map[string]map[string]any, len(res))
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
	vitals, err := p.Vitals(ctx)
	if err != nil || len(vitals.Devices) == 0 {
		return models.PowerwallTemps{Temps: make(map[string]float64)}
	}

	temps := make(map[string]float64, len(vitals.Devices))
	for dev, data := range vitals.Devices {
		if strings.HasPrefix(dev, "TETHC") {
			if t, ok := data["THC_AmbientTemp"].(float64); ok {
				temps[dev] = t
			}
		}
	}

	return models.PowerwallTemps{Temps: temps}
}

func collectDeviceAlerts(devices map[string]map[string]any, alertSet map[string]struct{}) {
	for _, data := range devices {
		switch rawAlerts := data["alerts"].(type) {
		case []any:
			// A JSON-decoded backend (e.g. cloud or fleetapi) yields []any.
			for _, a := range rawAlerts {
				if s, ok := a.(string); ok {
					alertSet[s] = struct{}{}
				} else {
					alertSet[fmt.Sprint(a)] = struct{}{}
				}
			}
		case []string:
			// The local backend stores the protobuf accessor's []string result
			// directly (see backend/local.go's devMap["alerts"] assignment).
			for _, a := range rawAlerts {
				alertSet[a] = struct{}{}
			}
		}
	}
}

func collectGridStatusAlert(gridStatus any, alertSet map[string]struct{}) {
	if gridStatus == nil {
		return
	}
	if lookup.Lookup(gridStatus, "grid_services_active") == true {
		alertSet["GridServicesActive"] = struct{}{}

		return
	}
	gStatus := lookup.Lookup(gridStatus, "grid_status")
	if gStatus == nil {
		return
	}
	if s, ok := gStatus.(string); ok {
		alertSet[s] = struct{}{}

		return
	}
	alertSet[fmt.Sprint(gStatus)] = struct{}{}
}

// Alerts returns the sorted, de-duplicated union of every device's alert
// list from [Powerwall.Vitals] plus a synthesized grid-status alert
// ("GridServicesActive" or the raw grid status string). Errors from the
// underlying Vitals and Poll calls are silently discarded; a disconnected
// Powerwall returns an empty (but non-nil) [models.AlertsList] rather than
// an error.
func (p *Powerwall) Alerts(ctx context.Context) models.AlertsList {
	alertSet := make(map[string]struct{})

	vitals, _ := p.Vitals(ctx)
	collectDeviceAlerts(vitals.Devices, alertSet)

	gridStatus := p.Poll(ctx, "/api/system_status/grid_status")
	collectGridStatusAlert(gridStatus, alertSet)

	list := make([]string, 0, len(alertSet))
	for a := range alertSet {
		norm := strings.ReplaceAll(a, "SystemGridConnected", "SystemConnectedToGrid")
		list = append(list, norm)
	}
	sort.Strings(list)

	return models.AlertsList{Alerts: list}
}

// lookupFloat retrieves m[key] as a float64, returning 0 if the key is
// absent or its value is neither a float64 nor an int - a missing key and a
// genuinely zero-valued field are therefore indistinguishable in the
// result. It exists to decode numeric fields out of the map[string]any
// per-device data [Powerwall.Vitals] returns.
func lookupFloat(m map[string]any, key string) float64 {
	v, _ := floatFromAny(m[key])

	return v
}

// lookupFloatPtr retrieves m[key] as a *float64, returning nil if the key is
// absent or its value is neither a float64 nor an int - unlike lookupFloat,
// it preserves the missing/zero distinction, matching pypowerwall's
// get_value(m, key), which returns raw None (JSON null) for a missing field
// rather than coercing it to zero (server.py:1132-1137, used throughout
// generate_pod's vitals-augmentation pass for the power/energy fields).
func lookupFloatPtr(m map[string]any, key string) *float64 {
	v, err := floatFromAny(m[key])
	if err != nil {
		return nil
	}

	return &v
}

// intOrZero coerces m[key] to an int, mirroring pypowerwall's
// int(get_value(v, key) or 0) coercion used throughout generate_pod's
// TEPOD vitals-augmentation pass (server.py:2196-2244): a missing, nil,
// false, or zero-valued field all yield 0, matching Python's falsy-or-0
// fallback; any other value is coerced to its int equivalent rather than
// clamped to 1, since upstream's "or 0" only substitutes a default and
// otherwise passes the field through as-is.
func intOrZero(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case bool:
		if v {
			return 1
		}

		return 0
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

// solarStringLabels are the possible PV string labels vitals fields carry.
// PW3 gateways report up to six strings, A-F; PW2 gateways report at most
// four, A-D. See pypowerwall's tedapi vitals synthesis
// (pypowerwall/tedapi/__init__.py:1030, "PW3 has 6 strings A-F") and its
// device_controller esCan.bus.PVAC fields (A-D only). Iterating the full A-F
// superset is safe for both: a label a given device does not report is
// simply absent from its vitals map and skipped (see hasStringLabel below).
//
//nolint:gochecknoglobals // Read-only constant table, not mutated.
var solarStringLabels = []string{"A", "B", "C", "D", "E", "F"}

// hasStringLabel reports whether data carries any per-string vitals field
// for label, so [Powerwall.Strings] does not fabricate an all-zero entry for
// a label a device never reported (e.g. E/F on a 4-string PW2 gateway).
func hasStringLabel(data map[string]any, label string) bool {
	suffixes := []string{
		"PVAC_PVMeasuredVoltage_",
		"PVAC_PVCurrent_",
		"PVAC_PVMeasuredPower_",
		"PVAC_PvState_",
	}
	for _, suffix := range suffixes {
		if _, ok := data[suffix+label]; ok {
			return true
		}
	}

	return false
}

// Strings returns per-solar-string measurements (voltage, current, power,
// state, and connected status), keyed by "<PVAC device name>_<label>" so
// that a site with more than one PVAC inverter keeps each device's strings
// distinct rather than colliding - pypowerwall's own upstream strings()
// keys on a letter derived from the field name plus a rotating per-device
// index instead (pypowerwall/__init__.py:495-549), a scheme this package
// has no direct equivalent for since Go's vitals map does not preserve
// PVAC-device iteration order; "<device>_<label>" is the simplest
// non-colliding choice that still preserves device identity.
//
// Each field is read from the real gateway vitals field names upstream
// produces: PVAC_PVMeasuredVoltage_<label>, PVAC_PVCurrent_<label>, and
// PVAC_PVMeasuredPower_<label> for voltage/current/power,
// PVAC_PvState_<label> for the raw PV state string, and
// PVS_String<label>_Connected - read from the sibling "PVS" device sharing
// the same device-name suffix as the PVAC device - for Connected. That
// mirrors pypowerwall/__init__.py:497-549's own field scan (it merges the
// PVS device's "*String*" fields into the PVAC device's dict before
// scanning) and pypowerwall/tedapi/__init__.py:1032-1069's TEDAPI-mode
// synthesis of those same field names from raw PCH_Pv* signals. Connected
// is read verbatim from that field rather than re-derived from State: on
// TEDAPI it was itself derived from state ("Pv_Active" in state) by the
// backend's own vitals synthesis, but on local firmware it is an
// independent hardware reading, so Powerwall.Strings must not recompute it.
//
// Only [ModeLocal], [ModeTEDAPI], and [ModeV1r] populate this (see
// [Powerwall.Vitals]); other modes, and any failure of the underlying
// Vitals call, silently return an empty (but non-nil) [models.SolarStrings].
func (p *Powerwall) Strings(ctx context.Context) models.SolarStrings {
	strMap := make(map[string]models.StringMetric)
	vitals, _ := p.Vitals(ctx)

	for dev, data := range vitals.Devices {
		if !strings.HasPrefix(dev, "PVAC") {
			continue
		}

		// The sibling PVS device shares everything after the "PVAC" prefix
		// in its own name (e.g. "PVAC--1" / "PVS--1"), mirroring
		// pypowerwall/__init__.py:509's `"PVS" + str(device)[4:]`.
		pvsData := vitals.Devices["PVS"+dev[len("PVAC"):]]

		for _, label := range solarStringLabels {
			if !hasStringLabel(data, label) {
				continue
			}

			var connected bool
			if pvsData != nil {
				connected, _ = pvsData["PVS_String"+label+"_Connected"].(bool)
			}

			state, _ := data["PVAC_PvState_"+label].(string)

			key := dev + "_" + label
			strMap[key] = models.StringMetric{
				Connected: connected,
				Voltage:   lookupFloat(data, "PVAC_PVMeasuredVoltage_"+label),
				Current:   lookupFloat(data, "PVAC_PVCurrent_"+label),
				Power:     lookupFloat(data, "PVAC_PVMeasuredPower_"+label),
				State:     state,
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
	sys := p.Poll(ctx, "/api/system_status")
	if sys == nil {
		return make(map[string]models.BatteryBlock)
	}

	blocks, ok := lookup.Lookup(sys, "battery_blocks").([]any)
	if !ok {
		return make(map[string]models.BatteryBlock)
	}

	res := make(map[string]models.BatteryBlock, len(blocks))

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
// wrapped JSON error on a decode failure. [Powerwall.Level] covers the same
// field with additional [ErrFieldMissing] handling for a malformed
// response; both now return an error rather than a nil pointer.
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

// gridConnected reports whether resp represents a grid-connected state,
// matching either spelling the gateway uses for it across firmware
// versions/backends.
func gridConnected(resp models.GridStatusResponse) bool {
	return resp.GridStatus == "SystemGridConnected" || resp.GridStatus == "SystemConnectedToGrid"
}

// GridStatusString reports whether the site is connected to the grid as a
// human-readable string, "Connected" or "Transition", or an error if the
// underlying [Powerwall.GridStatusResponse] call failed. See
// [Powerwall.GridStatusNumeric] for the same information as 1/0.
func (p *Powerwall) GridStatusString(ctx context.Context) (string, error) {
	resp, err := p.GridStatusResponse(ctx)
	if err != nil {
		return "", err
	}
	if gridConnected(resp) {
		return "Connected", nil
	}

	return "Transition", nil
}

// GridStatusNumeric reports whether the site is connected to the grid as 1
// (connected) or 0 (not connected), matching pypowerwall's numeric output
// mode, or an error if the underlying [Powerwall.GridStatusResponse] call
// failed. See [Powerwall.GridStatusString] for the same information as a
// string.
func (p *Powerwall) GridStatusNumeric(ctx context.Context) (int, error) {
	resp, err := p.GridStatusResponse(ctx)
	if err != nil {
		return 0, err
	}
	if gridConnected(resp) {
		return 1, nil
	}

	return 0, nil
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

// GetReserve returns the current backup reserve percentage as the gateway
// reports it, or an error if it could not be retrieved - this wraps
// [Powerwall.Operation]. GetReserve uses the poll cache; see
// [Powerwall.GetReserveForced] for an uncached, always-scaled read, and
// [Powerwall.GetReserveScaled] for a cached, scaled read.
func (p *Powerwall) GetReserve(ctx context.Context) (float64, error) {
	op, err := p.Operation(ctx)
	if err != nil {
		return 0, err
	}

	return op.BackupReservePercent, nil
}

// GetReserveScaled returns the current backup reserve percentage rescaled
// with [github.com/blackbirdworks/gopowerwall/pkgs/calc.ScaleBatteryLevel],
// the same scaling [Powerwall.LevelScaled] applies. See [Powerwall.GetReserve]
// for the unscaled, cached read this builds on.
func (p *Powerwall) GetReserveScaled(ctx context.Context) (float64, error) {
	val, err := p.GetReserve(ctx)
	if err != nil {
		return 0, err
	}

	return calc.ScaleBatteryLevel(val), nil
}

// GetReserveForced returns the current backup reserve percentage (always
// scaled, unlike [Powerwall.GetReserve]), bypassing the poll cache, or an
// error if it could not be retrieved. Callers use this right after a
// reserve write to confirm the value Tesla actually applied, since
// cloud/FleetAPI silently cap the requested reserve (e.g. to 80%) rather
// than rejecting the write.
func (p *Powerwall) GetReserveForced(ctx context.Context) (float64, error) {
	op, err := p.readOperation(ctx, true)
	if err != nil {
		return 0, err
	}

	return calc.ScaleBatteryLevel(op.BackupReservePercent), nil
}

// GetMode returns the current real operating mode (e.g.
// "self_consumption"), or an error if it could not be retrieved or was
// empty - an error from the underlying [Powerwall.Operation] call, and "the
// gateway reported an empty mode string" ([ErrFieldMissing]), are
// distinguished from each other via errors.Is/errors.As.
func (p *Powerwall) GetMode(ctx context.Context) (string, error) {
	op, err := p.Operation(ctx)
	if err != nil {
		return "", err
	}
	if op.RealMode == "" {
		return "", ErrFieldMissing
	}

	return op.RealMode, nil
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

	dinStr, _ := p.Din(ctx)

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

// GetTimeRemaining returns estimated backup time remaining, or an error if
// it could not be retrieved - no client for the active mode
// ([ErrNoClient]), the backend's own call failing, or the backend reporting
// no value at all ([ErrFieldMissing]) are all distinguishable via
// errors.Is/errors.As.
func (p *Powerwall) GetTimeRemaining(ctx context.Context) (time.Duration, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var (
		hours *float64
		err   error
	)

	switch p.mode {
	case ModeLocal:
		if p.local != nil {
			hours, err = p.local.GetTimeRemaining(ctx)
		} else {
			err = ErrNoClient
		}
	case ModeTEDAPI, ModeV1r:
		if p.tedapi != nil {
			hours, err = p.tedapi.GetTimeRemaining(ctx)
		} else {
			err = ErrNoClient
		}
	case ModeCloud:
		if p.cloud != nil {
			hours, err = p.cloud.GetTimeRemaining(ctx)
		} else {
			err = ErrNoClient
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			hours, err = p.fleetapi.GetTimeRemaining(ctx)
		} else {
			err = ErrNoClient
		}
	default:
		err = ErrUnsupported
	}

	if err != nil {
		return 0, err
	}
	if hours == nil {
		return 0, ErrFieldMissing
	}

	return time.Duration(*hours * float64(time.Hour)), nil
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
// currently enabled, or an error if it could not be retrieved. Only
// [ModeCloud] and [ModeFleetAPI] support this; other modes return
// [ErrUnsupported].
func (p *Powerwall) GetGridCharging(ctx context.Context) (bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var (
		val *bool
		err error
	)

	switch p.mode {
	case ModeCloud:
		if p.cloud != nil {
			val, err = p.cloud.GetGridCharging(ctx)
		} else {
			err = ErrNoClient
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			val, err = p.fleetapi.GetGridCharging(ctx)
		} else {
			err = ErrNoClient
		}
	default:
		err = ErrUnsupported
	}

	if err != nil {
		return false, err
	}
	if val == nil {
		return false, ErrFieldMissing
	}

	return *val, nil
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
// "pv_only", or "never"), or an error if it could not be retrieved. Only
// [ModeCloud] and [ModeFleetAPI] support this; other modes return
// [ErrUnsupported].
func (p *Powerwall) GetGridExport(ctx context.Context) (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var (
		val *string
		err error
	)

	switch p.mode {
	case ModeCloud:
		if p.cloud != nil {
			val, err = p.cloud.GetGridExport(ctx)
		} else {
			err = ErrNoClient
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			val, err = p.fleetapi.GetGridExport(ctx)
		} else {
			err = ErrNoClient
		}
	default:
		err = ErrUnsupported
	}

	if err != nil {
		return "", err
	}
	if val == nil {
		return "", ErrFieldMissing
	}

	return *val, nil
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

// GetTEDAPIStatus returns the basic device controller status map.
func (p *Powerwall) GetTEDAPIStatus(ctx context.Context) (map[string]any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		return p.tedapi.GetStatus(ctx), nil
	}

	return nil, ErrUnsupported
}

// GetTEDAPIComponents returns raw component signals query response.
func (p *Powerwall) GetTEDAPIComponents(ctx context.Context) (map[string]any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		return p.tedapi.GetComponents(ctx), nil
	}

	return nil, ErrUnsupported
}

// GetTEDAPIBattery returns battery blocks extracted from TEDAPI configuration.
func (p *Powerwall) GetTEDAPIBattery(ctx context.Context) ([]any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		return p.tedapi.GetBatteryBlocks(ctx), nil
	}

	return nil, ErrUnsupported
}

// GetTEDAPIDeviceController returns the full device controller query response.
func (p *Powerwall) GetTEDAPIDeviceController(ctx context.Context) (map[string]any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		return p.tedapi.GetDeviceController(ctx), nil
	}

	return nil, ErrUnsupported
}

// GetCloudBattery returns the raw Tesla site_status (battery summary) payload.
func (p *Powerwall) GetCloudBattery(ctx context.Context) (any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.cloud != nil {
		return p.cloud.GetBattery(ctx)
	}

	return nil, ErrUnsupported
}

// GetCloudPower returns the raw Tesla live_status (power summary) payload.
func (p *Powerwall) GetCloudPower(ctx context.Context) (any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.cloud != nil {
		return p.cloud.GetSitePower(ctx)
	}

	return nil, ErrUnsupported
}

// GetCloudConfig returns the raw Tesla site_info (config summary) payload.
func (p *Powerwall) GetCloudConfig(ctx context.Context) (any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.cloud != nil {
		return p.cloud.GetSiteConfig(ctx)
	}

	return nil, ErrUnsupported
}

// GetFleetAPIInfo returns the raw FleetAPI site_info payload.
func (p *Powerwall) GetFleetAPIInfo(ctx context.Context) (any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.fleetapi != nil {
		return p.fleetapi.GetSiteInfo(ctx)
	}

	return nil, ErrUnsupported
}

// GetFleetAPIStatus returns the raw FleetAPI live_status payload.
func (p *Powerwall) GetFleetAPIStatus(ctx context.Context) (any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.fleetapi != nil {
		return p.fleetapi.GetLiveStatus(ctx)
	}

	return nil, ErrUnsupported
}

// AggregatesOption customizes a single call to [Powerwall.Aggregates] (and,
// transitively, [Powerwall.Snapshot], which builds on the same corrections).
// Build one with [WithSiteZeroThreshold] or [WithNegativeSolarCorrection].
type AggregatesOption func(*aggregatesConfig)

type aggregatesConfig struct {
	siteZeroThreshold    float64
	correctNegativeSolar bool
}

// WithSiteZeroThreshold sets a +/-watts band around zero within which the
// site (grid) meter's instant_power is reported as exactly 0 rather than a
// small non-zero reading - useful for gateways whose CT clamps report a
// persistent small offset even at true zero net grid flow. The default,
// when this option is omitted, is 0 (disabled: report the raw value
// unconditionally).
func WithSiteZeroThreshold(watts float64) AggregatesOption {
	return func(c *aggregatesConfig) { c.siteZeroThreshold = watts }
}

// WithNegativeSolarCorrection sets whether a negative solar reading (which
// some inverters report briefly at dawn/dusk or during a transient) is
// clamped to 0, with the negative amount added to the load/home figure
// instead of appearing as "negative production". The default, when this
// option is omitted, is false (report the raw, possibly-negative value
// unconditionally) - matching [Powerwall.SiteReading] and its siblings, and
// pypowerwall's own PW_NEG_SOLAR=True default of allowing it through
// uncorrected.
func WithNegativeSolarCorrection(correct bool) AggregatesOption {
	return func(c *aggregatesConfig) { c.correctNegativeSolar = correct }
}

func newAggregatesConfig(opts []AggregatesOption) aggregatesConfig {
	cfg := aggregatesConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

// applyAggregateCorrections mutates agg in place per cfg: see
// [WithSiteZeroThreshold] and [WithNegativeSolarCorrection] for what each
// correction does.
func applyAggregateCorrections(cfg aggregatesConfig, agg *models.MetersAggregates) {
	if cfg.siteZeroThreshold > 0 {
		ip := agg.Site.InstantPower
		if ip >= -cfg.siteZeroThreshold && ip <= cfg.siteZeroThreshold {
			agg.Site.InstantPower = 0
		}
	}
	if cfg.correctNegativeSolar && agg.Solar.InstantPower < 0 {
		agg.Load.InstantPower -= agg.Solar.InstantPower
		agg.Solar.InstantPower = 0
	}
}

// decodeToJSON re-encodes an untyped [Powerwall.Poll] result (already
// backend-decoded to Go values, or occasionally a raw JSON string,
// depending on which backend answered) into bytes suitable for
// json.Unmarshal into a concrete struct.
func decodeToJSON(data any) ([]byte, error) {
	if s, ok := data.(string); ok {
		return []byte(s), nil
	}

	return json.Marshal(data)
}

// Aggregates returns the site, solar, battery, and load meters' entire
// readings - voltage, current, cumulative energy, and so on, not just their
// instant_power figures - as a [models.MetersAggregates] decoded from
// "/api/meters/aggregates", with any [AggregatesOption] corrections applied.
// With no options, this is a lossless decode of the gateway's own response.
func (p *Powerwall) Aggregates(ctx context.Context, opts ...AggregatesOption) (models.MetersAggregates, error) {
	data := p.Poll(ctx, "/api/meters/aggregates")
	if data == nil {
		return models.MetersAggregates{}, ErrNotFound
	}

	raw, err := decodeToJSON(data)
	if err != nil {
		return models.MetersAggregates{}, fmt.Errorf("marshal meters aggregates: %w", err)
	}

	var agg models.MetersAggregates
	if unmarshalErr := json.Unmarshal(raw, &agg); unmarshalErr != nil {
		return models.MetersAggregates{}, fmt.Errorf("unmarshal meters aggregates: %w", unmarshalErr)
	}

	cfg := newAggregatesConfig(opts)
	applyAggregateCorrections(cfg, &agg)

	return agg, nil
}

// Snapshot returns a composite, corrected power-and-status view: current
// power flow for every channel, battery state of charge, grid connectivity,
// backup reserve, estimated backup time remaining, pack energy capacity,
// and per-string solar detail, all from one call. Like [Powerwall.Power],
// it degrades gracefully rather than returning an error: any field whose
// underlying read fails is left at its zero value. opts apply the same
// [WithSiteZeroThreshold]/[WithNegativeSolarCorrection] corrections as
// [Powerwall.Aggregates] to the Grid/Home/Solar figures.
func (p *Powerwall) Snapshot(ctx context.Context, opts ...AggregatesOption) models.Snapshot {
	pwr, _ := p.powerSummary(ctx)

	cfg := newAggregatesConfig(opts)
	agg := models.MetersAggregates{
		Site:  models.MeterReading{InstantPower: pwr.Site},
		Solar: models.MeterReading{InstantPower: pwr.Solar},
		Load:  models.MeterReading{InstantPower: pwr.Load},
	}
	applyAggregateCorrections(cfg, &agg)

	batteryLevel, _ := p.Level(ctx)
	gridStatus, _ := p.GridStatusResponse(ctx)
	reserve, _ := p.GetReserve(ctx)
	timeRemaining, _ := p.GetTimeRemaining(ctx)
	sys, _ := p.SystemStatus(ctx)
	strs := p.Strings(ctx)

	return models.Snapshot{
		Grid:            agg.Site.InstantPower,
		Home:            agg.Load.InstantPower,
		Solar:           agg.Solar.InstantPower,
		Battery:         pwr.Battery,
		BatteryLevel:    batteryLevel,
		GridConnected:   gridConnected(gridStatus),
		Reserve:         reserve,
		TimeRemaining:   timeRemaining,
		FullPackEnergy:  sys.NominalFullPackEnergy,
		EnergyRemaining: sys.NominalEnergyRemaining,
		Strings:         strs,
	}
}

// PODView returns the per-battery-block operational view derived from
// [Powerwall.SystemStatus], [Powerwall.Vitals], [Powerwall.GetTimeRemaining],
// and [Powerwall.GetReserve]. Like [Powerwall.Snapshot], it degrades
// gracefully: a disconnected Powerwall or a failed SystemStatus read simply
// yields an empty Blocks slice and zero-valued totals rather than an error.
// TimeRemainingHours and BackupReservePercent are nil specifically when
// that one read fails, since pypowerwall's own /pod reports those two
// fields as JSON null rather than a zero number in that case.
//
// TEPODEntries is the second, independent augmentation pass upstream's
// generate_pod performs (server.py:2196-2244, "Augment with Vitals Data"):
// every vitals device whose name starts with "TEPOD" (a battery-block
// heating/POD-controller device - see pypowerwall/tedapi/__init__.py:
// 1018-1022 for how pypowerwall's own TEDAPI backend synthesizes one)
// contributes one entry, in vitals-iteration order. Upstream's own code
// comment ("Expansion packs are now included in vitals() as TEPOD entries,
// so they're automatically picked up by the loop above") documents that it
// trusts TEPOD devices to enumerate in the same order as SystemStatus's
// battery_blocks, an assumption this method mirrors by sorting device names
// for a deterministic order - Go's vitals map, unlike Python's dict, has no
// stable iteration order of its own to (mis)trust in the first place.
func (p *Powerwall) PODView(ctx context.Context) models.PODView {
	sys, _ := p.SystemStatus(ctx)

	view := models.PODView{
		Blocks:                 sys.BatteryBlocks,
		NominalFullPackEnergy:  sys.NominalFullPackEnergy,
		NominalEnergyRemaining: sys.NominalEnergyRemaining,
	}

	if tr, err := p.GetTimeRemaining(ctx); err == nil {
		hours := tr.Hours()
		view.TimeRemainingHours = &hours
	}
	if reserve, err := p.GetReserve(ctx); err == nil {
		view.BackupReservePercent = &reserve
	}

	vitals, _ := p.Vitals(ctx)

	deviceNames := make([]string, 0, len(vitals.Devices))
	for name := range vitals.Devices {
		if strings.HasPrefix(name, "TEPOD") {
			deviceNames = append(deviceNames, name)
		}
	}
	sort.Strings(deviceNames)

	for _, name := range deviceNames {
		data := vitals.Devices[name]
		view.TEPODEntries = append(view.TEPODEntries, models.PODTEPODEntry{
			Device:                  name,
			ActiveHeating:           intOrZero(data, "POD_ActiveHeating"),
			ChargeComplete:          intOrZero(data, "POD_ChargeComplete"),
			ChargeRequest:           intOrZero(data, "POD_ChargeRequest"),
			DischargeComplete:       intOrZero(data, "POD_DischargeComplete"),
			PermanentlyFaulted:      intOrZero(data, "POD_PermanentlyFaulted"),
			PersistentlyFaulted:     intOrZero(data, "POD_PersistentlyFaulted"),
			EnableLine:              intOrZero(data, "POD_enable_line"),
			AvailableChargePower:    lookupFloatPtr(data, "POD_available_charge_power"),
			AvailableDischargePower: lookupFloatPtr(data, "POD_available_dischg_power"),
			NomEnergyRemaining:      lookupFloatPtr(data, "POD_nom_energy_remaining"),
			NomEnergyToBeCharged:    lookupFloatPtr(data, "POD_nom_energy_to_be_charged"),
			NomFullPackEnergy:       lookupFloatPtr(data, "POD_nom_full_pack_energy"),
		})
	}

	return view
}

// GetFanSpeeds returns the raw cooling-fan speed readings the active
// TEDAPI/v1r client reports, keyed by synthesized PVAC device name -
// backing the gopowerwall proxy's /fans and /fans/pw routes, mirroring
// pypowerwall's `pw.tedapi.get_fan_speeds() if pw.tedapi else {}`
// (server.py:2483-2500). It is empty for every other connection mode,
// matching upstream's own `pw.tedapi` falsy gate. See
// [github.com/blackbirdworks/gopowerwall/backend/tedapi.ExtractFanSpeeds]'s
// doc comment for a static ambiguity in upstream's own query text that
// means this may legitimately return empty even in TEDAPI/v1r mode on real
// hardware - this method faithfully reproduces that upstream behavior
// rather than working around it.
func (p *Powerwall) GetFanSpeeds(ctx context.Context) map[string]models.FanSpeedEntry {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi == nil {
		return map[string]models.FanSpeedEntry{}
	}

	return p.tedapi.GetFanSpeeds(ctx, false)
}

// FrequencyView returns the per-device frequency/voltage view derived from
// [Powerwall.Vitals]: one [models.InverterFrequency] entry per TEPINV
// device (sorted by device name for a deterministic order), plus any
// ISLAND*/METER*-prefixed field reported by a TESYNC or TEMSA device. Like
// [Powerwall.Snapshot], it degrades gracefully: a disconnected Powerwall or
// a failed Vitals read simply yields an empty view rather than an error.
func (p *Powerwall) FrequencyView(ctx context.Context) models.FrequencyView {
	vitals, _ := p.Vitals(ctx)

	deviceNames := make([]string, 0, len(vitals.Devices))
	for name := range vitals.Devices {
		deviceNames = append(deviceNames, name)
	}
	sort.Strings(deviceNames)

	view := models.FrequencyView{SyncMeterFields: make(map[string]any)}
	for _, name := range deviceNames {
		data := vitals.Devices[name]
		switch {
		case strings.HasPrefix(name, "TEPINV"):
			view.Inverters = append(view.Inverters, models.InverterFrequency{
				Device:  name,
				Fout:    lookupFloat(data, "PINV_Fout"),
				VSplit1: lookupFloat(data, "PINV_VSplit1"),
				VSplit2: lookupFloat(data, "PINV_VSplit2"),
			})
		case strings.HasPrefix(name, "TESYNC"), strings.HasPrefix(name, "TEMSA"):
			for k, v := range data {
				if strings.HasPrefix(k, "ISLAND") || strings.HasPrefix(k, "METER") {
					view.SyncMeterFields[k] = v
				}
			}
		}
	}

	return view
}
