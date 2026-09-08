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

- **`/fans/pw` is not implemented.** No handler exists for it anywhere in
  `proxy/routes.go`, `proxy/handlers.go`, or `proxy/control.go` (confirmed by grep across
  the package); the README's route list already calls this out as the one intentionally
  missing route from the pypowerwall-compatible surface.
- **Cloud, FleetAPI, and TEDAPI modes return canned data, not live gateway data, for
  several `/api/*` endpoints.** `backend/stubs/stubs.go` defines fixed JSON payloads
  (`MockPowerwalls`, `MockMetersSite`, `MockMeters`, `MockSitemaster`, `MockCustomer`,
  `MockInstaller`, `MockNetworks`, `MockAuthToggle`, `MockUpdate`, `MockSolars`) and two
  templates (`MetersAggregatesStub`, `SystemStatusStub`). All three non-local backends
  (`backend/cloud/cloud.go`, `backend/fleetapi/fleetapi.go`, `backend/tedapi/tedapi.go`)
  wire these into their `pollAPIMap` for `/api/powerwalls`, `/api/meters/site`,
  `/api/meters`, `/api/sitemaster`, `/api/customer`, `/api/installer`, `/api/networks`,
  `/api/auth/toggle/supported`, `/api/system/update/status`, and `/api/solars`, and use the
  two stub templates as a base for `/api/meters/aggregates` and `/api/system_status`,
  overlaying whatever real fields each API does expose. This appears to be a deliberate
  design choice mirroring how pypowerwall backfills gateway-only introspection endpoints
  that have no cloud or Fleet API equivalent, but the exact field-for-field shape has not
  been checked against pypowerwall's own stub values, so treat the specific JSON as
  unverified rather than confirmed-compatible.
- **`GoOffGrid` and `ReconnectGrid` are not reachable through the proxy's `/control/*`
  surface.** The `Powerwall` facade exposes both (`powerwall.go`), and the TEDAPI backend
  implements them, but `proxy/control.go`'s `dispatchControl` only recognizes `reserve`,
  `mode`, `grid_charging`, `grid_export`, and `max_backup` actions — there is no
  `off_grid`/`reconnect_grid` case. Unconfirmed whether pypowerwall's proxy exposes
  equivalent control routes at all; flagging this as a gap to verify rather than asserting
  it is one.

## Major finding: cloud and FleetAPI writes report success without calling Tesla

This is a significant, verified gap beyond the three known issues below, uncovered while
auditing the write path for this document: **every settings write in `cloud` and
`fleetapi` mode is a no-op that unconditionally reports success without making any HTTP
call to Tesla.**

- `PyPowerwallCloud.postAPIOperation` and `PyPowerwallFleetAPI.postAPIOperation` (which
  back `Powerwall.SetReserve`, `SetMode`, and `SetOperation` in these two modes) each just
  log the payload, invalidate the local cache, and `return map[string]any{"status":
  "success"}, nil` — no request is sent to `TeslaOwnerURL` or the FleetAPI base URL.
- `PyPowerwallCloud.SetGridCharging`/`SetGridExport` and
  `PyPowerwallFleetAPI.SetGridCharging`/`SetGridExport` do the same: log and return
  `{"status": "success"}` with no network call at all.

Practical effect: `gopowerwall set --cloud --mode self_consumption --reserve 20`, and the
README's own `gopowerwall set --cloud --gridcharging on` example, run without error and
print a success message, but do not change anything on the actual Tesla site in cloud or
FleetAPI mode today. `GetReserve`/`GetMode`/`GetGridCharging`/`GetGridExport` (the read
side) do make real calls and reflect the gateway's actual last-known configuration, so a
read immediately after one of these "successful" writes will show the value unchanged —
that's the most direct way to notice this in practice. Local and TEDAPI/v1r mode writes are
real; this affects `cloud` and `fleetapi` specifically. The README's status table has been
corrected to reflect this (see the note at the end of this document).

## Known issues

These three are confirmed by reading the implementation, not merely suspected:

1. **`Powerwall.Strings` vitals field names are unverified against real hardware.**
   `Strings` (`powerwall.go`) builds its per-string keys from
   `PVAC_Vsolar<label>`/`PVAC_Isolar<label>`/`PVAC_Psolar<label>` (label ∈ {A,B,C,D}).
   These names appear nowhere in the vendored `.proto` definitions under `proto/`, because
   vitals field names arrive at runtime as `DeviceVital` name strings from the gateway, not
   as typed protobuf fields — there is nothing in the schema to check them against.
   pypowerwall's own implementation appears to use a different naming scheme entirely
   (`PVAC_PVMeasuredVoltage`/`PVAC_PVCurrent`), which has no equivalent field in what
   gopowerwall reads either. Against a real gateway, `/strings` may return all zeros. The
   loop-index bug that used to make this worse (see `CHANGELOG.md`) is already fixed; what
   remains is unrelated to that bug and needs checking against physical hardware.

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

## Additional finding: `PW_TEDAPI_AUTH_MODE` / `WithTEDAPIAuthMode` has no effect

`backend/tedapi.Client` stores its configured `authMode` field (set from
`TEDAPIAuthMode`/`PW_TEDAPI_AUTH_MODE`/`gopowerwall.WithTEDAPIAuthMode`, default `basic`)
but never reads it back anywhere in `backend/tedapi/*.go` (confirmed by grep for
`.authMode` across the package). `Client.PostTEDAPI` always authenticates with
`req.SetBasicAuth("teg", c.gwPwd)` regardless of what `authMode` holds — there is no branch
that would send a `bearer` token instead. Setting `PW_TEDAPI_AUTH_MODE=bearer` or calling
`WithTEDAPIAuthMode(gopowerwall.AuthModeBearer)` currently changes nothing observable.


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

## Note on the README

The README's connection-modes status table originally read "Implemented for read/write
operation" for both `cloud` and `fleetapi`. That line has been corrected to reflect the
write no-op finding above: reads are implemented and real; writes are accepted and reported
as successful but are not forwarded to Tesla in either mode.
