package version

import (
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
)

const (
	// Version is the library semantic version.
	Version = "0.12.0"
	// Build is the proxy build tag.
	Build = "t101"

	VersionMajor = 0
	VersionMinor = 12
	VersionPatch = 0
)

var (
	VersionTuple = [3]int{VersionMajor, VersionMinor, VersionPatch}
	versionRegex = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)*`)
)

// ParseVersion extracts semver digits from a firmware or gateway string and returns an integer.
// Matches Python parse_version("1.2.3") -> 10203 (3 + 2*100 + 1*10000).
func ParseVersion(v string) int {
	clean := strings.TrimSpace(v)
	if clean == "" {
		return 0
	}

	match := versionRegex.FindString(clean)
	if match == "" {
		return 0
	}

	parts := strings.Split(match, ".")
	if len(parts) == 0 {
		return 0
	}

	// Pad with up to 3 components
	for len(parts) < 3 {
		parts = append(parts, "0")
	}

	// Calculate component integer: part[0]*10000 + part[1]*100 + part[2]*1
	scale := 1
	total := 0
	for i := len(parts) - 1; i >= 0 && i >= len(parts)-3; i-- {
		n, err := strconv.Atoi(parts[i])
		if err == nil {
			total += n * scale
		}
		scale *= 100
	}

	return total
}

// BuildVersion is the gopowerwall release version, injected at link time via:
//
//	go build -ldflags "-X github.com/blackbirdworks/gopowerwall/pkgs/version.BuildVersion=<tag>"
//
// It is deliberately distinct from [Build], which is the pypowerwall proxy build
// tag reported verbatim on the proxy's parity API surface and must not change.
var BuildVersion = "dev" //nolint:gochecknoglobals // Build information is standard as a global.

// Get returns the injected build version, falling back to the module's embedded
// build info for `go install` builds, and finally to "dev".
func Get() string {
	if BuildVersion != "dev" {
		return BuildVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}

	return "dev"
}
