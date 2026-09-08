# gopowerwall

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

gopowerwall is a Go port of [pypowerwall](https://github.com/jasonacox/pypowerwall), the
Python library and proxy server for the Tesla Energy Gateway (Powerwall 2, Powerwall+, and
Powerwall 3). It gives Go programs the same kind of typed, cached access to a Powerwall
that pypowerwall gives Python programs, and it ships the same HTTP proxy server so that
existing pypowerwall dashboards, Home Assistant integrations, and Grafana/InfluxDB setups
can point at gopowerwall instead without changing their queries.

Parity with pypowerwall is a deliberate, but *scoped*, goal: the `gopowerwall` CLI's
subcommands/flags/output and the proxy's HTTP routes/JSON shapes track pypowerwall's
documented surface. Everything underneath that surface — package layout, exported Go
identifiers, function signatures — is idiomatic Go rather than a line-for-line port. See
[MISSING.md](MISSING.md) for the places where that parity is still incomplete.

> [!NOTE]
> This is a community Go implementation, not affiliated with or endorsed by Tesla, Inc. or
> the pypowerwall project.

## Status overview

| Area | Status |
|------|--------|
| Local gateway mode (`local`) | Implemented — session auth, vitals, control endpoints |
| TEDAPI WiFi mode (`tedapi`) | Implemented — protobuf queries over `192.168.91.1` |
| TEDAPI v1r LAN mode (`v1r`) | Implemented — RSA-signed transport for Powerwall 3 |
| Tesla Cloud mode (`cloud`) | Reads implemented, with automatic OAuth2 access-token refresh persisted back to the token file; writes are accepted and report success but are not forwarded to Tesla (see [MISSING.md](MISSING.md)) |
| Tesla Fleet API mode (`fleetapi`) | Reads implemented, same automatic token refresh as cloud mode; writes have the same no-op caveat as cloud mode (see [MISSING.md](MISSING.md)) |
| HTTP proxy server | Implemented — routes, caching, health, control endpoints |
| CLI: `get`, `set`, `scan`, `proxy` | Fully functional |
| CLI: `setup`, `authtoken`, `register`, `cloudcheck`, `tedapi` | Print guidance text only; no OAuth flow or live diagnostics yet (see [MISSING.md](MISSING.md)) |

## Connection modes

gopowerwall talks to a Powerwall through one of five backends, selected automatically or
forced with a flag:

| Mode | Flag | Transport | Credentials needed |
|------|------|-----------|---------------------|
| **local** | `--local` | Gateway's own HTTPS REST API (self-signed cert) | `--host`, `--password` (last 5 characters of the gateway password) |
| **tedapi** | `--tedapi` | Protobuf over the gateway's `/tedapi` endpoint, normally reached over the gateway's own WiFi AP (`192.168.91.1`) | `--gw_pwd` (full gateway WiFi password), optionally `--host` |
| **v1r** | `--v1r` | RSA-signed TEDAPI variant used by Powerwall 3 over the wired LAN/vendor subnet | `--host`, `--gw_pwd`, and an RSA private key (`--rsa_key_path`, default `./tedapi_rsa_private.pem`) |
| **cloud** | `--cloud` | Tesla Owner API (unofficial) | A `.pypowerwall.auth` token file (see [MISSING.md](MISSING.md) — `gopowerwall authtoken` does not yet generate one itself; use [tesla_auth](https://github.com/adriankumpf/tesla_auth) and `TESLA_REFRESH_TOKEN`, see below) |
| **fleetapi** | `--fleetapi` | Official Tesla Fleet API | A `.pypowerwall.fleetapi` config file produced by prior `setup --fleetapi` (same caveat) |

When no mode flag is given, gopowerwall auto-selects: if a host is set it uses `local`;
otherwise it looks for an existing FleetAPI config file, then a cloud auth file, in
`--authpath` (`$PW_AUTH_PATH`). Once connected, [`Powerwall.Connect`](powerwall.go) will
also cascade Local → FleetAPI → Cloud → Local on failure within a single call, so a
misconfigured mode does not necessarily produce an immediate hard failure.

Use **local** when you have LAN access to the gateway and its customer password — it is
the fastest and most complete mode. Use **tedapi** or **v1r** when you need TEDAPI-only
data (vitals, per-string solar, battery blocks) or don't have the customer password. Use
**cloud** or **fleetapi** when the gateway is not reachable on the local network at all;
note both only read pre-existing token files today (see below).

## Installation

### `go install`

```bash
go install github.com/blackbirdworks/gopowerwall/cmd/gopowerwall@latest
go install github.com/blackbirdworks/gopowerwall/cmd/proxy@latest
```

### Binary releases

Download a prebuilt archive for your platform from the project's GitHub Releases page
(published by `.goreleaser.yml`) — it contains both the `gopowerwall` CLI and the
standalone `proxy` binary.

### From source

```bash
git clone https://github.com/blackbirdworks/gopowerwall.git
cd gopowerwall
make build      # builds bin/gopowerwall and bin/proxy
```

### Docker

See [docs/docker.md](docs/docker.md) for running the proxy as a container, and
[examples/metrics-stack](examples/metrics-stack) for a full Telegraf/InfluxDB/Grafana
stack wired up against it.

## CLI usage

The `gopowerwall` binary exposes ten subcommands (`gopowerwall --help` lists all of them):
`version`, `register`, `authtoken`, `get`, `tedapi`, `setup`, `cloudcheck`, `proxy`, `set`,
`scan`.

Connection flags (`--host`, `--password`, `--gw_pwd`, `--rsa_key_path`, `--authpath`,
`--local`, `--cloud`, `--fleetapi`, `--tedapi`, `--v1r`, `--debug`) are shared by `get`,
`set`, and `proxy`.

```bash
# Read status/power levels from a gateway on the LAN
gopowerwall get --local --host 192.168.91.1 --password abcde

# Same, formatted as JSON or CSV
gopowerwall get --local --host 192.168.91.1 --password abcde --format json
gopowerwall get --local --host 192.168.91.1 --password abcde --format csv

# Read over TEDAPI (gateway WiFi AP), using the full gateway password
gopowerwall get --tedapi --gw_pwd <gateway_wifi_password>

# Set operating mode and backup reserve
gopowerwall set --local --host 192.168.91.1 --password abcde --mode self_consumption --reserve 20

# Enable/disable grid charging or export mode (cloud/fleetapi only — see MISSING.md)
gopowerwall set --cloud --gridcharging on
gopowerwall set --cloud --gridexport pv_only

# Discover gateways on the local network
gopowerwall scan 192.168.1.0/24
gopowerwall scan 192.168.1.0/24 --json

# Run the HTTP proxy
gopowerwall proxy --local --host 192.168.91.1 --password abcde --port 8675
```

`setup`, `authtoken`, `register`, `cloudcheck`, and `tedapi` currently print static
guidance text rather than performing the OAuth device flow, RSA key registration, live
diagnostics, or connection test that their pypowerwall counterparts do — see
[MISSING.md](MISSING.md) for the exact gap. In practice this means cloud/FleetAPI auth
files (`.pypowerwall.auth`, `.pypowerwall.fleetapi`) must currently be produced by another
tool and placed in `--authpath`/`$PW_AUTH_PATH` — see the next section for the recommended
way to do that with [tesla_auth](https://github.com/adriankumpf/tesla_auth).

### Tesla Cloud mode setup (tesla_auth + `.env`)

`gopowerwall` does not itself drive Tesla's interactive OAuth login (see above) — instead,
use [tesla_auth](https://github.com/adriankumpf/tesla_auth), the community-standard tool
for this, to obtain a **refresh token**:

1. Download and run the `tesla_auth` executable. It opens a native browser window for your
   Tesla account login (it supports MFA and captcha).
2. On the final screen, copy the refresh token it displays (and, optionally, the access
   token — gopowerwall will fetch one itself on first connect if you skip it).
3. Set `PW_EMAIL` and `TESLA_REFRESH_TOKEN`, either as real environment variables or in a
   `.env` file next to the binary (loaded automatically by both `gopowerwall` and the
   standalone `proxy` binary — see [`.env.example`](.env.example) for every supported
   variable; real environment variables always take precedence over `.env` values, and a
   missing `.env` is not an error).
4. Run `gopowerwall proxy --cloud` (or just start the container — see
   [docs/docker.md](docs/docker.md)).

On first connect, if no `.pypowerwall.auth` file exists yet under `--authpath`, gopowerwall
bootstraps one from `TESLA_REFRESH_TOKEN`/`TESLA_ACCESS_TOKEN` and immediately exchanges
the refresh token for a fresh access token. From then on, Tesla access tokens (which expire
after a few hours) are refreshed automatically as needed and the new token is written back
to `.pypowerwall.auth`, so a long-running proxy (e.g. in Docker) keeps working indefinitely
without manual re-authentication, and a container restart does not need a freshly minted
token from tesla_auth again. The same automatic refresh applies to `.pypowerwall.fleetapi`
in `fleetapi` mode.

For a complete Telegraf/InfluxDB/Grafana stack wired up this way, see
[examples/metrics-stack](examples/metrics-stack).

## Running the proxy

```bash
gopowerwall proxy --host 192.168.91.1 --password abcde
# or, using the standalone binary:
proxy
```

The proxy listens on `0.0.0.0:8675` by default and serves the same route surface as
pypowerwall's proxy (`/aggregates`, `/soe`, `/vitals`, `/strings`, `/csv`, `/csv/v2`,
`/pod`, `/freq`, `/temps/pw`, `/alerts/pw`, `/stats`, `/health`, `/version`, `/help`,
control endpoints under `/control/*`, a gopowerwall-specific `/pw/*` facade, and a proxied
copy of the gateway's own allow-listed `/api/*` routes) plus the embedded web UI at `/`.
See [docs/architecture/README.md](docs/architecture/README.md) for how requests are routed
and [MISSING.md](MISSING.md) for the routes that are not yet implemented (notably
`/fans/pw`).

All configuration is via environment variables (`PW_*`), matching pypowerwall:

| Env var | Default | Purpose |
|---------|---------|---------|
| `PW_HOST` | (empty) | Powerwall gateway IP/hostname |
| `PW_PASSWORD` | (empty) | Customer password |
| `PW_EMAIL` | `email@example.com` | Tesla account email (cloud mode) |
| `TESLA_REFRESH_TOKEN` | (empty) | Refresh token from [tesla_auth](https://github.com/adriankumpf/tesla_auth); bootstraps `.pypowerwall.auth` when it does not exist yet (cloud mode) |
| `TESLA_ACCESS_TOKEN` | (empty) | Optional access token from tesla_auth to seed the bootstrap; omit it and gopowerwall fetches one itself on first connect |
| `PW_GW_PWD` | (empty) | Gateway WiFi password (TEDAPI/v1r) |
| `PW_RSA_KEY_PATH` | (empty) | RSA private key path (v1r) |
| `PW_TIMEZONE` | `America/Los_Angeles` | Local timezone for time-based fields |
| `PW_SITEID` | (empty) | Tesla energy site ID (multi-site cloud/FleetAPI accounts) |
| `PW_AUTH_PATH` | (empty) | Directory holding auth/config files |
| `PW_AUTH_MODE` | `cookie` | Local gateway auth mode: `cookie` or `token` |
| `PW_CACHE_FILE` | `.powerwall` (under `PW_AUTH_PATH` if set) | Local session cache file |
| `PW_BIND_ADDRESS` | (empty, i.e. all interfaces) | Proxy bind address |
| `PW_PORT` | `8675` | Proxy listen port |
| `PW_DEBUG` | `false` | Enable debug logging |
| `PW_CACHE_EXPIRE` | `5` | Response cache TTL (seconds) |
| `PW_BROWSER_CACHE` | `0` | Browser `Cache-Control` max-age for static assets (seconds) |
| `PW_TIMEOUT` | `5` | Gateway request timeout (seconds) |
| `PW_POOL_MAXSIZE` | `15` | HTTP connection pool size |
| `PW_HTTPS` | `no` | `yes`/`http`/`no` — see note below |
| `PW_STYLE` | `clear` | Web UI background style (`.js` suffix added automatically) |
| `PW_CONTROL_SECRET` | (empty) | Shared secret required to use `/control/*`; unset disables control endpoints |
| `PW_WIFI_HOST` | (empty) | Fallback WiFi TEDAPI host for v1r mode |
| `PW_TEDAPI_API_VERSION` | `V2024_06` | TEDAPI protobuf/query version (`V2024_06` or `V2026_06`) |
| `PW_TEDAPI_AUTH_MODE` | `basic` | TEDAPI auth mode: `basic` or `bearer` |
| `PW_NEG_SOLAR` | `true` | Allow negative solar readings through unmodified |
| `PW_SITE_ZERO_THRESHOLD` | `0` | Zero out site power readings within this +/- watt band |
| `PROXY_BASE_URL` | `/` | Base path prefix for reverse-proxy deployments |
| `PW_SUPPRESS_NETWORK_ERRORS` | `false` | Suppress network error logging |
| `PW_NETWORK_ERROR_RATE_LIMIT` | `5` | Rate limit for network error logs |
| `PW_FAIL_FAST` | `false` | Serve cached data immediately once degraded, skipping a live retry |
| `PW_GRACEFUL_DEGRADATION` | `true` | Serve last-known-good cached data when a live call fails |
| `PW_HEALTH_CHECK` | `true` | Include connection health in `/health` and `/stats` |
| `PW_CACHE_TTL` | `30` | Max age (seconds) of degraded-mode cached data |
| `PW_TEDAPI_RECOVERY` | `true` | Enable automatic TEDAPI recovery probing |
| `PW_TEDAPI_PROBE_INTERVAL` | `30` (min `5`) | TEDAPI health probe interval (seconds) |
| `PW_FIRMWARE_CHECK_INTERVAL` | `300` (min `30`) | Firmware version poll interval (seconds) |

> [!NOTE]
> `PW_HTTPS` is honored by the standalone `proxy` binary (`cmd/proxy`), which serves TLS
> from `localhost.crt`/`localhost.key` when set to `yes`. The `gopowerwall proxy`
> subcommand does not currently read `PW_HTTPS` and always serves plain HTTP — use the
> standalone binary if you need TLS termination in-process.

## Library quickstart

```go
package main

import (
	"context"
	"fmt"

	"github.com/blackbirdworks/gopowerwall"
)

func main() {
	ctx := context.Background()

	pw, err := gopowerwall.New(ctx,
		gopowerwall.WithHost("192.168.91.1"),
		gopowerwall.WithPassword("abcde"),
		gopowerwall.WithCloudMode(false),
	)
	if err != nil {
		panic(err)
	}
	defer pw.Close(ctx)

	if !pw.IsConnected() {
		fmt.Println("could not connect")
		return
	}

	fmt.Println("mode:", pw.Mode())
	if level, err := pw.LevelScaled(ctx); err == nil {
		fmt.Println("battery level:", level)
	}
	if gridStatus, err := pw.GridStatusString(ctx); err == nil {
		fmt.Println("grid status:", gridStatus)
	}
	fmt.Printf("power: %+v\n", pw.Power(ctx))
}
```

Every method that talks to the gateway takes a `context.Context` as its first argument, so
callers control cancellation, timeouts, and (via `pkgs/logger`) structured logging
verbosity per call. See [docs/quickstart.md](docs/quickstart.md) for a fuller walkthrough
covering both local and cloud modes, and [docs/architecture/README.md](docs/architecture/README.md)
for how the library is layered.

## Development

```bash
make build           # build bin/gopowerwall and bin/proxy
make build-linux      # static linux build (CGO disabled)
make test             # unit tests (short mode, race, shuffled)
make integration-test  # integration-tagged tests
make total-coverage    # unit + integration coverage, merged into coverage.html
make lint              # golangci-lint + govulncheck
make lint-fix           # fieldalignment -fix, then golangci-lint --fix
make proto              # regenerate protobuf bindings (see docs/architecture/tedapi.md)
make bench              # benchmarks
make all                # lint-fix + total-coverage
```

`make install-deps` (invoked automatically by `lint`/`lint-fix`) installs `golangci-lint`
and `fieldalignment` if missing.

## Licensing and attribution

gopowerwall is released under the [MIT License](LICENSE). It is an independent Go port of
[jasonacox/pypowerwall](https://github.com/jasonacox/pypowerwall) (also MIT-licensed);
credit for the original protocol reverse-engineering, proxy design, and web dashboard goes
to that project and its contributors. The vendored TEDAPI protobuf definitions under
`proto/` originate from Tesla's own gateway firmware, via pypowerwall's `tedapi` tooling.
