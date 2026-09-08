# API redesign proposal: idiomatic Go signatures for the root package

## Status: implemented

Every proposal in the "Clearly worth it" and "Arguable" sections below has been
implemented, as part of the same change that moved all derived computation out of
`proxy/` and `commands/` and into the root `gopowerwall` package (see
[architecture/README.md](architecture/README.md) and
[migration-v2.md](migration-v2.md) for the full picture and a symbol-by-symbol
before/after). "Not worth the churn" was left alone, as recommended. Specifically:

- **Proposal 1** (pointer returns -> `(T, error)`) - done for all ten methods listed.
  `Uptime` and `GetTimeRemaining` also picked up the proposed `time.Duration` type change.
- **Proposal 2** (`Alerts`/`Strings` drop their ignored `...bool`) - done.
- **Proposal 3** (`Lookup`/`LookupFloat` off the public API) - done; both are gone from the
  root package. Internal callers switched to `pkgs/lookup.Lookup` directly (`LookupFloat`
  has no equivalent there - see migration-v2.md for the replacement pattern).
- **Proposal 4** (variadic `...bool`) - done via option 4a (split into named methods) for
  every case in the table, including `Status` and `GridStatus`, which the proposal itself
  flagged as candidates for 4b (functional options) instead. In practice a straight split
  read more clearly for both: `Status` becomes a single typed accessor with no parameter at
  all (callers read the field they want off the returned struct), and `GridStatus` becomes
  `GridStatusString`/`GridStatusNumeric` per proposal 5b's own suggested resolution. The
  `AggregatesOption` functional-options pattern proposed in 4b *was* used, just for the new
  `Aggregates`/`Snapshot` derived-view methods rather than `Status`/`GridStatus`.
