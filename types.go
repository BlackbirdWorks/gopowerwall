package gopowerwall

import "github.com/blackbirdworks/gopowerwall/models"

// ConnectionMode represents the active backend mode.
type ConnectionMode string

const (
	ModeUnknown  ConnectionMode = "unknown"
	ModeLocal    ConnectionMode = "local"
	ModeHybrid   ConnectionMode = "hybrid"
	ModeTEDAPI   ConnectionMode = "tedapi"
	ModeV1r      ConnectionMode = "v1r"
	ModeCloud    ConnectionMode = "cloud"
	ModeFleetAPI ConnectionMode = "fleetapi"
)

type (
	TEDAPIMode       = models.TEDAPIMode
	AuthMode         = models.AuthMode
	TEDAPIApiVersion = models.TEDAPIApiVersion
)

const (
	TEDAPIOff    = models.TEDAPIOff
	TEDAPIFull   = models.TEDAPIFull
	TEDAPIHybrid = models.TEDAPIHybrid
	TEDAPIV1r    = models.TEDAPIV1r

	AuthModeCookie = models.AuthModeCookie
	AuthModeToken  = models.AuthModeToken
	AuthModeBasic  = models.AuthModeBasic
	AuthModeBearer = models.AuthModeBearer

	TEDAPIVersion2024_06 = models.TEDAPIVersion2024_06
	TEDAPIVersion2026_06 = models.TEDAPIVersion2026_06
)

// CoerceTEDAPIApiVersion converts a string to a valid TEDAPIApiVersion.
func CoerceTEDAPIApiVersion(s string) TEDAPIApiVersion {
	return models.CoerceTEDAPIApiVersion(s)
}

// PowerData contains power usage across the 4 main channels in Watts.
type PowerData = models.PowerSummary

// GridStatusOutput specifies the return format for GridStatus().
type GridStatusOutput string

const (
	GridStatusString  GridStatusOutput = "string"
	GridStatusJSON    GridStatusOutput = "json"
	GridStatusNumeric GridStatusOutput = "numeric"
)
