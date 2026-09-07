package gopowerwall

import "github.com/blackbirdworks/gopowerwall/pkgs/version"

const (
	VersionMajor = version.VersionMajor
	VersionMinor = version.VersionMinor
	VersionPatch = version.VersionPatch
	Version      = version.Version
)

// VersionTuple returns the library semantic version as a (major, minor, patch) tuple.
func VersionTuple() [3]int {
	return version.Tuple()
}

// ParseVersion extracts the semantic version digits from a firmware or gateway
// version string and packs them into a single comparable integer, so that
// "1.2.3" becomes 10203.
func ParseVersion(v string) int {
	return version.ParseVersion(v)
}
