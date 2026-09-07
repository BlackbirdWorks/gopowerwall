# Architecture overview

gopowerwall is a facade over five gateway-connection backends, wrapped by a CLI and an
HTTP proxy that both consume the same facade. This page covers how those pieces fit
together; see the per-mode pages for what each backend actually talks to.

## Layering

```
cmd/gopowerwall, cmd/proxy   entry points: flag parsing, process wiring
        |
     commands/               kong command structs; translate flags into gopowerwall.Option values
        |
     gopowerwall (root)      the Powerwall facade: mode selection, typed accessors, Poll/Post
        |
     backend/{local,tedapi,cloud,fleetapi}
                              one package per connection mode; own HTTP/protobuf handling,
                              caching, and auth per mode
        |
     net/http, proto/*        stdlib HTTP clients and vendored TEDAPI protobuf bindings

proxy/                        a second, independent consumer of the same gopowerwall.Powerwall
                              facade; translates HTTP requests into facade calls and back into
                              pypowerwall-shaped JSON
```

`cmd/gopowerwall/cli.go` declares the ten-subcommand kong grammar and hands control to
`commands.Context.Run`. Each command in `commands/` (`get.go`, `set.go`, `scan.go`,
`proxy.go`, `misc.go`) either talks to `scan` directly or builds a `*gopowerwall.Powerwall`
via `ConnectionFlags.BuildPowerwall` (`commands/connection.go`), which maps CLI flags onto
`gopowerwall.Option` functions and calls `gopowerwall.New`.

`gopowerwall.Powerwall` (`powerwall.go`) is the facade. It holds at most one active backend
client (`local`, `tedapi`, `cloud`, or `fleetapi` — never more than one, selected by
`ConnectionMode`) behind a `sync.RWMutex`, and every exported method switches on
`p.mode` to route the call to whichever backend is live. Typed accessors like
`SystemStatus`, `SOE`, `GridStatusResponse`, `Operation`, and `SiteInfo` all go through
`PollRaw` and `json.Unmarshal` into a `models.*` struct; untyped accessors like `Poll`,
`Site`, `Solar`, `Status` return `any` the way pypowerwall's own dynamic responses do.

`proxy.Server` (`proxy/server.go`) is a second, independent consumer of the same facade —
it is not part of the `cmd`→`commands` call chain. `proxy.NewServer` either accepts an
existing `*gopowerwall.Powerwall` or builds its own from `proxy.Config`, then dispatches
incoming HTTP requests (`ServeHTTP` → `handleGet`/`handlePost` in `proxy/routes.go` and
`proxy/control.go`) to facade methods, shaping the results back into pypowerwall's JSON.

### The `client` package is not currently wired into any backend

`client/client.go` implements a generic, reusable HTTP client with response caching,
cookie/token reauth hooks, and generic `GetJSON`/`PostJSON` helpers, and it imports
`backend.Config`. Despite that shape, no backend (`backend/local`, `backend/cloud`,
`backend/fleetapi`, `backend/tedapi`) or any other non-test file in the module imports the
`client` package — each backend rolls its own `*http.Client` and its own copy of
`pkgs/cache.ResponseCache` directly. Treat `client/` as a standalone, currently-unused
building block rather than a layer actually sitting between `backend` and the network today.

## Mode selection

`ConnectionMode` (`types.go`) is one of `local`, `tedapi`, `v1r`, `cloud`, or `fleetapi`
(plus `hybrid`, used only for TEDAPI-over-local sub-mode tracking, and `unknown` as the
zero value). `gopowerwall.New` (`powerwall.go`) picks a starting mode from `Config.CloudMode`
and `Config.FleetAPI`, then, if `Config.AutoSelect` is set, `autoSelectMode` overrides that
choice:

1. If `Host` is set and neither `CloudMode` nor `FleetAPI` is set, use `local`.
2. Else if `<AuthPath>/.pypowerwall.fleetapi` exists, use `fleetapi`.
3. Else if `<AuthPath>/.pypowerwall.auth` exists, use `cloud`.
4. Otherwise, mode selection fails and connecting reports an error; nothing changes.

CLI commands set `AutoSelect` themselves whenever no mode flag (`--local`/`--cloud`/
`--fleetapi`/`--tedapi`/`--v1r`) is passed (`commands/connection.go`'s
`resolveModeOptions`).

Independently, `Powerwall.Connect(ctx, retry bool)` implements pypowerwall's circular
fallback: `local → fleetapi → cloud → local`, retried up to `maxConnectRetries` (3) times,
sleeping `connectRetryWait` (30s) before the last attempt when `retry` is true. This means a
single `New` call can end up connected in a different mode than the one it started with if
the first choice's authentication fails — check `pw.Mode()` after connecting rather than
assuming it matches what you configured.

