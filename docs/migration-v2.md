# Migration guide: gopowerwall library API redesign

This documents every breaking change to the root `gopowerwall` package's exported API made
while implementing [api-redesign-proposal.md](api-redesign-proposal.md). None of it affects
the two parity-frozen surfaces (`.agent/rules/powerwall.md`): the `gopowerwall` CLI's
subcommands, flags, and human-readable output, and the HTTP proxy's routes, JSON field
names, and CSV columns are all unchanged. This is purely a library (Go SDK) API change.

As noted in the proposal, there had been no tagged release when this was written, so there
are no deprecation shims or a v2 module path - callers simply update to the signatures
below.

## Pointer returns become `(T, error)`

Every method that used to signal "unavailable" with a `nil` pointer (silently discarding
the underlying cause) now returns `(T, error)`.

| Before | After |
|---|---|
| `Level(ctx, scale ...bool) *float64` | `Level(ctx) (float64, error)` (raw) + `LevelScaled(ctx) (float64, error)` (rescaled) |
| `SiteName(ctx) *string` | `SiteName(ctx) (string, error)` |
| `Uptime(ctx) *string` | `Uptime(ctx) (time.Duration, error)` - also changed type, see below |
| `Din(ctx) *string` | `Din(ctx) (string, error)` |
| `GetReserve(ctx, scale ...bool) *float64` | `GetReserve(ctx) (float64, error)` (raw) + `GetReserveScaled(ctx) (float64, error)` (rescaled) |
| `GetReserveForced(ctx) *float64` | `GetReserveForced(ctx) (float64, error)` |
| `GetMode(ctx) *string` | `GetMode(ctx) (string, error)` |
| `GetTimeRemaining(ctx) *float64` | `GetTimeRemaining(ctx) (time.Duration, error)` - also changed type, see below |
| `GetGridCharging(ctx) *bool` | `GetGridCharging(ctx) (bool, error)` |
| `GetGridExport(ctx) *string` | `GetGridExport(ctx) (string, error)` |

Migration is mechanical at every call site:

```go
// Before
if v := pw.Level(ctx, true); v != nil {
	fmt.Println(*v)
}

// After
if v, err := pw.LevelScaled(ctx); err == nil {
	fmt.Println(v)
}
```

`Uptime` and `GetTimeRemaining` additionally changed *type*, not just shape: both used to
hand back a duration in disguise (a formatted string, and hours as a bare float,
respectively). Both now return `time.Duration`:

```go
// Before
uptime := pw.Uptime(ctx)          // *string, e.g. "1541h38m20.998412744s"
remaining := pw.GetTimeRemaining(ctx) // *float64, hours

// After
uptime, err := pw.Uptime(ctx)             // time.Duration
remaining, err := pw.GetTimeRemaining(ctx) // time.Duration
fmt.Println(uptime.String())
fmt.Println(remaining.Hours()) // convert back to hours explicitly if you need the old number
```

