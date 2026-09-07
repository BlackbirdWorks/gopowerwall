# gopowerwall / pypowerwall parity matrix

**Upstream audited:** [jasonacox/pypowerwall](https://github.com/jasonacox/pypowerwall)
`main` @ commit `a3b327be32cd31bb7e297c22e98040c967a86fad` (2026-09-07), module version
string `0.17.2` (`pypowerwall/__init__.py`). Fetched directly via
`raw.githubusercontent.com` on 2026-09-07; every claim below is checked against that
checkout, not against memory of pypowerwall or against gopowerwall's own comments about
pypowerwall. Where a file could not be fetched, the affected row says so explicitly.

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
| 9 | `strings(jsonformat=False, verbose=False)` (`:495`) | `Strings(ctx, _ ...bool) models.SolarStrings` (`powerwall.go:805`) | divergent | See §4 backend capability and the corrected finding under "Corrections to MISSING.md" — the Go `verbose` parameter is accepted but **completely ignored** (`_ ...bool`), so there is no way to get the Python `verbose=True` per-device raw-field view from the Go facade at all. Field-naming scheme also diverges; see below. |
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
| 29 | `get_time_remaining()` (`:1029`) | `GetTimeRemaining(ctx) *float64` (`powerwall.go:1039`) | parity | Both are `None`/`nil`-on-unknown in local/tedapi mode; both hard-code `0.0` for cloud/fleetapi (see §4). |
| 30 | `set_grid_charging(mode) -> Optional[dict]` (`:1040`) | `SetGridCharging(ctx, mode bool) (models.Operation, error)` (`powerwall.go:1076`) | divergent | Python's `mode` parameter is a loosely-typed `on`/`off`/`True`/`False`/etc. string-or-bool; Go's `bool` is stricter and better. But **only reachable for cloud/tedapi mode in Python** (`local` mode has no `set_grid_charging` on `PyPowerwallLocal` either — see §4); Go's facade also restricts to `ModeCloud`/`ModeFleetAPI` (`powerwall.go:1080-1095`), so the *mode gating* is parity even though neither backend's write actually reaches Tesla in Go (§4). |
| 31 | `get_grid_charging() -> Optional[bool]` (`:1054`) | `GetGridCharging(ctx) *bool` (`powerwall.go:1101`) | parity | |
| 32 | `set_grid_export(mode) -> Optional[dict]` (`:1069`) | `SetGridExport(ctx, mode string) (models.Operation, error)` (`powerwall.go:1126`) | parity (signature); divergent (write reaches Tesla) — see §4 | |
| 33 | `get_grid_export() -> Optional[str]` (`:1086`) | `GetGridExport(ctx) *string` (`powerwall.go:1159`) | parity | |
| 34 | `go_off_grid(confirm=False) -> Optional[dict]` (`:1101`) | `GoOffGrid(ctx, confirm bool) (models.Operation, error)` (`powerwall.go:1229`) | **parity (both are non-functional stubs)** | See "Corrections to MISSING.md" — upstream's `go_off_grid` does `hasattr(self.client, 'go_off_grid')` (`__init__.py:1131`) and **no backend class in pypowerwall defines `go_off_grid`** (confirmed: `grep -n "def go_off_grid" pypowerwall/tedapi/__init__.py pypowerwall/local/pypowerwall_local.py pypowerwall/cloud/pypowerwall_cloud.py pypowerwall/fleetapi/pypowerwall_fleetapi.py` returns nothing), so it always logs `"go_off_grid is not supported by %s backend"` and returns `None`. Go's only implementation is likewise `backend/tedapi/tedapi.go:924`, an unconditional `return models.Operation{}, backend.ErrUnsupported`. **Both are dead code end-to-end.** MISSING.md's claim that "the TEDAPI backend implements them" is incorrect. |
| 35 | `reconnect_grid() -> Optional[dict]` (`:1136`) | `ReconnectGrid(ctx) (models.Operation, error)` (`powerwall.go:1247`) | parity (both non-functional stubs) | Same as item 34; `backend/tedapi/tedapi.go:929` is also an unconditional `ErrUnsupported`. |
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
| `set` (`:512`) | `set` (`cli.go:24`, `commands/set.go`) | partial | Flag surface matches (`-mode`, `-reserve`, `-current`, `-gridcharging`, `-gridexport`); behavior differs in the >80% cloud/fleetapi cap warning: upstream re-polls with `get_reserve(scale=True, force=True)` after the write and prints the *actual* capped value (`__main__.py:806-809`); Go's `applyReserve` (`commands/set.go:85-97`) only logs a warning, never re-reads to confirm the actual applied value. Also inherits the local-mode clobber bug from Library API item 22. |
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
| `GET /strings` | `:1764` | `proxy/routes.go:434` (`handleStrings`) | **divergent** | Both wrap `pw.strings(jsonformat=True)`, but the underlying data shape differs — see §1 item 9 and §4: upstream's `strings()` (`__init__.py:495-579`) builds keys like `"A"`, `"B1"` from the *last character* of matched vitals field names plus a rotating device index, with values `{Current, Power, Voltage, State, Connected}`; Go's `Strings()` (`powerwall.go:805`) fabricates keys as `"<PVAC device name>_<A|B|C|D>"` with values `{Connected, Voltage, Current, Power}` (no `State`) sourced from a field-naming scheme (`PVAC_Vsolar<label>`) that neither upstream backend produces (see §4). |
| `GET /temps` | `:2047` | `proxy/routes.go:466` | parity | |
| `GET /temps/pw` | `:2050` — `PW{N}_temp` | `proxy/routes.go:473`/`603` (`generatePWTemps`) — `PW{N}_temp` | parity | Field naming matches exactly. |
| `GET /alerts` | `:2064` | `proxy/routes.go:479` | parity | |
| `GET /alerts/pw` | `:2067` — `{alert: 1}` | `proxy/routes.go:486`/`621` (`generatePWAlerts`) — `{alert: 1}` | parity | |
| `GET /freq` | `:2080` | `proxy/routes.go:188` (`generateFreq`) | parity | Field names match exactly: `PW{N}_name`, `PW{N}_PINV_Fout`, `PW{N}_PINV_VSplit1/2`, `PW{N}_PackagePartNumber/SerialNumber`, `PW{N}_p_out/q_out/v_out/f_out/i_out`, plus `ISLAND*`/`METER*` passthrough from `TESYNC`/`TEMSA` vitals devices, and `grid_status` (numeric). |
| `GET /pod` | `:2126` | `proxy/routes.go:230` (`generatePOD`) | **divergent — Go is missing the vitals-augmentation pass** | Upstream builds the per-block fields from `system_status` (`PW{N}_p_out`, etc. — matches Go), **then augments** from any `TEPOD`-prefixed vitals device (`:2196-2244`), overwriting `PW{N}_name`, `PW{N}_POD_ActiveHeating`, `ChargeComplete`, `ChargeRequest`, `DischargeComplete`, `PermanentlyFaulted`, `PersistentlyFaulted`, `enable_line`, `available_charge_power`, `available_dischg_power`, `nom_energy_remaining`, `nom_full_pack_energy`, and a field Go never emits at all: **`PW{N}_POD_nom_energy_to_be_charged`**. Go's `generatePOD` (`proxy/routes.go:230-273`) sets all of those `POD_*` placeholder fields to `nil` unconditionally and never reads `s.PW.Vitals(ctx)` for `TEPOD` devices at all — confirmed by the function body containing no `Vitals` call. On any gateway/mode where `TEPOD` vitals are actually available, gopowerwall's `/pod` is a strict subset of upstream's, missing real (non-null) values upstream would report, and missing the `nom_energy_to_be_charged` key entirely in all cases. |
| `GET /json` | `:2245` | `proxy/routes.go:275` (`generateJSON`) | parity | All 11 fields (`grid,home,solar,battery,soe,grid_status,reserve,time_remaining_hours,full_pack_energy,energy_remaining,strings`) match name-for-name. |
| `GET /version` | `:2293` | `proxy/routes.go:632` | parity | `{"version": "SolarOnly", "vint": 0}` fallback matches exactly. |
| `GET /help` | `:2305` | `proxy/routes.go:539`/`handlers.go:353` | divergent (cosmetic) | Both are an HTML status page; upstream's is a much richer live-refreshing stats table (reusing `proxystats`, including the "Maintenance Mode" notice pointing at `pypowerwall-server`); Go's is 4 lines of static HTML. Not a JSON/field-name parity concern, but a real functional gap for anyone using `/help` as a dashboard. |
| `GET /api/troubleshooting/problems` | `:2379` — `{"problems": []}` | `proxy/routes.go:544` — `{"problems": []}` | parity | |
| `GET /tedapi/config` \| `/status` \| `/components` \| `/battery` \| `/controller` | `:2383-2397` | `proxy/routes.go:649` (`handleTedapiRoute`) | **partial** | Upstream implements all five sub-routes (`get_config`, `get_status`, `get_components`, `get_battery_blocks`, `get_device_controller`). Go's `handleTedapiRoute` only implements `/tedapi/config`; `/tedapi/status`, `/tedapi/components`, `/tedapi/battery`, `/tedapi/controller` fall through to the generic "Use /tedapi/config, /tedapi/status, ..." error message instead of returning data, even though `backend/tedapi` has `GetStatus` (`backend/tedapi/tedapi.go:322`) available to wire up. |
| `GET /cloud/battery` \| `/power` \| `/config` | `:2399-2410` | *(absent)* | missing | No equivalent route group exists anywhere in `proxy/routes.go` or `proxy/control.go`. |
| `GET /fleetapi/info` \| `/status` | `:2412-2421` | *(absent)* | missing | Same — no equivalent. |
| `GET /fans` (raw fan speeds) | `:2483` — `pw.tedapi.get_fan_speeds()` passthrough | *(absent)* | **missing** | **Correction to MISSING.md: this route is missing too, not just `/fans/pw`.** No `/fans` handler exists anywhere in `proxy/routes.go`/`proxy/control.go` (confirmed by grep). |
| `GET /fans/pw` | `:2488-2500` — quoted below | *(absent)* | **missing** | Resolves MISSING.md's open question (a): upstream's exact behavior is: <br>`fans = {}`<br>`for i, (_, value) in enumerate(sorted(fan_speeds.items())):`<br>&nbsp;&nbsp;`key = f"FAN{i+1}"`<br>&nbsp;&nbsp;`fans[f"{key}_actual"] = value.get("PVAC_Fan_Speed_Actual_RPM")`<br>&nbsp;&nbsp;`fans[f"{key}_target"] = value.get("PVAC_Fan_Speed_Target_RPM")`<br>i.e. keys `FAN1_actual`, `FAN1_target`, `FAN2_actual`, ... sorted by the underlying fan-speed dict's keys, sourced from `pw.tedapi.get_fan_speeds()` (TEDAPI-only; empty `{}` if `pw.tedapi` is falsy). gopowerwall has neither `/fans` nor `/fans/pw`, and `backend/tedapi` has no `get_fan_speeds`/`GetFanSpeeds` equivalent at all (not found by search) — this needs new TEDAPI protobuf-field extraction work, not just a route. |
| `POST /control/reserve` | `:2429`/POST body at `:1372-1460` | `proxy/control.go:112`/`128` | parity | Including the "companion `mode` parameter" optimization to avoid double-writing (`errInvalidMode` path, both sides). |
| `POST /control/mode` | `:2437`/`1487-1524` | `proxy/control.go:174` | parity | Including the companion `level` parameter. |
| `POST /control/grid_charging` | `:2445`/`1525-1544` | `proxy/control.go:220` | divergent (minor) | Same success shape (`{"grid_charging": "Set Successfully"}`), but the **no-value (read)** branch differs subtly: Python's `'{"grid_charging": %s}' % ("true" if ... else "false")` is a raw string substitution that always yields valid JSON `true`/`false`; Go's `json.NewEncoder(w).Encode(map[string]any{keyGridCharging: res})` where `res` is `*bool` correctly encodes `null` when unknown — Go is actually more correct here (upstream's `safe_pw_call(...) ` returning `None` would still coerce to the string `"false"`, silently misreporting "off" when the true state is unknown). |
| `POST /control/grid_export` | `:2453`/`1545-1567` | `proxy/control.go:247` | **divergent — upstream can emit invalid JSON** | Upstream's no-value branch: `'{"grid_export": %s}' % (str(safe_pw_call(pw_control.get_grid_export)).lower() or "false")`. If `get_grid_export()` returns `None`, `str(None).lower()` is the string `"none"` (not `"null"`), which is **not valid JSON** — `{"grid_export": none}` would fail to parse as valid JSON in a strict parser. Go's equivalent (`proxy/control.go:249`, `json.NewEncoder(w).Encode(map[string]any{keyGridExport: ge})` with `ge *string`) always emits well-formed `null`. This is a case where gopowerwall is *more correct* than upstream, not less — worth keeping, not "fixing to match". |
| `POST /control/max_backup` | `:2462`/`1592-1626` | `proxy/control.go:273` | parity | Same three sub-behaviors (get current events / cancel / schedule-N-seconds), same v1r-required gate, same response shapes. |
| `POST /control/off_grid`, `/control/reconnect_grid` | *(does not exist upstream — confirmed by exhaustive grep of `request_path ==`/`.startswith` conditions in `server.py`, list reproduced above; only `reserve`, `mode`, `grid_charging`, `grid_export`, `max_backup` actions exist)* | *(absent — `proxy/control.go:108-126`'s `dispatchControl` switch has no `off_grid`/`reconnect_grid` case)* | **n/a (deliberate) — resolves MISSING.md's open question (this is parity, not a gap)** | MISSING.md flagged this as "unconfirmed whether pypowerwall's proxy exposes equivalent control routes at all." Confirmed: it does not. Since §1 item 34/35 also established that `go_off_grid`/`reconnect_grid` are non-functional dead code on the *library* side of pypowerwall too, there is genuinely nothing to expose. |
| `GET /pw/*` (library-function passthrough) | `:2500-2549` — `simple_mappings` dict, 23 keys | `proxy/handlers.go:51-124` (`lookupPWFacingSensor`/`System`/`Control`) | parity | All 23 upstream keys (`level, power, site, solar, battery, battery_blocks, load, grid, home, vitals, temps, strings, din, uptime, version, status, system_status, grid_status, aggregates, site_name, alerts, is_connected, get_reserve, get_mode, get_time_remaining`) have a matching Go case; response-shape wrapping (e.g. `{"level": ...}` vs bare) matches key-for-key. |
| `GET /stats` | `:1770-1884` | `proxy/handlers.go:247` (`handleStats`) | **divergent** | Both share a common core (`pypowerwall`/`mode`-ish, `gets`, `posts`, `errors`, `timeout`, `uri`, `ts`, `start`, `clear`, `uptime`, `mem`, `site_name`, `cloudmode`, `fleetapi`). Upstream additionally tracks, and Go entirely lacks: `tedapi_mode`, `tedapi_api_version`, `tedapi_auth_mode`, `pw3`, `siteid`/`counter` (cloud/fleetapi only), a `fallback_mode` block (SolarOnly-fallback tracking: `is_fallback_mode`, `fallback_since`, `recovery_attempts`, ...), and a `mem_cache` block with per-cache byte-size accounting (`error_counts`, `network_error_summary`, `degradation_cache`, `performance_cache`, `endpoint_stats`, `total_cache_bytes`, `total_cache_mb`). Go's `config` sub-object also carries a materially different key set (`PW_BIND_ADDRESS, PW_HOST, PW_EMAIL, PW_TIMEZONE, PW_PORT, PW_STYLE, PW_CACHE_EXPIRE, PW_CACHE_TTL, PW_NEG_SOLAR, PW_SITE_ZERO_THRESHOLD` — 10 keys) vs upstream's larger set (`PW_BIND_ADDRESS, PW_PASSWORD*, PW_EMAIL, PW_HOST, PW_TIMEZONE, PW_DEBUG, PW_CACHE_EXPIRE, PW_BROWSER_CACHE, PW_TIMEOUT, PW_POOL_MAXSIZE, PW_HTTPS, PW_PORT, PW_STYLE, PW_SITEID, PW_AUTH_PATH, PW_AUTH_MODE, PW_CACHE_FILE, PW_CONTROL_SECRET*, PW_GW_PWD*, PW_RSA_KEY_PATH, ...` — truncated in this audit at line 330 of `server.py`, more keys likely follow; upstream masks secrets with `"*" * len(...)`, Go's config dump does not appear to mask any secret-shaped values). Not diffed further than this; a full byte-for-byte diff of `/stats` was out of scope for the time available. |
| `GET /stats/clear` | `:1885` | `proxy/routes.go:504` | parity (behavior); same caveat as `/stats` for full field parity | |
| `GET /health` | `:1893+` (not fully quoted in this audit — fetched but not diffed field-by-field beyond the summary) | `proxy/handlers.go:309` (`handleHealth`) | unverified (partial) | Both expose a `connection_health`/health-check concept and proxy-level counters; a full field-by-field diff was not completed in the time available for this audit — treat as unverified rather than parity. |
| `GET /health/reset` | `:2002` | `proxy/routes.go:520` | parity (behavior, not diffed field-by-field) | |
| ALLOWLIST passthrough routes (`GET /api/status`, `/api/site_info`, etc.) | `ALLOWLIST` list, `server.py:173-199`, 26 entries | `isAllowlisted`, `proxy/server.go:25-66`, 33 entries | **divergent — confirmed, not previously flagged in MISSING.md** | Upstream's 26-entry `ALLOWLIST` and Go's `isAllowlisted` switch diverge in two concrete ways: (1) upstream has `/api/system/networks` (exact path); Go instead has `/api/system/networks/conn_tests` — a **different path**, meaning a client polling upstream's exact allowlisted path gets 200 from pypowerwall but would fall through to `handleWeb`/static-file-404 in gopowerwall. (2) upstream allowlists `/api/synchrometer/ct_voltage_references`; Go's list has **no such entry at all** — that path is not allowlisted in gopowerwall and would 404 as a static asset instead of being proxied. (3) Go additionally allowlists 10 paths with no upstream counterpart at all: `/api/diagnostics`, `/api/generators`, `/api/generators/actions`, `/api/syncon/vitals`, `/api/syncon/actions`, `/api/inverters`, `/api/inverters/status`, `/api/meters/status`, `/api/powerwalls/status`, and `/api/system_status/soe` (the last is redundant with the dedicated handler at `proxy/routes.go:353` but not incorrect). These extra Go entries are not necessarily wrong (a newer/different gateway firmware may expose them) but they are **not present in the upstream project as of the audited commit**, so they are outside the parity contract's "match pypowerwall" definition even if individually reasonable. |
| DISABLED list (`/api/customer/registration`) | `server.py:201-203`, 1 entry | `isDisabled`, `proxy/server.go:69-76`, 1 entry | parity | Both disable exactly `/api/customer/registration`, and both check DISABLED before ALLOWLIST, so the entry's presence in both `ALLOWLIST` *and* `DISABLED` on both sides is dead/unreachable code identically on both sides (upstream: `elif ... in DISABLED` is checked before `elif ... in ALLOWLIST`; Go: `case isDisabled(reqPath): ... case isAllowlisted(reqPath):`, `proxy/routes.go:574-579`). |

### 3.2 Stub payloads (`backend/stubs/stubs.go`) vs upstream's cloud/fleetapi backfill

MISSING.md's open question (b) asked whether gopowerwall's stub JSON shapes match
upstream's field-for-field. Answer: **largely yes for the shape, but this needs one
correction** — upstream's equivalent constants live in
`pypowerwall/cloud/mock_data.py`/`pypowerwall/cloud/stubs.py` and
`pypowerwall/fleetapi/mock_data.py`/`pypowerwall/fleetapi/stubs.py` (per the
`from pypowerwall.cloud.mock_data import *` / `from pypowerwall.cloud.stubs import *`
imports seen in `pypowerwall_cloud.py:13-14`). **These two files were not in the list of
files this audit was scoped to fetch, and were not independently pulled** — mark the
field-for-field comparison of `MockPowerwalls`, `MockMetersSite`, `MockMeters`,
`MockSitemaster`, `MockCustomer`, `MockInstaller`, `MockNetworks`, `MockAuthToggle`,
`MockUpdate`, `MockSolars`, `MetersAggregatesStub`, and `SystemStatusStub`
(`backend/stubs/stubs.go`) against their Python counterparts as **unverified**, not
confirmed, despite MISSING.md's framing suggesting the shapes were "deliberate but
unchecked" — this audit did not close that loop either, for the same reason (files not
fetched). One partial confirmation: `getAPISystemStatus` in both `backend/cloud/cloud.go:445`
and upstream's `pypowerwall_cloud.py` (grep hit at "API_SYSTEM_STATUS_STUB") independently
show the pattern of overlaying a stub template with live-computed fields
(`nominal_full_pack_energy`, `nominal_energy_remaining`, `max_charge_power`, etc.) is
structurally the same on both sides — the *mechanism* is parity even where the *literal
values* are unverified.

---

## 4. Backend capability by connection mode

| Operation | `local` | `tedapi`/`v1r` | `cloud` | `fleetapi` |
|---|---|---|---|---|
| Read power/vitals/status | Both: real, live gateway data. | Both: real, live TEDAPI GraphQL/protobuf data. | Both: real Tesla Owner API data (`SITE_DATA`/`live_status`). | Both: real Tesla Fleet API data (`live_status`). |
| `SetReserve`/`SetMode` (`/api/operation`) | **Go has a confirmed correctness bug here — see §1 item 22.** Upstream is real and back-filled/safe; gopowerwall is real but unsafe (can clobber the untouched field). | Both: real, POSTs to the gateway/GraphQL mutation. | Both accept the call; **only upstream's reaches Tesla.** Upstream's `post_api_operation` (`pypowerwall_cloud.py:1196-1247`) calls `self.tesla.battery_list()` then, per battery, `battery.set_backup_reserve_percent(...)` / `battery.set_operation(...)` — teslapy methods that resolve (via `pypowerwall/cloud/teslapy/endpoints.json`) to real HTTP calls: `POST api/1/energy_sites/{site_id}/backup` body `{"backup_reserve_percent": <int>}`, and `POST api/1/energy_sites/{site_id}/operation` body `{"default_real_mode": <mode>}`, both with `Authorization: Bearer <access_token>` (teslapy `__init__.py:255`). Go's `postAPIOperation` (`backend/cloud/cloud.go:385-390`) only logs the payload, calls `c.cache.Invalidate(...)`, and returns `{"status": "success"}` — **confirmed zero `http.MethodPost`/`MethodPut` calls anywhere in the function or the file's write paths** (grep). | Same divergence. Upstream's `post_api_operation` (`pypowerwall_fleetapi.py:746-772`) calls `self.fleet.set_battery_reserve(...)`/`set_operating_mode(...)` (`pypowerwall/fleetapi/fleetapi.py:709-729`), which POST to `api/1/energy_sites/{site_id}/backup` / `.../operation` on the Fleet API host, `Authorization: Bearer <access_token>` (`fleetapi.py:388`). Go's `postAPIOperation` (`backend/fleetapi/fleetapi.go:340-345`) is the identical no-op-log-and-succeed pattern. |
| `SetGridCharging` | Not exposed by any backend upstream (`Powerwall.set_grid_charging` has no `local`-mode path in pypowerwall either — the facade restricts this to cloud/tedapi conceptually, but `PyPowerwallLocal` defines no `set_grid_charging`); gopowerwall's facade also gates this to `ModeCloud`/`ModeFleetAPI` only (`powerwall.go:1080-1095`) — parity in scope. | Real for TEDAPI (writes a GraphQL mutation) on both sides — not independently re-verified in this pass beyond the facade gating; treat the exact TEDAPI mutation shape as unverified pending a dedicated read of the relevant `tedapi/__init__.py` mutation function. | **Confirmed no-op in Go, real in upstream.** Upstream `set_grid_charging` (`pypowerwall_cloud.py:878-891`) calls `self._site_api("ENERGY_SITE_IMPORT_EXPORT_CONFIG", ttl=SITE_CONFIG_TTL, force=True, disallow_charge_from_grid_with_solar_installed=mode)`, which resolves (`teslapy/endpoints.json`: `ENERGY_SITE_IMPORT_EXPORT_CONFIG` → `{"TYPE": "POST", "URI": "api/1/energy_sites/{site_id}/grid_import_export"}`) to a real `POST api/1/energy_sites/{site_id}/grid_import_export` with body `{"disallow_charge_from_grid_with_solar_installed": <bool>}`. Go's `SetGridCharging` (`backend/cloud/cloud.go:538-543`) is `log.Debug(...); return {"status":"success"}, nil` — no HTTP call, confirmed by grep for `MethodPost`/`MethodPut` in the file (zero hits outside `Authenticate`/`findEnergySite`/`getSiteData`/`getSiteConfig`, all of which are reads). | Same divergence. Upstream `set_grid_charging` → `self.fleet.set_grid_charging(mode)` (`pypowerwall_fleetapi.py:733-754`) → `POST api/1/energy_sites/{site_id}/grid_import_export` body `{"disallow_charge_from_grid_with_solar_installed": <bool>}`. Go's `backend/fleetapi/fleetapi.go:492-497` is the identical no-op pattern. |
| `SetGridExport` | Same scoping note as `SetGridCharging`. | Same as `SetGridCharging` row — unverified TEDAPI mutation detail. | **Confirmed no-op in Go, real in upstream.** Same `ENERGY_SITE_IMPORT_EXPORT_CONFIG` endpoint as above, body `{"customer_preferred_export_rule": <mode>}` (`pypowerwall_cloud.py:896-911`). Go's `SetGridExport` (`backend/cloud/cloud.go:562-567`) is the same no-op-log-success pattern, zero HTTP calls. | Same divergence: `POST api/1/energy_sites/{site_id}/grid_import_export` body `{"customer_preferred_export_rule": <mode>}` (`fleetapi.py:756-772`) vs Go's no-op (`backend/fleetapi/fleetapi.go:516-521`). |
| `GetTimeRemaining` | Real (`ENERGY_SITE_BACKUP_TIME_REMAINING` upstream / gateway computation locally) on both sides. | Real on both sides. | **Parity (both hard-code a placeholder), but for different reasons.** Go: unconditionally returns `0.0` (`backend/cloud/cloud.go:490-496`, comment cites "DESIGN.md" invariant). Upstream's `get_time_remaining()` (`__init__.py:1029-1038`, not independently re-quoted in this section) also has known-flaky cloud coverage — this row is close to parity but the *exact* upstream cloud-mode value (real backup-time-remaining call vs. hard 0.0) was not independently re-verified against `pypowerwall_cloud.py`'s own `get_time_remaining` in this pass; treat the "both hard-code 0.0" framing as **unverified**, only the Go side is confirmed. |
| `GoOffGrid`/`ReconnectGrid` | n/a | Dead code both sides — see §1 items 34/35. | Not implemented either side. | Not implemented either side. |
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
3. **`GoOffGrid`/`ReconnectGrid` claim reversed.** MISSING.md: "The `Powerwall` facade
   exposes both..., and the TEDAPI backend implements them." Confirmed false: no backend in
   either project implements them — both are dead code end-to-end, upstream and downstream
   alike (§1 items 34-35).
4. **The `/control/*` off_grid/reconnect_grid gap is resolved, not open.** MISSING.md flags
   this as "unconfirmed whether pypowerwall's proxy exposes equivalent control routes at
   all." Confirmed: it does not (§3.1), which combined with correction #3 means this is
   `n/a (deliberate)` parity, not a gap to fix.
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
7. **Vitals/Strings field-naming claim was too hedged; the underlying facts are now
   confirmed.** MISSING.md says gopowerwall's `PVAC_Vsolar<label>` naming is "unverified
   against real hardware" and that pypowerwall's naming ("PVAC_PVMeasuredVoltage/Current")
   "has no equivalent field in what gopowerwall reads." Now confirmed precisely: upstream's
   TEDAPI-mode vitals computation (`pypowerwall/tedapi/__init__.py:1044-1050`) produces
   `PVAC_PvState_{n}`, `PVAC_PVMeasuredVoltage_{n}`, `PVAC_PVCurrent_{n}` (note: no
   "Measured" in the current field, an internal upstream naming inconsistency), and
   `PVAC_PVMeasuredPower_{n}` (n = numeric string index), from raw `PCH_PvVoltage{n}` /
   `PCH_PvCurrent{n}` GraphQL component signals — and **gopowerwall's TEDAPI backend
   computes none of this** (confirmed: zero matches for `PVAC_Pv`/`PvVoltage`/`PvCurrent`
   anywhere under `backend/tedapi/`). This is not merely "unverified against hardware" — it
   is confirmed that gopowerwall's `/strings` will read all-zero for every string on every
   connection mode, not just conditionally on firmware/hardware specifics, because the
   underlying fields are never populated in the first place.
8. **The proxy allowlist divergence was not previously flagged at all.** MISSING.md does not
   mention that gopowerwall's `isAllowlisted` (`proxy/server.go:25-66`) diverges from
   upstream's `ALLOWLIST` (`server.py:173-199`) on 12 of ~34 combined entries — see §3.1.
   This is a new finding from this audit.
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
2. **Make cloud/fleetapi `SetReserve`/`SetMode`/`SetGridCharging`/`SetGridExport` actually
   call Tesla (§4).** Exact endpoints, methods, bodies, and auth headers are now fully
   documented in §4 — this is implementation-ready. **Size: medium** (a day or two): each of
   `backend/cloud/cloud.go` and `backend/fleetapi/fleetapi.go` needs real
   `http.NewRequestWithContext(ctx, http.MethodPost, ..., bytes.NewReader(body))` calls with
   `Authorization: Bearer <token>`, replacing the four log-and-succeed stubs. **Cannot be
   verified end-to-end without a real Tesla account and an actual Powerwall/Solar site** —
   the request shape can be built and unit-tested against a mock HTTP server, but proving
   Tesla accepts it (and that the 80%-cap / async-command-race behavior upstream's comments
   describe is handled correctly) requires live credentials this environment does not have.
3. **`/strings` and TEDAPI-mode vitals field computation (§1 item 9, §4, correction #7).**
   Port `pypowerwall/tedapi/__init__.py:1020-1052`'s `PCH_PvVoltage{n}`/`PCH_PvCurrent{n}` →
   `PVAC_PVMeasuredVoltage_{n}`/`PVAC_PVCurrent_{n}`/`PVAC_PVMeasuredPower_{n}` computation
   into `backend/tedapi`, and reconsider whether `Powerwall.Strings()`'s output shape should
   move from the current fabricated `PVAC_Vsolar<label>` scheme to match one of the two real
   upstream schemes (`__init__.py:495-579`'s letter+index keys, or the verbose per-device
   view) — note this is also a decision point since upstream itself has two different
   internal naming conventions (local-firmware field names vs. TEDAPI-synthesized ones) that
   do not agree with each other. **Size: medium** (a day) for the TEDAPI computation; a
   further **small-medium** effort to decide and implement the public `Strings()` shape.
   **Partially verifiable without hardware**: the TEDAPI GraphQL-signal-to-field computation
   can be unit tested against recorded component-signal fixtures; whether *local-mode* vitals
   already carry the letter-suffixed names assumed by gopowerwall's current code cannot be
   confirmed without a real gateway's `/api/devices/vitals` response.
4. **Proxy allowlist reconciliation (§3.1, correction #8).** Add
   `/api/synchrometer/ct_voltage_references` and `/api/system/networks` (fixing the
   `conn_tests` typo/divergence) to `isAllowlisted`; decide whether to keep or remove the 10
   Go-only entries that have no upstream counterpart. **Size: trivial** (an hour, plus a
   deliberate decision on the extra entries). Fully verifiable without hardware — it is a
   pure static-list diff against the fetched `ALLOWLIST`.
5. **`/pod`'s `TEPOD` vitals augmentation pass (§3.1).** Port
   `server.py:2196-2244`'s vitals-overlay loop into `generatePOD`
   (`proxy/routes.go:230`), including the previously-unemitted
   `PW{N}_POD_nom_energy_to_be_charged` field. **Size: small** (a few hours) — the vitals
   data plumbing already exists (`s.PW.Vitals(ctx)` is already used elsewhere in
   `routes.go`). **Verifiable without hardware** via a vitals fixture containing a `TEPOD`
   device.
6. **`/tedapi/status`, `/components`, `/battery`, `/controller` routes (§3.1).** `backend/tedapi`
   already has most of the underlying data (`GetStatus` at minimum); wiring the remaining
   three sub-routes is comparatively cheap. **Size: small-medium.** Fully verifiable without
   real hardware for the routing/shape; the underlying TEDAPI query correctness against a
   real gateway is a separate, larger question already covered by the existing
   `backend/tedapi` test suite's assumptions.
7. **`/fans` and `/fans/pw` (§3.1, correction #1).** Needs a new TEDAPI
   `get_fan_speeds`-equivalent extracting `PVAC_Fan_Speed_Actual_RPM`/`_Target_RPM` from
   vitals or a dedicated query, plus the two routes. **Size: medium** — the TEDAPI query
   side is new work, not just route wiring. **Cannot be fully verified without real Powerwall
   hardware with cooling fans reporting non-zero speeds** — a synthetic fixture can prove the
   route logic, but confirming the underlying TEDAPI field names/query is correct needs a
   live gateway.
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

### Explicitly unverifiable from this machine, regardless of implementation effort

- Any claim about what a **real Tesla Owner API or Fleet API account** actually accepts or
  rejects for the write endpoints in §4 (headers, rate limits, the 80%-reserve cap behavior,
  whether a `POST` with a stale/refreshed access token really 403s as `tesla_auth.py`'s
  comments assert).
- Any claim about **real gateway firmware** behavior: whether `/api/operation` really
  clobbers an omitted field (this audit trusts pypowerwall's own code comment as the
  evidence, but did not — and could not — confirm it against physical hardware); the exact
  vitals field names a real local gateway emits for solar strings; real fan-speed vitals
  field names/values.
- `pypowerwall/tedapi/__main__.py` (the `tedapi` CLI subcommand's actual implementation) —
  not fetched in this audit; the CLI-forwarding surface (§2.1/§2.2) is confirmed, but the
  exact print format and any additional live checks it performs are not.
- `pypowerwall/cloud/mock_data.py`, `pypowerwall/cloud/stubs.py`,
  `pypowerwall/fleetapi/mock_data.py`, `pypowerwall/fleetapi/stubs.py` — the literal stub
  JSON values backing MISSING.md's open question (b); not fetched in this audit (outside the
  originally-scoped file list), so `backend/stubs/stubs.go`'s exact values remain unverified
  against upstream's exact values, only the overlay *mechanism* is confirmed parity.
- Full field-by-field parity of `/stats` and `/health` beyond the high-level key-set
  comparison in §3.1 — both files were fetched in full, but a byte-for-byte diff of every
  nested field was not completed given the size of `server.py` (2739 lines) relative to the
  time available for this audit.
