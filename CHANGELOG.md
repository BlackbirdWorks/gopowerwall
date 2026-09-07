# Changelog

All notable changes to gopowerwall are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
The current library version is tracked in `pkgs/version.Version`.

## [Unreleased]

Nothing yet.

## [0.12.0] - 2026-09-07

Initial Go port of [pypowerwall](https://github.com/jasonacox/pypowerwall): the
`gopowerwall` library, its CLI, and the HTTP proxy server, covering all five
gateway connection modes (local, TEDAPI WiFi, TEDAPI v1r LAN, Tesla Cloud, and
the Tesla Fleet API).

### Added

- The `gopowerwall` CLI (`cmd/gopowerwall`), built on `kong`, with ten
  subcommands: `version`, `register`, `authtoken`, `get`, `tedapi`, `setup`,
  `cloudcheck`, `proxy`, `set`, and `scan`. `get`, `set`, `proxy`, and `scan`
  are fully functional; `setup`, `authtoken`, `register`, `cloudcheck`, and
  `tedapi` currently print static guidance text rather than performing a live
  OAuth flow, RSA registration, or diagnostic check — see `MISSING.md`.
- A standalone `proxy` binary (`cmd/proxy`) and a `gopowerwall proxy`
  subcommand, both serving the pypowerwall-compatible route surface:
  `/aggregates`, `/soe`, `/vitals`, `/strings`, `/csv`, `/csv/v2`, `/pod`,
  `/freq`, `/temps/pw`, `/alerts/pw`, `/stats`, `/health`, `/version`,
  `/help`, `/control/*`, a gopowerwall-specific `/pw/*` facade, an
  allow-listed passthrough of the gateway's own `/api/*` routes, and the
  embedded web UI.
- Five connection backends under `backend/`: `local` (the gateway's own HTTPS
  API), `tedapi` (protobuf over the gateway's WiFi AP), the RSA-signed `v1r`
  LAN variant of TEDAPI (`backend/tedapi/v1r.go`, for Powerwall 3), `cloud`
  (the Tesla Owner API), and `fleetapi` (the official Tesla Fleet API), all
  selected through `Powerwall.autoSelectMode` or forced via CLI/library flags.
- Vendored Tesla TEDAPI protobuf definitions under `proto/` (`tedapi`,
  `tedapi/combined`, `tedapiv2/*`, `teslapower`), regenerated with `make
  proto` and byte-reproducible with `protoc-gen-go` v1.36.12.
- The gopherstack engineering tooling kit: a `Makefile` with
  `build`/`build-linux`/`lint`/`lint-fix`/`test`/`integration-test`/
  `total-coverage`/`proto`/`bench`/`clean`/`upgrade`; CI running
  golangci-lint, govulncheck, CodeQL, unit and integration tests, a merged
  coverage gate, a proto-drift check, and a static build; and goreleaser
  configuration publishing both binaries plus a multi-arch proxy container
  image to `ghcr.io/blackbirdworks/gopowerwall`.
- `log/slog`-based structured logging carried on `context.Context`
  (`pkgs/logger`), replacing a package-global printf logger, with the CLI's
  `--debug` flag installing a level-configured logger for the whole call tree.

### Fixed

Found and fixed while rewriting the test suite from 136 to 704 tests (each
with a regression test that failed before the fix — see `git log` for full
detail):

- `scan.Scan` raced on its progress writer: only the results slice append was
  mutex-guarded, while both progress `fmt.Fprintf` calls wrote to a shared
  `io.Writer` from every worker goroutine. Fixed with a dedicated output
  mutex.
- `Powerwall.Strings` ranged over `[]string{"A","B","C","D"}` by index rather
  than by value, so it built lookup keys `PVAC_Vsolar0`..`PVAC_Vsolar3`
  instead of `PVAC_VsolarA`..`PVAC_VsolarD`, and keyed its result map only by
  label, so multiple PVAC inverters on one site silently overwrote each
  other's entries. Both are fixed. The field names themselves
  (`PVAC_Vsolar<label>` etc.) remain unverified against real hardware — see
  `MISSING.md`.
- `Powerwall.Alerts` asserted device-level alerts were always `[]any`, so the
  local backend's `[]string` alerts (stored directly from the protobuf
  accessor) were silently dropped from `/alerts`. Now handles both shapes.
- The proxy's `handleWeb` called `Header().Set("Set-Cookie", ...)` twice, so
  the second call overwrote the first and `AuthCookie` was never actually
  sent to the browser. Changed to `Header().Add`.
- `proxy.NewServer` never passed `cfg.CacheFile` through when constructing its
  `Powerwall` client, making `PW_CACHE_FILE` a dead configuration knob and
  pinning every proxy instance to `./.powerwall` relative to the working
  directory.
- `Level(scale)` and `GetReserve(scale)` computed `val * 100.0 / 100.0` — an
  identity — so the reserve/level scaling option did nothing. The documented
  pypowerwall formula, `(level / 0.95) - (5 / 0.95)`, now lives in
  `pkgs/calc.ScaleBatteryLevel` and backs both call sites.

### Changed

- Logging migrated from a package-global, mutex-protected printf logger to
  `log/slog` carried on `context.Context`, threaded through
  `backend/{local,tedapi,cloud,fleetapi}`, the `Powerwall` facade, and the
  proxy. Every HTTP call now uses `http.NewRequestWithContext` instead of
  `context.Background()`, so requests are cancellable end to end.
- Removed the root-level `cache.go`, `regex.go`, and `version.go` shims that
  only re-exported symbols from `pkgs/cache`, `pkgs/validation`, and
  `pkgs/version` to mirror pypowerwall's flat module layout; call sites now
  import those packages directly.
- `scan.Scan` takes a caller-supplied `context.Context` and `io.Writer`
  instead of hardcoding `context.Background()` and `os.Stdout`, so it can be
  reused from a server or test without dragging in package-level I/O.
- `version.VersionTuple` (a mutable `[3]int` global) replaced by
  `version.Tuple()`, a pure function.
