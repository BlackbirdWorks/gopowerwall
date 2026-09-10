package lookup_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
)

var errSentinel = errors.New("test error")

type orNilTestCase struct {
	err      error
	input    any
	expected any
	name     string
}

func TestOrNil(t *testing.T) {
	t.Parallel()

	tests := []orNilTestCase{
		{
			name:     "string with nil error returns string",
			input:    "hello",
			err:      nil,
			expected: "hello",
		},
		{
			name:     "string with error returns nil",
			input:    "hello",
			err:      errSentinel,
			expected: nil,
		},
		{
			name:     "float64 with nil error returns float",
			input:    42.5,
			err:      nil,
			expected: 42.5,
		},
		{
			name:     "float64 with error returns nil",
			input:    42.5,
			err:      errSentinel,
			expected: nil,
		},
		{
			name:     "int with nil error returns int",
			input:    100,
			err:      nil,
			expected: 100,
		},
		{
			name:     "int with error returns nil",
			input:    100,
			err:      errSentinel,
			expected: nil,
		},
		{
			name:     "duration with nil error returns duration",
			input:    5 * time.Minute,
			err:      nil,
			expected: 5 * time.Minute,
		},
		{
			name:     "duration with error returns nil",
			input:    5 * time.Minute,
			err:      errSentinel,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := lookup.OrNil(tt.input, tt.err)
			assert.Equal(t, tt.expected, got)
		})
	}
}
