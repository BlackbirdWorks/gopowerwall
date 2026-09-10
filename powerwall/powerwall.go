package powerwall

import (
	"context"
	"sync"
	"time"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/pkgs/poller"
	"github.com/blackbirdworks/gopowerwall/powerwall/cloud"
	"github.com/blackbirdworks/gopowerwall/powerwall/fleetapi"
	"github.com/blackbirdworks/gopowerwall/powerwall/local"
	"github.com/blackbirdworks/gopowerwall/powerwall/tedapi"
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
	client       Client
	poller       *poller.Poller
	local        *local.Client
	tedapi       *tedapi.PyPowerwallTEDAPI
	cloud        *cloud.Client
	fleetapi     *fleetapi.Client
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

		return pw, &models.ConnectError{Mode: string(pw.Mode())}
	}

	return pw, nil
}

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

func (p *Powerwall) IsConnected() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.client != nil
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

// Poller returns the structured, error-preserving [poller.Poller] for querying
// this Powerwall, or nil if no backend is currently connected.
func (p *Powerwall) Poller() *poller.Poller {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.poller
}

// Close releases the active backend's resources and clears it, leaving p
// disconnected ([Powerwall.IsConnected] false afterward). It always returns
// nil; the return value exists so Powerwall satisfies patterns expecting an
// io.Closer-shaped Close, not because closing can currently fail. Close does
// not stop a New from being usable again - call [Powerwall.Connect] to
// reconnect the same instance.
