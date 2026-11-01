// Package validation provides host and email format validators.
package validation

import (
	"errors"
	"math"
	"net"
	"net/mail"
	"regexp"
	"strings"
)

const (
	maxHostLength              = 255
	MinDispatchDurationSeconds = 1
	MaxDispatchDurationSeconds = 86400
)

var (
	// ErrInvalidDispatchPower indicates power_watts is NaN or infinite.
	ErrInvalidDispatchPower = errors.New("power_watts must be a finite number")
	// ErrInvalidDispatchDuration indicates duration_seconds is outside [1, 86400].
	ErrInvalidDispatchDuration = errors.New("duration_seconds must be an integer from 1 to 86400")
)

// HostRegex matches valid hostnames or FQDNs.
var HostRegex = regexp.MustCompile(
	`^[0-9A-Za-z](?:(?:[0-9A-Za-z]|-){0,61}[0-9A-Za-z])?(?:\.[0-9A-Za-z](?:(?:[0-9A-Za-z]|-){0,61}[0-9A-Za-z])?)*\.?$`,
)

// EmailRegex matches a valid email address.
var EmailRegex = regexp.MustCompile(`^\S+@\S+\.\S+$`)

// IsValidHost checks if a string is a valid IP address or hostname, optionally with a :port suffix.
func IsValidHost(host string) bool {
	if host == "" || len(host) > maxHostLength {
		return false
	}
	hostPart := host
	if strings.Count(host, ":") == 1 {
		parts := strings.Split(host, ":")
		hostPart = parts[0]
	}

	if ip := net.ParseIP(hostPart); ip != nil {
		return true
	}

	return HostRegex.MatchString(hostPart)
}

// IsValidEmail checks if an email string is formatted validly.
func IsValidEmail(email string) bool {
	if email == "" {
		return false
	}
	if !EmailRegex.MatchString(email) {
		return false
	}
	_, err := mail.ParseAddress(email)

	return err == nil
}

// ValidateBatteryDispatch rejects non-finite powerWatts or durationSeconds outside [1, 86400].
// Matches pypowerwall.helpers.validate_battery_dispatch.
func ValidateBatteryDispatch(powerWatts float64, durationSeconds int) error {
	if math.IsNaN(powerWatts) || math.IsInf(powerWatts, 0) {
		return ErrInvalidDispatchPower
	}
	if durationSeconds < MinDispatchDurationSeconds || durationSeconds > MaxDispatchDurationSeconds {
		return ErrInvalidDispatchDuration
	}

	return nil
}
