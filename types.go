package gopowerwall

import "github.com/blackbirdworks/gopowerwall/models"

// ConnectionMode identifies which backend a [Powerwall] is (or is trying to
// be) connected through. [Powerwall.Mode] returns the active value; [New]
// and [Powerwall.Connect] are what change it.
type ConnectionMode string

const (
	// ModeUnknown is the zero value: no connection mode has been selected or
	// determined yet. [Powerwall.Connect] refuses to run while the mode is
	// ModeUnknown.
	ModeUnknown ConnectionMode = "unknown"
	// ModeLocal selects the gateway's own local HTTPS REST API
	// (backend/local), authenticated with the short customer password.
	ModeLocal ConnectionMode = "local"
	// ModeHybrid marks a local-mode connection that has also layered a
	// TEDAPI client on top (see [TEDAPIHybrid]). Powerwall.mode itself never
	// takes this value in current code - it exists for the sub-mode combination,
	// not as a value New or Connect assigns to a Powerwall.
	ModeHybrid ConnectionMode = "hybrid"
	// ModeTEDAPI selects a pure TEDAPI connection over the gateway's WiFi
	// access point (backend/tedapi), authenticated with the full gateway
	// WiFi password rather than the local customer password.
	ModeTEDAPI ConnectionMode = "tedapi"
	// ModeV1r selects the RSA-signed TEDAPI variant used by Powerwall 3 over
	// the wired LAN (backend/tedapi's v1r.go), authenticated with an RSA
	// private key plus the last five characters of the gateway password.
	ModeV1r ConnectionMode = "v1r"
	// ModeCloud selects the Tesla Owner API (backend/cloud), authenticated
	// from an existing OAuth2 token file rather than a gateway on the LAN.
	ModeCloud ConnectionMode = "cloud"
	// ModeFleetAPI selects the official Tesla Fleet API (backend/fleetapi),
	// authenticated from an existing FleetAPI config file.
	ModeFleetAPI ConnectionMode = "fleetapi"
)

type (
	// TEDAPIMode is an alias of [github.com/blackbirdworks/gopowerwall/models.TEDAPIMode],
	// re-exported so callers need not import the models package directly.
	// [Powerwall.TEDAPIMode] returns the active value.
	TEDAPIMode = models.TEDAPIMode
	// AuthMode is an alias of [github.com/blackbirdworks/gopowerwall/models.AuthMode],
	// re-exported so callers need not import the models package directly. It
	// selects how a backend authenticates: [AuthModeCookie] or [AuthModeToken]
	// for the local backend, [AuthModeBasic] or [AuthModeBearer] for TEDAPI.
	AuthMode = models.AuthMode
	// TEDAPIApiVersion is an alias of
	// [github.com/blackbirdworks/gopowerwall/models.TEDAPIApiVersion],
	// re-exported so callers need not import the models package directly. It
	// selects which TEDAPI protobuf/query definitions [WithTEDAPIApiVersion]
	// configures a connection to use.
	TEDAPIApiVersion = models.TEDAPIApiVersion
)

const (
	// TEDAPIOff means no TEDAPI client is active for the current connection.
	TEDAPIOff = models.TEDAPIOff
	// TEDAPIFull means a pure TEDAPI connection is active (ModeTEDAPI).
	TEDAPIFull = models.TEDAPIFull
	// TEDAPIHybrid means a TEDAPI client is layered on top of an
	// authenticated local-mode session (see connectLocal's GwPwd branch).
	TEDAPIHybrid = models.TEDAPIHybrid
	// TEDAPIV1r means the RSA-signed v1r TEDAPI variant is active (ModeV1r).
	TEDAPIV1r = models.TEDAPIV1r

	// AuthModeCookie selects cookie-based session authentication, the
	// default for the local backend.
	AuthModeCookie = models.AuthModeCookie
	// AuthModeToken selects token-based authentication for the local
	// backend.
	AuthModeToken = models.AuthModeToken
	// AuthModeBasic selects HTTP Basic authentication, the default for
	// TEDAPI ([WithTEDAPIAuthMode]).
	AuthModeBasic = models.AuthModeBasic
	// AuthModeBearer selects bearer-token authentication for TEDAPI
	// ([WithTEDAPIAuthMode]).
	AuthModeBearer = models.AuthModeBearer

	// TEDAPIVersion2024_06 selects the 2024-06 TEDAPI protobuf/query
	// definitions, the default set by [DefaultConfig].
	TEDAPIVersion2024_06 = models.TEDAPIVersion2024_06
	// TEDAPIVersion2026_06 selects the 2026-06 TEDAPI protobuf/query
	// definitions.
	TEDAPIVersion2026_06 = models.TEDAPIVersion2026_06
)

// CoerceTEDAPIApiVersion converts s into a valid [TEDAPIApiVersion],
// accepting case-insensitive variants such as "2026_06" or "V2026" in
// addition to the canonical "V2024_06"/"V2026_06" strings. Anything it does
// not recognise - including an empty string - falls back to
// [TEDAPIVersion2024_06] rather than returning an error, so a typo silently
// selects the older query set instead of failing configuration.
func CoerceTEDAPIApiVersion(s string) TEDAPIApiVersion {
	return models.CoerceTEDAPIApiVersion(s)
}

// PowerData is an alias of
// [github.com/blackbirdworks/gopowerwall/models.PowerSummary]: instant power
// in Watts for the site (grid) meter, solar, battery, and load channels, as
// returned by [Powerwall.Power].
type PowerData = models.PowerSummary
