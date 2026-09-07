package gopowerwall

import "github.com/blackbirdworks/gopowerwall/pkgs/validation"

// IsValidHost checks if a string is a valid IP address or hostname, optionally with a :port suffix.
func IsValidHost(host string) bool {
	return validation.IsValidHost(host)
}

// IsValidEmail checks if an email string is formatted validly.
func IsValidEmail(email string) bool {
	return validation.IsValidEmail(email)
}
