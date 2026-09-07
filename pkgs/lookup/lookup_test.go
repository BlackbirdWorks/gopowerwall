package lookup_test

import (
	"testing"

	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
)

func TestLookupTable(t *testing.T) {
	t.Parallel()

	data := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": 42,
			},
		},
		"simple": "hello",
	}

	tests := []struct {
		expected any
		name     string
		keys     []string
	}{
		{
			name:     "flat key lookup",
			keys:     []string{"simple"},
			expected: "hello",
		},
		{
			name:     "nested variadic keys",
			keys:     []string{"a", "b", "c"},
			expected: 42,
		},
		{
			name:     "nested dot notation",
			keys:     []string{"a.b.c"},
			expected: 42,
		},
		{
			name:     "missing key",
			keys:     []string{"a", "missing"},
			expected: nil,
		},
		{
			name:     "nil input",
			keys:     []string{"a"},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var input any = data
			if tt.name == "nil input" {
				input = nil
			}

			got := lookup.Lookup(input, tt.keys...)
			if got != tt.expected {
				t.Errorf("Lookup(%v) = %v, want %v", tt.keys, got, tt.expected)
			}
		})
	}
}
