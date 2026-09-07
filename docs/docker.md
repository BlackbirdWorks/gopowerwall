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
...
RUN go build \
    -tags 'netgo osusergo static_build' \
    -trimpath \
    -ldflags="-w -s -extldflags '-static -fno-PIC'" \
    -o proxy ./cmd/proxy
FROM scratch
COPY --from=builder /app/proxy .
EXPOSE 8675
CMD ["./proxy"]
```

Because the final stage is `scratch`, there is no shell, no CA bundle, and no other tooling
in the image — just the `proxy` binary. `Dockerfile.goreleaser` is a separate, simpler
Dockerfile used only by the goreleaser release pipeline (it copies in an
already-cross-compiled binary rather than building one); use the top-level `Dockerfile` for
a local build.

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

For `cloud`/`fleetapi` modes, place a pre-existing `.pypowerwall.auth` or
`.pypowerwall.fleetapi` file in that same volume before starting the container — gopowerwall
does not generate either file itself yet (see
[MISSING.md](../MISSING.md#cli-subcommands-that-only-print-guidance-text)).

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
