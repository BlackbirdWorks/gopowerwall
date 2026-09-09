// Package powerwall is a Go client library for the Tesla Energy Gateway
// that ships with Powerwall 2, Powerwall+, and Powerwall 3 installations. It
// is a Go port of [pypowerwall], the Python library and proxy server for the
// same gateway, and provides the same kind of cached, resilient access to
// site power, battery state, vitals, and control endpoints that pypowerwall
// gives Python programs - plus the derived views (aggregate meters,
// frequency/voltage, POD, and the composite /json-style [Powerwall.Snapshot])
// that pypowerwall's own proxy computes ad hoc, exposed here as typed
// methods any consumer can call directly.
//
// Parity with pypowerwall is a deliberate but scoped goal, limited to two
// surfaces: the gopowerwall CLI's subcommands, flags, and human-readable
// output, and the HTTP proxy server's routes and JSON response shapes (see
// the proxy subpackage). This package - the Go library itself - is free to
// be idiomatic Go rather than a line-for-line transliteration: every
// accessor that can fail returns (T, error) rather than a nil-on-failure
// pointer, sensor and reserve/level readers that used to fork on a trailing
// ...bool are now two clearly-named methods, and derived JSON shapes decode
// into typed [github.com/blackbirdworks/gopowerwall/models] structs instead
// of bare any. See docs/migration-v2.md in the module's source repository
// for a symbol-by-symbol before/after if you are updating code written
// against an earlier version.
//
// # Connecting
//
// [New] builds a [Config] from [DefaultConfig] plus a chain of [Option]
// functions (WithHost, WithPassword, and so on) and attempts to connect
// immediately, matching pypowerwall's own "construct and connect" pattern.
// A malformed [Config] is returned as an error with a nil *Powerwall. A
// failed *connection* attempt is different: New still returns a non-nil,
// usable [*Powerwall], now paired with a [ConnectError] instead of a
// silently discarded failure - callers that only care whether they have a
// live backend can check [Powerwall.IsConnected] (or [Powerwall.Mode]) and
// ignore the error entirely; callers that want to know why can
// [errors.As] it into a *ConnectError.
//
//	pw, err := gopowerwall.New(ctx,
//		gopowerwall.WithHost("192.168.91.1"),
//		gopowerwall.WithPassword("abcde"), // last 5 characters of the gateway password
//	)
//	var connectErr *gopowerwall.ConnectError
//	if err != nil && !errors.As(err, &connectErr) {
//		log.Fatal(err) // a genuine Config problem, not just "not connected yet"
//	}
//	defer pw.Close(ctx)
//
//	if !pw.IsConnected() {
//		log.Fatal("could not connect to gateway")
//	}
//
//	level, err := pw.LevelScaled(ctx)
//	if err != nil {
//		log.Fatal(err)
//	}
//	fmt.Println("mode:", pw.Mode(), "battery:", level)
//
// # Connection modes
//
// A [Powerwall] talks to a gateway through exactly one of five backends at a
// time, selected by [ConnectionMode]:
//
//   - local ([ModeLocal]) - the gateway's own HTTPS REST API over a
//     self-signed certificate, authenticated with the short customer
//     password (the last five characters of the full gateway WiFi
//     password). This is the fastest and most complete mode when the
//     gateway is reachable on the LAN.
//   - tedapi ([ModeTEDAPI]) - protobuf queries over the gateway's own WiFi
//     access point, authenticated with the full gateway WiFi password
//     instead of the short customer password. Use this for vitals,
//     per-string solar, and battery-block detail without the customer
//     password.
//   - v1r ([ModeV1r]) - the RSA-signed TEDAPI variant used by Powerwall 3
//     over the wired LAN, authenticated with a registered RSA private key.
//   - cloud ([ModeCloud]) - the Tesla Owner API, authenticated from an
//     existing OAuth2 token file rather than a gateway on the local
//     network.
//   - fleetapi ([ModeFleetAPI]) - the official Tesla Fleet API,
//     authenticated from an existing FleetAPI config file; the same
//     "already have a token file" caveat as cloud mode applies.
//
// When no mode is forced by [WithCloudMode]/[WithFleetAPI] and
// [WithAutoSelect] is set, [New] picks local if a host is configured,
// otherwise whichever of a FleetAPI or cloud token file exists under
// [Config.AuthPath]. Independently, [Powerwall.Connect] implements
// pypowerwall's circular fallback across modes (local -> FleetAPI -> cloud
// -> local) on a failed attempt, so [Powerwall.Mode] after connecting can
// differ from what was originally configured. See
// [github.com/blackbirdworks/gopowerwall/backend/local],
// [github.com/blackbirdworks/gopowerwall/backend/tedapi],
// [github.com/blackbirdworks/gopowerwall/backend/cloud], and
// [github.com/blackbirdworks/gopowerwall/backend/fleetapi] for what each
// backend actually implements, and docs/architecture in the module's
// source repository for the full picture.
//
// # Typed vs. untyped accessors
//
// Where pypowerwall itself would return an untyped dict/response,
// gopowerwall exposes typed accessors that decode into a
// [github.com/blackbirdworks/gopowerwall/models] struct wherever one
// exists - [Powerwall.SystemStatus], [Powerwall.SOE],
// [Powerwall.GridStatusResponse], [Powerwall.Operation],
// [Powerwall.SiteInfo], [Powerwall.Vitals], [Powerwall.Power],
// [Powerwall.Status], [Powerwall.SiteReading] and its Solar/Battery/Load
// siblings, and others - each returning (T, error), or a documented
// zero-value-on-failure for the handful (Power, Temps, Alerts, Strings,
// BatteryBlocks) that degrade gracefully by design. Built on top of those,
// [Powerwall.Aggregates], [Powerwall.Snapshot], [Powerwall.PODView], and
// [Powerwall.FrequencyView] are derived views - aggregation, unit
// conversion, and cross-endpoint composition that a Prometheus exporter or
// any other consumer would otherwise have to reimplement itself; the
// gopowerwall proxy is just one caller of these, not a special one.
// Endpoints without a typed accessor yet remain reachable through the
// lower-level [Powerwall.Poll], [Powerwall.PollRaw], and
// [Powerwall.PollJSON], which return any/[]byte/string respectively; prefer
// a typed accessor when one exists.
//
// [pypowerwall]: https://github.com/jasonacox/pypowerwall
package powerwall
