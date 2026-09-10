---
trigger: always_on
---

## Domain rules

gopowerwall is a Go port of [pypowerwall](https://github.com/jasonacox/pypowerwall). The
upstream project is the reference for behaviour, not for Go structure.

### Parity contract

Parity with pypowerwall is required for two surfaces only:

- The `gopowerwall` CLI: subcommand names, flags, and human-readable output.
- The proxy's HTTP surface: route paths, JSON response shapes, and field names.

Everything else - package layout, exported Go identifiers, function signatures - is free to
be idiomatic Go. Do not add re-export shims whose only purpose is to mirror a Python module
layout.

Values that appear on the parity surface are frozen. In particular `version.Build`
("t101") is pypowerwall's proxy build tag reported by `/api/status`; it is a constant and
must not be repurposed for release versioning. Use `version.BuildVersion`, which the
Makefile injects via ldflags, for that.


### When in doubt, read the Python

pypowerwall is the authority on behaviour. When you are unsure what a field means, what
an endpoint returns, what a default should be, or how an edge case is handled, **go and
read the upstream source** rather than inferring it from this repo, from a fixture, or
from what seems reasonable.

    https://github.com/jasonacox/pypowerwall

Fetch the raw files and read them directly; a summarised fetch loses the exact field
names and constants that matter here. Useful entry points:

    pypowerwall/__init__.py                  the Powerwall facade and its defaults
    pypowerwall/local/pypowerwall_local.py   local gateway HTTP
    pypowerwall/tedapi/__init__.py           TEDAPI, including vitals synthesis
    pypowerwall/cloud/pypowerwall_cloud.py   Owner API
    pypowerwall/fleetapi/pypowerwall_fleetapi.py
    proxy/server.py                          the proxy routes and computed fields
    pwsimulator/stub.py                      what the emulator actually serves

Cite what you found as `file:line` when you record a conclusion, and say which upstream
ref you read: pypowerwall changes, and an uncited claim cannot be rechecked later.
`docs/parity-matrix.md` is an audit against a specific commit and is a good starting
point, but it is a snapshot, not a substitute for the source.

If the Python genuinely does not settle the question, say so explicitly and mark the
conclusion unverified rather than inventing a plausible answer.

### Connection modes

Five backends live under `powerwall/`, selected by `ConnectionMode` in `types.go`:

- `local` - the gateway's own HTTP API over self-signed TLS.
- `tedapi` - protobuf over the gateway's `/tedapi` endpoint.
- `cloud` - the Tesla Owner API.
- `fleetapi` - the Tesla Fleet API.
- `v1r` - the LAN/RSA TEDAPI variant, in `powerwall/tedapi/v1r.go`.

`InsecureSkipVerify` against the local gateway is inherent to the protocol - the gateway
ships a self-signed certificate. Those `gosec` waivers are legitimate and stay.

### Protobuf

The Tesla `.proto` definitions under `proto/` are vendored. Regenerate with `make proto`,
never by hand: the generated files embed a serialised descriptor whose length prefixes
encode the `go_package` string, so editing a generated file with sed corrupts it.
Generation is byte-reproducible with protoc-gen-go v1.36.12.

### Fixtures

`proxy/web/bogus/*.json` are recorded gateway responses. Prefer them over hand-written
literals when building `httptest` fixtures.
