// Package gopowerwall is a Go client library for the Tesla Energy Gateway
// that ships with Powerwall 2, Powerwall+, and Powerwall 3 installations. It
// is a Go port of [pypowerwall], the Python library and proxy server for the
// same gateway, and provides the same kind of cached, resilient access to
// site power, battery state, vitals, and control endpoints that pypowerwall
// gives Python programs.
//
// Parity with pypowerwall is a deliberate but scoped goal, limited to two
// surfaces: the gopowerwall CLI's subcommands, flags, and human-readable
// output, and the HTTP proxy server's routes and JSON response shapes (see
// the proxy subpackage). This package - the Go library itself - is free to
// be idiomatic Go rather than a line-for-line transliteration, and in
// several places (documented on the affected methods below) it still shows
// its Python origins: methods that return bare any, pointer types used to
// signal "value unavailable" with the underlying error discarded, and
// variadic ...bool parameters standing in for Python's optional keyword
// arguments. Read each such method's doc comment before relying on it.
//
// # Connecting
//
// [New] builds a [Config] from [DefaultConfig] plus a chain of [Option]
// functions (WithHost, WithPassword, and so on) and attempts to connect
// immediately, matching pypowerwall's own "construct and connect" pattern.
// A malformed [Config] is returned as an error; a failed *connection*
// attempt is not - New logs it and still returns a non-nil [*Powerwall]
// with a nil error, so callers must check [Powerwall.IsConnected] (or
// [Powerwall.Mode]) afterward rather than trusting the error return alone.
//
//	pw, err := gopowerwall.New(ctx,
//		gopowerwall.WithHost("192.168.91.1"),
//		gopowerwall.WithPassword("abcde"), // last 5 characters of the gateway password
//	)
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer pw.Close(ctx)
//
//	if !pw.IsConnected() {
//		log.Fatal("could not connect to gateway")
//	}
//
//	level := pw.Level(ctx, true) // *float64, nil if unavailable
//	fmt.Println("mode:", pw.Mode(), "battery:", *level)
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
// [Powerwall.SiteInfo], [Powerwall.Vitals], [Powerwall.Power], and others -
// each returning (T, error) or a zero value on failure as documented on the
// method. Endpoints without a typed accessor yet remain reachable through
// the lower-level [Powerwall.Poll], [Powerwall.PollRaw], and
// [Powerwall.PollJSON], which return any/[]byte/string respectively; prefer
// a typed accessor when one exists.
//
// [pypowerwall]: https://github.com/jasonacox/pypowerwall
package gopowerwall
