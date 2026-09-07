# fleetapi mode

Implementation: `backend/fleetapi/fleetapi.go` (`PyPowerwallFleetAPI`). This is Tesla's
current officially-supported API, as opposed to `cloud` mode's unofficial Owner API — see
[cloud.md](cloud.md) for that alternative.

## Transport

Plain HTTPS JSON calls to a regional FleetAPI base URL: `FleetAPIURLNA`
(`https://fleet-api.prd.na.vn.cloud.tesla.com`, the default from `BaseURL("na")`),
`FleetAPIURLEU`, or `FleetAPIURLCN`, selected by `BaseURL(region)`. Unlike `cloud` mode's
`TeslaOwnerURL` (a fixed constant — see [MISSING.md](../../MISSING.md#known-issues), known
issue 3), the base URL here **is** overridable per instance: `Authenticate` reads
`fleet_api_url` out of the `.pypowerwall.fleetapi` config file and uses it verbatim
(trimming a trailing slash) if present, falling back to the region-derived default only
when that key is absent.

## Auth model

No login step of its own: `Authenticate` reads a pre-existing `.pypowerwall.fleetapi` file
(`ConfigFile`) from `AuthPath`/`PW_AUTH_PATH`, expecting a flat JSON object with at least
`access_token`; `site_id` and `fleet_api_url` are read from the same file when present.
Failure to find an `access_token` key is a hard `backend.ErrLogin`. The access token is sent
as a Bearer token on every call, the same as `cloud` mode.

As with `cloud` mode, gopowerwall does not create this file itself — the CLI's `setup
--fleetapi` and `register` commands, which pypowerwall uses to drive Fleet API client
registration and OAuth, only print guidance text today (see
[MISSING.md](../../MISSING.md#cli-subcommands-that-only-print-guidance-text)). The file
must come from elsewhere.

## What it can read

Structurally identical to `cloud` mode: `getSiteData`/`getSiteConfig` call the FleetAPI
equivalents of the Owner API's live-status/site-info endpoints and feed the same
`Powerwall` typed accessors (`SystemStatus`, `SOE`, `GridStatusResponse`, `SiteInfo`,
`Power`), cached the same way (`siteConfigTTL` = 59s for site config).

## What it cannot read (or fakes)

Identical caveats to `cloud` mode: `Vitals` always returns an empty map,
`GetTimeRemaining` always returns `0.0`, and the same canned-data endpoints from
`backend/stubs` back `/api/powerwalls`, `/api/meters/site`, `/api/meters`,
`/api/sitemaster`, `/api/customer`, `/api/installer`, `/api/networks`,
`/api/auth/toggle/supported`, `/api/system/update/status`, and `/api/solars` — see
[MISSING.md](../../MISSING.md#proxy-routes).

## Writing

**Same no-op caveat as `cloud` mode.** `postAPIOperation` (backing `SetReserve`/`SetMode`/
`SetOperation`) and `SetGridCharging`/`SetGridExport` all log the request, invalidate the
local cache, and return `{"status": "success"}` without making any HTTP call to the
FleetAPI base URL. See
[MISSING.md](../../MISSING.md#major-finding-cloud-and-fleetapi-writes-report-success-without-calling-tesla)
for the full detail; the same "read afterward to notice" caveat applies.

## When to choose it

Use `fleetapi` over `cloud` for anything new: it's the officially-supported Tesla API, and
its base URL is configurable per-instance via the config file's `fleet_api_url` key, which
`cloud` mode has no equivalent for. Both modes share the write-no-op limitation above, so
neither is currently suitable for programmatically changing reserve, mode, or grid
charging/export settings without an external Tesla API integration to actually apply the
change.