- **Proposal 5a** (`any` -> concrete types for `Status` and verbose sensor reads) - done.
  `models.GatewayStatus` and `models.MeterReading` were both extended to be lossless
  decodes of a real gateway response first (see migration-v2.md's "any returns become
  concrete types" section for the exact fields added), then wired in. The proxy's two
  `Status(ctx)` call sites (`/pw/status` and `renderIndexHTML`) were updated deliberately,
  not as a drop-in replacement, per the proposal's own caution.
- **Proposal 5b** (`Version`/`GridStatus` polymorphism) - resolved by proposal 4's split,
  as anticipated.
- **Proposal 6** (`New`'s connect behavior) - Option A implemented: `New` still attempts to
  connect and still returns a non-nil, usable `*Powerwall` on a failed attempt, but now
  wraps that failure as a `*ConnectError` instead of discarding it. The CLI's
  `ConnectionFlags.BuildPowerwall` deliberately un-wraps and discards a bare `*ConnectError`
  before returning, so the CLI's existing "build, then check `IsConnected()`" flow and its
  human-readable output are unaffected - see that function's doc comment.
- **Proposals 7 and 8** ("not worth the churn") - left alone, as recommended.
- **Proposal 9** (wrapping `Power`/`Temps`/`Alerts`/`Strings`/`BatteryBlocks` in
  `(T, error)` too) - left alone, as recommended; these still degrade to a documented zero
  value.

The rest of this document is preserved as the original design record; each proposal above
still describes the rationale and trade-offs that were weighed before implementing it.

This was originally a proposal only, with nothing implemented and no exported signature
changed as part of writing it - see `doc.go` and the expanded doc comments across
`powerwall.go`, `options.go`, `types.go`, and `errors.go` for the documentation-only pass
that accompanied the original proposal, both since superseded by the implementation above.

## Why now

Per `.agent/rules/powerwall.md`, parity with pypowerwall is required only for
the `gopowerwall` CLI and the proxy's HTTP surface; the Go library API
(everything in the root `gopowerwall` package) is explicitly free to be
idiomatic Go. It currently is not - it is close to a line-for-line
transliteration of pypowerwall's Python surface, including patterns that only
make sense in a dynamically-typed language with keyword arguments.

Nothing outside this module consumes the root package's API yet, and there
have been no tagged releases. That makes this the cheapest point at which to
make breaking changes: every proposal below can be adopted in place, with no
deprecation shims, no v2 module path, and no migration guide for downstream
users, because there are none yet. That will stop being true the moment this
is tagged v0.x and picked up by even one external `go.mod`. This document
exists so a decision about which changes to make gets made deliberately,
before that window closes, rather than by default.

## How to read this

Each proposal gives:

- **Current** - the signature as it exists today.
- **Proposed** - what it would become.
- **Rationale** - why the change is worth making (or not).
- **Migration cost** - what breaks, and how mechanical the fix is for a
  caller (today, that caller is the CLI and the proxy package, both in this
  same module).

Proposals are grouped into three tiers:

- [Clearly worth it](#clearly-worth-it) - real bugs or real ambiguity today;
  low risk, low cost to fix.
- [Arguable](#arguable) - genuine idiomatic improvements, but with real
  trade-offs (verbosity, API surface growth, or design work not yet done)
  that make "do it now" a judgment call rather than an obvious yes.
- [Not worth the churn](#not-worth-the-churn) - changes that would look more
  "Go-like" on paper but cost more than they return.

---

## Clearly worth it

### 1. Pointer returns (`*float64`/`*string`/`*bool`) become `(T, error)`

**Current** (10 methods):

| Method | Current |
|---|---|
| `Level` | `func (p *Powerwall) Level(ctx context.Context, scale ...bool) *float64` |
| `SiteName` | `func (p *Powerwall) SiteName(ctx context.Context) *string` |
| `Uptime` | `func (p *Powerwall) Uptime(ctx context.Context) *string` |
| `Din` | `func (p *Powerwall) Din(ctx context.Context) *string` |
| `GetReserve` | `func (p *Powerwall) GetReserve(ctx context.Context, scale ...bool) *float64` |
| `GetReserveForced` | `func (p *Powerwall) GetReserveForced(ctx context.Context) *float64` |
| `GetMode` | `func (p *Powerwall) GetMode(ctx context.Context) *string` |
| `GetTimeRemaining` | `func (p *Powerwall) GetTimeRemaining(ctx context.Context) *float64` |
| `GetGridCharging` | `func (p *Powerwall) GetGridCharging(ctx context.Context) *bool` |
| `GetGridExport` | `func (p *Powerwall) GetGridExport(ctx context.Context) *string` |

**Proposed:**

```go
func (p *Powerwall) Level(ctx context.Context) (float64, error)
func (p *Powerwall) SiteName(ctx context.Context) (string, error)
func (p *Powerwall) Uptime(ctx context.Context) (time.Duration, error) // see note below
func (p *Powerwall) Din(ctx context.Context) (string, error)
func (p *Powerwall) GetReserve(ctx context.Context) (float64, error)
func (p *Powerwall) GetReserveForced(ctx context.Context) (float64, error)
func (p *Powerwall) GetMode(ctx context.Context) (string, error)
func (p *Powerwall) GetTimeRemaining(ctx context.Context) (time.Duration, error) // see note below
func (p *Powerwall) GetGridCharging(ctx context.Context) (bool, error)
func (p *Powerwall) GetGridExport(ctx context.Context) (string, error)
```

**Rationale:** This is the single highest-value change in this document. Today
`nil` means "no connection", "network error", "field missing from the
response", and "field present but the wrong JSON type" - four different
failure modes collapsed into one value, with the actual `error` discarded at
the point of collapse. `GetReserve` and `GetMode`, for instance, call
`p.Operation(ctx)` directly, get back a real `error`, and throw it away before
returning `nil`; `Level` and `SiteName` get the same treatment one layer down,
since the `Poll` call they build on has already discarded its own error by
the time it hands back `nil`. A caller has
no way to log *why* a read failed, no way to distinguish "definitely
unsupported in this mode" from "transient network blip, try again", and no way
to use `errors.Is`/`errors.As` against the sentinel errors already exported in
`errors.go` (`ErrUnsupported`, `ErrTimeout`, `ErrNotFound`, ...) - those errors
exist and are returned by the typed accessors (`SystemStatus`, `SOE`,
`Operation`, ...) but are invisible from this family of methods. `(T, error)`
is the one Go idiom every Go programmer already knows, and it is the pattern
this same package already uses successfully for `SystemStatus`, `SOE`,
`GridStatusResponse`, `Operation`, and `SiteInfo`. This proposal simply
extends that existing, working pattern to the other ten methods that don't
yet follow it, for consistency across the whole API.

`Uptime` and `GetTimeRemaining` also get a bonus idiomatic fix along the way:
both currently return a string/`*float64` that is actually a duration in
disguise (`up_time_seconds` formatted as a string; hours as a bare float).
`time.Duration` is the correct Go type for "how much time" and gets `String()`
formatting, comparison, and arithmetic for free. This is a small enough
change to fold into the same pass rather than proposing separately, but it is
worth flagging on its own merits during review since it changes the type, not
just the shape.

**Migration cost:** Mechanical at every call site: `v := pw.Level(ctx, true)`
becomes `v, err := pw.Level(ctx); if err != nil { ... }`. Every current call
site is inside this module (the CLI's `commands/get.go`, `commands/set.go`,
and the proxy package) since there are no external consumers. The
`Uptime`/`GetTimeRemaining` type change additionally requires callers that
format the value themselves to switch to `time.Duration`'s own formatting or
convert explicitly (`d.Hours()`, `d.Seconds()`) - slightly more than
mechanical, but confined to a handful of call sites.

### 2. The two lying signatures: `Alerts` and `Strings` drop their ignored parameter

**Current:**

```go
func (p *Powerwall) Alerts(ctx context.Context, _ ...bool) models.AlertsList
func (p *Powerwall) Strings(ctx context.Context, _ ...bool) models.SolarStrings
```

**Proposed:**

```go
func (p *Powerwall) Alerts(ctx context.Context) models.AlertsList
func (p *Powerwall) Strings(ctx context.Context) models.SolarStrings
```

**Rationale:** These are not "flexible" signatures, they are false advertising.
The blank identifier as the parameter name is itself the tell: whatever a
caller passes is compiled, accepted, and then never read. Both parameters
exist purely to mirror pypowerwall's `alerts(hush=False)` and
`strings(verbose=False)` Python signatures, but neither Go method's body ever
looks at the value. A caller reading only the signature (which is all godoc
shows) has every reason to believe passing `true` changes behavior; it does
not, silently. This is the one item in this whole document with no
counterargument - there is no design trade-off, no verbosity cost, nothing to
weigh. It is dead, misleading surface that should simply be deleted.

**Migration cost:** Low but not zero: `proxy/handlers.go` and
`proxy/routes.go` do call `s.PW.Strings(ctx, false)`, `s.PW.Strings(ctx,
true)`, and `s.PW.Alerts(ctx, false)` today (presumably written to mirror
pypowerwall's `strings(verbose=...)`/`alerts(hush=...)` call shape, even
though the parameter has never done anything on the Go side) - those four
call sites need the now-invalid extra argument dropped. `powerwall_test.go`
and `test/integration/local_test.go` already call both with zero arguments,
so no test changes are needed beyond the proxy package's call sites.

### 3. `Lookup` and `LookupFloat` should not be on the public API

**Current:**

```go
func Lookup(data any, keys ...string) any
func LookupFloat(m map[string]any, key string) float64
```

**Proposed:** Move both out of the exported root-package surface entirely -
either unexported (`lookup`/`lookupFloat` inside `gopowerwall`, if the root
package still needs them internally after the `any`-return cleanup below) or
simply deleted from the root package, since they already exist as
`pkgs/lookup.Lookup` and are one-line wrappers here. If a genuine external use
case for a "safely walk a `map[string]any`" helper emerges, it belongs in
`pkgs/lookup` (already an importable package) rather than duplicated on the
facade type's own namespace.

**Rationale:** `Lookup`/`LookupFloat` are implementation details of how this
package decodes untyped gateway responses, exported today only because the
untyped accessors (`Poll`, `Site`, `Status`, ...) hand callers an `any` and
those callers need *some* way to dig into it. If the `any`-return proposals
below are adopted, most of that need disappears - callers get typed structs
instead and never need to walk a `map[string]any` by hand. Keeping `Lookup`
exported after that point would be advertising a workaround for a problem
this package no longer has. Even without the `any` cleanup, `Lookup` is
already redundant with the standard library idiom for this
(`m, ok := data.(map[string]any)`) plus a type switch, and with
`pkgs/lookup.Lookup`, which already exists and is directly importable by
anyone who genuinely wants this specific helper.

**Migration cost:** Zero for the CLI and proxy packages - a grep confirms
neither calls `gopowerwall.Lookup`/`LookupFloat`; every production use is
internal to `powerwall.go` itself (`Alerts`, `Strings`, `Status`,
`fetchSensor`, `BatteryBlocks`, `SiteName`, `Strings`'s per-string decode),
so those call sites simply switch to calling `lookup.Lookup` from
`pkgs/lookup` directly (already imported in `powerwall.go`) instead of
through the root-package re-export. The one real external caller is
`test/integration`, which calls `gopowerwall.Lookup(data, "din")` and
similar a handful of times to assert on fields of an untyped `Poll` result;
those call sites would need to either import `pkgs/lookup` directly or
switch to asserting against a typed accessor's fields where one covers the
same endpoint (e.g. `Din()`/`Status()` today, or `GatewayStatus` if proposal
5a is adopted).

---

## Arguable

### 4. Variadic `...bool` parameters

**Current** (11 exported methods, one unexported helper):

```go
func (p *Powerwall) Level(ctx context.Context, scale ...bool) *float64
func (p *Powerwall) Site(ctx context.Context, verbose ...bool) any
func (p *Powerwall) Solar(ctx context.Context, verbose ...bool) any
func (p *Powerwall) Battery(ctx context.Context, verbose ...bool) any
func (p *Powerwall) Load(ctx context.Context, verbose ...bool) any
func (p *Powerwall) Grid(ctx context.Context, verbose ...bool) any
func (p *Powerwall) Home(ctx context.Context, verbose ...bool) any
func (p *Powerwall) Version(ctx context.Context, intValue ...bool) any
func (p *Powerwall) GetReserve(ctx context.Context, scale ...bool) *float64
func (p *Powerwall) Status(ctx context.Context, param ...string) any
func (p *Powerwall) GridStatus(ctx context.Context, outputType ...GridStatusOutput) any
```

This pattern exists purely to fake Python's optional keyword arguments
(`def level(self, scale=False)`) in a language that doesn't have them. In Go
it buys nothing: only element `[0]` is ever read, so `pw.Level(ctx, true,
false, false)` silently behaves identically to `pw.Level(ctx, true)`, and the
signature does not tell a godoc reader what the boolean *means* without
reading the doc comment (which, as of this pass, now says so explicitly - but
a self-documenting signature would not need the comment to do that work).

Three ways to fix this, in order of how much they're recommended:

**4a. Split into two clearly-named methods (recommended for `Level`,
`GetReserve`, `Version`, and the sensor family `Site`/`Solar`/`Battery`/
`Load`/`Grid`/`Home`):**

```go
func (p *Powerwall) Level(ctx context.Context) (float64, error)
func (p *Powerwall) LevelScaled(ctx context.Context) (float64, error)

func (p *Powerwall) Site(ctx context.Context) (float64, error)
func (p *Powerwall) SiteReading(ctx context.Context) (models.MeterReading, error)
// ... and equivalently for Solar/Battery/Load/Grid/Home
```

*Rationale:* The boolean in these specific methods isn't really a "modifier",
it's a *fork in what the method returns* (a scaled vs. raw percentage; a
scalar Watts figure vs. the sensor's entire reading). That is exactly the
situation where two named methods communicate more than one method with a
flag, because the name documents the difference instead of requiring a doc
comment to explain what `true` does. It's also the smallest, most literal
fix - a 1:1 split with no new types.

*Migration cost:* Mechanical - `pw.Level(ctx, true)` becomes
`pw.LevelScaled(ctx)`. Doubles the number of exported names in the sensor
family specifically (6 methods become 12), which is the real cost of this
option: more names to document, discover, and keep consistent.

**4b. Functional options (recommended for `Status` and `GridStatus`, where the
parameter set may reasonably grow later):**

```go
type StatusOption func(*statusConfig)
func WithStatusField(field string) StatusOption
func (p *Powerwall) Status(ctx context.Context, opts ...StatusOption) (models.GatewayStatus, error)

func (p *Powerwall) GridStatus(ctx context.Context, format GridStatusOutput) (any, error) // or split per-format, see below
```

*Rationale:* This package already has the functional-options pattern
established and idiomatic elsewhere (`Option` for `Config`, `PollOption` for
`Poll`) - reusing it here is consistent rather than introducing a fourth way
to configure a call. It also scales better than a positional bool if more
than one axis of configuration is ever needed (e.g. `Status` gaining a
"bypass cache" knob alongside the field filter).

*Migration cost:* Higher than 4a per call site (`pw.Status(ctx, "version")`
becomes `pw.Status(ctx, gopowerwall.WithStatusField("version"))`), and
requires designing and naming the option type and its constructors - this is
real API-design work, not a mechanical rename, which is exactly why it's
filed as "arguable" rather than "clearly worth it".

**4c. Plain required parameter (simplest, but least flexible):**

```go
func (p *Powerwall) Level(ctx context.Context, scale bool) (float64, error)
```

*Rationale:* If the boolean genuinely is just "on/off" for every foreseeable
caller (arguably true for `Level`/`GetReserve`'s scale flag), making it
required removes the "what happens if I omit it" ambiguity entirely, at the
cost of every call site needing to pass something. This is a reasonable
fallback wherever 4a's two-method split feels like overkill for a
rarely-toggled flag.

*Recommendation:* Prefer 4a for `Level`, `GetReserve`, `Version`, and the
sensor family, since in every one of those cases the flag picks between two
genuinely different return values/types, not a minor behavior tweak. Prefer
4b for `Status`/`GridStatus`, where the "parameter" is closer to a
configuration axis than a fork in output shape. Avoid leaving any of them as
`...bool` - that option serves no one.

### 5. `any` returns become concrete types

**Current** (12 places returning bare `any`): `Poll`, `Post`, `Site`, `Solar`,
`Battery`, `Load`, `Grid`, `Home`, `Status`, `Version`, `GridStatus`, and the
free function `Lookup` (addressed separately above).

This group splits cleanly into two cases:

**5a. A typed model already exists - this is pure debt, not missing work.**

| Method | Untyped today | Existing typed model |
|---|---|---|
| `Status(ctx)` (no field arg) | `any` (really `map[string]any` decoded from `/api/status`) | `models.GatewayStatus` already exists in `models/status.go`, covering every field `Status` callers use today (`din`, `version`, `up_time_seconds`, and so on); it would need a couple of fields added (e.g. `cellular_disabled`, `can_reboot`, both present in the recorded `/api/status` fixture but not yet modeled) to be a full 1:1 replacement rather than a subset |
| `Site`/`Solar`/`Battery`/`Load` with `verbose=true` | `any` (really the sensor's sub-object from `/api/meters/aggregates`) | `models.MeterReading` already exists in `models/aggregates.go` and covers the commonly-used fields of this exact sub-object (`instant_power`, voltages, currents, cumulative energy); a few fixture fields it doesn't yet carry (`num_meters_aggregated`, the three `last_phase_*_communication_time` fields) would need adding for a lossless round-trip |

*Proposed:*

```go
func (p *Powerwall) Status(ctx context.Context) (models.GatewayStatus, error)
// paired with a separate accessor for the single-field case, see proposal 4b
func (p *Powerwall) SiteReading(ctx context.Context) (models.MeterReading, error)
// ... equivalently for Solar/Battery/Load
```

*Rationale:* There is very little design work left to do here - the
destination type already exists and is used elsewhere in this same codebase
(`models.GatewayStatus` and `models.MeterReading`). The only reason `Status`
and verbose `Site`/`Solar`/`Battery`/`Load` still return `any` is that nobody
has wired the existing struct in.

*Migration cost:* Real, and worth calling out explicitly for `Status`
specifically: `proxy/handlers.go` uses `s.PW.Status(ctx)` twice today, once
returned directly as the body of the parity-critical `/pw/status` JSON
response and once type-asserted to `map[string]any` in `renderIndexHTML` to
pull out `version`/`git_hash` for the web UI's index page. Both call sites
would need updating (the second is a real logic change, not a mechanical
rename), and - because the proxy's HTTP JSON shape is a frozen parity
surface per `.agent/rules/powerwall.md` - `models.GatewayStatus` would need
to be a genuinely lossless model of `/api/status` (including
`cellular_disabled` and `can_reboot`, not modeled today) before this switch
can happen without silently dropping fields from the proxy's own output.
`Site`/`Solar`/`Battery`/`Load`'s verbose case has one comparable caller
(`proxy/routes.go`'s `/api/meters/aggregates` handling), with the same
"model must be complete first" caveat. This is still a good change, but it
is "extend `models.GatewayStatus`/`models.MeterReading` to be complete, then
switch `Status`/verbose sensor reads over" - not the pure freebie it would
be if the root package had no downstream parity surface depending on it.

**5b. No existing model fits cleanly, or the value is genuinely polymorphic.**

`Poll`/`PollRaw`/`PollJSON`/`Post` are the generic escape hatches for
endpoints without a typed accessor at all - by definition, no single
concrete type can replace their return type without turning them into
something else entirely (they'd stop being generic). These are correctly
`any`/`[]byte`/`string` today and should stay that way; the fix for their
callers is not "change Poll's signature" but "add a typed accessor and use
that instead for any endpoint that's called often enough to be worth it" -
which is a `models/` and coverage question, not an API-shape one.

`Version(ctx, intValue ...bool)` and `GridStatus(ctx, outputType
...GridStatusOutput)` are genuinely polymorphic by design: the caller
explicitly asks for one of two (or three) different concrete types via the
flag/enum, and pypowerwall's own proxy surface depends on being able to
request each. The right fix for these is proposal 4 (split or option-ify the
selector), which incidentally also fixes the `any` return - once
`GridStatus` becomes `GridStatusJSON(ctx) (models.GridStatusResponse,
error)` / `GridStatusNumeric(ctx) (int, error)` / `GridStatusString(ctx)
(string, error)` (or equivalent), each individual method has a concrete
return type, and the only thing that stays `any`-shaped is the *dispatch*,
not any one call's result.

**Recommendation:** Do 5a, but complete the two `models` structs first, and
update the proxy's `Status(ctx)` call sites deliberately rather than
assuming a drop-in replacement. Treat 5b as resolved by proposal 4, not
as a separate change.

### 6. Should `New` stop connecting implicitly?

**Current:**

```go
func New(ctx context.Context, opts ...Option) (*Powerwall, error)
// - always attempts Connect internally
// - a failed Connect is logged, not returned
// - err is non-nil only for a Config validation failure
```

**Option A - keep constructing-and-connecting, but return a typed connection
error instead of swallowing it:**

```go
func New(ctx context.Context, opts ...Option) (*Powerwall, error)
// - still attempts Connect internally
// - still returns *Powerwall (non-nil) even on a failed connect, for callers
//   that want to retry later without reconstructing
// - but wraps a failed Connect's cause in a typed *ConnectError alongside the
//   *Powerwall, e.g.: pw, err := New(ctx, opts...); if err != nil { var ce
//   *ConnectError; if errors.As(err, &ce) { ... } }
```

**Option B - split construction from connection entirely, the more
conventionally idiomatic Go shape:**

```go
func NewPowerwall(opts ...Option) (*Powerwall, error) // validates Config only, never dials
func (p *Powerwall) Connect(ctx context.Context, retry bool) bool // unchanged, caller decides when to dial
```

**Rationale:** pypowerwall's own `Powerwall.__init__` connects synchronously
and swallows a connect failure into an internal "not connected" state,
matching option A's spirit (New still tries to connect, but at least surfaces
*why* it failed). That's a smaller behavioral change from today, keeps the
`New(...) ; if !pw.IsConnected() { ... }` idiom the README and quickstart
already teach, and fixes the actual complaint (an error is currently
possible to get but useless - `New` never returns one for a connect failure
today, only for bad config). Option B is the more idiomatic Go shape in the
abstract - constructors normally shouldn't do I/O - but it's a bigger
behavioral change that would require rewriting the README/quickstart's
core example, the CLI's `commands/connection.go`, and the proxy's
`NewServer`, all of which currently rely on "construct implies connect".

**Migration cost:** Option A: low - existing `if err != nil` checks keep
working (config errors still come through the same path), only callers that
want the new connect-error detail need to add an `errors.As`. Option B:
moderate - every current call site that does `pw, err := New(ctx, opts...)`
and then checks `pw.IsConnected()` would need a second explicit `pw.Connect(ctx,
false)` call inserted, and the CLI/proxy wiring that assumes a connected (or
at least connect-attempted) `*Powerwall` comes back from construction would
need review.

**Recommendation:** Option A. It fixes the actual complaint in this task (an
error the caller can't get today) without touching the construction idiom
this package's own docs, CLI, and proxy already depend on.

---

## Not worth the churn

### 7. Re-deriving `Grid`/`Home` as their own methods instead of aliases

`Grid` and `Home` are one-line aliases for `Site` and `Load` respectively,
existing purely because pypowerwall exposes both names for the same data.
Giving them independent implementations, or removing them in favor of a
single canonical name, would be pure API-surface churn with no behavior
change and a real cost: `Grid`/`Home` are the names pypowerwall/Home
Assistant integrations reach for by convention, and removing them buys
nothing but a forced rename at every call site for no functional gain. Leave
these as thin aliases; if proposal 4a's method-split is adopted for
`Site`/`Load`, extend the same aliasing to `Grid`/`Home` (`GridReading` =
alias of `SiteReading`, etc.) rather than dropping either name.

### 8. Renaming `PowerData`/`PowerSummary` or collapsing the `models` re-export aliases in `types.go`

`types.go` re-exports several `models` package types and constants
(`TEDAPIMode`, `AuthMode`, `TEDAPIApiVersion`, the `TEDAPIOff`/`AuthModeCookie`/
etc. constants, `PowerData = models.PowerSummary`) purely so callers don't
have to add a second import for `github.com/blackbirdworks/gopowerwall/models`
just to name a type they get back from a `Powerwall` method. This is already
idiomatic - it's the same pattern the standard library uses for e.g.
`context.Context` being usable without importing anything beyond `context`
itself in simple cases - and collapsing it (forcing every caller to import
`models` directly) would be a strictly worse experience for a purely
cosmetic "fewer aliases" goal. No change proposed.

### 9. Wrapping every remaining "returns zero value on failure" method
(`Power`, `Temps`, `Alerts`, `Strings`, `BatteryBlocks`) in `(T, error)` too

Unlike the pointer-return group in proposal 1, these methods return a
*struct or map*, not a pointer - `Power` returns a zero-value
`models.PowerSummary{}` on failure, not `nil`, so there is no "nil trap" of
the kind proposal 1 fixes; a caller who checks `pw.IsConnected()` first (as
the package's own docs now instruct) already gets a reliable signal. Adding
`error` returns here mainly duplicates what `IsConnected` already tells the
caller, at the cost of four more `(T, error)` pairs to unwrap at call sites
that mostly don't care (e.g. a metrics exporter that's happy to report zero
power on a blip). This is a smaller win than proposal 1 for a real cost in
call-site verbosity; worth revisiting only if a real caller reports being
unable to distinguish "genuinely zero" from "failed" in practice.

---

## Summary: top three recommendations

1. **Proposal 1** (pointer returns -> `(T, error)`) - the highest-value,
   lowest-risk change in this document. It fixes a real, already-observed bug
   class (the `commands/get.go` `%v`-on-a-typed-nil-pointer bug that motivated
   this whole review) at its root, not just at the one call site that was
   caught.
2. **Proposal 2** (drop `Alerts`/`Strings`'s ignored `...bool`) - free, safe,
   and removes two actively misleading signatures.
3. **Proposal 5a** (wire `Status`/verbose sensor reads into the
   `models.GatewayStatus`/`models.MeterReading` types that already exist) -
   a real correctness/discoverability win once those two structs are made
   complete, since most of the destination type already exists and is
   tested elsewhere in this codebase; budget time for closing the small
   field gaps and updating the proxy's two `Status(ctx)` call sites,
   since that surface is parity-frozen.

Proposals 4 (variadic bool), 5a's proxy-facing parts, and 6 (New's connect
behavior) are worth doing but deserve their own design discussion before
implementation, since - unlike 1 and 2 - they involve real trade-offs and
non-mechanical follow-up rather than a single obviously-better
answer.
