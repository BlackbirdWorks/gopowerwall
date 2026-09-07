package version_test

import (
	"testing"

	"github.com/blackbirdworks/gopowerwall/pkgs/version"
)

func TestParseVersionTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "standard three component semver",
			input:    "1.2.3",
			expected: 10203,
		},
		{
			name:     "two components padded with zero",
			input:    "2.5",
			expected: 20500,
		},
		{
			name:     "version with prefix and suffix build string",
			input:    "v23.44.1-build",
			expected: 234401,
		},
		{
			name:     "firmware version with space and git hash",
			input:    "24.36.2 46990655",
			expected: 243602,
		},
		{
			name:     "unknown gateway string returns 0",
			input:    "unknown",
			expected: 0,
		},
		{
			name:     "empty string returns 0",
			input:    "",
			expected: 0,
		},
		{
			name:     "single component version",
			input:    "5",
			expected: 50000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := version.ParseVersion(tt.input)
			if got != tt.expected {
				t.Errorf("ParseVersion(%q) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}
