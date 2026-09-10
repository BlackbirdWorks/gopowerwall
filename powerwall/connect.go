package powerwall

import (
	"context"
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

// Mode returns the active [ConnectionMode]. This can differ from what the
// caller configured: [Powerwall.Connect]'s circular fallback may have moved
// p to a different mode than the one [New] started with, so check Mode
// rather than assuming it still matches the [Option] values passed to New.
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
		p.setClientLocked(p.tedapi)
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
		p.setClientLocked(localBackend)
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
		p.setClientLocked(fb)
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
		p.setClientLocked(cb)
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
	p.cloud = nil
	p.fleetapi = nil

	return nil
}

// PollOption customizes a single call to [Powerwall.Poll], [Powerwall.PollRaw],
// or [Powerwall.PollJSON]. Build one with [WithForce] or [WithRaw].
