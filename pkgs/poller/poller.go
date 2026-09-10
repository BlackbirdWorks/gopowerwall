// Package poller provides structured, option-driven polling for Powerwall endpoints.
package poller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrNoSource indicates no backend source has been configured for the poller.
var ErrNoSource = errors.New("no poller source available")

// Source is the interface implemented by transport drivers and backends that can be polled.
type Source interface {
	Poll(ctx context.Context, api string, force, recursive, raw bool) (any, error)
}

// Option configures an endpoint poll request.
type Option func(*Config)

// Config holds the flags passed to an underlying Source.Poll call.
type Config struct {
	Force     bool
	Recursive bool
	Raw       bool
}

// WithForce bypasses caching and forces an upstream fetch.
func WithForce(force bool) Option {
	return func(c *Config) { c.Force = force }
}

// WithRecursive allows internal retry/re-auth loops.
func WithRecursive(recursive bool) Option {
	return func(c *Config) { c.Recursive = recursive }
}

// WithRaw requests undecoded, raw response bytes.
func WithRaw(raw bool) Option {
	return func(c *Config) { c.Raw = raw }
}

// ApplyOptions compiles a slice of options into a Config.
func ApplyOptions(opts []Option) Config {
	cfg := Config{}
	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

// Poller executes queries against a Source with structured option handling.
type Poller struct {
	source Source
}

// New creates a Poller targeting the given Source.
func New(source Source) *Poller {
	return &Poller{source: source}
}

// Source returns the poller's underlying Source.
func (p *Poller) Source() Source {
	if p == nil {
		return nil
	}

	return p.source
}

// Poll queries the given API endpoint and returns the parsed response or an error.
func (p *Poller) Poll(ctx context.Context, api string, opts ...Option) (any, error) {
	if p == nil || p.source == nil {
		return nil, ErrNoSource
	}
	cfg := ApplyOptions(opts)

	return p.source.Poll(ctx, api, cfg.Force, cfg.Recursive, cfg.Raw)
}

// PollRaw queries the given API endpoint requesting raw bytes. If the source returned
// another representation (e.g. a string or a Go map), it converts it to []byte.
func (p *Poller) PollRaw(ctx context.Context, api string, opts ...Option) ([]byte, error) {
	if p == nil || p.source == nil {
		return nil, ErrNoSource
	}
	cfg := ApplyOptions(opts)
	val, err := p.source.Poll(ctx, api, cfg.Force, cfg.Recursive, true)
	if err != nil {
		return nil, err
	}
	if b, ok := val.([]byte); ok {
		return b, nil
	}
	if str, ok := val.(string); ok {
		return []byte(str), nil
	}

	return json.Marshal(val)
}

// PollJSON queries the given API endpoint and guarantees a serialized JSON string return.
func (p *Poller) PollJSON(ctx context.Context, api string, opts ...Option) (string, error) {
	if p == nil || p.source == nil {
		return "", ErrNoSource
	}
	cfg := ApplyOptions(opts)
	val, err := p.source.Poll(ctx, api, cfg.Force, cfg.Recursive, cfg.Raw)
	if err != nil {
		return "", err
	}
	if str, ok := val.(string); ok {
		return str, nil
	}
	if b, ok := val.([]byte); ok {
		return string(b), nil
	}
	b, marshalErr := json.Marshal(val)
	if marshalErr != nil {
		return "", fmt.Errorf("marshal JSON: %w", marshalErr)
	}

	return string(b), nil
}
