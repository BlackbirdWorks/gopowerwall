# gopowerwall metrics stack

A ready-to-run `docker compose` stack wiring the gopowerwall proxy to
Telegraf, InfluxDB, and Grafana, for 24/7 Powerwall metrics against Tesla
Cloud (Owner API) mode.

```
Tesla Cloud API <-- OAuth2 refresh token -- gopowerwall proxy <-- scrape -- Telegraf --> InfluxDB <-- query -- Grafana
```

## Data caveat - read this before building dashboards

**In Tesla Cloud mode (and FleetAPI mode), Tesla's API exposes no per-device
vitals.** `/vitals`, `/strings`, `/temps` and `/alerts` return empty or stub
data - `backend/cloud`'s and `backend/fleetapi`'s `Vitals()` return an empty
map, and several `/api/*` endpoints are served from `backend/stubs`. Only
**aggregate power** (`/aggregates`, `/json`), **state of charge**, and
**grid status** (`/api/system_status/*`) are real in cloud/FleetAPI mode.
This is an upstream Tesla limitation shared with
[pypowerwall](https://github.com/jasonacox/pypowerwall), not a gopowerwall
defect. If you build a Grafana panel against `powerwall_vitals`,
`powerwall_strings`, `powerwall_temps` or `powerwall_alerts` in cloud mode,
expect it to stay flat - that's expected, not broken. Those measurements
only populate with real values when the proxy runs in local or TEDAPI mode
against the Gateway directly on your LAN.

## Setup: Tesla Cloud mode via tesla_auth

1. Download and run [tesla_auth](https://github.com/adriankumpf/tesla_auth)
   - it opens a native browser login window for your Tesla account (supports
   MFA and captcha).
2. On the final screen, copy the refresh token it displays (and, optionally,
   the access token).
3. In this directory, copy the example environment file and fill it in:

   ```bash
   cp ../../.env.example .env
   ```

   At minimum set:

   ```dotenv
   PW_EMAIL=you@example.com
   TESLA_REFRESH_TOKEN=<refresh token from tesla_auth>
   ```

   On first run, gopowerwall bootstraps `.pypowerwall.auth` from
   `TESLA_REFRESH_TOKEN` (persisted in the `powerwall-auth` volume) and
   immediately exchanges it for a fresh access token. From then on, the
   proxy refreshes the access token automatically as it expires and
   persists the new one back to that file, so a container restart never
   needs a freshly minted token from tesla_auth again.

4. Start the stack:

   ```bash
   docker compose up -d
   ```

5. Open Grafana at <http://localhost:3000> (default `admin` / `admin` -
   change it on first login). The InfluxDB datasource is provisioned
   automatically, pointed at the `powerwall` bucket.

## What gets scraped

See [`telegraf/telegraf.conf`](telegraf/telegraf.conf) - every endpoint it
polls is taken directly from `proxy/routes.go`:

| Measurement                | Endpoint                        | Real in cloud mode? |
| --------------------------- | -------------------------------- | -------------------- |
| `powerwall_summary`         | `/json`                          | Yes                   |
| `powerwall_aggregates`      | `/aggregates`                    | Yes                   |
| `powerwall_soe`             | `/api/system_status/soe`         | Yes                   |
| `powerwall_grid_status`     | `/api/system_status/grid_status` | Yes                   |
| `powerwall_proxy_stats`     | `/stats`                         | Yes (proxy self-stats)|
| `powerwall_vitals`          | `/vitals`                        | No - stub/empty       |
| `powerwall_strings`         | `/strings`                       | No - stub/empty       |
| `powerwall_temps`           | `/temps`                         | No - stub/empty       |
| `powerwall_alerts`          | `/alerts`                        | No - stub/empty       |

## Building dashboards

No dashboard is provisioned out of the box, since a good one deserves to be
tuned to your own panels rather than shipped as an untested guess. Start
from a Flux query against `powerwall_summary` in Grafana's Explore view,
for example:

```flux
from(bucket: "powerwall")
  |> range(start: -6h)
  |> filter(fn: (r) => r._measurement == "powerwall_summary")
  |> filter(fn: (r) => r._field == "grid" or r._field == "solar" or r._field == "battery" or r._field == "home")
```

## Configuration

All services read from `.env` in this directory (`env_file: .env` on the
`proxy` service) plus the `INFLUXDB_*`, `GRAFANA_*` variables in
`docker-compose.yml`, which default to development-friendly values - change
`INFLUXDB_ADMIN_TOKEN`, `INFLUXDB_PASSWORD` and `GRAFANA_ADMIN_PASSWORD`
before exposing this stack beyond your own machine.

The proxy's own auth files (`.pypowerwall.auth`, `.pypowerwall.site`) persist
in the `powerwall-auth` Docker volume via `PW_AUTH_PATH=/data`, so they
survive `docker compose down` (but not `docker compose down -v`).
