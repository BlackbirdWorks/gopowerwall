# Docker

gopowerwall's proxy server ships as a container image. This page covers building it from
the repository `Dockerfile` and running it with the published image; for the routes and
environment variables the proxy itself understands, see the main
[README](../README.md#running-the-proxy) and
[architecture/README.md](architecture/README.md).

## Build from source

The repository `Dockerfile` builds only the `proxy` binary (not the `gopowerwall` CLI) as a
static, `CGO_ENABLED`-free binary and copies it into a `scratch` final image:

```bash
docker build -t gopowerwall-proxy .
```

Internally this runs:

```dockerfile
FROM golang:1.27-alpine AS builder
RUN apk add --no-cache ca-certificates
...
RUN go build \
    -tags 'netgo osusergo static_build' \
    -trimpath \
    -ldflags="-w -s -extldflags '-static -fno-PIC'" \
    -o proxy ./cmd/proxy
RUN go build ... -o healthcheck ./cmd/healthcheck
FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /app/proxy .
COPY --from=builder /app/healthcheck .
EXPOSE 8675
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/app/healthcheck"]
USER 65532:65532
CMD ["./proxy"]
```

Because the final stage is `scratch`, there is no shell and no other tooling in the image —
just the `proxy` binary, a small `healthcheck` binary, and a CA certificate bundle. The CA
bundle matters specifically for **cloud** and **fleetapi** mode: those backends make
verified TLS connections to Tesla's APIs (unlike the local/tedapi/v1r backends, which talk
to the gateway's self-signed certificate on the LAN with verification intentionally
disabled), and without a CA bundle those connections fail with
`x509: certificate signed by unknown authority`. The `healthcheck` binary exists only
because a `scratch` image has no `curl`/`wget` for Docker's `HEALTHCHECK` instruction to
shell out to; it performs a single GET against the proxy's own `/health` route and exits
0/1 accordingly. The container also runs as a fixed non-root UID/GID (`65532:65532`) rather
than root.

`Dockerfile.goreleaser` is a separate, simpler Dockerfile used only by the goreleaser
release pipeline (it copies in already-cross-compiled `proxy` and `healthcheck` binaries
rather than building them, and gets its CA bundle from a small `alpine` stage instead of a
Go builder stage); use the top-level `Dockerfile` for a local build.

## Run it

```bash
docker run --rm -p 8675:8675 \
  -e PW_HOST=192.168.91.1 \
  -e PW_PASSWORD=abcde \
  gopowerwall-proxy
```

`8675` is the proxy's default port (`proxy.defaultPort` in `proxy/config.go`, and the port
`EXPOSE`d in the `Dockerfile`) — matching pypowerwall's own default. Override it with
`PW_PORT` if you need a different container-internal port, remapping with `-p` as usual.

## Published image

Releases publish a multi-arch (`linux/amd64`, `linux/arm64`) image via `.goreleaser.yml`'s
`dockers_v2` block:

```bash
docker pull ghcr.io/blackbirdworks/gopowerwall:latest
# or a specific version
docker pull ghcr.io/blackbirdworks/gopowerwall:<version>

docker run --rm -p 8675:8675 \
  -e PW_HOST=192.168.91.1 \
  -e PW_PASSWORD=abcde \
  ghcr.io/blackbirdworks/gopowerwall:latest
```

`latest` is only tagged for non-snapshot (tagged) releases; a snapshot/dev build carries
only its version tag.

## Environment variables

The container reads the same `PW_*`/`PROXY_BASE_URL` environment variables as the
`gopowerwall proxy` subcommand — all loaded in `proxy/config.go`'s `DefaultConfig`. The
ones most relevant to a container deployment:

| Env var | Default | Purpose |
|---|---|---|
| `PW_HOST` | (empty) | Powerwall gateway IP/hostname (required for `local`/`tedapi`/`v1r`) |
| `PW_PASSWORD` | (empty) | Customer password (local mode) |
| `PW_GW_PWD` | (empty) | Gateway WiFi password (TEDAPI/v1r) |
| `PW_RSA_KEY_PATH` | (empty) | RSA private key path (v1r) — mount it into the container and point this at the mounted path |
| `PW_AUTH_PATH` | (empty) | Directory holding `.pypowerwall.auth`/`.pypowerwall.fleetapi`/the cache file — mount a volume here for cloud/FleetAPI modes or to persist the local session cache across restarts |
| `PW_BIND_ADDRESS` | (empty, all interfaces) | Address the proxy binds inside the container; leave unset so `-p` mapping works normally |
| `PW_PORT` | `8675` | Listen port |
| `PW_DEBUG` | `false` | Enable debug logging |
| `PW_CONTROL_SECRET` | (empty) | Shared secret required to use `/control/*`; unset disables control endpoints entirely |
| `PROXY_BASE_URL` | `/` | Base path prefix, useful behind a reverse proxy that mounts gopowerwall under a sub-path |

