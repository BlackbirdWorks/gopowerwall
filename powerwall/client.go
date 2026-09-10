package powerwall

import "context"

// Client represents the unified interface for communicating with a Powerwall
// gateway across any transport mode (local HTTPS, TEDAPI protobuf, cloud Owner API, or FleetAPI).
type Client interface {
	Poll(ctx context.Context, api string, force, recursive, raw bool) (any, error)
	Post(ctx context.Context, api string, payload any, din string, force, recursive bool) (any, error)
	Power(ctx context.Context) (map[string]float64, error)
	Vitals(ctx context.Context) (map[string]any, error)
	GetTimeRemaining(ctx context.Context) (*float64, error)
	Close(ctx context.Context) error
}
