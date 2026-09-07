package models

import "strings"

// TEDAPIMode represents the TEDAPI sub-mode.
type TEDAPIMode string

const (
	TEDAPIOff    TEDAPIMode = "off"
	TEDAPIFull   TEDAPIMode = "full"
	TEDAPIHybrid TEDAPIMode = "hybrid"
	TEDAPIV1r    TEDAPIMode = "v1r"
)

// TEDAPIApiVersion represents the protobuf/query version set.
type TEDAPIApiVersion string

const (
	TEDAPIVersion2024_06 TEDAPIApiVersion = "V2024_06"
	TEDAPIVersion2026_06 TEDAPIApiVersion = "V2026_06"
)

// CoerceTEDAPIApiVersion parses a string into a valid TEDAPIApiVersion.
func CoerceTEDAPIApiVersion(v string) TEDAPIApiVersion {
	u := strings.ToUpper(strings.TrimSpace(v))
	if u == "V2026_06" || u == "2026_06" || u == "V2026" {
		return TEDAPIVersion2026_06
	}

	return TEDAPIVersion2024_06
}
