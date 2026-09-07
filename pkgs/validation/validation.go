// Package validation provides host and email format validators.
package validation

import (
	"net"
	"net/mail"
	"regexp"
	"strings"
)

const maxHostLength = 255

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
