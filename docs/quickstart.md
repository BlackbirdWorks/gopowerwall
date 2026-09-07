# Quickstart

This walks through using gopowerwall as a Go library against both a local gateway and
Tesla's cloud, then the equivalent CLI commands. For route-by-route proxy configuration see
[docker.md](docker.md) and the main [README](../README.md); for how the pieces fit together
see [architecture/README.md](architecture/README.md).

## Prerequisites

- Go 1.27+ (matching `go.mod`), or a prebuilt binary/Docker image — see the README's
  Installation section.
- For **local** mode: LAN access to the gateway and its customer password (the last 5
  characters of the full gateway WiFi password).
- For **tedapi**/**v1r** mode: the full gateway WiFi password, and for v1r an RSA private
  key registered with the gateway (see [architecture/v1r.md](architecture/v1r.md)).
- For **cloud**/**fleetapi** mode: a `.pypowerwall.auth` or `.pypowerwall.fleetapi` file
  produced by another tool. gopowerwall's own `setup`/`authtoken`/`register` commands do
  not yet drive the OAuth or RSA registration flow that creates these files — see
  [MISSING.md](../MISSING.md).

## Add the module

```bash
go get github.com/blackbirdworks/gopowerwall
```

## Local gateway mode

This is the fastest and most complete mode: everything the gateway itself exposes is
reachable, including vitals and control endpoints.

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/blackbirdworks/gopowerwall"
)

func main() {
	ctx := context.Background()

	pw, err := gopowerwall.New(ctx,
		gopowerwall.WithHost("192.168.91.1"),
		gopowerwall.WithPassword("abcde"), // last 5 chars of the gateway password
		gopowerwall.WithCloudMode(false),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer pw.Close(ctx)

	if !pw.IsConnected() {
		log.Fatal("could not connect to gateway")
	}

	fmt.Println("mode:", pw.Mode()) // "local"
	fmt.Println("battery level:", *pw.Level(ctx, true))
	fmt.Println("grid status:", pw.GridStatus(ctx))
	fmt.Printf("power: %+v\n", pw.Power(ctx))

	// Typed accessors decode straight into models.* structs instead of any/map[string]any.
	status, err := pw.SystemStatus(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("battery blocks: %d\n", len(status.BatteryBlocks))
}
```

`New` attempts a connection synchronously; a failed connection is not a returned error —
check `pw.IsConnected()` afterward, matching pypowerwall's own "connect and check" pattern.
Every data-fetching method takes `ctx` as its first argument, so callers control
cancellation, timeouts, and (via [`pkgs/logger`](../pkgs/logger)) how much gets logged for
that call.

### Reading and writing settings

```go
// Read
reserve := pw.GetReserve(ctx, false) // *float64, nil if unavailable
mode := pw.GetMode(ctx)              // *string

// Write (local mode requires no extra scope; cloud/fleetapi cap reserve at 80%)
if _, err := pw.SetReserve(ctx, 20); err != nil {
	log.Println("set reserve failed:", err)
}
if _, err := pw.SetMode(ctx, "self_consumption"); err != nil {
	log.Println("set mode failed:", err)
}
```

### Raw endpoint access

`Poll`/`PollRaw`/`PollJSON` mirror pypowerwall's low-level `poll()` for endpoints without a
typed accessor yet:

```go
raw := pw.Poll(ctx, "/api/system_status/grid_status")            // any (map[string]any, usually)
bytes := pw.PollRaw(ctx, "/api/system_status/grid_status")        // []byte, bypasses JSON decoding
json := pw.PollJSON(ctx, "/api/system_status/grid_status")        // string
```

Passing the same endpoint through both a raw and non-raw poll on the **local** backend
specifically can race against its single-key cache — see the second known issue in
[MISSING.md](../MISSING.md) before relying on that combination.

## TEDAPI mode

For vitals, per-string solar, and battery-block detail without the customer password, use
TEDAPI directly:

```go
pw, err := gopowerwall.New(ctx,
	gopowerwall.WithGwPwd("<full gateway wifi password>"),
	// WithHost defaults to 192.168.91.1, the gateway's own WiFi AP, when unset.
)
```

See [architecture/tedapi.md](architecture/tedapi.md) for the WiFi-AP variant and
[architecture/v1r.md](architecture/v1r.md) for the RSA-signed LAN variant used by
Powerwall 3.

## Cloud and Fleet API modes

Both require an existing auth file; gopowerwall does not create one for you yet (see
Prerequisites above). Once the file exists in `--authpath`/`PW_AUTH_PATH`:

```go
pw, err := gopowerwall.New(ctx,
	gopowerwall.WithEmail("me@example.com"),
	gopowerwall.WithAuthPath("."),      // directory holding .pypowerwall.auth
	gopowerwall.WithCloudMode(true),
	gopowerwall.WithFleetAPI(false),    // true selects Fleet API instead of the Owner API
)
```

Or let gopowerwall pick automatically: call `gopowerwall.New` with `WithAutoSelect(true)`
and no `WithHost`, and it looks for a FleetAPI config file first, then a cloud auth file, in
`AuthPath`. See [architecture/cloud.md](architecture/cloud.md) and
[architecture/fleetapi.md](architecture/fleetapi.md) for what each mode can and cannot read
— several introspection endpoints return fixed stub data rather than live values in these
two modes (see [MISSING.md](../MISSING.md)).

## Equivalent CLI usage

Everything above has a CLI counterpart:

```bash
# Local mode, human-readable
gopowerwall get --local --host 192.168.91.1 --password abcde

# Local mode, JSON
gopowerwall get --local --host 192.168.91.1 --password abcde --format json

# TEDAPI mode
gopowerwall get --tedapi --gw_pwd <gateway_wifi_password>

# Set mode and reserve
gopowerwall set --local --host 192.168.91.1 --password abcde --mode self_consumption --reserve 20

# Run the proxy (defaults to 0.0.0.0:8675)
gopowerwall proxy --local --host 192.168.91.1 --password abcde
```

`--debug` on any of these installs a debug-level `log/slog` logger for the whole call —
see [architecture/README.md](architecture/README.md#context-and-logging) for how that's threaded
through. The full flag and environment-variable reference lives in the main
[README](../README.md).

## Next steps

- [architecture/README.md](architecture/README.md) — how `cmd` → `commands` → the
  `Powerwall` facade → `backend` → `client` fit together, and how the proxy consumes the
  facade.
- One page per connection mode: [local](architecture/local.md),
  [tedapi](architecture/tedapi.md), [v1r](architecture/v1r.md),
  [cloud](architecture/cloud.md), [fleetapi](architecture/fleetapi.md).
- [docker.md](docker.md) — running the proxy as a container.
- [MISSING.md](../MISSING.md) — what isn't ported yet, and three confirmed implementation
  issues worth knowing about before relying on this in production.
