package version_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/gopowerwall/pkgs/version"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		in   string
		want int
	}

	for _, tc := range []testCase{
		{name: "three components", in: "1.2.3", want: 10203},
		{name: "two components padded with zero", in: "2.5", want: 20500},
		{name: "prefix and build suffix stripped", in: "v23.44.1-build", want: 234401},
		{name: "firmware version with trailing git hash", in: "24.36.2 46990655", want: 243602},
		{name: "unknown gateway string returns zero", in: "unknown", want: 0},
		{name: "empty string returns zero", in: "", want: 0},
		{name: "single component version", in: "5", want: 50000},
		{name: "surrounding whitespace trimmed", in: "  1.0.0  ", want: 10000},
		{name: "four components truncates to last three", in: "1.2.3.4", want: 20304},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, version.ParseVersion(tc.in))
		})
	}
}

func TestTuple(t *testing.T) {
	t.Parallel()

	want := [3]int{version.VersionMajor, version.VersionMinor, version.VersionPatch}
	assert.Equal(t, want, version.Tuple())
}

func TestConstants(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		got  string
		want string
	}

	for _, tc := range []testCase{
		{name: "library version", got: version.Version, want: "0.12.0"},
		{name: "proxy build tag", got: version.Build, want: "t101"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.got)
		})
	}
}

// TestGet is a thin smoke test for the exported entry point. The branch logic
// itself (including the ldflags-override path) is table-tested against the
// pure, unexported resolve helper in resolve_internal_test.go, so this test
// only needs to read the BuildVersion package global, never mutate it, and is
// therefore safe to run in parallel.
func TestGet(t *testing.T) {
	t.Parallel()

	assert.NotEmpty(t, version.Get(), "Get should fall back to build info or \"dev\"")
}
