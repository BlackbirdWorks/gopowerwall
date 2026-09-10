package powerwall

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/gopowerwall/models"
)

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

func (p *Powerwall) fetchAggregatesJSON(ctx context.Context) ([]byte, error) {
	if raw := p.PollRaw(ctx, "/api/meters/aggregates"); len(raw) > 0 {
		return raw, nil
	}

	data := p.Poll(ctx, "/api/meters/aggregates")
	if data == nil {
		return nil, ErrNotFound
	}

	rawBytes, err := decodeToJSON(data)
	if err != nil {
		return nil, fmt.Errorf("marshal meters aggregates: %w", err)
	}

	return rawBytes, nil
}

// Aggregates returns the site, solar, battery, and load meters' entire
// readings - voltage, current, cumulative energy, and so on, not just their
// instant_power figures - as a [models.MetersAggregates] decoded from
// "/api/meters/aggregates", with any [AggregatesOption] corrections applied.
// With no options, this is a lossless decode of the gateway's own response.
func (p *Powerwall) Aggregates(ctx context.Context, opts ...AggregatesOption) (models.MetersAggregates, error) {
	raw, err := p.fetchAggregatesJSON(ctx)
	if err != nil {
		return models.MetersAggregates{}, err
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
	var (
		wg            sync.WaitGroup
		pwr           models.PowerSummary
		batteryLevel  float64
		gridStatus    models.GridStatusResponse
		reserve       float64
		timeRemaining time.Duration
		sys           models.SystemStatus
		strs          models.SolarStrings
	)

	wg.Go(func() { pwr, _ = p.powerSummary(ctx) })
	wg.Go(func() { batteryLevel, _ = p.Level(ctx) })
	wg.Go(func() { gridStatus, _ = p.GridStatusResponse(ctx) })
	wg.Go(func() { reserve, _ = p.GetReserve(ctx) })
	wg.Go(func() { timeRemaining, _ = p.GetTimeRemaining(ctx) })
	wg.Go(func() { sys, _ = p.SystemStatus(ctx) })
	wg.Go(func() { strs = p.Strings(ctx) })
	wg.Wait()

	cfg := newAggregatesConfig(opts)
	agg := models.MetersAggregates{
		Site:  models.MeterReading{InstantPower: pwr.Site},
		Solar: models.MeterReading{InstantPower: pwr.Solar},
		Load:  models.MeterReading{InstantPower: pwr.Load},
	}
	applyAggregateCorrections(cfg, &agg)

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
	var (
		wg      sync.WaitGroup
		sys     models.SystemStatus
		tr      time.Duration
		trErr   error
		reserve float64
		resErr  error
		vitals  models.VitalsData
	)

	wg.Go(func() { sys, _ = p.SystemStatus(ctx) })
	wg.Go(func() { tr, trErr = p.GetTimeRemaining(ctx) })
	wg.Go(func() { reserve, resErr = p.GetReserve(ctx) })
	wg.Go(func() { vitals, _ = p.Vitals(ctx) })
	wg.Wait()

	view := models.PODView{
		Blocks:                 sys.BatteryBlocks,
		NominalFullPackEnergy:  sys.NominalFullPackEnergy,
		NominalEnergyRemaining: sys.NominalEnergyRemaining,
	}

	if trErr == nil {
		hours := tr.Hours()
		view.TimeRemainingHours = &hours
	}
	if resErr == nil {
		view.BackupReservePercent = &reserve
	}

	deviceNames := make([]string, 0, len(vitals.Devices))
	for name := range vitals.Devices {
		if strings.HasPrefix(name, "TEPOD") {
			deviceNames = append(deviceNames, name)
		}
	}
	slices.Sort(deviceNames)

	view.TEPODEntries = make([]models.PODTEPODEntry, 0, len(deviceNames))
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

// FrequencyView returns the per-device frequency/voltage view derived from
// [Powerwall.Vitals]: one [models.InverterFrequency] entry per TEPINV
// device (sorted by device name for a deterministic order), plus any
// ISLAND*/METER*-prefixed field reported by a TESYNC or TEMSA device. Like
// [Powerwall.Snapshot], it degrades gracefully: a disconnected Powerwall or
// a failed Vitals read simply yields an empty view rather than an error.
func (p *Powerwall) FrequencyView(ctx context.Context) models.FrequencyView {
	vitals, _ := p.Vitals(ctx)

	deviceNames := slices.Sorted(maps.Keys(vitals.Devices))

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
