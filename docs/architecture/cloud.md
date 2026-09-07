# cloud mode

Implementation: `backend/cloud/cloud.go` (`PyPowerwallCloud`).

## Transport

Plain HTTPS JSON calls to the Tesla Owner API, `cloud.TeslaOwnerURL`
(`https://owner-api.teslamotors.com`), a hardcoded constant with no per-instance override —
see [MISSING.md](../../MISSING.md#known-issues) (known issue 3) if you need to point this
at a different endpoint or a test double.

## Auth model

No login step of its own: `Authenticate` reads a pre-existing `.pypowerwall.auth` file
(`AuthFile`) from `AuthPath`/`PW_AUTH_PATH`, expecting `{"<email>": {"access_token":
"...", ...}, ...}`. If `Email`/`PW_EMAIL` is unset or left at its default
(`nobody@nowhere.com`), gopowerwall picks an arbitrary key out of the file
(`anyKey`) rather than failing — useful for a single-account auth file, misleading for a
multi-account one. The resolved access token is sent as `Authorization: Bearer <token>` on
every call. If `SiteID`/`PW_SITEID` is unset, `Authenticate` either reads a cached
`.pypowerwall.site` file or calls `GET /api/1/products` and picks the first
`energy_site_id`/`id` it finds, caching that choice to `.pypowerwall.site` for next time.

gopowerwall does not create `.pypowerwall.auth` itself — see
[MISSING.md](../../MISSING.md#cli-subcommands-that-only-print-guidance-text): the CLI's
`authtoken` command, which would drive the OAuth2 exchange that produces this file, only
prints the Tesla authorize URL today. The file has to come from elsewhere (e.g. from
running pypowerwall's own `setup`).

## What it can read

`GET /api/1/energy_sites/<site>/live_status` and `.../site_info` back most of the
`Powerwall` facade's typed accessors (`SystemStatus`, `SOE`, `GridStatusResponse`,
`SiteInfo`, `Power`) — mapped by `getSiteData`/`getSiteConfig` into the JSON shapes the
`/api/...` poll map expects, cached for the site config specifically for `siteConfigTTL`
(59s). Grid status, state of charge, and site info are backed by live cloud data.

## What it cannot read (or fakes)

- `Vitals` unconditionally returns an empty map — the Tesla Owner API has no vitals
  equivalent, so `Powerwall.Strings`/`Temps`/`Alerts` (which all derive from `Vitals`) are
  effectively empty in cloud mode.
- `GetTimeRemaining` always returns `0.0` rather than `nil`/unknown — a deliberate
  documented invariant in the code (a comment references a `DESIGN.md` that does not exist
  in this tree, so the invariant itself is confirmed but its original design rationale
  document is not present to check against).
- The same stub/canned-data endpoints described in
  [MISSING.md](../../MISSING.md#proxy-routes) apply here: `/api/powerwalls`,
  `/api/meters/site`, `/api/meters`, `/api/sitemaster`, `/api/customer`, `/api/installer`,
  `/api/networks`, `/api/auth/toggle/supported`, `/api/system/update/status`,
  `/api/solars`, plus a stub-based `/api/system_status` with `battery_blocks` always forced
  to an empty slice (`getAPISystemStatus`).

## Writing

**Reserve, mode, grid-charging, and grid-export writes do not reach Tesla in this mode.**
`postAPIOperation` (backing `SetReserve`/`SetMode`/`SetOperation`) and `SetGridCharging`/
`SetGridExport` all log the requested change, invalidate the local cache, and return
`{"status": "success"}` without ever issuing an HTTP request. This is a significant,
verified gap — see
[MISSING.md](../../MISSING.md#major-finding-cloud-and-fleetapi-writes-report-success-without-calling-tesla)
— and means a caller cannot currently distinguish a real write from this no-op by return
value alone; the only way to notice is that a subsequent read (`GetReserve`, `GetMode`,
`GetGridCharging`, `GetGridExport`, which are real reads) shows the value unchanged.

## When to choose it

Use `cloud` when the gateway is not reachable on the local network at all and you already
have a valid `.pypowerwall.auth` file, and only for read-oriented use cases given the write
caveat above. `fleetapi` (see [fleetapi.md](fleetapi.md)) is Tesla's currently-supported
official API and shares the same write limitation; prefer it over `cloud` for anything new
unless you specifically need the unofficial Owner API's read surface.