New sentinel errors returned by these and other accessors: [`ErrFieldMissing`](#new-sentinel-errors-and-types)
below.

## `Alerts` and `Strings` drop their ignored parameter

```go
// Before
func (p *Powerwall) Alerts(ctx context.Context, _ ...bool) models.AlertsList
func (p *Powerwall) Strings(ctx context.Context, _ ...bool) models.SolarStrings

// After
func (p *Powerwall) Alerts(ctx context.Context) models.AlertsList
func (p *Powerwall) Strings(ctx context.Context) models.SolarStrings
```

Both parameters were accepted and silently ignored - no caller ever changed either
method's behavior by passing one. Drop the extra argument at every call site.

## `Lookup` and `LookupFloat` are no longer exported

`gopowerwall.Lookup` and `gopowerwall.LookupFloat` have been removed from the root
package's public API. Callers that were digging into an untyped `Poll`/`Status` result:

- Prefer a typed accessor where one now exists - `Status`, `SiteReading`, `SystemStatus`,
  and the rest of the [derived-view methods](#new-derived-view-methods) below cover most of
  what `Lookup` was previously needed for.
- For the general "walk a `map[string]any`/`[]any` by path" helper, import
  [`pkgs/lookup`](../pkgs/lookup) directly: `lookup.Lookup(data, "din")` in place of
  `gopowerwall.Lookup(data, "din")`. There is no replacement for `LookupFloat` in
  `pkgs/lookup`; use a type switch (`v, ok := m[key].(float64)`) at the call site, or an
  `errors.As` check against `ErrFieldMissing` if you switch to a typed accessor instead.

## Variadic `...bool` parameters are gone

Every method that faked a Python-style optional keyword argument with a trailing
`...bool` has been split into two clearly-named methods, matching the fork in behavior the
boolean actually selected:

| Before | After |
|---|---|
| `Level(ctx, scale ...bool) *float64` | `Level(ctx)` (raw) / `LevelScaled(ctx)` |
| `GetReserve(ctx, scale ...bool) *float64` | `GetReserve(ctx)` (raw) / `GetReserveScaled(ctx)` |
| `Version(ctx, intValue ...bool) any` | `Version(ctx) (string, error)` / `VersionNumeric(ctx) (int, error)` |
| `Site(ctx, verbose ...bool) any` | `Site(ctx) (float64, error)` / `SiteReading(ctx) (models.MeterReading, error)` |
| `Solar(ctx, verbose ...bool) any` | `Solar(ctx) (float64, error)` / `SolarReading(ctx) (models.MeterReading, error)` |
| `Battery(ctx, verbose ...bool) any` | `Battery(ctx) (float64, error)` / `BatteryReading(ctx) (models.MeterReading, error)` |
| `Load(ctx, verbose ...bool) any` | `Load(ctx) (float64, error)` / `LoadReading(ctx) (models.MeterReading, error)` |
| `Grid(ctx, verbose ...bool) any` | `Grid(ctx) (float64, error)` / `GridReading(ctx) (models.MeterReading, error)` |
| `Home(ctx, verbose ...bool) any` | `Home(ctx) (float64, error)` / `HomeReading(ctx) (models.MeterReading, error)` |
| `Status(ctx, param ...string) any` | `Status(ctx) (models.GatewayStatus, error)` - read a field directly off the struct instead of passing its name |
| `GridStatus(ctx, outputType ...GridStatusOutput) any` | `GridStatusString(ctx) (string, error)` / `GridStatusNumeric(ctx) (int, error)`; `GridStatusResponse(ctx) (models.GridStatusResponse, error)` already covered the JSON case |

`GridStatusOutput` and its constants (`GridStatusString`, `GridStatusJSON`,
`GridStatusNumeric`) have been removed from `types.go` entirely - there is no dispatch
value left to name.

```go
// Before
v := pw.Status(ctx, "version")
gs := pw.GridStatus(ctx, gopowerwall.GridStatusNumeric).(int)

// After
st, err := pw.Status(ctx)
v := st.Version
gs, err := pw.GridStatusNumeric(ctx)
```

## `any` returns become concrete types

`Status`, `Site`/`Solar`/`Battery`/`Load`'s verbose case, and `GridStatus`'s dispatch are
covered above. Two supporting models were extended to make this a lossless decode of the
real gateway response:

- `models.GatewayStatus` gained `CellularDisabled` and `CanReboot` (both present in
  `/api/status`, previously unmodeled).
- `models.MeterReading` gained `NumMetersAggregated`, `LastPhaseVoltageCommunicationTime`,
  `LastPhasePowerCommunicationTime`, and `LastPhaseEnergyCommunicationTime`; its `Timeout`
  field, previously (incorrectly) typed `bool`, is now `time.Duration` - the gateway
  reports it as a nanosecond duration (e.g. `1500000000`), not a boolean.

`Poll`, `PollRaw`, `PollJSON`, and `Post` remain `any`/`[]byte`/`string`/`any` by design:
they are the generic escape hatches for endpoints without a typed accessor at all.

## New derived-view methods

These are new additions, not renames - the computation they contain used to live
duplicated across `proxy/routes.go` and (partially) `commands/get.go`:

- `Aggregates(ctx, opts ...AggregatesOption) (models.MetersAggregates, error)` - every
  meter's full reading, with optional site-zero-threshold and negative-solar corrections.
- `Snapshot(ctx, opts ...AggregatesOption) models.Snapshot` - the composite power/SOE/grid
  status/reserve/time-remaining/pack-energy/strings view.
- `PODView(ctx) models.PODView` - per-battery-block operational data.
- `FrequencyView(ctx) models.FrequencyView` - per-inverter frequency/voltage plus
  sync/meter fields, derived from `Vitals`.
- `SiteReading`/`SolarReading`/`BatteryReading`/`LoadReading`/`GridReading`/`HomeReading` -
  see the variadic-bool table above.
- `WithSiteZeroThreshold(watts float64)` and `WithNegativeSolarCorrection(correct bool)` -
  the `AggregatesOption` functional options `Aggregates` and `Snapshot` both accept.

## `New`'s connect behavior

```go
// Before
pw, err := gopowerwall.New(ctx, opts...)
// err is non-nil only for a Config validation failure; a failed connect is logged
// and swallowed - pw is always non-nil when err is nil.

// After
pw, err := gopowerwall.New(ctx, opts...)
// err is non-nil for a Config validation failure (pw is nil in that case), OR when
// the initial connection attempt failed across every applicable mode (pw is still
// non-nil and usable in that case, wrapping the failure as a *ConnectError).
```

Callers that only ever checked `pw.IsConnected()` after `New` need no changes at all - that
still works identically. Callers that did `pw, err := New(...); if err != nil { ... }`
unconditionally now need to decide whether a connect failure should be treated the same as
a config failure for their purposes; most should not, so:

```go
pw, err := gopowerwall.New(ctx, opts...)
var connectErr *gopowerwall.ConnectError
if err != nil && !errors.As(err, &connectErr) {
	return err // a genuine Config problem
}
// pw is valid here either way; check pw.IsConnected() if that matters to you.
```

## New sentinel errors and types

- `ErrFieldMissing` (`backend.ErrFieldMissing`, re-exported at the root) - a typed
  accessor's underlying poll succeeded, but the specific field it needed was absent or the
  wrong type. Distinct from `ErrNoClient` ("not connected at all") and `ErrUnsupported`
  ("this backend never offers that data").
- `ConnectError` (`backend.ConnectError`, re-exported at the root) - wraps a failed initial
  connection attempt from `New`; `Mode` names the `ConnectionMode` active when retries were
  exhausted.

## Not changed

Per [api-redesign-proposal.md](api-redesign-proposal.md)'s "not worth the churn" section,
these were considered and deliberately left alone:

- `Grid`/`Home` remain thin aliases of `Site`/`Load` (and `GridReading`/`HomeReading` of
  `SiteReading`/`LoadReading`) - pypowerwall/Home Assistant integrations expect both names.
- The `models` re-export aliases in `types.go` (`TEDAPIMode`, `AuthMode`, `PowerData`, and
  so on) stay, so callers don't need a second import just to name a returned type.
- `Power`, `Temps`, `Alerts`, `Strings`, and `BatteryBlocks` still degrade to a documented
  zero value on failure rather than returning `(T, error)` - a caller who checks
  `IsConnected()` first already has the signal an error return would duplicate.
