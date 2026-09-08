# gopowerwall / pypowerwall parity matrix

**Upstream audited:** [jasonacox/pypowerwall](https://github.com/jasonacox/pypowerwall)
`main` @ commit `a3b327be32cd31bb7e297c22e98040c967a86fad` (2026-09-07), module version
string `0.17.2` (`pypowerwall/__init__.py`). Fetched directly via
`raw.githubusercontent.com` on 2026-09-07; every claim below is checked against that
checkout, not against memory of pypowerwall or against gopowerwall's own comments about
pypowerwall. Where a file could not be fetched, the affected row says so explicitly.

## Second pass: resolving open questions against source (2026-09-07)

Per `.agent/rules/powerwall.md` ("When in doubt, read the Python"), this pass re-reads
upstream directly to settle every `unverified` marker and every question raised about
this repo's own reasoning, rather than accepting the first audit's inferences. **Upstream
re-audited at `main` @ commit `4d092c7ed0b83100a486545a711204feb7e4b7f6` (module version
`0.17.3`)** — `main` had moved 5 commits ahead of the original `a3b327be3` audit point.
Those 5 commits (`92fed7b9a1` merge of PR #379, plus `d2e159cdd9`, `dd54ecddd3`,
`284b422bcb`, and the version bump `4d092c7ed0`) touch only PW3 v1r islanding support and
land in exactly three files this matrix cites: `pypowerwall/__init__.py`,
`pypowerwall/tedapi/__init__.py`, and `pypowerwall/tedapi/pypowerwall_tedapi.py`. That
drift **does** change one finding below (`go_off_grid`/`reconnect_grid` — see §1 items
34/35 and §4), which is called out explicitly where it appears; nothing else in this
document is affected by the version drift, since `proxy/server.py`, the cloud/fleetapi
backends, and the stub/mock files are untouched between the two commits (confirmed via
the GitHub compare API).

Files fetched fresh in this pass, beyond what the first audit read:
`pypowerwall/fleetapi/fleetapi.py`, `pypowerwall/tedapi/pypowerwall_tedapi.py`,
`pypowerwall/tedapi/auth_mode.py`, `pypowerwall/tedapi/tedapi_v1r.py`,
`pypowerwall/cloud/mock_data.py`, `pypowerwall/cloud/stubs.py`,
`pypowerwall/fleetapi/mock_data.py`, `pypowerwall/fleetapi/stubs.py`,
`pypowerwall/tedapi/mock_data.py`, `pypowerwall/tedapi/stubs.py`,
`pypowerwall/cloud/teslapy/endpoints.json`, `pypowerwall/__main__.py` (reread in full for
the reserve-cap CLI logic), `pypowerwall/tedapi/queries/V2026_06.json`.

Rows revised in this pass (see the referenced section for detail): §1 items 9, 30, 34, 35;
§2.1 `set` row; §3.1 `/strings`, `/pod`, `/fans`+`/fans/pw`, `/health`, allowlist row,
control-route row; §3.2 (rewritten in full — the stub comparison is no longer
"unverified"); §4 `SetGridCharging`/`SetGridExport`/`GetTimeRemaining`/`GoOffGrid` rows;
the "Additional finding: `PW_TEDAPI_AUTH_MODE`" section of `MISSING.md` (reversed — the
option is not vestigial upstream).

**gopowerwall audited:** working tree at
`/Users/andrew.bishop/Documents/Code/gopowerwall`, `main`, uncommitted state as of this
audit (see file:line citations for exact provenance).

**Status legend:** `parity` (matches upstream), `partial` (some but not all upstream
behavior present), `missing` (upstream feature entirely absent), `divergent` (both sides
implement something, but shapes/semantics differ), `n/a (deliberate)` (upstream also
lacks it, or the difference is an intentional idiomatic-Go choice on a non-parity
surface), `unverified` (could not be confirmed from either side's source alone — usually
because it depends on live Tesla-account or gateway-firmware behavior).

This document supersedes the unverified claims in `MISSING.md` where the two disagree;
each such case is called out under "Corrections to MISSING.md" at the end.

---

## 1. Library API — `Powerwall` facade

Upstream: `pypowerwall.Powerwall` class, `pypowerwall/__init__.py`. Go: package
`gopowerwall`, `powerwall.go` (facade type `Powerwall`).

| # | Upstream method (file:line) | Go method (file:line) | Status | Notes |
|---|---|---|---|---|
| 1 | `__init__(host, password, email, timezone, pwcacheexpire, timeout, poolmaxsize, cloudmode, siteid, authpath, authmode, cachefile, fleetapi, auto_select, retry_modes, gw_pwd, rsa_key_path, wifi_host, tedapi_api_version, tedapi_auth_mode)` (`__init__.py:133`) | `New(ctx, opts ...Option)` (`powerwall.go:54`) | divergent | Idiomatic: Go replaces ~19 constructor kwargs with functional options (`options.go`). Coverage is otherwise equivalent; this is the correct idiomatic-Go transliteration and not a parity concern per `.agent/rules/powerwall.md`. |
| 2 | `connect(retry=False) -> bool` (`:254`) | `Connect(ctx, retry bool) bool` (`powerwall.go:255`) | parity | Same circular-fallback semantics (Local→FleetAPI→Cloud→Local), same retry-with-backoff shape. |
| 3 | `is_connected()` (`:385`) | `IsConnected() bool` (`powerwall.go:305`) | parity | |
| 4 | `poll(api, jsonformat=False, raw=False, recursive=False, force=False)` (`:400`) | `Poll(ctx, api, opts ...PollOption) any` (`powerwall.go:415`), plus `PollRaw` (`:430`) and `PollJSON` (`:449`) | divergent | Python's single method with 4 boolean kwargs is split into three Go methods plus a `PollOption` functional-option set (`WithForce`, `WithRaw`). This is a reasonable idiomatic split, but note the **error-swallowing** divergence: Python's `poll()` returns `None` on any failure and callers cannot distinguish "no data" from "network error" either — so this part is actually parity, not a regression. |
| 5 | `post(api, payload, din=None, jsonformat=False, raw=False, recursive=False)` (`:424`) | `Post(ctx, api, payload any, din ...string) any` (`powerwall.go:469`) | divergent (idiomatic) | Python's `Optional[str]` `din` becomes a Go variadic `...string`. Same "return nil/None on error, discard the error" shape on both sides — this one **is** parity in its error-discarding behavior, just idiomatically transliterated for the optional param. |
| 6 | `level(scale=False)` (`:448`) | `Level(ctx, scale ...bool) *float64` (`powerwall.go:512`) | parity | Both default `scale` to `False`/`false` when omitted. `*float64` vs `Optional[float]` is the correct idiomatic rendering of "nil on failure". |
| 7 | `power() -> dict` (`:468`) | `Power(ctx) models.PowerSummary` (`powerwall.go:544`) | divergent (idiomatic, but silent-zero) | Python returns `{}` (falsy, distinguishable from real 0-watt data by identity) on failure. Go returns a zero-valued `models.PowerSummary{}` — indistinguishable from "site is genuinely producing 0W everywhere". This is a real, if minor, information-loss divergence worth noting for any consumer that needs to detect "no data yet" vs "confirmed zero". |
| 8 | `vitals(jsonformat=False)` (`:477`) | `Vitals(ctx) (models.VitalsData, error)` (`powerwall.go:709`) | divergent (idiomatic) | Go is the only method in the whole facade that returns `(T, error)` instead of a bare value/pointer — inconsistent with the rest of the facade's "nil-on-failure" convention, though it is a defensible improvement (callers *can* distinguish error from empty-vitals here, unlike everywhere else in the facade). |
| 9 | `strings(jsonformat=False, verbose=False)` (`:495-579`) | `Strings(ctx context.Context) models.SolarStrings` (`powerwall.go:1042`) | **divergent — confirmed, field names and key scheme both wrong** | Fully resolved in this pass (was "unverified against real hardware" in MISSING.md); see §2 of this section's writeup below and the rewritten correction #7. Go reads `PVAC_Vsolar<label>`/`PVAC_Isolar<label>`/`PVAC_Psolar<label>` (`powerwall.go:1063-1065`) — fields that upstream **never emits under any backend**. Upstream's non-verbose path (`__init__.py:497-549`) keys the result on a letter derived from the *last character* of the matched field name (`name = e[-1] + devicemap[deviceidx % len(devicemap)]`, `__init__.py:536`), scanning for substrings `PVAC_PVCurrent`, `PVAC_PVMeasuredPower`, `PVAC_PVMeasuredVoltage`, `PVAC_PvState`, and `PVS_String` in each `PVAC`-prefixed vitals device's fields, plus a `Connected` case keyed on `e[10]` (the character right after the literal `"PVS_String"` prefix, `__init__.py:544-546`). Result values are `{Current, Power, Voltage, State, Connected}`, not Go's `{Connected, Voltage, Current, Power}` (Go has no `State` at all). The `verbose=True` path (accepted-and-ignored in Go, confirmed) instead keys on the full device name and copies only the raw matched fields, unfiltered. There is also a **third, fallback path** (`__init__.py:551-579`) used only when `vitals()` returns no devices at all: it reads `/api/solar_powerwall`'s `pvac_status.string_vitals` array positionally into a precomputed `string_map` (`'', 'A'..'D', '1'+'A'..'D', ...`) — Go has no equivalent of this fallback either. |
| 10 | `site/solar/battery/load/grid/home(verbose=False)` (`:585-614`) | `Site/Solar/Battery/Load/Grid/Home(ctx, verbose ...bool) any` (`powerwall.go:589-612`) | parity | |
| 11 | `site_name() -> Optional[str]` (`:619`) | `SiteName(ctx) *string` (`powerwall.go:645`) | parity | |
| 12 | `status(param=None, jsonformat=False)` (`:629`) | `Status(ctx, param ...string) any` (`powerwall.go:660`) | parity | |
| 13 | `version(int_value=False)` (`:668`) | `Version(ctx, intValue ...bool) any` (`powerwall.go:673`) | parity | |
| 14 | `uptime()` (`:675`) | `Uptime(ctx) *string` (`powerwall.go:687`) | parity | |
| 15 | `din()` (`:679`) | `Din(ctx) *string` (`powerwall.go:698`) | parity | |
| 16 | `temps(jsonformat=False)` (`:683`) | `Temps(ctx) models.PowerwallTemps` (`powerwall.go:746`) | parity | Both key off vitals devices prefixed `TETHC`/`THC_AmbientTemp`. |
| 17 | `alerts(jsonformat=False, alertsonly=True)` (`:700`) | `Alerts(ctx, _ ...bool) models.AlertsList` (`powerwall.go:765`) | divergent | The Python `alertsonly` parameter (when `False`, returns richer alert objects, not just names) is accepted-and-ignored in Go (`_ ...bool`) exactly like `Strings`'s `verbose`. |
| 18 | `get_reserve(scale=True, force=False) -> Optional[float]` (`:752`) | `GetReserve(ctx, scale ...bool) *float64` (`powerwall.go:970`) | **divergent — default flipped** | Python defaults `scale=True` (applies the Tesla-App 5%-buffer transform `(pct/0.95) - (5/0.95)` by default). Go defaults to **unscaled** when the variadic is omitted (`len(scale) > 0 && scale[0]`, else raw). A caller doing `pw.GetReserve(ctx)` in Go gets a **different number** than `pw.get_reserve()` in Python for the same gateway state. This is exactly the "Python default argument rendered as Go variadic with an inverted default" pattern the audit was asked to flag. `force` is also silently dropped from the Go signature entirely — no way to bypass cache. |
| 19 | `get_mode(force=False) -> Optional[float]` (`:769`) — despite the type hint this actually returns the `real_mode` **string** (`.py:775`, `return data['real_mode']`), a doc/annotation bug upstream | `GetMode(ctx) *string` (`powerwall.go:984`) | parity | Go's signature is actually more correct than upstream's own (wrong) type annotation. `force` again silently dropped. |
| 20 | `set_reserve(level) -> Optional[dict]` (`:778`) | `SetReserve(ctx, level float64) (models.Operation, error)` (`powerwall.go:994`) | divergent | See item 22 below — both call through a combined "set operation" path, but the Go path is missing a safety behavior Python has for local-mode partial writes. |
| 21 | `set_mode(mode) -> Optional[dict]` (`:790`) | `SetMode(ctx, mode string) (models.Operation, error)` (`powerwall.go:999`) | divergent | Same underlying issue as item 22. |
| 22 | `set_operation(level=None, mode=None, jsonformat=False) -> Optional[Union[dict,str]]` (`:802`) | `SetOperation(ctx, level *float64, mode *string) (models.Operation, error)` (`powerwall.go:1004`) | **divergent — confirmed correctness bug for local mode** | **This is the most severe library-level finding in this audit.** Upstream's `set_operation` (`__init__.py:851-877`) explicitly back-fills the omitted field from the *live* gateway state before POSTing, but **only for the local backend** (`full_overwrite = isinstance(self.client, PyPowerwallLocal)`), with this exact comment: `"The local gateway /api/operation is a full overwrite: an omitted field would clobber the current setting, so back-fill from the live values."` For cloud/fleetapi/tedapi it deliberately sends a *partial* payload instead, because "Tesla applies BACKUP_RESERVE and OPERATION_MODE as two asynchronous commands" and back-filling there would race the other write. Go's `SetOperation` (`powerwall.go:1004-1036`) builds `payload` with **only the non-nil field**, unconditionally, for every mode including local — there is no `IsLocal()` branch and no read-back of the current `real_mode`/`backup_reserve_percent`. Confirmed further at the transport layer: `backend/local/local.go:436` (`Post`) does a bare `json.Marshal(payload)` and POSTs it verbatim to `/api/operation` with **zero** field back-filling logic anywhere in the file (`grep -n "operation\|backup_reserve\|real_mode" backend/local/local.go` returns nothing). Net effect: on a real local gateway, `gopowerwall set --local --reserve 20` (which calls `SetReserve` → `SetOperation(ctx, &level, nil)`) sends `{"backup_reserve_percent": 20}` with no `real_mode` key, and per upstream's own documented gateway behavior this **clobbers the operating mode** to whatever the gateway treats a missing field as. Symmetrically, `set --local --mode backup` clobbers the reserve percentage. This is a live-hardware data-corruption risk in the one connection mode where gopowerwall's writes are otherwise real (see §4). |
| 23 | `schedule_max_backup(duration_seconds=7200)` (`:866`) | `ScheduleMaxBackup(ctx, durationSeconds ...int)` (`powerwall.go:1184`) | parity | Both require v1r/TEDAPI transport with backup capability. |
| 24 | `cancel_max_backup()` (`:873`) | `CancelMaxBackup(ctx)` (`powerwall.go:1203`) | parity | |
| 25 | `get_backup_events()` (`:880`) | `GetBackupEvents(ctx)` (`powerwall.go:1217`) | parity | |
| 26 | `grid_status(output_type="string", type=None)` (`:888`) | `GridStatus(ctx, outputType ...GridStatusOutput) any` (`powerwall.go:892`) | parity | Python's untyped `output_type: str` string enum (`"string"`/`"numeric"`/`"json"`) becomes a proper Go typed enum `GridStatusOutput` — a genuine idiomatic improvement, not a parity gap. |
| 27 | `system_status(jsonformat=False)` (`:932`) | `SystemStatus(ctx) (models.SystemStatus, error)` (`powerwall.go:864`) | parity | |
| 28 | `battery_blocks(jsonformat=False)` (`:965`) | `BatteryBlocks(ctx) map[string]models.BatteryBlock` (`powerwall.go:837`) | divergent | Python returns a list-shaped structure keyed by index/serial ambiguously; Go keys explicitly by `PackageSerialNumber`. Behaviorally equivalent for typical use; shape differs if serialized directly. |
| 29 | `get_time_remaining()` (`:1029`) | `GetTimeRemaining(ctx) *float64` (`powerwall.go:1039`) | **divergent for cloud/fleetapi — corrected from "parity" in the first audit** | Both are `None`/`nil`-on-unknown in local/tedapi mode (parity there). For cloud/fleetapi, the first audit's "both hard-code 0.0" framing is now confirmed **wrong**: upstream makes a real API call and returns live data, Go hard-codes `0.0` unconditionally — see the rewritten §4 row for full detail and citations. |
| 30 | `set_grid_charging(mode) -> Optional[dict]` (`:1040`) | `SetGridCharging(ctx, mode bool) (models.Operation, error)` (`powerwall.go:1076`) | **divergent — confirmed field-negation bug, live-hardware write** | Python's `mode` parameter is a loosely-typed `on`/`off`/`True`/`False`/etc. string-or-bool; Go's `bool` is stricter and better. Reachable for cloud/fleetapi in Python, and — newly confirmed in this pass — also for **TEDAPI's v1r transport** via a local config write, not a "GraphQL mutation" as previously guessed (see §4); Go's facade only reaches cloud/fleetapi (`powerwall.go:1080-1095`), so TEDAPI/v1r grid-charging control has no Go path at all (a real, if narrower, gap on top of the one below). **The severe finding**: upstream's `set_grid_charging` on *every* backend that implements it (cloud `pypowerwall_cloud.py:878-891`, fleetapi `fleetapi.py:733-754`, TEDAPI/v1r `pypowerwall_tedapi.py:861-870`) negates the caller's boolean before writing it — `mode=True` ("enable grid charging") is translated to `disallow_charge_from_grid_with_solar_installed: False`, and `mode=False` to `: True` — because the wire field is phrased as a *prohibition*, not as the enable flag the public API name implies. Go's `backend/cloud/cloud.go:785-798` and `backend/fleetapi/fleetapi.go:688-701` (`SetGridCharging`) both forward `mode` to that field **verbatim, unnegated**: `map[string]any{"disallow_charge_from_grid_with_solar_installed": mode}`. A user calling `gopowerwall set --cloud --gridcharging on` sends `disallow_charge_from_grid_with_solar_installed: true`, the exact opposite of what upstream would send and the exact opposite of the user's request — this is a live write to a real Tesla site once §4's "no-op" bug is fixed, so shipping the "make the write real" fix (§6 item 2) without also fixing this negation would make the regression worse, not better, by turning a harmless no-op into an inverted live write. **Required fix**: in both `SetGridCharging` functions, write `!mode` (not `mode`) for the `disallow_charge_from_grid_with_solar_installed` field. |
| 31 | `get_grid_charging() -> Optional[bool]` (`:1054`) | `GetGridCharging(ctx) *bool` (`powerwall.go:1101`) | parity | |
| 32 | `set_grid_export(mode) -> Optional[dict]` (`:1069`) | `SetGridExport(ctx, mode string) (models.Operation, error)` (`powerwall.go:1126`) | parity (signature); divergent (write reaches Tesla) — see §4 | |
| 33 | `get_grid_export() -> Optional[str]` (`:1086`) | `GetGridExport(ctx) *string` (`powerwall.go:1159`) | parity | |
| 34 | `go_off_grid(confirm=False) -> Optional[dict]` (`:1104-1146` at `a3b327be3`; upstream now `:1104-1149` at `4d092c7ed0` with an added "USE WITH EXTREME CARE" docstring) | `GoOffGrid(ctx, confirm bool) (models.Operation, error)` (`powerwall.go:1229`) | **divergent, superseding the first audit's "parity" finding — upstream moved** | **This finding changed between the two upstream commits audited in this document, and must not be read as "dead code both sides" any more.** At `a3b327be3` (first audit) no backend implemented `go_off_grid`, so it was dead code end-to-end on both projects — that finding was correct *at the time*. Commit `d2e159cdd9`/`dd54ecddd3` (PR #379, merged into `main` after `a3b327be3`, present at the `4d092c7ed0` commit this pass re-audited) added a real implementation: `pypowerwall/tedapi/__init__.py` gained `go_off_grid()`/`reconnect_grid()` (new code, ~20 lines added directly above the "Max Backup" section) that require `self.v1r and self.v1r_transport` and call `self.v1r_transport.send_island_mode(self.din, mode=6, force=True)` / `send_island_mode(self.din, mode=1)` respectively; `pypowerwall/tedapi/pypowerwall_tedapi.py` (new code) exposes these as thin delegations (`go_off_grid(self): return self.tedapi.go_off_grid()`); and `send_island_mode` itself lives in `pypowerwall/tedapi/tedapi_v1r.py:486-516`, sending Tesla's signed `TEGAPISetIslandModeRequest` (`setIslandModeRequest.mode`/`.force`, legacy TEG oneof fields 3/4) over the v1r transport and reading `setIslandModeResponse.result` back. **This is real, v1r-only, and genuinely dangerous** (physically opens/closes the grid contactor) — hence upstream's new "EXTREME CARE" warnings in `pypowerwall/__init__.py`. Go's `backend/tedapi/tedapi.go:923-926` remains an unconditional `return models.Operation{}, backend.ErrUnsupported` — confirmed unchanged, still dead code on the Go side only. **This is now a real, v1r-gated missing feature in Go**, not a shared no-op; MISSING.md's original claim that "the TEDAPI backend implements them" was wrong when written but is, by coincidence, now the direction upstream's own TEDAPI backend has moved (Go has not). |
| 35 | `reconnect_grid() -> Optional[dict]` (`:1148` at `4d092c7ed0`) | `ReconnectGrid(ctx) (models.Operation, error)` (`powerwall.go:1247`) | **divergent — same upstream-moved finding as item 34** | `backend/tedapi/tedapi.go:928-931` is likewise still an unconditional `ErrUnsupported`; upstream's v1r implementation is real (see item 34). |
| 36 | *(no upstream equivalent)* | `GetFileStoreConfig(ctx) (map[string]any, error)` (`powerwall.go:1261`) | n/a (deliberate) | Go addition, backs the `/tedapi/config` proxy route; not a parity gap since it exposes TEDAPI-only data with no Python analogue name to match. |
| 37 | `set_debug(toggle=True, color=True)` (module-level, `:118`) | *(no equivalent — Go uses `pkgs/logger` levels)* | n/a (deliberate) | Logging configuration is explicitly out of the parity contract per `.agent/rules/powerwall.md`. |

---

## 2. CLI

Upstream: `python -m pypowerwall <command>`, `pypowerwall/__main__.py` (argparse
subparsers, `main()` at `:414`, dispatch `if/elif command ==` chain from `:596`).
Go: `cmd/gopowerwall/cli.go` (kong grammar) + `commands/*.go`.

### 2.1 Subcommand inventory

| Upstream subcommand | Go subcommand | Status |
|---|---|---|
| `setup` (`:435`) | `setup` (`cli.go:21`, `commands/misc.go:21`) | **partial → effectively missing** (see 2.2) |
| `login` (`:452`) — **deprecated**, prints `"'login' command is deprecated. Use 'setup' instead."` and exits 1 (`:680-683`) | *(absent)* | missing, but low-impact: upstream's own `login` is a dead deprecation shim, not a functioning command. |
| `authtoken` (`:462`) | `authtoken` (`cli.go:18`, `commands/misc.go:42`) | **partial → effectively missing** (see 2.2) |
| `fleetapi` (`:467`) — **deprecated**, prints a warning then still runs `PyPowerwallFleetAPI(None, authpath=authpath).setup()` (`:719-732`) | *(absent as a distinct subcommand — `setup -fleetapi` is the closest Go equivalent, matching upstream's *recommended* replacement)* | n/a (deliberate) — gopowerwall correctly omits the deprecated alias and only needs the `setup -fleetapi` path, but that path is itself unimplemented (see 2.2). |
| `tedapi` (`:470`) | `tedapi` (`cli.go:20`, `commands/misc.go:70`) | **partial → effectively missing** (see 2.2) |
| `register` (`:494`) | `register` (`cli.go:17`, `commands/misc.go:86`) | **partial → effectively missing** (see 2.2) |
| `scan` (`:497`) | `scan` (`cli.go:25`, `commands/scan.go`) | parity | Flag-for-flag match: `network` positional, `-ip`, `-timeout` (default `1.0` both sides — `__main__.py:419` vs `commands/scan.go:18`), `-hosts` (default `30` both sides — `__main__.py:420` vs `commands/scan.go:19`), `-json`, `-nocolor`. |
| `set` (`:512`) | `set` (`cli.go:24`, `commands/set.go`) | partial | Flag surface matches (`-mode`, `-reserve`, `-current`, `-gridcharging`, `-gridexport`). **The 80%-cap behavior claim in the first audit was stale/wrong and is corrected here** (resolves Q3 of the second pass, see also §6 below): re-reading `commands/set.go` in this pass shows `applyReserve` (`commands/set.go:85-102`) **already** re-polls with `pw.GetReserveForced(ctx)` (`powerwall.go:1281`) after a capped write and prints "Powerwall Reserve actually set to %.1f" — matching upstream's `get_reserve(scale=True, force=True)` re-check (`__main__.py:808-809`) in substance, just gated slightly differently (Go re-reads whenever `pw.IsCloud() \|\| pw.IsFleetAPI()`, regardless of whether the requested value exceeded 80%; upstream only re-reads and prints when `reserve > 80`). Confirmed as a **remaining real gap**: `applyCurrent` (`commands/set.go:104-116`, the `-current` flag) has **neither** the pre-write warning **nor** the post-write re-read that `applyReserve` has, whereas upstream's `-current` branch (`__main__.py:812-826`) has both, symmetrically with `-reserve`. Also inherits the local-mode clobber bug from Library API item 22. Neither pypowerwall nor gopowerwall clamps, rejects, or otherwise pre-empts the 80% cap anywhere in the write path itself (confirmed: `pypowerwall_cloud.py:877-891`'s `set_grid_charging`/`:...`'s reserve write and `fleetapi.py:709-729`'s `set_battery_reserve` forward whatever value was requested verbatim to Tesla, with no local clamp) — the cap, where it exists at all, is enforced server-side by Tesla, and both CLIs only manage user expectations around it (warn beforehand, confirm after). Backends should not pre-empt the cap; this is intentional, not a gap. |
| `get` (`:526`) | `get` (`cli.go:19`, `commands/get.go`) | parity (format surface); needs verification on exact field parity — not deeply diffed field-by-field in this audit; `-format text/json/csv` matches. |
| `version` (`:535`) | `version` (`cli.go:16`, `commands/misc.go:11`) | parity | |
| `cloudcheck` (`:538`) | `cloudcheck` (`cli.go:22`, `commands/misc.go:56`) | **missing (stub)** (see 2.2) |
| *(none — proxy is a separate top-level script, `proxy/server.py`, not a `__main__.py` subcommand)* | `proxy` (`cli.go:23`, `commands/proxy.go`) | n/a (deliberate) | gopowerwall folding the proxy into the same CLI binary is a reasonable idiomatic packaging choice; it does not need a 1:1 upstream subcommand to match, since the proxy's *behavior* is what §3 checks. |

### 2.2 What each guidance-only Go subcommand needs to actually do

All five are confirmed, by reading `commands/misc.go` in full, to only `fmt.Fprintln` static
text and `return nil` — no file I/O, no network calls, no key generation. Below is what
upstream actually does, in enough detail to implement against.

**`setup`** (`__main__.py:596-671`, cloud path only shown; v1r/fleetapi paths delegate):
- `-v1r`: calls `pypowerwall.v1r_register.main(authpath=authpath)` — i.e. the *entire*
  `register` flow (below), just invoked from `setup` instead of top-level.
- `-fleetapi`: constructs `PyPowerwallFleetAPI(None, authpath=authpath)` and calls its
  `.setup()` (`pypowerwall/fleetapi/pypowerwall_fleetapi.py:734`), which interactively
  collects Tesla Developer app credentials and writes `.pypowerwall.fleetapi`
  (`CONFIGFILE`, JSON).
- Default (cloud): checks for an existing `.pypowerwall.auth`, offers to overwrite: if a
  new login is needed, calls `pypowerwall.tesla_auth.login(headless, region, debug)`
  (`tesla_auth.py:1129`) — **not** a device-code flow (see `authtoken` below for the exact
  mechanism) — then calls `PyPowerwallCloud(email, authpath=authpath).setup(email=email,
  token_data=token_data)` (`pypowerwall_cloud.py:1034`) which writes the auth file (JSON,
  keyed by email, with a nested `"sso"` object holding `access_token`/`refresh_token`/
  `expires_at`) via `os.open(..., 0o600)`.
- Size to implement in Go: large. Needs a full PKCE web-login flow (below), an interactive
  overwrite-confirmation prompt, and JSON auth-file writers matching the schema both
  `cloud.AuthFile` and `fleetapi.ConfigFile` readers in gopowerwall already expect (those
  readers already exist — `backend/cloud/cloud.go`, `backend/fleetapi/fleetapi.go` — only
  the *writer* side, i.e. this command, is missing).

**`authtoken`** (`__main__.py:686-717`, delegating to `pypowerwall/tesla_auth.py:1139`
`get_authtoken(region, debug)`): **Correction to MISSING.md — this is not a device-code
flow.** The actual mechanism is Tesla's PKCE authorization-code grant
(`tesla_auth.py:111-141`, `_build_auth_url`):
  - `client_id = "ownerapi"` (`tesla_auth.py:56`)
  - `GET https://auth.tesla.com/oauth2/v3/authorize` (region-hosted, `AUTH_URL_PATH`,
    `tesla_auth.py:57`) with `code_challenge` = base64url(SHA-256(`code_verifier`)),
    `code_challenge_method=S256`, `redirect_uri=tesla://auth/callback`,
    `scope="openid email offline_access"`, `response_type=code`.
  - The redirect is captured either via a native WebView with navigation interception
    (macOS/desktop, `_local_login_pywebview` / `_local_login_macos`,
    `tesla_auth.py:426-920`) or, for headless/SSH sessions, a manual copy-paste flow
    (`_remote_login`, `tesla_auth.py:358-397`) that tells the user to run `authtoken` on a
    machine *with* a display and paste the resulting Refresh Token and Access Token back.
  - Code exchange: `POST https://auth.tesla.com/oauth2/v3/token` with
    `grant_type=authorization_code`, `client_id`, `code`, `code_verifier`,
    `redirect_uri` (`tesla_auth.py:193-211`, `_exchange_code`).
  - **The `authtoken` CLI command itself only prints the resulting RT/AT to stdout — it
    does not write `.pypowerwall.auth`.** That file is only written by `setup` (via
    `save_token`/`PyPowerwallCloud.setup`). MISSING.md's description ("writes
    `.pypowerwall.auth`") describes `setup`'s behavior, not `authtoken`'s; correcting that
    below.
  - Size to implement in Go: large — needs an embeddable web view or a manual browser
    hand-off, PKCE crypto (already available via Go's stdlib `crypto/sha256` +
    `encoding/base64`), and the token POST. The manual/headless path (`_remote_login`) is
    the only piece plausibly implementable without a native webview dependency, and is
    the one most CLI/server users of gopowerwall would actually hit.

**`register`** (`__main__.py:760-762` → `pypowerwall/v1r_register.py:main()`): generates
and registers a v1r LAN RSA key.
  - `generate_rsa_key()` (`v1r_register.py:183-231`): RSA-4096 keypair
    (`rsa.generate_private_key(public_exponent=65537, key_size=4096)`, via the `cryptography`
    package — the same primitive gopowerwall already imports for `crypto/rsa`), saves the
    private key as PEM and the DER-encoded public key to `tedapi_rsa_public.der`; prints the
    SHA-256 fingerprint.
  - `step1_get_auth_code` / `step2_exchange_token` (`:234-317`): a **separate**, full
    Tesla-Fleet-API OAuth2 authorization-code exchange (`POST
    {TOKEN_BASE}/oauth2/v3/token`, `grant_type=authorization_code`, with
    `client_id`/`client_secret`/`redirect_uri` collected interactively or from
    `TESLA_CLIENT_ID`/`TESLA_CLIENT_SECRET`/`TESLA_REDIRECT_URI`/`TESLA_FLEET_API_BASE` env
    vars, `v1r_register.py:55-100`) — this is a *developer-app* OAuth flow, distinct from
    the `ownerapi` PKCE flow used by `authtoken`/`setup`.
  - `step3_get_site_id` (`:325`): `GET {fleet_api_base}/api/1/products` to find
    `energy_site_id` and `gateway_din`.
  - `step4_register_key` (`:462-500`): `POST
    {fleet_api_base}/api/1/energy_sites/{energy_site_id}/command` with body
    ```json
    {"command_properties": {"message": {"authorization": {"add_authorized_client_request":
      {"key_type": 1, "public_key": "<base64 DER pubkey>", "authorized_client_type": 1,
       "description": "Powerwall LAN Client"}}}, "identifier_type": 1},
     "command_type": "grpc_command"}
    ```
    then polls (`_poll_key_state`, `:421`) the site's authorized-client list to confirm the
    key transitioned to an approved state.
  - Size to implement in Go: large — a second, independent OAuth2 flow (client-credentials
    style, not PKCE), RSA-4096 keygen (trivial in Go via `crypto/rsa`), and a multi-step,
    poll-and-retry Fleet API command sequence.

**`cloudcheck`** (`__main__.py:149-413`, `_run_cloud_diagnostics`): the richest of the
five. Confirmed to actually perform live checks, not just print a status line:
  - Environment: Python/OpenSSL/platform version, TLS 1.3 `SSLContext` support, `httpx`/`h2`
    package presence (HTTP/2 stack — Tesla's owner-api requires HTTP/2), proxy env vars
    (`HTTP_PROXY` et al., with credential redaction).
  - Auth file: reads `.pypowerwall.auth`, decodes the embedded JWT access-token claims
    (`aud`, `exp`, presence of `x-enc`) to distinguish a fresh PKCE code-exchange AT (which
    owner-api accepts) from a refreshed AT (which owner-api 403s), without a live call.
  - Connectivity (skipped only with `-noconnect`): live `GET` to `https://auth.tesla.com/`
    and `https://owner-api.teslamotors.com/` over HTTP/2, checking negotiated protocol.
  - Token-refresh test: if a refresh token is present, actually exercises
    `teslapy.Tesla.refresh_token()` and then `battery_list()`/`solar_list()` to prove
    end-to-end auth works, printing site IDs/names found.
  - Go's `CloudCheckCmd.Run` (`commands/misc.go:56-67`) does none of this regardless of
    `-noconnect` (confirmed by MISSING.md and re-confirmed here: no `net/http` calls appear
    in `commands/misc.go`).
  - Size to implement in Go: medium — mostly straightforward HTTP calls and a JWT-claims
    decode (no crypto verification needed, just base64url + JSON), but reproducing the
    HTTP/2-specific check requires deliberately-enabled HTTP/2 in Go's `http.Transport`.

**`tedapi`** (`__main__.py:733-758` → `pypowerwall/tedapi/__main__.py:run_tedapi_test`,
**not fetched in this audit** — the file was not in the list of files requested and was not
independently pulled; mark this specific sub-behavior `unverified`). What is verifiable:
the parent CLI forwards `-gw_pwd`, `-host`, `-v1r`, `-password`, `-rsa_key_path`,
`-wifi_host`, `-tedapi_api_version`, `-firmware`, `-details`, `--debug` into that
subprocess-equivalent call. Go's `TedapiCmd` (`commands/misc.go:70-83`) accepts a subset
(`GwPwd`, `Host`, `Password`, `V1r`) but never calls `backend/tedapi` at all — confirmed by
reading `Run()`, which only does two `Fprintf`s. Size: medium, assuming the goal is "open a
`tedapi.NewClient`, call `Connect`, print `GetStatus`/`GetConfig`" — gopowerwall already has
all underlying pieces (`backend/tedapi/tedapi.go`); this command mainly needs wiring, not new
protocol work.

---

## 3. Proxy HTTP surface

Upstream: `proxy/server.py` (2739 lines, `Handler.do_GET`/`do_POST`,
`ALLOWLIST`/`DISABLED` at `:173-204`). Go: `proxy/routes.go`, `proxy/control.go`,
`proxy/constants.go`, `proxy/server.go`, `proxy/handlers.go`.

### 3.1 Route-by-route

| Route | Upstream (server.py:line) | Go (file:line) | Status |
|---|---|---|---|
| `GET /aggregates`, `/api/meters/aggregates` | `:1616` | `proxy/routes.go:333` (`generateAggregates`) | parity | Both apply site-zero-threshold suppression and negative-solar correction with the same semantics (shift negative solar into load, clamp solar to 0). |
| `GET /soe` | `:1663` | `proxy/routes.go:339` | parity | |
| `GET /api/system_status/soe` | `:1669` — `{"percentage": level}` from `pw.level(scale=True)` | `proxy/routes.go:353` — `{"percentage": %v}` from `s.PW.Level(ctx, true)` | parity | |
| `GET /api/system_status/grid_status` | `:1675` | `proxy/routes.go:367` | parity | |
| `GET /csv`, `/csv/v2` (`?headers`) | `:1680-1758` | `proxy/routes.go:585` (`handleCSVRoute`) | parity | Field order and format strings (`%0.2f,...,%d,%d\n`) match exactly for both v1 (Grid,Home,Solar,Battery,BatteryLevel) and v2 (+ GridStatus,Reserve). |
| `GET /vitals` | `:1758` | `proxy/routes.go:411` (`handleVitals`) | parity (route/caching shape); content depends on §1 item 8/§4 backend gaps | |
| `GET /strings` | `:1764-1768` — `cached_route_handler("/strings", lambda: safe_endpoint_call("/strings", pw.strings, jsonformat=True))` | `proxy/routes.go:434` (`handleStrings`) | **divergent, fully confirmed (was partially unverified)** | Confirmed the proxy calls `pw.strings(jsonformat=True)` with `verbose` at its default (`False`) — the non-verbose, letter-keyed shape from §1 item 9, not the verbose per-device view. Upstream's `strings()` (`__init__.py:495-549`) builds keys like `"A"`, `"B1"` from the *last character* of matched vitals field names (`e[-1]`, or `e[10]` — the char right after the literal `"PVS_String"` prefix — for the `Connected` case) plus a rotating per-PVAC-device index, with values `{Current, Power, Voltage, State, Connected}`; Go's `Strings()` (`powerwall.go:1042`) fabricates keys as `"<PVAC device name>_<A|B|C|D>"` with values `{Connected: true (hardcoded), Voltage, Current, Power}` (no `State`, and `Connected` is never actually read from any field) sourced from a field-naming scheme (`PVAC_Vsolar<label>`/`PVAC_Isolar<label>`/`PVAC_Psolar<label>`) that **no upstream backend produces under any firmware or transport** (see §4 and correction #7) — every `/strings` response in gopowerwall today reads all-zero voltage/current/power with `Connected` always `true`, regardless of real string state. |
| `GET /temps` | `:2047` | `proxy/routes.go:466` | parity | |
| `GET /temps/pw` | `:2050` — `PW{N}_temp` | `proxy/routes.go:473`/`603` (`generatePWTemps`) — `PW{N}_temp` | parity | Field naming matches exactly. |
| `GET /alerts` | `:2064` | `proxy/routes.go:479` | parity | |
| `GET /alerts/pw` | `:2067` — `{alert: 1}` | `proxy/routes.go:486`/`621` (`generatePWAlerts`) — `{alert: 1}` | parity | |
| `GET /freq` | `:2080` | `proxy/routes.go:188` (`generateFreq`) | parity | Field names match exactly: `PW{N}_name`, `PW{N}_PINV_Fout`, `PW{N}_PINV_VSplit1/2`, `PW{N}_PackagePartNumber/SerialNumber`, `PW{N}_p_out/q_out/v_out/f_out/i_out`, plus `ISLAND*`/`METER*` passthrough from `TESYNC`/`TEMSA` vitals devices, and `grid_status` (numeric). |
| `GET /pod` | `:2126-2244` (`generate_pod`) | `proxy/routes.go:130` (`generatePOD`, called via `handleMetricsJSONRoutes` at `proxy/routes.go:262`) | **divergent — confirmed, Go still missing the vitals-augmentation pass (function relocated by the concurrent refactor, bug unchanged)** | Full upstream body re-read in this pass. Two loops: (1) `d["battery_blocks"]` from `system_status` seeds `PW{N}_name`/`POD_*` fields to `None` placeholders, then fills `PW{N}_POD_nom_energy_remaining`/`nom_full_pack_energy`/`PackagePartNumber`/`PackageSerialNumber`/`pinv_state`/`pinv_grid_state`/`p_out`/`q_out`/`v_out`/`f_out`/`i_out`/`energy_charged`/`energy_discharged`/`off_grid`/`vf_mode`/`wobble_detected`/`charge_power_clamped`/`backup_ready`/`OpSeqState`/`version` per block (`idx` 1-based, incrementing per block) — Go's `generatePOD` (`proxy/routes.go:130-165`) matches this loop field-for-field via `s.PW.PODView(ctx).Blocks`. (2) **A second, independent loop over `pw.vitals()`** (`__main__.py`... no — `server.py:2196-2244`) that iterates every device, and for each whose name **starts with** `"TEPOD"`, **re-derives its own 1-based index from the vitals iteration order** (not from DIN-matching against the block list — the code comment even says "Expansion packs are now included in vitals() as TEPOD entries, so they're automatically picked up by the loop above", trusting that TEPOD vitals devices enumerate in the same order as `battery_blocks`) and overwrites `PW{idx}_name` (to the raw device name string, e.g. `"TEPOD--<din>"`), `PW{idx}_POD_ActiveHeating`, `ChargeComplete`, `ChargeRequest`, `DischargeComplete`, `PermanentlyFaulted`, `PersistentlyFaulted`, `enable_line` (all `int(...or 0)` — 0/1, not bool), `available_charge_power`, `available_dischg_power`, `nom_energy_remaining`, **`nom_energy_to_be_charged`**, `nom_full_pack_energy`. The vitals source for these is the `TEPOD--{din}` device pypowerwall's TEDAPI backend synthesizes at `pypowerwall/tedapi/__init__.py:1018-1022`: `{"alerts": ..., "POD_nom_energy_remaining": ..., "POD_nom_energy_to_be_charged": nom_full_pack_energy - nom_energy_remaining, "POD_nom_full_pack_energy": ...}` — note upstream's TEDAPI synthesis itself never sets `POD_ActiveHeating`/`ChargeComplete`/`ChargeRequest`/`DischargeComplete`/`PermanentlyFaulted`/`PersistentlyFaulted`/`enable_line`/`available_charge_power`/`available_dischg_power` on this device, so in TEDAPI mode those particular keys are overwritten with `int(None or 0) == 0`/`None` even on upstream — only local-gateway-mode vitals (native `TEPOD` devices) would populate them for real. Confirmed: Go's `PODView` (`powerwall.go:1879-1897`) calls only `p.SystemStatus`, `p.GetTimeRemaining`, `p.GetReserve` — **zero calls to `p.Vitals`, confirmed by reading the function in full** — so this second pass is entirely absent from Go, including the `nom_energy_to_be_charged` key, which Go never emits under any connection mode. The aggregate tail fields (`nominal_full_pack_energy`, `nominal_energy_remaining` from `system_status`, `time_remaining_hours` from `pw.get_time_remaining()`, `backup_reserve_percent` from `pw.get_reserve()`) **are already correctly implemented** in Go (`proxy/routes.go:166-169`) — the first audit's framing that Go's `/pod` was missing wholesale was too broad; only the vitals-augmentation pass is missing. |
| `GET /json` | `:2245` | `proxy/routes.go:275` (`generateJSON`) | parity | All 11 fields (`grid,home,solar,battery,soe,grid_status,reserve,time_remaining_hours,full_pack_energy,energy_remaining,strings`) match name-for-name. |
| `GET /version` | `:2293` | `proxy/routes.go:632` | parity | `{"version": "SolarOnly", "vint": 0}` fallback matches exactly. |
| `GET /help` | `:2305` | `proxy/routes.go:539`/`handlers.go:353` | divergent (cosmetic) | Both are an HTML status page; upstream's is a much richer live-refreshing stats table (reusing `proxystats`, including the "Maintenance Mode" notice pointing at `pypowerwall-server`); Go's is 4 lines of static HTML. Not a JSON/field-name parity concern, but a real functional gap for anyone using `/help` as a dashboard. |
| `GET /api/troubleshooting/problems` | `:2379` — `{"problems": []}` | `proxy/routes.go:544` — `{"problems": []}` | parity | |
| `GET /tedapi/config` \| `/status` \| `/components` \| `/battery` \| `/controller` | `:2383-2397` | `proxy/routes.go:649` (`handleTedapiRoute`) | **partial** | Upstream implements all five sub-routes (`get_config`, `get_status`, `get_components`, `get_battery_blocks`, `get_device_controller`). Go's `handleTedapiRoute` only implements `/tedapi/config`; `/tedapi/status`, `/tedapi/components`, `/tedapi/battery`, `/tedapi/controller` fall through to the generic "Use /tedapi/config, /tedapi/status, ..." error message instead of returning data, even though `backend/tedapi` has `GetStatus` (`backend/tedapi/tedapi.go:322`) available to wire up. |
| `GET /cloud/battery` \| `/power` \| `/config` | `:2399-2410` | *(absent)* | missing | No equivalent route group exists anywhere in `proxy/routes.go` or `proxy/control.go`. |
| `GET /fleetapi/info` \| `/status` | `:2412-2421` | *(absent)* | missing | Same — no equivalent. |
| `GET /fans` (raw fan speeds) | `server.py:2483-2487` — `json.dumps(safe_pw_call(pw.tedapi.get_fan_speeds) if pw.tedapi else {})` | *(absent)* | **missing, implementation-ready (details below)** | Confirms MISSING.md: neither `/fans` nor `/fans/pw` exists in `proxy/routes.go`/`proxy/control.go` (grep-confirmed again in this pass). Emits the **raw** dict `get_fan_speeds()` returns, keyed by synthesized device name (see below), unfiltered — `{}` if `pw.tedapi` is falsy (i.e. not a TEDAPI-family backend). |
| `GET /fans/pw` | `server.py:2488-2500` | *(absent)* | **missing, implementation-ready (details below)** | `fans = {}; for i, (_, value) in enumerate(sorted(fan_speeds.items())): key = f"FAN{i+1}"; fans[f"{key}_actual"] = value.get("PVAC_Fan_Speed_Actual_RPM"); fans[f"{key}_target"] = value.get("PVAC_Fan_Speed_Target_RPM")` — keys `FAN1_actual`, `FAN1_target`, `FAN2_actual`, ... **1-based**, ordered by sorting the fan-speed dict's own keys (device names), not by any inherent index. `{}` if `pw.tedapi` is falsy. **`get_fan_speeds()` itself** (`pypowerwall/tedapi/__init__.py:1879-1906`, `extract_fan_speeds`/`get_fan_speeds`): calls `self.get_device_controller(force=force)` (the `DEVICE_CONTROLLER_FULL` GraphQL query), then scans `data["components"]["msa"]` — **not** `data["esCan"]["bus"]["PVAC"]["PVAC_Logging"]`, where the fan fields are also visible in raw wire data — for each component's `signals` list, keeping only `{"PVAC_Fan_Speed_Actual_RPM", "PVAC_Fan_Speed_Target_RPM"}` named signals with a non-`None` value, and keys the result `f"PVAC--{componentPartNumber}--{componentSerialNumber}"`. **This is a genuine ambiguity this pass cannot resolve from source alone**: the `DeviceControllerQuery` text in `pypowerwall/tedapi/queries/V2026_06.json` defines the `msa` GraphQL alias as `components(filter:{types:[TEMSA]})` requesting only `MSA_*`/`METER_Z_*` signal names — it never requests `PVAC_Fan_Speed_*` under that alias, only under the separate `esCan.bus.PVAC.PVAC_Logging` block (same query file, confirmed present: `PVAC_Logging{... PVAC_Fan_Speed_Actual_RPM PVAC_Fan_Speed_Target_RPM}`, V2026_06 only — absent from V2024_06's `PVAC_Logging`). Read statically, `extract_fan_speeds`'s `components["msa"]` scan therefore appears to always find zero fan signals against this specific query — whether it works in practice depends on either a live gateway's actual JSON response shape (which may differ from the static query text in ways this audit cannot see) or on a code path this pass did not locate. Flagging as unverified-from-source rather than asserting the function works or is dead: **confirming this needs a real V2026_06-firmware gateway**, not more static reading. What is fully confirmed regardless: the two field names (`PVAC_Fan_Speed_Actual_RPM`, `PVAC_Fan_Speed_Target_RPM`), and that `backend/tedapi`'s existing GraphQL query text (`backend/tedapi/queries/V2026_06.json`) **already requests both fields** inside its own `PVAC_Logging` block — so the wire data reaches gopowerwall today but is discarded: `grep -rn "PVAC_Pv\|PvVoltage\|PvCurrent\|PVMeasuredVoltage\|PVMeasuredPower\|PVAC_PVCurrent\|PvState\|PVS_String\|Fan_Speed" backend/tedapi/*.go` (`.go` files only, excluding the query JSON) returns **zero matches** — no Go code parses `PVAC_Logging`'s response into named fields for `/strings` or `/fans` at all. |
| `POST /control/reserve` | `:2429`/POST body at `:1372-1460` | `proxy/control.go:112`/`128` | parity | Including the "companion `mode` parameter" optimization to avoid double-writing (`errInvalidMode` path, both sides). |
| `POST /control/mode` | `:2437`/`1487-1524` | `proxy/control.go:174` | parity | Including the companion `level` parameter. |
| `POST /control/grid_charging` | `:2445`/`1525-1544` | `proxy/control.go:220` | divergent (minor) | Same success shape (`{"grid_charging": "Set Successfully"}`), but the **no-value (read)** branch differs subtly: Python's `'{"grid_charging": %s}' % ("true" if ... else "false")` is a raw string substitution that always yields valid JSON `true`/`false`; Go's `json.NewEncoder(w).Encode(map[string]any{keyGridCharging: res})` where `res` is `*bool` correctly encodes `null` when unknown — Go is actually more correct here (upstream's `safe_pw_call(...) ` returning `None` would still coerce to the string `"false"`, silently misreporting "off" when the true state is unknown). |
| `POST /control/grid_export` | `:2453`/`1545-1567` | `proxy/control.go:247` | **divergent — upstream can emit invalid JSON** | Upstream's no-value branch: `'{"grid_export": %s}' % (str(safe_pw_call(pw_control.get_grid_export)).lower() or "false")`. If `get_grid_export()` returns `None`, `str(None).lower()` is the string `"none"` (not `"null"`), which is **not valid JSON** — `{"grid_export": none}` would fail to parse as valid JSON in a strict parser. Go's equivalent (`proxy/control.go:249`, `json.NewEncoder(w).Encode(map[string]any{keyGridExport: ge})` with `ge *string`) always emits well-formed `null`. This is a case where gopowerwall is *more correct* than upstream, not less — worth keeping, not "fixing to match". |
| `POST /control/max_backup` | `:2462`/`1592-1626` | `proxy/control.go:273` | parity | Same three sub-behaviors (get current events / cancel / schedule-N-seconds), same v1r-required gate, same response shapes. |
| `POST /control/off_grid`, `/control/reconnect_grid` | *(does not exist upstream — confirmed by exhaustive grep of `request_path ==`/`.startswith` conditions in `proxy/server.py` at both audited commits, unchanged between them; only `reserve`, `mode`, `grid_charging`, `grid_export`, `max_backup` actions exist)* | *(absent — `proxy/control.go:108-126`'s `dispatchControl` switch has no `off_grid`/`reconnect_grid` case)* | **n/a (deliberate) at the proxy-route level, but the underlying justification changed — see §1 items 34/35** | MISSING.md flagged this as "unconfirmed whether pypowerwall's proxy exposes equivalent control routes at all." Confirmed still true as of `4d092c7ed0`: `proxy/server.py` was not among the 5 commits' changed files, so upstream's proxy still exposes no `off_grid`/`reconnect_grid` control route, and this row's "n/a (deliberate)" verdict for the *proxy* stands. **However**, the original reasoning — "there is genuinely nothing to expose because `go_off_grid`/`reconnect_grid` are dead code on the library side too" — is now **only half true**: §1 items 34/35 confirm upstream's *library* (`pypowerwall.Powerwall.go_off_grid`/`reconnect_grid`) is real and functional for v1r as of this pass's re-audit, even though the *proxy* still doesn't route to it. So there is now something upstream *could* expose via `/control/*` that it chooses not to (possibly deliberately, given the "EXTREME CARE" warnings on the physical-contactor operation) — this remains a correct "no gap" finding for gopowerwall's proxy, but for a different reason than originally stated. |
| `GET /pw/*` (library-function passthrough) | `:2500-2549` — `simple_mappings` dict, 23 keys | `proxy/handlers.go:51-124` (`lookupPWFacingSensor`/`System`/`Control`) | parity | All 23 upstream keys (`level, power, site, solar, battery, battery_blocks, load, grid, home, vitals, temps, strings, din, uptime, version, status, system_status, grid_status, aggregates, site_name, alerts, is_connected, get_reserve, get_mode, get_time_remaining`) have a matching Go case; response-shape wrapping (e.g. `{"level": ...}` vs bare) matches key-for-key. |
| `GET /stats` | `:1770-1884` | `proxy/handlers.go:247` (`handleStats`) | **divergent** | Both share a common core (`pypowerwall`/`mode`-ish, `gets`, `posts`, `errors`, `timeout`, `uri`, `ts`, `start`, `clear`, `uptime`, `mem`, `site_name`, `cloudmode`, `fleetapi`). Upstream additionally tracks, and Go entirely lacks: `tedapi_mode`, `tedapi_api_version`, `tedapi_auth_mode`, `pw3`, `siteid`/`counter` (cloud/fleetapi only), a `fallback_mode` block (SolarOnly-fallback tracking: `is_fallback_mode`, `fallback_since`, `recovery_attempts`, ...), and a `mem_cache` block with per-cache byte-size accounting (`error_counts`, `network_error_summary`, `degradation_cache`, `performance_cache`, `endpoint_stats`, `total_cache_bytes`, `total_cache_mb`). Go's `config` sub-object also carries a materially different key set (`PW_BIND_ADDRESS, PW_HOST, PW_EMAIL, PW_TIMEZONE, PW_PORT, PW_STYLE, PW_CACHE_EXPIRE, PW_CACHE_TTL, PW_NEG_SOLAR, PW_SITE_ZERO_THRESHOLD` — 10 keys) vs upstream's larger set (`PW_BIND_ADDRESS, PW_PASSWORD*, PW_EMAIL, PW_HOST, PW_TIMEZONE, PW_DEBUG, PW_CACHE_EXPIRE, PW_BROWSER_CACHE, PW_TIMEOUT, PW_POOL_MAXSIZE, PW_HTTPS, PW_PORT, PW_STYLE, PW_SITEID, PW_AUTH_PATH, PW_AUTH_MODE, PW_CACHE_FILE, PW_CONTROL_SECRET*, PW_GW_PWD*, PW_RSA_KEY_PATH, ...` — truncated in this audit at line 330 of `server.py`, more keys likely follow; upstream masks secrets with `"*" * len(...)`, Go's config dump does not appear to mask any secret-shaped values). Not diffed further than this; a full byte-for-byte diff of `/stats` was out of scope for the time available. |
| `GET /stats/clear` | `:1885` | `proxy/routes.go:504` | parity (behavior); same caveat as `/stats` for full field parity | |
| `GET /health` | `server.py:1893-2000` (`health_info` dict, quoted below) | `proxy/handlers.go:311-350` (`handleHealth`) | **partial, now fully diffed (resolves the earlier "unverified (partial)")** | Field-by-field: Go matches upstream on `pypowerwall`, `mode`, `pypowerwall_cache_expire`, `degradation_cache_ttl_seconds`, `graceful_degradation`, `fail_fast_mode`, `health_check_enabled`, `startup_time`, `current_time`, `proxy_stats{total_gets,total_posts,total_errors,total_timeouts}`, the conditional `connection_health` block, the conditional `cached_data{cache_size,endpoints}` block, and `endpoint_statistics`. **Confirmed missing from Go, present upstream**: top-level `tedapi_mode` (`server.py:1898`); top-level `transports` from `get_transport_health()` (`:1911`, v1r/hybrid-transport status — not investigated further in this pass); and the entire `fallback_mode` block (`:1935-1950`: `is_fallback_mode`, `fallback_since`, `fallback_duration_seconds`, `recovery_attempts`, `last_recovery_attempt`, `next_attempt_at`, `recovery_enabled`, `recovery_thread_alive` — this is upstream's "SolarOnly fallback" tracking, distinct from the `connection_health` degradation concept both sides share). These three gaps mirror the `/stats` row's similar `fallback_mode` gap below — likely the same underlying missing feature (upstream's SolarOnly automatic-fallback machinery) surfacing in two different endpoints. |
| `GET /health/reset` | `:2002` | `proxy/routes.go:520` | parity (behavior, not diffed field-by-field) | |
| ALLOWLIST passthrough routes (`GET /api/status`, `/api/site_info`, etc.) | `ALLOWLIST` list, `server.py:173-199`, 26 entries | `isAllowlisted`, `proxy/server.go:25-66`, 35 entries | **divergent — fully enumerated in this pass (resolves Q8)** | Exact symmetric difference between upstream's `ALLOWLIST` (26 entries) and Go's `isAllowlisted` (35 entries), read directly off both switch/list bodies: **present upstream, absent from Go (2 entries)** — `/api/system/networks` (Go has `/api/system/networks/conn_tests` instead, a *different* path: a client requesting upstream's exact allowlisted path falls through to Go's static-file handler and 404s instead of being proxied); `/api/synchrometer/ct_voltage_references` (no Go entry at all, at any path — also 404s as a static asset in Go instead of being proxied). **Present in Go, absent upstream (11 entries)** — `/api/system/networks/conn_tests` (the mismatched path above), `/api/diagnostics`, `/api/generators`, `/api/generators/actions`, `/api/syncon/vitals`, `/api/syncon/actions`, `/api/inverters`, `/api/inverters/status`, `/api/meters/status`, `/api/powerwalls/status`, `/api/system_status/soe` (redundant with the dedicated handler at `proxy/routes.go:223`, harmless but still not in upstream's list). Total symmetric difference: **13 entries** (2 missing + 11 extra; the earlier count of "twelve" undercounted by one — recounted directly off both lists in this pass rather than estimated). The two missing entries are the actionable half of this finding: adding `/api/system/networks` (fixing the `conn_tests` typo/divergence) and `/api/synchrometer/ct_voltage_references` to `isAllowlisted` is a direct, low-risk parity fix. The 11 Go-only entries are not necessarily wrong (a newer/different gateway firmware may expose them) but are outside the parity contract's "match pypowerwall" definition as of either audited commit, and keeping vs. removing them is a deliberate product decision, not a parity bug to silently fix. |
| DISABLED list (`/api/customer/registration`) | `server.py:201-203`, 1 entry | `isDisabled`, `proxy/server.go:69-76`, 1 entry | parity | Both disable exactly `/api/customer/registration`, and both check DISABLED before ALLOWLIST, so the entry's presence in both `ALLOWLIST` *and* `DISABLED` on both sides is dead/unreachable code identically on both sides (upstream: `elif ... in DISABLED` is checked before `elif ... in ALLOWLIST`; Go: `case isDisabled(reqPath): ... case isAllowlisted(reqPath):`, `proxy/routes.go:574-579`). |

### 3.2 Stub payloads (`backend/stubs/stubs.go`) vs upstream's cloud/fleetapi backfill

**Fully resolved in this pass** — `pypowerwall/cloud/mock_data.py`, `pypowerwall/cloud/stubs.py`,
`pypowerwall/fleetapi/mock_data.py`, `pypowerwall/fleetapi/stubs.py`, and (for
completeness) `pypowerwall/tedapi/mock_data.py`/`pypowerwall/tedapi/stubs.py` were all
fetched and read in full. **Answer, superseding the first audit's "largely yes for the
shape": no — the field-for-field match is poor, in ways that go well beyond the single
correction the first audit anticipated.** `pypowerwall/cloud/mock_data.py` and
`pypowerwall/fleetapi/mock_data.py` are byte-identical (diffed); `pypowerwall/cloud/stubs.py`
and `pypowerwall/fleetapi/stubs.py` are likewise byte-identical. `pypowerwall/tedapi/mock_data.py`
is also byte-identical to the cloud/fleetapi copy; `pypowerwall/tedapi/stubs.py` differs
from the cloud/fleetapi version in exactly one respect: `instant_total_current` is `None`
in all four meter sections of the TEDAPI copy, vs `0`/`None` (see below) in the
cloud/fleetapi copy — three separate stub literals upstream, one shared
`backend/stubs/stubs.go` in Go used identically by `backend/cloud`, `backend/fleetapi`,
and `backend/tedapi` (confirmed: all three call `stubs.MetersAggregatesStub()`/
`stubs.SystemStatusStub()`).

**`MetersAggregatesStub()` vs `_API_METERS_AGGREGATES_TEMPLATE`** (`pypowerwall/cloud/stubs.py:3-79`,
`backend/stubs/stubs.go:30-112`): key sets match exactly across all four sections
(`site`/`battery`/`load`/`solar`), including `load`'s and `solar`'s/`battery`'s correctly
varying absence/presence of `num_meters_aggregated`, and the `timeout` values
(1500000000 for site/battery/load, 1000000000 for solar) — this part is genuine parity.
**One confirmed, systematic value mismatch**: Python's template sets `instant_power: None`
in all four sections; Go's `MetersAggregatesStub()` sets `keyInstantPower: 0.0` in all
four (`backend/stubs/stubs.go:34,54,74,93`) — `null` vs `0` is a real wire-format
difference for a strict JSON consumer, though in practice `/api/meters/aggregates` and
`/aggregates` overlay real computed power onto this template (per `generateAggregates`),
so the default value only surfaces when that overlay itself fails.

**`SystemStatusStub()` vs `_API_SYSTEM_STATUS_TEMPLATE`** (`pypowerwall/cloud/stubs.py:88-121`,
`backend/stubs/stubs.go:115-138`): **substantially divergent**, not a near-match.
Confirmed **19 keys present in Python's template and absent from Go's**:
`instantaneous_max_apparent_power`, `hardware_capability_charge_power`,
`hardware_capability_discharge_power`, `available_charger_blocks`,
`ffr_power_availability_high`, `ffr_power_availability_low`, `load_charge_constraint`,
`max_sustained_ramp_rate`, `can_reboot`, `smart_inv_delta_p`, `smart_inv_delta_q`,
`last_toggle_timestamp`, `score`, `blocks_controlled`, `primary`, `auxiliary_load`,
`all_enable_lines_high`, `inverter_nominal_usable_power`, `expected_energy_remaining`.
Confirmed **3 keys present in both, but with a different default value**:
`grid_services_power` (Python `None`; Go `0.0`), `system_island_state` (Python `None`;
Go hardcodes `"SystemGridConnected"`), `available_blocks` (Python `None`; Go hardcodes
`1`). Confirmed **3 Go-only keys with no Python counterpart at all**: `ff_nps`,
`generator_inpower`, `generator_energy_supplied` (`backend/stubs/stubs.go:133-136`).

**Far more consequential than the template's own field set**: the *overlay* the first
audit described as "structurally the same on both sides" is, on closer reading in this
pass, **not present at all in Go's cloud/fleetapi backends**. Upstream's
`get_api_system_status` (`pypowerwall_cloud.py:835-874`, identically at
`pypowerwall_fleetapi.py:597-629`) reads `get_site_power`/`get_site_config`/`get_battery`
(cloud) or `get_live_status`/`get_site_info` (fleetapi), and when all are available,
computes `nominal_full_pack_energy` (from `total_pack_energy`), `nominal_energy_remaining`
(from `energy_left`), `max_charge_power`/`max_discharge_power`/`max_apparent_power` (from
`nameplate_power`), `grid_services_power`, `system_island_state` (derived from
`island_status`/`grid_status`), `available_blocks`/`blocks_controlled` (from
`battery_count`), and `solar_real_power_limit` (from `solar_power`) — nine real,
site-specific values `.update()`-ed onto the stub template — and returns `None` entirely
if any of the three source calls fails. Go's `getAPISystemStatus` in both
`backend/cloud/cloud.go:684-689` and `backend/fleetapi/fleetapi.go:587-592` is, in full:
```go
func (c *PyPowerwallCloud) getAPISystemStatus(_ context.Context, _ bool) (any, error) {
	stub := stubs.SystemStatusStub()
	stub["battery_blocks"] = []any{}
	return stub, nil
}
```
No live data is read or overlaid at all — every one of those nine fields is served as the
template's placeholder (`None`/`nil` for most, or the three hardcoded-wrong defaults
above) regardless of the real site's state. `backend/tedapi/tedapi.go:717-737` does a
partial overlay (a single `battery_blocks` entry with `nominal_energy_remaining`/
`nominal_full_pack_energy` when `p.v1r` is absent and `GetConfig` has a `vin`), which is
closer to upstream's intent but still nowhere near the nine-field overlay cloud/fleetapi
should have. **This is a confirmed, real functional gap, not a values-only nit**: `/api/system_status`
in gopowerwall's cloud and fleetapi modes returns almost entirely placeholder data today.
Whether this reflects the pre-refactor or in-flight state of the concurrent architectural
refactor mentioned in this task's instructions was not determined — it is reported as
observed in the current working tree, file:line cited for re-verification once that
refactor lands.

**The small canned-JSON constants** (`MockPowerwalls`, `MockMetersSite`, `MockMeters`,
`MockSitemaster`, `MockCustomer`, `MockInstaller`, `MockNetworks`, `MockAuthToggle`,
`MockUpdate`, `MockSolars` in `backend/stubs/stubs.go:142-165`), compared against their
upstream sources (`pypowerwall/cloud/mock_data.py`'s `POWERWALLS`/`METERS_SITE`/`METERS`/
`INSTALLER`/`NETWORKS`/`SOLARS_BRANDS` constants, and the inline dict literals in
`pypowerwall_cloud.py:961-1032`'s `get_api_*` methods for the rest):

| Go constant | Upstream source | Verdict |
|---|---|---|
| `MockPowerwalls` | `POWERWALLS` (`mock_data.py`) via `get_api_powerwalls` (`pypowerwall_cloud.py:973`) | **divergent** — Go keeps only `enumerating`/`updating`/`checking_if_offgrid`/`running_phase_detection`/`powerwalls`/`gateway_din`, and the one `powerwalls[0]` entry only `PackagePartNumber`/`PackageSerialNumber`/`type`/`grid_state`. Upstream's real payload additionally has `phase_detection_last_error`, `bubble_shedding`, `on_grid_check_error`, `grid_qualifying`, `grid_code_validating`, `phase_detection_not_available`, a *second* `powerwalls[1]` entry (an `"ACPW"` device), nested `commissioning_diagnostic`/`update_diagnostic` objects on each powerwall, and top-level `sync`/`msa`/`states` keys. Go's version is a much-simplified subset, not a full match. |
| `MockMetersSite` | `METERS_SITE` (`mock_data.py`) via `get_api_meters_site` (`pypowerwall_tedapi_pypowerwall_tedapi.py:704`) | **divergent** — Go's `connection` object drops `https_conf`; Go's entry drops top-level `cts`/`inverted` arrays; Go's `Cached_readings` keeps only `instant_power`/`frequency`/`instant_average_voltage`/`instant_average_current` where upstream's has ~20 fields (`last_communication_time`, `instant_reactive_power`, `instant_apparent_power`, `energy_exported`, `energy_imported`, `i_a_current`/`i_b_current`/`i_c_current`, three `last_phase_*_communication_time` fields, `v_l1n`/`v_l2n`, `real_power_a`/`real_power_b`, `reactive_power_a`/`reactive_power_b`, `serial_number`, `version`, `timeout`, `instant_total_current`). |
| `MockMeters` | `METERS` (`mock_data.py`) | **divergent — different schema, not just fewer fields**. Go's three entries use `{"id", "location", "type"}` keys (`0`/`"site"`, `1`/`"load"`, `2`/`"solar"`, all `type: "synchrometerX"`). Upstream's three entries use an entirely different key set — `{"serial", "short_id", "type", "connected", "cts", "ip_address", "mac"}` for the first (`type: "neurio_w2_tcp"`), `{"serial", "short_id", "type"}` for the second (`type: "synchrometerY"`), `{"serial", "short_id", "type", "cts"}` for the third (`type: "synchrometerX"`) — **no `id`/`location`/`connected` keys exist anywhere in upstream's real payload**, and no `serial`/`short_id` keys exist in Go's. This is a genuinely different shape, not an approximation of the same one. |
| `MockSitemaster` | Inline dict, `get_api_sitemaster` (`pypowerwall_cloud.py:967-970`) | **divergent (partial subset)** — Go has `{"status", "running", "connected_to_tesla"}`; upstream additionally has `"power_supply_mode": False` and `"can_reboot": "Yes"`. |
| `MockCustomer` | Inline dict, `get_api_customer` (`pypowerwall_cloud.py:1006-1009`, distinct from `get_api_customer_registration`) | **parity** — both are exactly `{"registered": true}`. |
| `MockInstaller` | `INSTALLER` (`mock_data.py`) | **divergent — different schema**. Go: `{"ready_for_customer": true}` — a key that **does not exist anywhere in upstream's real payload**. Upstream: `{"company", "customer_id", "phone", "email", "location", "mounting", "wiring", "backup_configuration", "solar_installation", "solar_installation_type", "run_sitemaster", "verified_config", "installation_types"}` — 13 keys, none of which is `ready_for_customer`. |
| `MockNetworks` | `NETWORKS` (`mock_data.py`) | **divergent** — Go is a 1-entry array with `{"network_name", "enabled"}`; upstream is a 2-entry array (an `ethernet_tesla_internal_default` and a `gsm_tesla_internal_default` interface), each with `interface`, `dhcp`, `extra_ips`/`active`/`primary`/`lastTeslaConnected`/`lastInternetConnected`, and a nested `iface_network_info` object. Go's fields are a strict subset of the field *names* used, but the array is missing an entire second element. |
| `MockAuthToggle` | Inline dict, `get_api_auth_toggle_supported` (`pypowerwall_cloud.py:962-965`) | **divergent — wrong key name and wrong default value.** Go: `{"toggle_supported": false}`. Upstream: `{"toggle_auth_supported": true}`. The key name itself differs (`toggle_supported` vs `toggle_auth_supported`) — a client checking for upstream's exact key would find it absent in Go — and the boolean default is inverted (`false` vs `true`). |
| `MockUpdate` | Inline dict, `get_api_system_update_status` (`pypowerwall_cloud.py:984-987`) | **divergent — entirely different schema.** Go: `{"status": "idle", "percentage": 0.0}`. Upstream: `{"state": "/update_succeeded", "info": {"status": ["nonactionable"]}, "current_time": ..., "last_status_time": ..., "version": "23.28.2 27626f98", "offline_updating": false, "offline_update_error": "", "estimated_bytes_per_second": null}` — note upstream's top-level key is `state`, not `status` (Go's `status` key doesn't exist upstream at any level), and `percentage` doesn't exist upstream at all. |
| `MockSolars` | Inline dict, `get_api_solars` (`pypowerwall_cloud.py:996-999`) | **divergent — wrong container type plus missing field.** Go: a bare object `{"brand": "Tesla", "model": "Solar Inverter"}`. Upstream: **a one-element array**, `[{"brand": "Tesla", "model": "Solar Inverter 7.6", "power_rating_watts": 7600}]` — Go returns an object where upstream returns an array, drops `power_rating_watts` entirely, and truncates the model string. |

Net finding for §3.2: MISSING.md's framing that these shapes are "deliberate but unchecked
against pypowerwall's own stub values" undersold the gap. Several are not close
approximations with missing detail (acceptable for a stub) but **different schemas or
wrong key names** (`MockAuthToggle`, `MockMeters`, `MockInstaller`, `MockUpdate`,
`MockSolars`) that would break a client relying on the documented upstream field name. The
`SystemStatusStub` overlay gap (cloud/fleetapi never compute the nine live-derived fields
at all) is the most consequential of these findings for real usage, since `/api/system_status`
is on the parity surface and is read through `/pw/system_status` and `/pod`'s
`nominal_full_pack_energy`/`nominal_energy_remaining` fields.

---

## 4. Backend capability by connection mode

| Operation | `local` | `tedapi`/`v1r` | `cloud` | `fleetapi` |
|---|---|---|---|---|
| Read power/vitals/status | Both: real, live gateway data. | Both: real, live TEDAPI GraphQL/protobuf data. | Both: real Tesla Owner API data (`SITE_DATA`/`live_status`). | Both: real Tesla Fleet API data (`live_status`). |
| `SetReserve`/`SetMode` (`/api/operation`) | **Go has a confirmed correctness bug here — see §1 item 22.** Upstream is real and back-filled/safe; gopowerwall is real but unsafe (can clobber the untouched field). | Both: real, POSTs to the gateway/GraphQL mutation. | Both accept the call; **only upstream's reaches Tesla.** Upstream's `post_api_operation` (`pypowerwall_cloud.py:1196-1247`) calls `self.tesla.battery_list()` then, per battery, `battery.set_backup_reserve_percent(...)` / `battery.set_operation(...)` — teslapy methods that resolve (via `pypowerwall/cloud/teslapy/endpoints.json`) to real HTTP calls: `POST api/1/energy_sites/{site_id}/backup` body `{"backup_reserve_percent": <int>}`, and `POST api/1/energy_sites/{site_id}/operation` body `{"default_real_mode": <mode>}`, both with `Authorization: Bearer <access_token>` (teslapy `__init__.py:255`). Go's `postAPIOperation` (`backend/cloud/cloud.go:385-390`) only logs the payload, calls `c.cache.Invalidate(...)`, and returns `{"status": "success"}` — **confirmed zero `http.MethodPost`/`MethodPut` calls anywhere in the function or the file's write paths** (grep). | Same divergence. Upstream's `post_api_operation` (`pypowerwall_fleetapi.py:746-772`) calls `self.fleet.set_battery_reserve(...)`/`set_operating_mode(...)` (`pypowerwall/fleetapi/fleetapi.py:709-729`), which POST to `api/1/energy_sites/{site_id}/backup` / `.../operation` on the Fleet API host, `Authorization: Bearer <access_token>` (`fleetapi.py:388`). Go's `postAPIOperation` (`backend/fleetapi/fleetapi.go:340-345`) is the identical no-op-log-and-succeed pattern. |
| `SetGridCharging` | Real for TEDAPI's **v1r transport only** (confirmed in this pass): `pypowerwall_tedapi.py:861-870`'s `set_grid_charging` requires `self.tedapi.v1r`, else logs an error and returns `None` — it is **not** a GraphQL mutation as the first audit guessed, but a local config write (`self.tedapi._write_config({'site_info.disallow_charge_from_grid_with_solar_installed': not enable})`) — same field, same negation as cloud/fleetapi (see §1 item 30). gopowerwall's facade has **no TEDAPI/v1r case at all** in `SetGridCharging` (`powerwall.go:1080-1095` only switches on `ModeCloud`/`ModeFleetAPI`) — confirmed missing, not merely "not exposed by any backend" as the first audit assumed; `backend/tedapi` has no `SetGridCharging` method (grep-confirmed: zero matches for `GridCharging` anywhere under `backend/tedapi/`). | Same as the `local` column — real for v1r, present at the Go facade only for cloud/fleetapi. | **Confirmed no-op *and* inverted field in Go; real in upstream (§1 item 30 has the full negation finding).** Upstream `set_grid_charging` (`pypowerwall_cloud.py:878-891`) calls `self._site_api("ENERGY_SITE_IMPORT_EXPORT_CONFIG", ttl=SITE_CONFIG_TTL, force=True, disallow_charge_from_grid_with_solar_installed=mode)` after negating `mode`, resolving (`teslapy/endpoints.json:541-544`: `ENERGY_SITE_IMPORT_EXPORT_CONFIG` → `{"TYPE": "POST", "URI": "api/1/energy_sites/{site_id}/grid_import_export"}`) to a real `POST` with body `{"disallow_charge_from_grid_with_solar_installed": <negated bool>}`. Go's `SetGridCharging` (`backend/cloud/cloud.go:785-798`, current line numbers — file has grown since the first audit) now **does** send a real HTTP POST to the same URL (the "no-op" finding from the first audit no longer holds as written — verify against the concurrent refactor's final state) but forwards `mode` **unnegated**: `map[string]any{"disallow_charge_from_grid_with_solar_installed": mode}`. `GetGridCharging` (`backend/cloud/cloud.go:801-815`) reads `lookup.Lookup(cfg, "response", "grid_charging")` — a field name (`response.grid_charging`) that **does not exist anywhere in upstream's site_info/site_config schema** (confirmed: `pypowerwall_cloud.py:504-588`'s documented `get_site_config()` response has no `grid_charging` key at top level of `response`, only `response.components.disallow_charge_from_grid_with_solar_installed`); this read will return `backend.ErrNotFound` against a real gateway every time. **Required fix**: read `response.components.disallow_charge_from_grid_with_solar_installed` and return its logical negation (defaulting to "enabled"/`true` when absent, per `pypowerwall_cloud.py:920-923`: `state = components.get(...); return not state`). | Same divergence, confirmed identically. Upstream `set_grid_charging` → `self.fleet.set_grid_charging(mode)` (`fleetapi.py:733-754`) negates then `POST api/1/energy_sites/{site_id}/grid_import_export`. Upstream `get_grid_charging` (`fleetapi.py:694-698`) reads `components.disallow_charge_from_grid_with_solar_installed` from `get_site_info()` and negates. Go's `SetGridCharging` (`backend/fleetapi/fleetapi.go:688-701`) sends `mode` unnegated; Go's `GetGridCharging` (`backend/fleetapi/fleetapi.go:704-718`) reads the same nonexistent `response.grid_charging` path as the cloud backend. Same required fix. |
| `SetGridExport` | Same scoping note as `SetGridCharging` — TEDAPI/v1r only, via `pypowerwall_tedapi.py:882-891`'s local config write to `site_info.customer_preferred_export_rule`, validated against `{'battery_ok','pv_only','never'}`. gopowerwall's facade has no TEDAPI/v1r case for `SetGridExport` either. | Same as `local` column. | Confirmed real HTTP write in Go now (current state) to the same `ENERGY_SITE_IMPORT_EXPORT_CONFIG` endpoint as `SetGridCharging`, body `{"customer_preferred_export_rule": <mode>}` (`pypowerwall_cloud.py:896-911`) — **no negation involved for this field** (unlike grid charging, `customer_preferred_export_rule` is not a prohibition-phrased field, so passing `mode` through verbatim is correct here). No bug found in this row beyond the general TEDAPI/v1r gap noted above. | Same shape, same "no bug" verdict: `POST api/1/energy_sites/{site_id}/grid_import_export` body `{"customer_preferred_export_rule": <mode>}` (`fleetapi.py:756-772`). |
| `GetTimeRemaining` | Real (gateway computation) on both sides — not affected by this row's finding. | Real (v1r/TEDAPI query) on both sides. | **Resolved, correcting the first audit's "parity (both hard-code a placeholder)" — this is not parity.** Upstream's cloud `get_time_remaining()` (`pypowerwall_cloud.py:596-611`) makes a **real** API call — `GET api/1/energy_sites/{site_id}/backup_time_remaining` (`ENERGY_SITE_BACKUP_TIME_REMAINING`) — and returns the live `response.time_remaining_hours` value; it returns `None` only if the whole response is missing/malformed, and `0.0` only as a narrow fallback when the response lacks the expected key (an edge case, not the normal path). Go's `GetTimeRemaining` (`backend/cloud/cloud.go:730-735`) is, in full, `zero := 0.0; return &zero, nil` — it makes **no API call whatsoever** and always returns `0.0` regardless of the real site's backup time. This is a confirmed, real missing-feature gap, not a shared design choice; the code comment citing a "DESIGN.md invariant" for this hardcoding does not reflect upstream's actual behavior. | Same finding, same fix needed. Upstream `pypowerwall_fleetapi.py:372-380` calls `self.fleet.get_backup_time_remaining(force=force)` (a real Fleet API call) and returns `response.time_remaining_hours`, falling back to `0.0` only when the key is absent from an otherwise-valid response. Go's fleetapi `GetTimeRemaining` needs the same real-call treatment as cloud. |
| `GoOffGrid`/`ReconnectGrid` | **No longer "dead code both sides" — upstream moved (§1 items 34/35).** Real for TEDAPI's v1r transport as of the `4d092c7ed0` re-audit: `pypowerwall/tedapi/__init__.py`'s new `go_off_grid()`/`reconnect_grid()` call `self.v1r_transport.send_island_mode(self.din, mode=6, force=True)` / `mode=1` (`tedapi_v1r.py:486-516`, Tesla's signed `setIslandModeRequest`/`setIslandModeResponse`). Go's `backend/tedapi/tedapi.go:923-931` remains an unconditional `backend.ErrUnsupported` for both — confirmed still true in the current tree. | n/a | n/a | n/a |
| `ScheduleMaxBackup`/`CancelMaxBackup`/`GetBackupEvents` | n/a | Real on both sides, v1r-gated on both sides (`p.v1r == nil` check in Go, `tedapi_mode != "v1r"` check in Python). | n/a | n/a |

---

## 5. Corrections to MISSING.md

1. **`/fans/pw` scope was too narrow.** MISSING.md says "`/fans/pw` is not implemented,"
   implying `/fans` (without the `/pw` suffix) might be. Confirmed: **neither** `/fans` nor
   `/fans/pw` exists anywhere in gopowerwall's proxy package (§3.1).
2. **`authtoken`'s upstream behavior is mischaracterized.** MISSING.md: "Performs the Tesla
   OAuth2 device-code flow and writes `.pypowerwall.auth`." Upstream's actual mechanism is a
   **PKCE authorization-code flow** (RFC 7636), not a device-code grant (RFC 8628) — there is
   no `device_code` endpoint anywhere in `tesla_auth.py` (confirmed by grep). Further, the
   `authtoken` *command specifically* never writes any file — it only prints tokens to
   stdout; only `setup` writes `.pypowerwall.auth`. See §2.2.
3. **`GoOffGrid`/`ReconnectGrid` claim reversed — and now reversed again by upstream's own
   movement.** MISSING.md: "The `Powerwall` facade exposes both..., and the TEDAPI backend
   implements them." At the first audit's commit (`a3b327be3`) this was confirmed false: no
   backend in either project implemented them. **In this second pass, re-auditing at
   upstream's current `main` (`4d092c7ed0`, 5 commits ahead), the picture changed again**:
   upstream's TEDAPI backend now genuinely implements both for v1r transport
   (`pypowerwall/tedapi/__init__.py`'s new `go_off_grid()`/`reconnect_grid()`, added by PR
   #379 after the first audit's commit) — see §1 items 34-35. gopowerwall's TEDAPI backend
   does not (`backend/tedapi/tedapi.go:923-931`, unconditional `ErrUnsupported`, confirmed
   unchanged). So MISSING.md's original sentence, wrong when written, is now **accidentally
   half-true of upstream** — but still describes gopowerwall's own `Powerwall.GoOffGrid`
   facade method (which exists) rather than a working backend (which, on the Go side,
   still doesn't). This is now a real, if narrow (v1r-hardware-only, "extreme care"
   physical-contactor) missing feature in gopowerwall, not shared dead code.
4. **The `/control/*` off_grid/reconnect_grid gap is resolved, not open — but the
   underlying justification has partially changed.** MISSING.md flags this as "unconfirmed
   whether pypowerwall's proxy exposes equivalent control routes at all." Confirmed across
   both audited commits: it does not (`proxy/server.py` is unchanged between them). The
   verdict for gopowerwall's proxy (`n/a (deliberate)`, nothing to expose via `/control/*`)
   still stands, but per correction #3 above it is no longer because "the library side is
   dead code too" — upstream's library side is now real for v1r. Upstream simply chose not
   to route it through the proxy's `/control/*` surface (plausibly deliberate, given the
   new "USE WITH EXTREME CARE" warnings on the physical grid-contactor operation).
5. **The `backend/local` raw/parsed cache-key collision is upstream's own behavior, not a
   gopowerwall-introduced bug.** MISSING.md lists this under "Known issues" as if it were a
   gopowerwall defect. Confirmed: `pypowerwall/local/pypowerwall_local.py:151-267`'s `poll()`
   does the identical thing — `self.pwcache[api] = payload` with no raw/parsed dimension in
   the key, for the same reasons (the only endpoint that forces `raw=True` internally is
   `/api/devices/vitals`, so the practical collision surface is narrow on both sides). This
   should be reclassified from "gopowerwall implementation issue" to "faithfully replicated
   upstream behavior (possibly a latent bug in both projects)."
6. **A more severe, previously-undocumented bug exists in local-mode writes.** MISSING.md's
   "Major finding" section covers the cloud/fleetapi write no-op thoroughly, but does not
   mention that `SetOperation`'s missing local-mode back-fill (§1 item 22) is arguably
   worse: cloud/fleetapi writes silently do nothing to a *cloud-managed* account, but the
   local-mode bug can silently corrupt a *real gateway's* configuration on every
   single-field `set --local --reserve`/`set --local --mode` call. This was not flagged
   anywhere in MISSING.md and should be considered the top-priority correctness fix (see §6
   below), not the cloud/fleetapi no-op.
7. **Vitals/Strings field-naming claim was too hedged; the underlying facts are now fully
   confirmed, including the index base.** MISSING.md says gopowerwall's `PVAC_Vsolar<label>`
   naming is "unverified against real hardware" and that pypowerwall's naming
   ("PVAC_PVMeasuredVoltage/Current") "has no equivalent field in what gopowerwall reads."
   Now confirmed precisely, with the exact separators and index base this pass was asked to
   settle: upstream's TEDAPI-mode vitals computation (`pypowerwall/tedapi/__init__.py:1032-1069`)
   produces, per PW3 string letter `n` in `{"A","B","C","D","E","F"}` (**letter-based, not
   numeric** — "PW3 has 6 strings A-F", `:1030`) — `PVAC_PvState_{n}`, `PVAC_PVMeasuredVoltage_{n}`,
   `PVAC_PVCurrent_{n}` (note: no "Measured" in the current field name, an internal upstream
   naming inconsistency), and `PVAC_PVMeasuredPower_{n}` (computed as voltage×current), all
   with an underscore before the letter, from raw `PCH_PvVoltage{n}`/`PCH_PvCurrent{n}`
   (**no** underscore before the letter) and `PCH_PvState_{n}` (**with** underscore) GraphQL
   component signals — plus a *separately*-named device, `PVS_String{n}_Connected` (no
   underscore between "String" and the letter), sourced from `pv_state` rather than a raw
   signal. `Powerwall.strings()` itself (`__init__.py:497-549`) then re-keys these by taking
   the *last character* of the field name (`e[-1]`, or `e[10]` for the `Connected` case —
   the character right after the literal `"PVS_String"` prefix) — confirming the facade's
   own key derivation is letter-based too, not index-based. **gopowerwall's TEDAPI backend
   computes none of this** (confirmed again in this pass: zero matches for
   `PVAC_Pv`/`PvVoltage`/`PvCurrent`/`PVMeasuredVoltage`/`PVMeasuredPower`/`PvState`/`PVS_String`
   in any `.go` file under `backend/tedapi/`, even though **the GraphQL query text itself
   already requests these exact fields** — `backend/tedapi/queries/V2024_06.json` and
   `V2026_06.json` both include `PVAC_PVCurrent_A..D`/`PVAC_PVMeasuredVoltage_A..D` in
   `PVAC_Logging`, `PVS_StringA..D_Connected` in `PVS_Status`, and `V2026_06.json`
   additionally requests PW3's `PCH_PvVoltageA..F`/`PCH_PvCurrentA..F`/`PCH_PvState_A..F` and
   `PVAC_Fan_Speed_Actual_RPM`/`_Target_RPM` — so the raw wire data already arrives at
   gopowerwall today and is silently discarded by the response parser, which is a smaller
   fix than "add a new query" would be). This is not merely "unverified against hardware" —
   it is confirmed that gopowerwall's `/strings` reads all-zero voltage/current/power with a
   hardcoded `Connected: true` for every string on every connection mode that populates
   vitals, because the underlying fields are requested from the gateway but never parsed
   into named fields in the first place.
8. **The proxy allowlist divergence was not previously flagged at all — now enumerated in
   full.** MISSING.md does not mention that gopowerwall's `isAllowlisted`
   (`proxy/server.go:25-66`) diverges from upstream's `ALLOWLIST` (`server.py:173-199`).
   This pass enumerates the exact symmetric difference (§3.1): **13** entries total (2
   upstream entries Go lacks — `/api/system/networks`, `/api/synchrometer/ct_voltage_references`
   — and 11 Go-only entries upstream lacks), correcting the earlier estimate of "12."
9. **`/pod`'s missing vitals-augmentation pass was not previously flagged.** New finding
   from this audit (§3.1) — gopowerwall's `/pod` never reads `TEPOD` vitals devices at all,
   unlike upstream.
10. **CLI subcommand inventory gap: `login` and `fleetapi` top-level subcommands.**
    MISSING.md's CLI section only discusses the five guidance-only stubs and does not
    mention that upstream additionally has `login` and `fleetapi` top-level subcommands
    (both now deprecated shims pointing at `setup`/`setup -fleetapi`) that gopowerwall has
    no equivalent for at all, not even a deprecation notice. Low practical impact since
    upstream's own versions are dead-end shims, but worth recording for CLI-surface
    completeness.
11. **`PW_TEDAPI_AUTH_MODE` is not vestigial upstream — MISSING.md's framing needs
    reversal.** MISSING.md's "Additional finding" section characterizes `authMode` as an
    option upstream defines but that has no real transport behind it, implying gopowerwall
    is merely (correctly) mirroring a no-op upstream concept. **Confirmed false in this
    pass**: `pypowerwall/tedapi/auth_mode.py` defines a real `AuthMode` enum (`BASIC`/`BEARER`)
    landed by PR #359 ("Add bearer auth mode", merged 2026-08-16, present at both audited
    commits — not new since the first audit), and `BEARER` is a fully wired, actively-used
    transport: `pypowerwall/tedapi/__init__.py` branches on `self.auth_mode == AuthMode.BEARER`
    at at least 8 call sites (`:828`, `:1138`, `:1358`, `:1380`, `:1522`, `:1622`, `:1764`,
    `:1767`) to POST `/api/login/Basic` for a Bearer token (`:1254-1255`, `:2593-2609`) and
    wrap each subsequent query in an `AuthEnvelope` (`:1288-1299`) instead of using HTTP
    Basic Auth — a mode that "also works over the wired LAN IP" and works on PW2/solar-only
    installs (per `:158-162`'s docstring), unlike plain Basic which is Gateway-Wi-Fi-only.
    See the corrected "Additional finding" section rewritten below: `PW_TEDAPI_AUTH_MODE` is
    a real, meaningful upstream feature that gopowerwall has not implemented (`PostTEDAPI`
    always uses HTTP Basic regardless of the configured mode) — this is a **missing
    feature**, not dead configuration safe to delete.
12. **`SystemStatusStub`'s cloud/fleetapi overlay claim was wrong, not merely unverified.**
    MISSING.md's "Proxy routes" section describes the stub backfill as "This appears to be a
    deliberate design choice mirroring how pypowerwall backfills gateway-only introspection
    endpoints... but the exact field-for-field shape has not been checked... so treat the
    specific JSON as unverified." Now checked in full (§3.2): the *mechanism* (a stub
    template overlaid with live-computed fields) is real upstream, but **gopowerwall's
    cloud and fleetapi `getAPISystemStatus` do not perform any such overlay at all** —
    they return the raw un-overlaid stub with only `battery_blocks` reset to `[]`. Nine
    fields upstream computes from live site/battery/config data
    (`nominal_full_pack_energy`, `nominal_energy_remaining`, `max_charge_power`,
    `max_discharge_power`, `max_apparent_power`, `grid_services_power`,
    `system_island_state`, `available_blocks`/`blocks_controlled`, `solar_real_power_limit`)
    are placeholder values in gopowerwall's cloud/fleetapi `/api/system_status` today. This
    is a confirmed functional gap, not an unverified literal-value question.
13. **`GetTimeRemaining`'s "both hard-code 0.0" framing in this document itself was wrong**
    (not a MISSING.md correction, but a correction to this matrix's own first-pass finding
    in §4): upstream's cloud and fleetapi `get_time_remaining()` both make real API calls
    and return live data; only gopowerwall hard-codes `0.0`. See the rewritten §4 row.

---

## 6. Prioritized implementation plan

Ordered by user impact (highest first), with a rough size estimate and an explicit note on
what cannot be verified from this machine.

1. **Fix `SetOperation`'s missing local-mode back-fill (§1 item 22).** *Highest priority.*
   This is a live-hardware data-corruption bug, not a missing feature: any local-gateway
   user who runs `gopowerwall set --local --reserve N` or `--mode X` today risks silently
   resetting the field they did *not* specify. Fix: in `powerwall.go`'s `SetOperation`, when
   `p.mode == ModeLocal`, read back the current `real_mode`/`backup_reserve_percent` (via
   `p.Operation(ctx)`) for whichever field is nil before building the payload, mirroring
   `__init__.py:846-877`'s `full_overwrite` branch. **Size: small** (a few hours) — the
   read-back call (`p.Operation`) already exists; this is a straightforward conditional
   addition. **Verifiable:** partially, with an `httptest` fixture asserting the POST body
   contains both fields when only one was requested in local mode — no real hardware needed
   for the unit-level fix, though final confirmation that the gateway really does treat a
   missing field as "clear it" would need a real gateway.
2. **Superseded by the current tree — re-verify, don't re-implement.** This item originally
   read "make cloud/fleetapi `SetReserve`/`SetMode`/`SetGridCharging`/`SetGridExport`
   actually call Tesla," on the premise that all four were log-and-succeed no-ops. Re-reading
   the current working tree in this pass shows all four now issue real
   `http.NewRequestWithContext` POSTs (`backend/cloud/cloud.go:558-830`,
   `backend/fleetapi/fleetapi.go:462-733`) — whether that landed before or during the
   concurrent architectural refactor mentioned in this task's scope was not determined, but
   it is done. **What replaces this item, at the same or higher priority: fix
   `SetGridCharging`'s field negation and `GetGridCharging`'s wrong field name (§1 item 30,
   §4).** Both `backend/cloud/cloud.go` and `backend/fleetapi/fleetapi.go` need (a) their
   `SetGridCharging` to send `!mode`, not `mode`, for
   `disallow_charge_from_grid_with_solar_installed`, and (b) their `GetGridCharging` to read
   `response.components.disallow_charge_from_grid_with_solar_installed` (negated), not
   `response.grid_charging` (a field that does not exist upstream). **Size: small** (well
   under a day — a one-line negation plus a lookup-path fix in each of two files).
   **Verifiable without hardware**: an `httptest` fixture can assert the exact POST body and
   the read-side negation against a canned `site_info`/`site_config` response; this needs no
   live Tesla account, unlike proving Tesla's 80%-cap/async-command-race handling (which
   still requires live credentials this environment does not have, and remains true of the
   already-real writes above).
3. **`/strings` and TEDAPI-mode vitals field computation (§1 item 9, §4, correction #7) —
   letter-based index confirmed, and the raw fields already arrive over the wire.** Port
   `pypowerwall/tedapi/__init__.py:1032-1069`'s `PCH_PvVoltage{n}`/`PCH_PvCurrent{n}`/
   `PCH_PvState_{n}` → `PVAC_PVMeasuredVoltage_{n}`/`PVAC_PVCurrent_{n}`/
   `PVAC_PVMeasuredPower_{n}`/`PVAC_PvState_{n}` (plus `PVS_String{n}_Connected` on a
   sibling device) computation into `backend/tedapi`, where **`n` is confirmed to be a
   letter `A`-`F`, not a number** (PW3 has 6 strings). This is less new work than the first
   pass estimated: `backend/tedapi/queries/V2024_06.json`/`V2026_06.json` **already request
   every one of these fields** in their `PVAC_Logging`/`pch` component signal lists — the
   task is parsing the existing response, not adding a new query. Separately, reconsider
   whether `Powerwall.Strings()`'s output shape should move from the current fabricated
   `PVAC_Vsolar<label>` scheme to match one of the two real upstream schemes
   (`__init__.py:497-549`'s letter+index keys derived via `e[-1]`/`e[10]`, or the verbose
   per-device view) — upstream itself has two different internal naming conventions
   (local-firmware field names vs. TEDAPI-synthesized ones) that do not agree with each
   other. **Size: medium** (a day) for the TEDAPI parsing, now that the query side is
   confirmed already correct; a further **small-medium** effort to decide and implement the
   public `Strings()` shape. **Partially verifiable without hardware**: the parsing can be
   unit tested against recorded component-signal fixtures built from the query's own JSON
   shape; whether *local-mode* vitals already carry the same field names (this pass's
   working assumption, based on the TEDAPI code visibly reconstructing what native gateway
   vitals would contain) cannot be confirmed without a real gateway's
   `/api/devices/vitals` response.
4. **Proxy allowlist reconciliation (§3.1, correction #8) — exact list now enumerated.**
   Add `/api/synchrometer/ct_voltage_references` and `/api/system/networks` (fixing the
   `conn_tests` typo/divergence) to `isAllowlisted`; decide whether to keep or remove the 11
   Go-only entries that have no upstream counterpart (`/api/system/networks/conn_tests`,
   `/api/diagnostics`, `/api/generators`, `/api/generators/actions`, `/api/syncon/vitals`,
   `/api/syncon/actions`, `/api/inverters`, `/api/inverters/status`, `/api/meters/status`,
   `/api/powerwalls/status`, `/api/system_status/soe` — 13-entry symmetric difference total,
   corrected from the earlier estimate of 12). **Size: trivial** (an hour, plus a deliberate
   decision on the extra entries). Fully verifiable without hardware — it is a pure
   static-list diff against the fetched `ALLOWLIST`.
5. **`/pod`'s `TEPOD` vitals augmentation pass (§3.1) — confirmed still missing after the
   concurrent refactor relocated the function.** Port `server.py:2196-2244`'s vitals-overlay
   loop into `generatePOD` (now `proxy/routes.go:130`, via `s.PW.PODView(ctx)` at
   `powerwall.go:1879`) — for each vitals device whose name starts with `"TEPOD"`, overwrite
   `PW{idx}_name`/`POD_ActiveHeating`/`ChargeComplete`/`ChargeRequest`/`DischargeComplete`/
   `PermanentlyFaulted`/`PersistentlyFaulted`/`enable_line`/`available_charge_power`/
   `available_dischg_power`/`nom_energy_remaining`/`nom_full_pack_energy`, plus the
   previously-unemitted `nom_energy_to_be_charged`, using an index derived from vitals
   iteration order (not DIN-matched against `battery_blocks` — mirror upstream's own
   assumption that iteration order aligns). **Size: small** (a few hours) — the vitals data
   plumbing already exists (`p.Vitals(ctx)` is already used elsewhere, e.g.
   `FrequencyView`). **Verifiable without hardware** via a vitals fixture containing a
   `TEPOD` device.
6. **`/tedapi/status`, `/components`, `/battery`, `/controller` routes (§3.1).** `backend/tedapi`
   already has most of the underlying data (`GetStatus` at minimum); wiring the remaining
   three sub-routes is comparatively cheap. **Size: small-medium.** Fully verifiable without
   real hardware for the routing/shape; the underlying TEDAPI query correctness against a
   real gateway is a separate, larger question already covered by the existing
   `backend/tedapi` test suite's assumptions.
7. **`/fans` and `/fans/pw` (§3.1, correction #1) — field names confirmed, one upstream
   ambiguity flagged.** Needs a TEDAPI `get_fan_speeds`-equivalent extracting
   `PVAC_Fan_Speed_Actual_RPM`/`PVAC_Fan_Speed_Target_RPM`, plus the two routes
   (`/fans`: raw dict; `/fans/pw`: `FAN{i+1}_actual`/`_target`, 1-based, sorted by the
   fan-speed dict's own keys). **`backend/tedapi/queries/V2026_06.json` already requests
   both fields** (inside `PVAC_Logging`), so — unlike the first pass's framing — this may be
   less "new TEDAPI query work" and more "parse a field that's already being fetched,"
   possibly sharing the same parsing work as item 3 above. One upstream ambiguity this pass
   could not resolve from source: `get_fan_speeds()`'s own `extract_fan_speeds()`
   (`pypowerwall/tedapi/__init__.py:1879-1903`) scans `data["components"]["msa"]` for the
   fan-speed signal names, but the `msa`-aliased GraphQL query in the same file's own
   `DeviceControllerQuery` requests only `MSA_*`/`METER_Z_*` names under that alias, never
   `PVAC_Fan_Speed_*` — so whether this function returns real data in practice, or is itself
   a latent upstream bug, needs a live gateway to settle, not more static reading. **Size:
   medium.** **Cannot be fully verified without real Powerwall hardware with cooling fans
   reporting non-zero speeds** — a synthetic fixture can prove the route logic, but
   confirming the underlying field/query correctness (including the ambiguity above) needs
   a live gateway.
8. **Guidance-only CLI subcommands: `cloudcheck`, `authtoken`, `setup`, `register`,
   `tedapi`.** Ordered here by realistic size, smallest first:
   - `cloudcheck` (**medium**): mostly straightforward HTTP + JWT-claims decoding; no OAuth
     flow needed. Environment/TLS/proxy-env checks are trivially verifiable from this
     machine; the live connectivity and token-refresh checks need network access this
     environment does have (outbound HTTPS worked in this session) but **the token-refresh
     test specifically needs a real `.pypowerwall.auth` file with a live refresh token**,
     which cannot be produced without a Tesla account.
   - `tedapi` (**small-medium**): mostly wiring existing `backend/tedapi` calls; the
     `pypowerwall/tedapi/__main__.py` source for the exact print format was not fetched in
     this audit (unverified) but the underlying connect/status calls are already
     implemented in Go. **Verifiable against real or emulated TEDAPI gateway hardware
     only** — this environment has neither.
   - `authtoken`/`setup` (**large**): a full PKCE web-login flow (browser hand-off or
     manual paste) plus auth-file writers. The manual/headless paste path
     (`_remote_login`) is buildable and testable without a live Tesla account (it is just a
     token POST once the user supplies RT/AT), but **cannot be end-to-end verified without a
     real Tesla account** to actually complete a login and confirm the written auth file is
     accepted by `backend/cloud`/`backend/fleetapi`.
   - `register` (**large**): RSA-4096 keygen is trivial in Go; the two-step OAuth
     (developer-app credentials) + Fleet API command-and-poll sequence is real integration
     work. **Cannot be verified at all without a Tesla Developer account, a registered Fleet
     API application, and a physical Powerwall gateway to actually receive and accept the
     key** — this is the single most hardware/account-dependent item in this entire matrix.
9. **`/help`, `/stats`, `/health` field/content richness (§3.1).** Lowest priority: these are
   operational/debugging surfaces, not data-correctness surfaces. Bringing `/stats` up to
   upstream's `fallback_mode`/`mem_cache` richness and `/help` up to its live-refresh
   dashboard is **medium** effort per route and has no correctness impact — purely
   informational. Fully verifiable without hardware.
10. **`/cloud/battery,/power,/config` and `/fleetapi/info,/status` routes.** Low priority —
    these largely duplicate data already reachable via `/pw/*` and the standard `/api/*`
    routes; upstream keeps them mostly for direct low-level client access. **Size: small.**
    Verifiable without hardware if a mock cloud/FleetAPI backend is used, same as the rest of
    the existing cloud/fleetapi test suites.
11. **New from this pass, and the single highest-priority item in this list: the
    `SetGridCharging`/`GetGridCharging` negation and field-name bugs (§1 item 30, §4, and
    item 2 above).** This supersedes item 2's original text — see there for the fix. Ranked
    above everything else here because, unlike the missing routes/fields elsewhere in this
    plan, this one now performs a real write to a live Tesla site with the user's intent
    inverted, silently, with no error returned.
12. **New from this pass: `getAPISystemStatus`'s missing live-data overlay in cloud/fleetapi
    (§3.2, §4).** Port `pypowerwall_cloud.py:835-874`/`pypowerwall_fleetapi.py:597-629`'s
    nine-field overlay (`nominal_full_pack_energy`, `nominal_energy_remaining`,
    `max_charge_power`, `max_discharge_power`, `max_apparent_power`, `grid_services_power`,
    `system_island_state`, `available_blocks`/`blocks_controlled`,
    `solar_real_power_limit`) into `backend/cloud/cloud.go:684-689` and
    `backend/fleetapi/fleetapi.go:587-592`, sourced from the site power/config/battery (or
    live-status/site-info) reads each backend already has. **Size: small-medium** (the
    source reads already exist elsewhere in each file; this is composing them, not adding
    new API surface). Fully verifiable without hardware against a mock HTTP server.
13. **New from this pass: `GetTimeRemaining` should call Tesla instead of hard-coding `0.0`
    in cloud/fleetapi (§4).** Replace `backend/cloud/cloud.go:730-735`'s unconditional
    `0.0` with a real call to the backup-time-remaining endpoint each backend already has
    the plumbing to reach (`ENERGY_SITE_BACKUP_TIME_REMAINING` for cloud,
    `get_backup_time_remaining` for fleetapi), falling back to `0.0` only when the response
    lacks the key. **Size: small.** Verifiable without hardware against a mock HTTP server;
    confirming Tesla's real response shape needs a live account.
14. **New from this pass: stub-constant field/schema fixes (§3.2).** `MockAuthToggle`'s key
    name (`toggle_supported` → `toggle_auth_supported`, default `false` → `true`),
    `MockMeters`'s schema (`{id,location,type}` → `{serial,short_id,type,...}`),
    `MockInstaller`'s payload (drop the nonexistent `ready_for_customer`, add upstream's 13
    real fields), `MockUpdate`'s schema (`{status,percentage}` → `{state,info,...}`), and
    `MockSolars`'s container type (bare object → one-element array, plus the missing
    `power_rating_watts` field) are all confirmed wrong against upstream's literal values.
    **Size: small** (literal-value fixes, no new logic) — see §3.2's table for exact target
    values.
15. **New from this pass: `PW_TEDAPI_AUTH_MODE`'s bearer transport is unimplemented (see
    the corrected "Additional finding" in MISSING.md).** Implement a `/api/login/Basic`
    POST to obtain a Bearer token and wrap TEDAPI request/response bodies in an
    `AuthEnvelope` when `authMode == bearer`, instead of the current always-Basic behavior
    in `Client.PostTEDAPI`. **Size: medium-large** — this is a second authentication
    transport, not a config toggle. **Only verifiable against a PW2/solar-only gateway with
    bearer mode enabled** (per upstream's own scoping: "PW2/solar-only — not PW3").
16. **New from this pass: `backend/tedapi`'s `GoOffGrid`/`ReconnectGrid` are missing a real
    v1r implementation upstream now has (§1 items 34-35, §4).** Port
    `send_island_mode(din, mode=6, force=True)`/`send_island_mode(din, mode=1)`
    (`pypowerwall/tedapi/tedapi_v1r.py:486-516`, Tesla's signed `setIslandModeRequest`) into
    `backend/tedapi/tedapi.go:923-931`, gated on v1r transport being present, matching
    upstream's `if not self.v1r or not self.v1r_transport: return None` guard. **This
    operates a real, physical grid contactor** — upstream's own code comments call this "USE
    WITH EXTREME CARE." **Size: medium**, mostly protobuf message construction (the vendored
    `.proto` under `proto/` should already define the TEG oneof fields involved, per this
    project's protobuf vendoring policy in `.agent/rules/powerwall.md`). **Cannot be verified
    at all without a physical, v1r-paired Powerwall gateway** — an incorrect implementation
    here fails safe (command rejected) or, in the worst case, actually islands or
    reconnects a real home's grid connection; treat this as the highest-caution item in this
    entire plan despite its modest code size.

### Explicitly unverifiable from this machine, regardless of implementation effort

*(Updated in the 2026-09-07 second pass — several items below were resolved and are
removed; the four remaining genuinely need live credentials or physical hardware.)*

- Any claim about what a **real Tesla Owner API or Fleet API account** actually accepts or
  rejects for the write endpoints in §4 (headers, rate limits, the 80%-reserve cap's exact
  server-side behavior — confirmed in this pass that neither pypowerwall nor gopowerwall
  clamps it client-side, but what Tesla's server does with an out-of-range value is only
  observable live; whether a `POST` with a stale/refreshed access token really 403s as
  `tesla_auth.py`'s comments assert).
- Any claim about **real gateway firmware** behavior: whether `/api/operation` really
  clobbers an omitted field (this audit trusts pypowerwall's own code comment as the
  evidence, but did not — and could not — confirm it against physical hardware); whether
  local-mode vitals field names for solar strings match the letter-suffixed TEDAPI-synthesis
  naming this pass confirmed (`PVAC_PVMeasuredVoltage_{A..F}` etc. — the working assumption,
  based on the TEDAPI code visibly reconstructing what native gateway vitals would contain,
  but not independently confirmed against a real local gateway's `/api/devices/vitals`
  response); whether `get_fan_speeds()`'s `components.msa` scan actually returns fan data in
  practice, given the static ambiguity noted in §3.1/§6 item 7 (the `msa`-aliased GraphQL
  query's own signal list appears not to include `PVAC_Fan_Speed_*` names); whether
  `PW_TEDAPI_AUTH_MODE=bearer` actually authenticates against a real PW2/solar-only gateway
  once implemented (upstream scopes it to "PW2/solar-only — not PW3," itself unverifiable
  without that specific hardware); the real, physical result of a v1r `go_off_grid`/
  `reconnect_grid` command (§6 item 16) — whether it actually opens/closes the contactor,
  and whether an incorrect implementation fails safe.
- `pypowerwall/tedapi/__main__.py` (the `tedapi` CLI subcommand's actual implementation) —
  not fetched in this audit; the CLI-forwarding surface (§2.1/§2.2) is confirmed, but the
  exact print format and any additional live checks it performs are not.
- Full field-by-field parity of `/stats` beyond the high-level key-set comparison in §3.1 —
  fetched in full, but a byte-for-byte diff of every nested field (particularly the
  `fallback_mode`/`mem_cache` blocks) was not completed given the size of `server.py` (2739
  lines) relative to the time available. (`/health`'s field-by-field diff **was** completed
  in this pass — see the rewritten §3.1 row; it is no longer in this unverifiable list.)

**Resolved in this pass, removed from this list**: `pypowerwall/cloud/mock_data.py`,
`pypowerwall/cloud/stubs.py`, `pypowerwall/fleetapi/mock_data.py`,
`pypowerwall/fleetapi/stubs.py`, and `pypowerwall/tedapi/mock_data.py`/`stubs.py` were all
fetched and diffed field-by-field against `backend/stubs/stubs.go` in this pass — see §3.2.
`/health`'s field-by-field parity (see above) and the TEDAPI grid-charging/export mutation
shape (§4 — confirmed to be a local config write, not a GraphQL mutation, with the same
field name and negation as cloud/fleetapi) were also resolved and removed.
