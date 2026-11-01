package powerwall

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/pkgs/poller"
	"github.com/blackbirdworks/gopowerwall/powerwall/cloud"
	"github.com/blackbirdworks/gopowerwall/powerwall/fleetapi"
	"github.com/blackbirdworks/gopowerwall/powerwall/local"
	"github.com/blackbirdworks/gopowerwall/powerwall/tedapi"
)

type connectionSnapshot struct {
	client       Client
	local        *local.Client
	tedapi       *tedapi.PyPowerwallTEDAPI
	cloud        *cloud.Client
	fleetapi     *fleetapi.Client
	mode         ConnectionMode
	tedapiMode   TEDAPIMode
	cloudmode    bool
	fleetapiFlag bool
	tedapiFlag   bool
}

func (p *Powerwall) snapshotConnectionState() connectionSnapshot {
	return connectionSnapshot{
		client:       p.client,
		local:        p.local,
		tedapi:       p.tedapi,
		cloud:        p.cloud,
		fleetapi:     p.fleetapi,
		mode:         p.mode,
		tedapiMode:   p.tedapiMode,
		cloudmode:    p.cloudmode,
		fleetapiFlag: p.fleetapiFlag,
		tedapiFlag:   p.tedapiFlag,
	}
}

func (p *Powerwall) restoreFailedConnectState(prev connectionSnapshot) {
	if prev.client != nil {
		p.setClientLocked(prev.client)
		p.local = prev.local
		p.tedapi = prev.tedapi
		p.cloud = prev.cloud
		p.fleetapi = prev.fleetapi
		p.mode = prev.mode
		p.tedapiMode = prev.tedapiMode
		p.cloudmode = prev.cloudmode
		p.fleetapiFlag = prev.fleetapiFlag
		p.tedapiFlag = prev.tedapiFlag

		return
	}

	p.restoreConfiguredState()
}

