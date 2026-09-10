package powerwall

import (
	"context"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/calc"
	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
)

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

	if p.client == nil {
		return models.PowerSummary{}, ErrNoClient
	}

	res, err := p.client.Power(ctx)

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
