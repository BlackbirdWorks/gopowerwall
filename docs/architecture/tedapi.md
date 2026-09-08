# tedapi mode

Implementation: `backend/tedapi/tedapi.go` (`Client` + `PyPowerwallTEDAPI`),
`backend/tedapi/queries.go`, `backend/tedapi/system_info.go`. This page covers the WiFi-AP
transport; for the RSA-signed wired-LAN variant see [v1r.md](v1r.md) — both share this same
`Client`/`PyPowerwallTEDAPI` facade, just with a different transport underneath.

## Transport

Protobuf over HTTPS `POST https://<host>/tedapi/v1`, where `host` defaults to the gateway's
own WiFi access point address, `tedapi.DefaultGWIP` (`192.168.91.1`), when unset. The
request/response bodies are wire-format protobuf `tedapi.Message`s (vendored proto:
`proto/tedapi/tedapi.proto`), gzip-decompressed automatically when the gateway sends a
gzip-magic-byte response (firmware 25.42.2+). As with local mode, TLS certificate
verification is intentionally skipped for this self-signed endpoint.

## Auth model

HTTP Basic auth on every request: username `"teg"`, password `GwPwd` — the **full** gateway
WiFi password, not the 5-character customer password local mode uses (`--gw_pwd`/
`PW_GW_PWD`/`gopowerwall.WithGwPwd`). There is no separate login step; every `PostTEDAPI`
call carries the credential.

`TEDAPIAuthMode`/`PW_TEDAPI_AUTH_MODE`/`WithTEDAPIAuthMode` (`basic` or `bearer`, default
`basic`) is accepted and stored on the client but currently has no effect: `PostTEDAPI`
always sends HTTP Basic auth regardless of its value — see
[MISSING.md](../../MISSING.md#additional-finding-pw_tedapi_auth_mode--withtedapiauthmode-has-no-effect).

## Query versions

`TEDAPIApiVersion`/`PW_TEDAPI_API_VERSION`/`WithTEDAPIApiVersion` selects between
`V2024_06` and `V2026_06` (default `V2024_06`), which changes which GraphQL-style query
text `queries.go`'s `GetQuery` sends for a given role (e.g.
`QueryRoleDeviceControllerBasic`) — the gateway firmware version in the field determines
which query set it understands.

## What it can read

- **Config** (`GetConfig`): reads `config.json` off the gateway's file store via a TEDAPI
  `FileStoreAPIReadFileRequest`-style message (over the legacy WiFi protobuf envelope when
  no v1r transport is attached), cached for `configTTL` (the same `PWCacheExpire` value
  passed to `NewClient`). Firmware version (`GetFirmwareVersion`) is read out of this
  config's `version` field.
- **Status** (`GetStatus`): a GraphQL-style query executed via `execGraphQL`, cached under
  key `"status"`.
- Device vitals, per-string solar data, and battery-block detail, surfaced through
  `PyPowerwallTEDAPI.Vitals`/`Power`/`FetchPower` and consumed by `Powerwall.Vitals`/
  `Strings`/`BatteryBlocks` the same way local mode's vitals are — this is the main reason
  to pick TEDAPI when local-mode credentials aren't available: the customer password isn't
  needed, only the gateway WiFi password.
- Operational writes: `ScheduleMaxBackup`, `CancelMaxBackup`, `GetBackupEvents`,
  `GoOffGrid`, `ReconnectGrid` are all implemented against TEDAPI specifically (see
  `tedapi.go`'s corresponding methods) — these are the only backend that supports them at
  all; `Powerwall`'s facade methods for these return `ErrUnsupported` on every other mode.

## What it cannot read (or fakes)

A number of gateway-introspection endpoints have no TEDAPI equivalent and are served from
`backend/stubs`: `/api/powerwalls`, `/api/meters/site`, `/api/meters`, `/api/customer`,
`/api/installer`, `/api/networks`, `/api/auth/toggle/supported`, `/api/system/update/status`,
`/api/solars`, and `/api/sitemaster` (the last one hardcoded to `MockSitemaster` in
`getAPISiteMaster` rather than derived from any query). `/api/meters/aggregates` and
`/api/system_status` are built from a stub template overlaid with whatever real fields the
TEDAPI status/config queries do provide — see
[MISSING.md](../../MISSING.md#proxy-routes) for the full list and the caveat about their
exact shape being unverified against pypowerwall's own values.

## When to choose it

Use `tedapi` (or `v1r`) when you need TEDAPI-specific data — vitals, per-string solar,
battery-block detail, off-grid/backup-event control — or when you don't have the gateway's
customer password but do have its WiFi password. It normally requires being on the
gateway's own WiFi access point (`192.168.91.1`); for a wired-LAN Powerwall 3 alternative,
see [v1r.md](v1r.md).
