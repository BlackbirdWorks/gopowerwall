# local mode

Implementation: `backend/local/local.go` (`PyPowerwallLocal`).

## Transport

Plain HTTPS directly to the gateway's own REST API at `https://<host>/api/...`, using a
self-signed certificate — `InsecureSkipVerify: true` is set on the `http.Transport`
deliberately (see the parity contract in
[`.agent/rules/powerwall.md`](../../.agent/rules/powerwall.md); this is inherent to the
gateway's protocol, not a shortcut). There is no separate transport library: `local.New`
builds a plain `*http.Client` with a connection-pool size (`PoolMaxSize`) and per-request
timeout (`Timeout`) taken from `gopowerwall.Config`.

## Auth model

Login is `POST /api/login/Basic` with `{"username": "customer", "password": <customer
password>, "email": ..., "clientInfo": {"timezone": ...}}`. The customer password is the
**last 5 characters** of the full gateway WiFi password — this is what `--password`/
`PW_PASSWORD`/`gopowerwall.WithPassword` expects, and it is different from `--gw_pwd`/
`WithGwPwd`, which is the *full* password used by TEDAPI.

Two auth modes, selected by `AuthMode` (`--authmode`/`PW_AUTH_MODE`, default `cookie`):

- **`cookie`** (default): the gateway sets `AuthCookie` and `UserRecord` cookies on a
  successful login; `PyPowerwallLocal` captures both and replays them
  (`Secure`, `HttpOnly`, `SameSite=Strict`) on every subsequent request.
- **`token`**: the login response instead carries a JSON `{"token": "..."}` body, which is
  sent back as `Authorization: Bearer <token>`.

Whichever succeeded is persisted to `CacheFile` (default `.powerwall`, or
`PW_CACHE_FILE`/`gopowerwall.WithCacheFile`) as JSON, so a later `New` call can skip the
login round trip. A `401`/`403` on any subsequent call triggers exactly one re-login
attempt (`handleHTTPStatus`'s non-`recursive` branch) before giving up.

## What it can read

Everything the gateway's REST API exposes, since this mode simply proxies straight through
to it: `/api/status`, `/api/system_status` and its `/soe`/`/grid_status` sub-paths,
`/api/site_info` and `/site_name`, `/api/meters/aggregates`, `/api/operation`, and the rest
of the allow-listed paths in `proxy/constants.go`'s `isAllowlisted`. `Poll` caches
successful reads in `pkgs/cache.ResponseCache` for `PWCacheExpire` and negatively caches
404s for 10 minutes (`negCacheTTL`); a `429`/`503` triggers a 5-minute cooldown
(`cooldownTTL`) during which further calls short-circuit with `backend.ErrRateLimited`.

Device vitals come from a distinct endpoint, `/api/devices/vitals`, decoded as
`teslapower.DeviceListing` protobuf (`proto/teslapower/tesla.proto`) rather than JSON —
`local.go`'s `Vitals` forces `raw = true` for that one path and unmarshals the protobuf
response itself. If the gateway ever 404s that endpoint, `vitalsAPI` latches `false` and
`Vitals` short-circuits with `backend.ErrNotFound` for the rest of the process's life
(no automatic recovery/retry).

## What it cannot read

Nothing endpoint-shaped is deliberately withheld in local mode — it is the most complete
backend. The one caveat that applies here specifically is the caching issue in
[MISSING.md](../../MISSING.md#known-issues): interleaving a raw (`PollRaw`) and non-raw
(`Poll`) read of the same endpoint can return the wrong Go type from cache, because
`ResponseCache` keys purely on the endpoint string.

## Writing

`Post` (`local.go`) issues authenticated `POST`/`PUT` calls the same way `Poll` issues
`GET`s, and invalidates the corresponding cache entries via
`cache.WriteOpReadOpCacheMap` — e.g. writing `/api/operation` invalidates both
`/api/operation` and a `SITE_CONFIG` cache key. `Powerwall.SetOperation`,
`SetReserve`, and `SetMode` all route through this in local mode with no additional
restriction (the 80%-reserve cap that cloud/FleetAPI impose does not apply here).

## Hybrid TEDAPI layering

If `GwPwd` is set *and* `Host` is the gateway's default WiFi AP address
(`tedapi.DefaultGWIP`, `192.168.91.1`, with or without `:443`), `Powerwall.connectLocal`
also opens a TEDAPI client alongside the local session and calls
`localBackend.SetTEDAPIClient`, setting `TEDAPIMode` to `hybrid`. This lets a caller
connected locally over the WiFi AP also pull whatever the local REST API does not expose
directly through the attached TEDAPI client. This only fires for that specific host value;
a LAN-side local connection (any other host) never gets a hybrid TEDAPI client attached
automatically.

## When to choose it

Use `local` whenever the gateway is reachable on the LAN and the customer password is
known — it is the fastest mode (no cloud round trip), has the fullest read/write surface,
and is the only mode with no per-instance auth-file prerequisite (login happens inline).