func (p *Powerwall) noteConnectError(mode ConnectionMode, reason string) {
	if reason == "" || p.connectError != "" {
		return
	}
	p.connectError = fmt.Sprintf("%s mode failed: %s", mode, reason)
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

func (p *Powerwall) setClientLocked(c Client) {
	p.client = c
	if c != nil {
		p.poller = poller.New(c)
	} else {
		p.poller = nil
	}
}

func (p *Powerwall) connectV1r(cfg *Config) bool {
	pwd := cfg.Password
	if pwd == "" && cfg.GwPwd != "" && len(cfg.GwPwd) >= 5 {
		pwd = cfg.GwPwd[len(cfg.GwPwd)-5:]
	}
	if pwd == "" {
		p.noteConnectError(ModeLocal, "customer password or gw_pwd (last 5 chars) is required for TEDAPI v1r login")

		return false
	}
	v1r, err := tedapi.NewTEDAPIv1r(cfg.Host, pwd, cfg.RSAKeyPath, cfg.Timeout, cfg.PoolMaxSize)
	if err != nil {
		p.noteConnectError(ModeLocal, err.Error())

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
	tedClient.SetFailover(p.failover, p.wifiV1rFallback)
	if cfg.WiFiHost != "" && p.wifiV1rFallback {
		wifiV1r, wifiErr := tedapi.NewTEDAPIv1r(cfg.WiFiHost, pwd, cfg.RSAKeyPath, cfg.Timeout, cfg.PoolMaxSize)
		if wifiErr != nil {
			p.noteConnectError(ModeLocal, wifiErr.Error())

			return false
		}
		tedClient.SetWiFiV1rTransport(wifiV1r)
	}
	p.tedapi = tedapi.NewBackend(tedClient, v1r)
	p.setClientLocked(p.tedapi)
	p.tedapiMode = TEDAPIV1r
	p.tedapiFlag = true
	// Pure v1r: no local HTTP backend exists (p.local stays nil), so the
	// facade's reads must dispatch on ModeV1r rather than the leftover
	// ModeLocal value New() assigned before Connect ran - see
	// pollInternal and friends' `case ModeTEDAPI, ModeV1r:` arms.
	p.mode = ModeV1r

	return true
}

func (p *Powerwall) connectPureTEDAPI(cfg *Config) bool {
	tedClient := tedapi.NewClient(
		cfg.Host,
		cfg.GwPwd,
		cfg.Timeout,
		cfg.PWCacheExpire,
		cfg.PoolMaxSize,
		cfg.TEDAPIApiVersion,
		cfg.TEDAPIAuthMode,
	)
	tedClient.SetFailover(p.failover, false)
	p.tedapi = tedapi.NewBackend(tedClient, nil)
	p.setClientLocked(p.tedapi)
	p.tedapiMode = TEDAPIFull
	p.tedapiFlag = true
	// Pure TEDAPI: same reasoning as the v1r branch above - no local
	// backend, so p.mode must reflect TEDAPI or every mode-dispatching
	// read method silently falls through the dead ModeLocal branch.
	p.mode = ModeTEDAPI

	return true
}

func (p *Powerwall) connectLocal(ctx context.Context) bool {
	cfg := p.config

	if cfg.RSAKeyPath != "" {
		return p.connectV1r(cfg)
	}

	if cfg.Password == "" && cfg.GwPwd != "" {
		return p.connectPureTEDAPI(cfg)
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
		} else {
			tedClient.Close()
		}
	}

	authErr := localBackend.Authenticate(ctx)
	if authErr == nil {
		p.local = localBackend
		p.setClientLocked(localBackend)
		p.cloudmode = false
		p.fleetapiFlag = false

		return true
	}

	p.noteConnectError(ModeLocal, authErr.Error())
	_ = localBackend.Close(ctx)
	p.tedapiMode = TEDAPIOff
	p.tedapiFlag = false

	return false
}

func (p *Powerwall) connectFleetAPI(ctx context.Context) bool {
	fb := fleetapi.New(p.config.Email, p.config.PWCacheExpire, p.config.Timeout, p.config.SiteID, p.config.AuthPath)
	authErr := fb.Authenticate(ctx)
	if authErr == nil {
		if p.tedapi != nil {
			_ = p.tedapi.Close(ctx)
			p.tedapi = nil
		}
		p.fleetapi = fb
		p.setClientLocked(fb)
		p.cloudmode = true
		p.fleetapiFlag = true
		p.tedapiFlag = false
		p.tedapiMode = TEDAPIOff

		return true
	}

	p.noteConnectError(ModeFleetAPI, authErr.Error())
	_ = fb.Close(ctx)

	return false
}

func (p *Powerwall) connectCloud(ctx context.Context) bool {
	cb := cloud.New(p.config.Email, p.config.PWCacheExpire, p.config.Timeout, p.config.SiteID, p.config.AuthPath)
	authErr := cb.Authenticate(ctx)
	if authErr == nil {
		if p.tedapi != nil {
			_ = p.tedapi.Close(ctx)
			p.tedapi = nil
		}
		p.cloud = cb
		p.setClientLocked(cb)
		p.cloudmode = true
		p.fleetapiFlag = false
		p.tedapiFlag = false
		p.tedapiMode = TEDAPIOff

		return true
	}

	p.noteConnectError(ModeCloud, authErr.Error())
	_ = cb.Close(ctx)

	return false
}

func (p *Powerwall) restoreConfiguredState() {
	p.mode = p.configuredMode
	p.cloudmode = p.configuredCloudmode
	p.fleetapiFlag = p.configuredFleetapiFlag
	p.tedapiFlag = false
	p.tedapiMode = TEDAPIOff
}

func (p *Powerwall) tryMode(ctx context.Context, mode ConnectionMode) bool {
	switch mode {
	case ModeLocal, ModeTEDAPI, ModeV1r:
		logger.Load(ctx).DebugContext(ctx, "trying local mode")
		if p.connectLocal(ctx) {
			return true
		}
		logger.Load(ctx).WarnContext(ctx, "local mode failed")

		return false

	case ModeFleetAPI:
		logger.Load(ctx).DebugContext(ctx, "trying FleetAPI mode")
		if p.connectFleetAPI(ctx) {
			return true
		}
		logger.Load(ctx).WarnContext(ctx, "FleetAPI mode failed")

		return false

	case ModeCloud:
		logger.Load(ctx).DebugContext(ctx, "trying cloud mode")
		if p.connectCloud(ctx) {
			return true
		}
		logger.Load(ctx).WarnContext(ctx, "cloud mode failed")

		return false

	default:
		return false
	}
}

func nextFailoverMode(mode ConnectionMode) ConnectionMode {
	switch mode {
	case ModeLocal, ModeTEDAPI, ModeV1r:
		return ModeFleetAPI
	case ModeFleetAPI:
		return ModeCloud
	case ModeCloud:
		return ModeLocal
	default:
		return ModeUnknown
	}
}

func (p *Powerwall) connectStrict(ctx context.Context, retry bool, prev connectionSnapshot) bool {
	for {
		if p.tryMode(ctx, p.configuredMode) {
			p.connectError = ""

			return true
		}
		if !retry {
			p.restoreFailedConnectState(prev)

			return false
		}
		logger.Load(ctx).WarnContext(
			ctx,
			"Failed to connect to Powerwall (failover off), waiting to retry",
			"mode", p.configuredMode,
			"wait", connectRetryWait,
		)
		select {
		case <-ctx.Done():
			p.restoreFailedConnectState(prev)

			return false
		case <-time.After(connectRetryWait):
		}
	}
}

func (p *Powerwall) connectWithFailover(ctx context.Context, retry bool, prev connectionSnapshot) bool {
	for attempt := 0; attempt < maxConnectRetries; {
		attempt++
		if p.tryMode(ctx, p.mode) {
			p.connectError = ""

			return true
		}
		p.mode = nextFailoverMode(p.mode)
		if retry && attempt == maxConnectRetries {
			logger.Load(ctx).WarnContext(
				ctx,
				"failed to connect with all modes, waiting to retry",
				"wait", connectRetryWait,
			)
			select {
			case <-ctx.Done():
				p.restoreFailedConnectState(prev)

				return false
			case <-time.After(connectRetryWait):
			}
			attempt = 0
			p.mode = p.configuredMode
		}
	}

	p.restoreFailedConnectState(prev)

	return false
}

// Connect (re)establishes the backend connection for p's current
// [ConnectionMode], with circular fallback across modes: Local -> FleetAPI
// -> Cloud -> Local, up to three attempts total. It returns true as soon as
// any mode connects, and updates [Powerwall.Mode] to whichever mode that
// was - which may not be the mode p started with. If retry is true, Connect
// sleeps 30 seconds before its last attempt rather than failing immediately.
// When failover is disabled via [WithFailover], only the configured mode is
// attempted.
func (p *Powerwall) Connect(ctx context.Context, retry bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.connectError = ""
	prev := p.snapshotConnectionState()

	if p.configuredMode == "" || p.configuredMode == ModeUnknown {
		p.configuredMode = p.mode
		p.configuredCloudmode = p.cloudmode
		p.configuredFleetapiFlag = p.fleetapiFlag
	}

	if p.configuredMode == ModeUnknown {
		logger.Load(ctx).ErrorContext(ctx, "unable to determine mode to connect")
		p.connectError = "unable to determine mode to connect"

		return false
	}

	p.mode = p.configuredMode
	p.cloudmode = p.configuredCloudmode
	p.fleetapiFlag = p.configuredFleetapiFlag

	if !p.failover {
		return p.connectStrict(ctx, retry, prev)
	}

	return p.connectWithFailover(ctx, retry, prev)
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

	if p.client != nil {
		_ = p.client.Close(ctx)
		p.setClientLocked(nil)
	}
	if p.local != nil {
		_ = p.local.Close(ctx)
		p.local = nil
	}
	if p.tedapi != nil {
		_ = p.tedapi.Close(ctx)
		p.tedapi = nil
	}
	if p.cloud != nil {
		_ = p.cloud.Close(ctx)
		p.cloud = nil
	}
	if p.fleetapi != nil {
		_ = p.fleetapi.Close(ctx)
		p.fleetapi = nil
	}

	return nil
}
