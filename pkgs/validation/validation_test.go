package validation_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/gopowerwall/pkgs/validation"
)

func TestIsValidHost(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		host string
		want bool
	}

	for _, tc := range []testCase{
		{name: "valid IPv4", host: "192.168.91.1", want: true},
		{name: "valid IPv4 with port", host: "192.168.91.1:443", want: true},
		{name: "valid hostname", host: "teg.lan", want: true},
		{name: "valid hostname with port", host: "teg.lan:443", want: true},
		{name: "valid fully qualified hostname with trailing dot", host: "teg.example.com.", want: true},
		{name: "valid IPv6", host: "::1", want: true},
		{name: "empty string", host: "", want: false},
		{name: "invalid hostname characters", host: "!!!bad!!!", want: false},
		{name: "too long", host: strings.Repeat("a", 256), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, validation.IsValidHost(tc.host))
		})
	}
}

func TestIsValidEmail(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name  string
		email string
		want  bool
	}

	for _, tc := range []testCase{
		{name: "valid email", email: "user@example.com", want: true},
		{name: "missing domain", email: "user@", want: false},
		{name: "missing at sign", email: "userexample.com", want: false},
		{name: "empty string", email: "", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, validation.IsValidEmail(tc.email))
		})
	}
}
