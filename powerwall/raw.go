package powerwall

import (
	"context"
)

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