See the README's full environment variable table for the complete list (cache TTLs,
health-check/degradation tuning, TEDAPI-specific knobs, and more) — every `PW_*` variable
documented there works identically inside the container.

> [!NOTE]
> `PW_HTTPS` is honored by this standalone `proxy` binary: `cmd/proxy/main.go` calls
> `ListenAndServeTLS("localhost.crt", "localhost.key")` when `PW_HTTPS=yes` specifically
> (`"http"` and `"no"` both serve plain HTTP from this binary). This is unlike the
> `gopowerwall proxy` CLI subcommand, which always serves plain HTTP regardless of
> `PW_HTTPS`. The container's `WORKDIR` is `/app`, so both files must be mounted there —
> `-v $(pwd)/localhost.crt:/app/localhost.crt -v $(pwd)/localhost.key:/app/localhost.key`
> — alongside `-e PW_HTTPS=yes`.

## Persisting auth/session data

`local` mode's session cache and `cloud`/`fleetapi` mode's auth files all live under
`PW_AUTH_PATH` (or the working directory if unset). Mount a volume there to avoid
re-authenticating on every container restart:

```bash
docker run --rm -p 8675:8675 \
  -e PW_HOST=192.168.91.1 \
  -e PW_PASSWORD=abcde \
  -e PW_AUTH_PATH=/data \
  -v gopowerwall-data:/data \
  ghcr.io/blackbirdworks/gopowerwall:latest
```

For `cloud` mode, you can either place a pre-existing `.pypowerwall.auth` file in that same
volume before starting the container, or set `TESLA_REFRESH_TOKEN` (obtained via
[tesla_auth](https://github.com/adriankumpf/tesla_auth) — see the README's
[Tesla Cloud mode setup](../README.md#tesla-cloud-mode-setup-tesla_auth--env) section) and
let gopowerwall bootstrap the file itself on first run. Either way, gopowerwall refreshes
the Tesla access token automatically as it expires and persists the new one back to that
file, so the container never needs a freshly minted token after the first run. `fleetapi`
mode still requires a pre-existing `.pypowerwall.fleetapi` file — gopowerwall does not
generate one itself (see
[MISSING.md](../MISSING.md#cli-subcommands-that-only-print-guidance-text)).

## Using a `.env` file

Both the `proxy` binary and the `gopowerwall` CLI load a `.env` file (via
[godotenv](https://github.com/joho/godotenv)) from their working directory before reading
configuration, if one is present. Real environment variables always win over `.env`
values, so the same image works unmodified whether it's configured with real environment
variables or a `.env` file.

Note that `docker run --env-file` and Compose's `env_file:` already inject `.env` entries
as real environment variables into the container before the process starts — godotenv
never even sees a file in those cases, since there isn't one on the container's
filesystem. godotenv's own loading matters when you mount an actual `.env` file into the
container's `/app` working directory (`-v $(pwd)/.env:/app/.env:ro`), or when running the
`proxy`/`gopowerwall` binaries directly on a host without Docker at all. Either way, the
same [`.env.example`](../.env.example) at the repository root documents every supported
variable, including `TESLA_REFRESH_TOKEN`/`TESLA_ACCESS_TOKEN` for cloud mode.

## Docker Compose example

```yaml
services:
  gopowerwall-proxy:
    image: ghcr.io/blackbirdworks/gopowerwall:latest
    ports:
      - "8675:8675"
    environment:
      - PW_HOST=192.168.91.1
      - PW_PASSWORD=abcde
      - PW_AUTH_PATH=/data
    volumes:
      - gopowerwall-data:/data

volumes:
  gopowerwall-data:
```

```bash
docker compose up -d
docker compose logs -f
```

For a complete example wiring the proxy to Telegraf, InfluxDB, and Grafana (Tesla Cloud
mode via tesla_auth), see [examples/metrics-stack](../examples/metrics-stack).