TEDAPI has its own sub-mode, `TEDAPIMode` (`types.go`): `off`, `full` (WiFi AP,
password-only), `hybrid` (TEDAPI layered on top of an authenticated local session, when
`GwPwd` is set and `Host` is the gateway's default WiFi IP), and `v1r` (RSA-signed LAN
transport, when `RSAKeyPath` is set). See [tedapi.md](tedapi.md) and [v1r.md](v1r.md).

## Context and logging

Every backend method, and every `Powerwall` method that talks to a gateway, takes a
`context.Context` as its first argument and threads it down to `http.NewRequestWithContext`
— there is no `context.Background()` call left in the request path. Cancelling the caller's
context cancels the in-flight HTTP call.

Logging is `log/slog`, carried on that same context rather than held as package-global
state (`pkgs/logger`):

- `logger.New(w, level)` builds a `*slog.Logger` writing text-formatted, timestamp-free
  records (the gateway's own data carries its own timestamps).
- `logger.Into(ctx, l)` attaches a logger to a context; `logger.Load(ctx)` retrieves it,
  falling back to `slog.Default()` if none was attached — it never returns `nil`.
- `logger.With(ctx, args...)` returns a context whose logger has extra key/value attributes
  bound, so attributes accumulate as a call descends through the facade into a backend.

`ConnectionFlags.WithLogger` (`commands/connection.go`) is where the CLI wires this in: it
calls `logger.Into(ctx, logger.New(os.Stderr, logger.LevelFor(c.Debug)))` once, using the
`--debug` flag to pick between `slog.LevelDebug` and `slog.LevelInfo`. Every backend call
made from that command's `Run` inherits that logger through the context it was handed.

## The TEDAPI protobuf story

TEDAPI (both the WiFi-AP and v1r LAN variants) speaks protobuf, not JSON. The `.proto`
sources are vendored under `proto/` — `tedapi/`, `tedapi/combined/`, `tedapiv2/{common,
device,registration,transport}/`, and `teslapower/` — copied from Tesla's own gateway
firmware by way of pypowerwall's `tedapi` tooling (see the README's licensing section).

**Generated `.pb.go` files must never be hand-edited.** Each one embeds a serialized
`FileDescriptorProto` as a byte literal, and that descriptor's length-prefixed encoding
includes the `go_package` option string. A `sed`-style in-place edit that changes any string
in the file — including something as innocuous-looking as a package path — shifts every
subsequent length prefix in the embedded descriptor and corrupts it, in a way that will not
necessarily fail to compile but can break reflection-based protobuf operations at runtime.
Regenerate instead:

```bash
make proto
```

which runs, per `Makefile`:

```bash
protoc --proto_path=proto/tedapiv2 --go_out=. --go_opt=module=github.com/blackbirdworks/gopowerwall proto/tedapiv2/*.proto
protoc --proto_path=proto/tedapi --go_out=. --go_opt=module=github.com/blackbirdworks/gopowerwall proto/tedapi/tedapi.proto
protoc --proto_path=proto/tedapi/combined --go_out=. --go_opt=module=github.com/blackbirdworks/gopowerwall proto/tedapi/combined/tedapi_combined.proto
protoc --proto_path=proto/teslapower --go_out=. --go_opt=module=github.com/blackbirdworks/gopowerwall proto/teslapower/tesla.proto
```

Generation is byte-reproducible with `protoc-gen-go` v1.36.12 (the version pinned in
`go.mod`'s `google.golang.org/protobuf` requirement) — CI's proto job reruns `make proto`
and fails the build if the checked-in bindings drift from what that produces, which is the
intended guardrail against accidental hand edits.

`backend/tedapi/queries.go` and `system_info.go` build on top of the generated types:
`Query`/`GetQuery`/`GetQueryByName` select the right GraphQL-style TEDAPI query definition
for the configured `TEDAPIApiVersion` (`V2024_06` or `V2026_06`), and `SystemInfo`/
`SystemUpdate`/`RadioInfo` decode specific config responses (including a firmware git-hash
decoder) into Go structs.

## Further reading

- [local.md](local.md) — the gateway's own HTTPS REST API.
- [tedapi.md](tedapi.md) — protobuf over the gateway's WiFi AP.
- [v1r.md](v1r.md) — the RSA-signed TEDAPI variant for Powerwall 3's wired LAN.
- [cloud.md](cloud.md) — the Tesla Owner API.
- [fleetapi.md](fleetapi.md) — the official Tesla Fleet API.
- [../quickstart.md](../quickstart.md) — using the library and CLI end to end.
- [../docker.md](../docker.md) — running the proxy as a container.
- [../../MISSING.md](../../MISSING.md) — parity gaps and known issues.
