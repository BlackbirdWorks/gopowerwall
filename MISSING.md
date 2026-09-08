# Parity gaps and known issues

Per [`.agent/rules/powerwall.md`](.agent/rules/powerwall.md), gopowerwall's parity
contract with [pypowerwall](https://github.com/jasonacox/pypowerwall) covers exactly two
surfaces: the `gopowerwall` CLI (subcommand names, flags, human-readable output) and the
proxy's HTTP surface (route paths, JSON shapes, field names). Everything else — package
layout, exported Go identifiers, internal structure — is intentionally idiomatic Go and not
a parity target.

This document tracks where that parity is still incomplete, plus a few implementation
issues that are worth knowing about even though they don't affect the parity surface
directly. It is audited against the CLI in `cmd/gopowerwall/cli.go` and `commands/*.go`,
and the proxy routes in `proxy/routes.go`, `proxy/control.go`, and `proxy/constants.go`.

## CLI subcommands that only print guidance text

Five of the ten `gopowerwall` subcommands do not perform the operation their pypowerwall
counterpart performs; their `Run` methods (`commands/misc.go`) print static text to stdout
and return `nil`:

| Subcommand | pypowerwall behavior | gopowerwall behavior today |
|---|---|---|
| `setup` | Interactive wizard that walks through Cloud/FleetAPI/v1r configuration, driving the OAuth or RSA registration flow | Prints three lines of instructions naming the files to create by hand |
| `authtoken` | Performs the Tesla OAuth2 device-code flow and writes `.pypowerwall.auth` | Prints the Tesla OAuth authorize URL and nothing else; no token exchange, no file written |
| `register` | Registers a v1r RSA public key with the gateway or Fleet API | Prints a single status line; no key generation, no registration request |
| `cloudcheck` | Runs live reachability/auth checks against Tesla's Auth and Owner API endpoints and reports pass/fail | Prints a single status line; no network calls are made regardless of `--noconnect` |
| `tedapi` | Opens a live TEDAPI connection and reports connection status/diagnostics | Prints the target host and nothing else; `TedapiCmd.Run` never calls `backend/tedapi` |

Practical effect: `.pypowerwall.auth` and `.pypowerwall.fleetapi` must be produced by
another tool and placed under `--authpath`/`PW_AUTH_PATH` before `cloud` or `fleetapi` mode
can be used. This is documented in the README's CLI section and status table. The
recommended tool for cloud mode is [tesla_auth](https://github.com/adriankumpf/tesla_auth):
its refresh token, set as `TESLA_REFRESH_TOKEN`, lets gopowerwall bootstrap
`.pypowerwall.auth` itself on first run (see the README's
[Tesla Cloud mode setup](README.md#tesla-cloud-mode-setup-tesla_auth--env) section) — this
narrows the gap to "no interactive OAuth login flow", not "no way to get a token in at
all". Once an auth file exists (bootstrapped or otherwise), `backend/cloud` and
`backend/fleetapi` both refresh the Tesla access token automatically via
`golang.org/x/oauth2` as it expires and persist the refreshed token back to the file, so a
long-running proxy does not need re-authentication.

`get`, `set`, `scan`, and `proxy` are fully implemented and exercise the real backends.

## Proxy routes

- **Neither `/fans` nor `/fans/pw` is implemented** (corrected: MISSING.md previously named
  only `/fans/pw`; `/fans` without the suffix is equally absent). No handler exists for
  either anywhere in `proxy/routes.go`, `proxy/handlers.go`, or `proxy/control.go`
  (confirmed by grep across the package). Both routes are implementation-ready from
  upstream's exact behavior (`server.py:2483-2500`, `docs/parity-matrix.md` §3.1): `/fans`
  emits the raw dict from `pw.tedapi.get_fan_speeds()` (`{}` if not a TEDAPI-family
  backend); `/fans/pw` maps it to `FAN1_actual`/`FAN1_target`, `FAN2_actual`/`FAN2_target`,
  ... (1-based, ordered by sorting the fan-speed dict's own device-name keys), reading
  `PVAC_Fan_Speed_Actual_RPM`/`PVAC_Fan_Speed_Target_RPM` off each entry. Upstream's
  `get_fan_speeds()` (`pypowerwall/tedapi/__init__.py:1879-1906`) derives this from
  `get_device_controller()`'s `components.msa` component signals — though whether that
  specific alias actually carries fan-speed-named signals against upstream's own
  `DeviceControllerQuery` (`pypowerwall/tedapi/queries/V2026_06.json`, which requests
  `PVAC_Fan_Speed_*` under `esCan.bus.PVAC.PVAC_Logging`, not under the `msa`-aliased
  `components` section) is a genuine ambiguity this project cannot resolve from static
  source alone; it would need a real V2026_06-firmware gateway to confirm. What is
  confirmed regardless: gopowerwall's own `backend/tedapi/queries/V2026_06.json` GraphQL
  query text already requests both `PVAC_Fan_Speed_Actual_RPM`/`_Target_RPM`, but no `.go`
  file under `backend/tedapi/` parses them into any field (confirmed by grep) — the wire
  data already arrives and is discarded.
- **Cloud, FleetAPI, and TEDAPI modes return canned data, not live gateway data, for
  several `/api/*` endpoints — now confirmed to diverge from upstream's own canned values
  in several places, not merely "unverified."** `backend/stubs/stubs.go` defines fixed
  JSON payloads (`MockPowerwalls`, `MockMetersSite`, `MockMeters`, `MockSitemaster`,
  `MockCustomer`, `MockInstaller`, `MockNetworks`, `MockAuthToggle`, `MockUpdate`,
  `MockSolars`) and two templates (`MetersAggregatesStub`, `SystemStatusStub`). All three
  non-local backends wire these in for `/api/powerwalls`, `/api/meters/site`,
  `/api/meters`, `/api/sitemaster`, `/api/customer`, `/api/installer`, `/api/networks`,
  `/api/auth/toggle/supported`, `/api/system/update/status`, and `/api/solars`. Having now
  fetched and read `pypowerwall/cloud/mock_data.py`, `pypowerwall/cloud/stubs.py`, and
  their fleetapi/tedapi equivalents in full (`docs/parity-matrix.md` §3.2), several of
  these are confirmed **wrong, not merely simplified**: `MockAuthToggle` uses the key
  `toggle_supported` where upstream's real key is `toggle_auth_supported` (and inverts the
  default, `false` vs upstream's `true`); `MockMeters` uses an entirely different schema
  (`{id, location, type}`) from upstream's real one (`{serial, short_id, type, connected,
  cts, ...}`); `MockInstaller` returns `{"ready_for_customer": true}`, a key that does not
  exist anywhere in upstream's real 13-field payload; `MockUpdate` and `MockSolars` are
  likewise schema-mismatched (`MockSolars` even returns a bare object where upstream
  returns a one-element array). Separately and more consequentially, `getAPISystemStatus`
  in `backend/cloud/cloud.go`/`backend/fleetapi/fleetapi.go` does **not** perform the
  live-data overlay upstream's identically-named `get_api_system_status` does (nine
  fields — `nominal_full_pack_energy`, `nominal_energy_remaining`, `max_charge_power`,
  `max_discharge_power`, `max_apparent_power`, `grid_services_power`, `system_island_state`,
  `available_blocks`/`blocks_controlled`, `solar_real_power_limit` — computed from live
  site/battery/config data upstream, left as stub placeholders in gopowerwall today). See
  `docs/parity-matrix.md` §3.2 for the full field-by-field table and citations.
- **`GoOffGrid` and `ReconnectGrid` are not reachable through the proxy's `/control/*`
  surface, and — corrected in this pass — gopowerwall's own TEDAPI backend does not
  implement them either.** The `Powerwall` facade exposes both (`powerwall.go`), but
  `backend/tedapi/tedapi.go:923-931` is an unconditional `backend.ErrUnsupported` for
  both — this document previously claimed "the TEDAPI backend implements them," which was
  never true of gopowerwall. **Upstream's TEDAPI backend, however, now does implement both
  for v1r transport** (`pypowerwall/tedapi/__init__.py`'s `go_off_grid()`/`reconnect_grid()`,
  calling `send_island_mode()` in `tedapi_v1r.py:486-516` to send Tesla's signed
  `setIslandModeRequest` and physically open/close the grid contactor — added by PR #379
  after this project's `docs/parity-matrix.md` was first audited). Confirmed: upstream's
  proxy (`proxy/server.py`) still exposes no equivalent `/control/*` route for either, so
  `proxy/control.go`'s `dispatchControl` lacking an `off_grid`/`reconnect_grid` case
  remains correct parity at the proxy layer — the gap is entirely in `backend/tedapi`
  itself not implementing the v1r command upstream now has. See
  `docs/parity-matrix.md` §1 items 34-35 and §4 for full detail and citations.

## Superseded finding: cloud and FleetAPI writes no longer no-ops — but grid charging now has a worse bug

**This section originally reported that every settings write in `cloud`/`fleetapi` mode
was an unconditional no-op. Re-reading the current working tree while resolving parity
questions against pypowerwall's source (see `docs/parity-matrix.md`'s 2026-09-07 second
pass) shows that finding is now stale: the writes are real.**

- `PyPowerwallCloud.postAPIOperation`/`PyPowerwallFleetAPI.postAPIOperation`
  (`backend/cloud/cloud.go:558-577`, `backend/fleetapi/fleetapi.go:462-481`) now delegate
  to `postBackupReserve`/`postOperationMode` helpers that send real
  `POST api/1/energy_sites/{site_id}/backup` / `.../operation` requests, matching
  upstream's `battery.set_backup_reserve_percent(...)`/`set_operation(...)`
  (`pypowerwall_cloud.py:1216-1224`) and `fleet.set_battery_reserve(...)`/
  `set_operating_mode(...)` (`fleetapi.py:709-729`).
- `SetGridCharging`/`SetGridExport` in both backends
  (`backend/cloud/cloud.go:785-830`, `backend/fleetapi/fleetapi.go:688-733`) likewise now
  send real `POST api/1/energy_sites/{site_id}/grid_import_export` requests, matching
  upstream's `ENERGY_SITE_IMPORT_EXPORT_CONFIG` endpoint
  (`pypowerwall_cloud.py:878-916`, `fleetapi.py:733-772`).

**However, a new and more severe bug was found in the same code path (see
`docs/parity-matrix.md` §1 item 30 and §4's `SetGridCharging` row for full detail):
`SetGridCharging` writes the `disallow_charge_from_grid_with_solar_installed` field
*without negating it*, and `GetGridCharging` reads a nonexistent `response.grid_charging`
field instead of `response.components.disallow_charge_from_grid_with_solar_installed`.**
Upstream negates on both the write (`mode=True` → `disallow_charge_from_grid_with_solar_installed: False`)
and the read (`return not state`) on every backend that implements it — cloud
(`pypowerwall_cloud.py:878-891,920-923`), FleetAPI (`fleetapi.py:733-754,694-698`), and
TEDAPI/v1r (`pypowerwall_tedapi.py:851-870`, a local config write, not a GraphQL mutation
as previously guessed) — because the wire field is phrased as a prohibition
("disallow"), not as the enable flag the public `set_grid_charging(mode)`/
`get_grid_charging()` names imply. gopowerwall's cloud and fleetapi backends forward
`mode` to that field verbatim and unnegated, and read a field name Tesla's API does not
use at all. Net effect: `gopowerwall set --cloud --gridcharging on` now sends a real HTTP
request that sets the *opposite* of what the user asked for
(`disallow_charge_from_grid_with_solar_installed: true` — i.e. charging from the grid
gets *disabled*, not enabled), and `GetGridCharging` will report "not found" against a
real gateway every time rather than reflecting the actual setting. Because this write now
reaches a real Tesla site (per the correction above), **this is the more urgent of the two
findings** — a caller trusting the "success" response would believe they enabled grid
charging while actually disabling it. `README.md`'s connection-modes status table and the
grid-charging example there should be re-checked against this: the write is real now (not
a no-op as previously documented), but the specific `gridcharging` example is inverted
until the field-name/negation fix lands. `commands/set.go`'s reserve/mode/grid-export
paths were not found to have the same class of bug in this pass.

## Known issues

These three are confirmed by reading the implementation, not merely suspected:

1. **`Powerwall.Strings` vitals field names are confirmed wrong, not merely unverified.**
   `Strings` (`powerwall.go:1042-1071`) builds its per-string keys from
   `PVAC_Vsolar<label>`/`PVAC_Isolar<label>`/`PVAC_Psolar<label>` (label ∈ {A,B,C,D}).
   These names appear nowhere in the vendored `.proto` definitions under `proto/` (vitals
   field names arrive at runtime as `DeviceVital` name strings, not typed protobuf fields),
   **and, confirmed by reading `pypowerwall/tedapi/__init__.py:1032-1069` directly, they
   also appear nowhere in any field pypowerwall's TEDAPI backend produces.** Upstream
   instead produces `PVAC_PvState_{n}`, `PVAC_PVMeasuredVoltage_{n}`, `PVAC_PVCurrent_{n}`
   (no "Measured" — an upstream internal inconsistency), and `PVAC_PVMeasuredPower_{n}`,
   where `n` is a **letter** `A`-`F` (PW3 has 6 strings), not a number, plus a separate
   `PVS_String{n}_Connected` boolean on a sibling `PVS--` device. `Powerwall.strings()`
   (`pypowerwall/__init__.py:497-549`) re-keys these to a short letter-plus-device-index
   scheme (e.g. `"A"`, `"B1"`) with values `{Current, Power, Voltage, State, Connected}` —
   entirely unlike gopowerwall's `"<device>_<label>"` keys with `{Connected: true
   (hardcoded), Voltage, Current, Power}` and no `State`. Notably, `backend/tedapi`'s own
   GraphQL query text (`backend/tedapi/queries/V2024_06.json`, `V2026_06.json`) **already
   requests the correct upstream field names** (`PVAC_PVCurrent_A..D`,
   `PVAC_PVMeasuredVoltage_A..D`, `PVS_StringA..D_Connected`, and for PW3,
   `PCH_PvVoltageA..F`/`PCH_PvCurrentA..F`/`PCH_PvState_A..F`) — the raw wire data reaches
   gopowerwall today, but no `.go` file under `backend/tedapi/` parses any of it into named
   fields (confirmed by grep). Against any gateway, `/strings` reads all-zero
   voltage/current/power with `Connected` always reported `true`, unconditionally — not
   "may return all zeros" depending on hardware specifics as previously framed. See
   `docs/parity-matrix.md` §1 item 9, §3.1's `/strings` row, and correction #7 for full
   detail and citations. Fix: parse `PVAC_Logging`/`pch` component signals in
   `backend/tedapi` into the upstream field names above, and reconsider whether
   `Powerwall.Strings()`'s public shape should move to match one of upstream's two
   conventions.

2. **`backend/local`'s cache does not distinguish raw and parsed reads of the same
   endpoint.** `PyPowerwallLocal.Poll` (`backend/local/local.go`) caches under the bare
   `api` string regardless of the `raw` flag: a JSON-decoded response is stored with
   `l.cache.Set(api, parsed)` and a raw-byte response with `l.cache.Set(api, data)`, both
   under the same key. `pkgs/cache.ResponseCache` (`pkgs/cache/*.go`) has no concept of a
   raw/parsed dimension in its key. If two callers interleave a raw poll (e.g.
   `Powerwall.PollRaw`/`SystemStatus`) and a non-raw poll (e.g. `Powerwall.Poll`) of the
   same endpoint such as `/api/system_status`, whichever cached first can be handed back to
   the caller expecting the other shape — a `[]byte` where `any`(map) was expected, or vice
   versa — with no error signaled.

3. **`backend/cloud`'s Tesla Owner API URL has no per-instance override.**
   `cloud.TeslaOwnerURL` (`backend/cloud/cloud.go`) is a package-level `const` used
   directly at every call site (`/api/1/products`, `/api/1/energy_sites/.../live_status`,
   `/api/1/energy_sites/.../site_info`). There is no config field or option to override it.
   `backend/fleetapi`, by contrast, reads `fleet_api_url` out of its
   `.pypowerwall.fleetapi` config file (`fleetapi.go`) and falls back to
   `BaseURL(region)` only if that key is absent. This asymmetry makes the cloud backend
   harder to point at a test double and gives operators no way to work around a Tesla
   endpoint change or regional redirect without a code change.

## Corrected finding: `PW_TEDAPI_AUTH_MODE` mirrors a real upstream feature — it is a missing implementation, not vestigial configuration

**This section previously implied `auth_mode` might be a no-op or vestigial concept
upstream too, since gopowerwall's own handling of it does nothing. Reading
`pypowerwall/tedapi/auth_mode.py` and its call sites directly (per
`docs/parity-matrix.md`'s 2026-09-07 second pass) shows that is not the case: upstream's
`auth_mode` is a real, actively-used, security-relevant transport selector, landed by PR
#359 ("Add bearer auth mode") and present at both commits this project has audited
(`a3b327be3`/v0.17.2 and the current `main`/v0.17.3) — it is not a recent or unstable
addition.**

`pypowerwall/tedapi/auth_mode.py` defines `AuthMode.BASIC` (HTTP Basic Auth to the
gateway's Wi-Fi IP only) and `AuthMode.BEARER` (POSTs `/api/login/Basic` for a Bearer
token and wraps every subsequent query in a protobuf `AuthEnvelope`; also works over the
wired LAN IP; PW2/solar-only, not PW3). `pypowerwall/tedapi/__init__.py` branches on
`self.auth_mode == AuthMode.BEARER` at more than half a dozen call sites to change how
requests are authenticated and how responses are unwrapped (bearer login/logout at
`:1254-1330`, envelope handling at `:1358-1767`).

`backend/tedapi.Client` stores its configured `authMode` field (set from
`TEDAPIAuthMode`/`PW_TEDAPI_AUTH_MODE`/`gopowerwall.WithTEDAPIAuthMode`, default `basic`)
but never reads it back anywhere in `backend/tedapi/*.go` (confirmed by grep for
`.authMode` across the package, re-confirmed in this pass). `Client.PostTEDAPI` always
authenticates with `req.SetBasicAuth("teg", c.gwPwd)` regardless of what `authMode` holds
— there is no branch that would POST `/api/login/Basic` or wrap requests in an
`AuthEnvelope` instead. Setting `PW_TEDAPI_AUTH_MODE=bearer` or calling
`WithTEDAPIAuthMode(gopowerwall.AuthModeBearer)` currently changes nothing observable.

**This is therefore a missing feature, not dead configuration safe to remove**: bearer
mode is upstream's documented way to authenticate to a PW2/solar-only gateway over a
*wired* LAN connection (Basic mode is Wi-Fi-only), so gopowerwall cannot currently
replicate that connectivity path at all. Implementing it requires: a `/api/login/Basic`
POST to obtain and cache a Bearer token (~1h lifetime per upstream's comment,
`__init__.py:89`), and wrapping the existing protobuf request/response bodies in the
`AuthEnvelope` message (`combined_pb2.AuthEnvelope`, `externalAuth.type =
EXTERNAL_AUTH_TYPE_PRESENCE`) instead of sending them bare.


## Corrupt recorded fixtures

Four files under `proxy/web/bogus/` are failed captures rather than valid gateway
responses, inherited from pypowerwall's own fixture directory:

| File | Contents |
|---|---|
| `api.meters.readings.json` | 8 bytes, the literal text `TIMEOUT!` |
| `api.site_info.grid_codes.json` | 8 bytes, the literal text `TIMEOUT!` |
| `api.system.networks.json` | 8 bytes, the literal text `TIMEOUT!` |
| `api.solars.brands.json` | 5333 bytes of JSON array truncated mid-string |

Nothing serves them at runtime and no test uses them, so they are inert. They are
recorded here because they look authoritative and will mislead anyone reaching for
a fixture. Re-capture them from a real gateway or the simulator before use.

## Lower-priority housekeeping

- `.golangci.yml` still carries lint-exclusion rules for `pkgs/lockmetrics/lockmetrics.go`,
  `dynamodb/streams_ops.go`, and `dynamodb/expr/parser.go` — paths that exist in the sibling
  gopherstack project this tooling was ported from (per the "Add the gopherstack
  engineering tooling kit" commit) but not in gopowerwall. They match nothing here, so
  they're harmless, but worth pruning the next time that file is touched.
- `Makefile`'s `integration-test` target and `.testcoverage.yml`'s exclusion list both
  reference `./test/integration/...` and a `^test/` path prefix, but no `test/` directory
  exists anywhere in this tree, so `make integration-test` currently has nothing to run.
  `total-coverage` already guards for this (it checks `test -d test/integration` and skips
  with a message otherwise), but `integration-test` itself does not have the same guard. As
  with the coverage target, it's unclear whether an integration suite was planned and never
  added, or the tooling kit's target was ported over unchanged from gopherstack — flagging
  for awareness rather than asserting either way.

## `GetTimeRemaining` hard-codes 0.0 in cloud and FleetAPI mode instead of calling Tesla

`backend/cloud/cloud.go:730-735` and the fleetapi equivalent return `0.0` unconditionally,
citing a "DESIGN.md invariant" in the code comment. Reading upstream directly
(`pypowerwall_cloud.py:596-611`, `pypowerwall_fleetapi.py:372-380`) shows both make a real
API call — `GET api/1/energy_sites/{site_id}/backup_time_remaining` (cloud) /
`self.fleet.get_backup_time_remaining()` (FleetAPI) — and return the live
`time_remaining_hours` value, falling back to `0.0` only in the narrow case where a
well-formed response lacks that key. This is a confirmed, real gap: a cloud/FleetAPI user
of `gopowerwall get --cloud` (or the `/pw/get_time_remaining` proxy route) always sees
`0.0` regardless of the site's actual state, where pypowerwall would show the real number.
See `docs/parity-matrix.md` §4's `GetTimeRemaining` row.

## Note on the README

The README's connection-modes status table originally read "Implemented for read/write
operation" for both `cloud` and `fleetapi`, then was corrected to say writes are accepted
and reported successful but not forwarded to Tesla. **That correction is now itself stale**:
re-reading the current working tree (see "Superseded finding" above) shows
`SetReserve`/`SetMode`/`SetGridCharging`/`SetGridExport` now send real HTTP requests to
Tesla in both `cloud` and `fleetapi` mode. The README should be re-checked against this —
but note the new grid-charging negation bug documented above means the write, while now
real, sets the *opposite* of the requested grid-charging state until that bug is fixed.
(README.md is outside this document's ownership for this pass and was not edited here.)
