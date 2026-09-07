package validation_test

import (
	"testing"

	"github.com/blackbirdworks/gopowerwall/pkgs/validation"
)

func TestValidationTable(t *testing.T) {
	t.Parallel()

	hostTests := []struct {
		host  string
		name  string
		valid bool
	}{
		{
			name:  "valid IPv4",
			host:  "192.168.91.1",
			valid: true,
		},
		{
			name:  "valid IPv4 with port",
			host:  "192.168.91.1:443",
			valid: true,
		},
		{
			name:  "valid hostname",
			host:  "teg.lan",
			valid: true,
		},
		{
			name:  "empty string",
			host:  "",
			valid: false,
		},
	}

	for _, tt := range hostTests {
		t.Run("host_"+tt.name, func(t *testing.T) {
			t.Parallel()

			got := validation.IsValidHost(tt.host)
			if got != tt.valid {
				t.Errorf("IsValidHost(%q) = %v, want %v", tt.host, got, tt.valid)
			}
		})
	}

	emailTests := []struct {
		email string
		name  string
		valid bool
	}{
		{
			name:  "valid email",
			email: "user@example.com",
			valid: true,
		},
		{
			name:  "missing domain",
			email: "user@",
			valid: false,
		},
		{
			name:  "empty string",
			email: "",
			valid: false,
		},
	}

	for _, tt := range emailTests {
		t.Run("email_"+tt.name, func(t *testing.T) {
			t.Parallel()

			got := validation.IsValidEmail(tt.email)
			if got != tt.valid {
				t.Errorf("IsValidEmail(%q) = %v, want %v", tt.email, got, tt.valid)
			}
		})
	}
}
