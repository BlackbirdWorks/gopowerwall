package powerwall

import (
	"context"

	"github.com/blackbirdworks/gopowerwall/pkgs/poller"
)

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
	plr := p.poller
	p.mu.RUnlock()

	if plr == nil {
		return nil, ErrNoClient
	}

	var opts []poller.Option
	if force {
		opts = append(opts, poller.WithForce(true))
	}
	if recursive {
		opts = append(opts, poller.WithRecursive(true))
	}
	if raw {
		opts = append(opts, poller.WithRaw(true))
	}

	return plr.Poll(ctx, api, opts...)
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

	p.mu.RLock()
	plr := p.poller
	p.mu.RUnlock()

	if plr == nil {
		return nil
	}

	var pollerOpts []poller.Option
	if cfg.force {
		pollerOpts = append(pollerOpts, poller.WithForce(true))
	}
	if cfg.recursive {
		pollerOpts = append(pollerOpts, poller.WithRecursive(true))
	}

	b, err := plr.PollRaw(ctx, api, pollerOpts...)
	if err != nil {
		return nil
	}

	return b
}

// PollJSON queries api like [Powerwall.Poll] but re-encodes the result as a
// JSON string, returning "" if the underlying poll failed or the result
// could not be marshalled. An empty string is therefore ambiguous between
// "no data" and "the gateway returned literally an empty response" -
// callers that need to tell those apart should use [Powerwall.PollRaw]
// instead and check for a nil/empty byte slice explicitly alongside their
// own error handling.
func (p *Powerwall) PollJSON(ctx context.Context, api string, opts ...PollOption) string {
	cfg := &pollConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	p.mu.RLock()
	plr := p.poller
	p.mu.RUnlock()

	if plr == nil {
		return ""
	}

	var pollerOpts []poller.Option
	if cfg.force {
		pollerOpts = append(pollerOpts, poller.WithForce(true))
	}
	if cfg.recursive {
		pollerOpts = append(pollerOpts, poller.WithRecursive(true))
	}

	str, err := plr.PollJSON(ctx, api, pollerOpts...)
	if err != nil {
		return ""
	}

	return str
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

	if p.client == nil {
		return nil
	}

	res, err := p.client.Post(ctx, api, payload, dinStr, false, false)
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
