package gopowerwall

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/blackbirdworks/gopowerwall/pkgs/validation"
)

const (
	defaultCacheExpire = 5 * time.Second
	defaultTimeout     = 5 * time.Second
	defaultPoolMaxSize = 10
	dirPerms           = 0750
	maxTCPPort         = 65535
)

// Config holds all configuration parameters for a Powerwall instance.
type Config struct {
	TEDAPIAuthMode   AuthMode
	Password         string
	Email            string
	Timezone         string
	SiteID           string
	AuthPath         string
	CacheFile        string
	GwPwd            string
	WiFiHost         string
	RSAKeyPath       string
	Host             string
	TEDAPIApiVersion TEDAPIApiVersion
	AuthMode         AuthMode
	PWCacheExpire    time.Duration
	Timeout          time.Duration
	PoolMaxSize      int
	CloudMode        bool
	FleetAPI         bool
	AutoSelect       bool
	RetryModes       bool
}

// Option is a functional option for configuring Powerwall.
type Option func(*Config)

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		Email:            "nobody@nowhere.com",
		Timezone:         "America/Los_Angeles",
		PWCacheExpire:    defaultCacheExpire,
		Timeout:          defaultTimeout,
		PoolMaxSize:      defaultPoolMaxSize,
		CloudMode:        false,
		AuthMode:         AuthModeCookie,
		CacheFile:        ".powerwall",
		FleetAPI:         false,
		AutoSelect:       false,
		RetryModes:       false,
		TEDAPIApiVersion: TEDAPIVersion2024_06,
		TEDAPIAuthMode:   AuthModeBasic,
	}
}

// WithHost sets the Powerwall host/IP.
func WithHost(host string) Option {
	return func(c *Config) { c.Host = host }
}

// WithPassword sets the customer password.
func WithPassword(password string) Option {
	return func(c *Config) { c.Password = password }
}

// WithEmail sets the customer email.
func WithEmail(email string) Option {
	return func(c *Config) { c.Email = email }
}

// WithTimezone sets the gateway timezone.
func WithTimezone(tz string) Option {
	return func(c *Config) { c.Timezone = tz }
}

// WithPWCacheExpire sets the cache expiration duration.
func WithPWCacheExpire(d time.Duration) Option {
	return func(c *Config) { c.PWCacheExpire = d }
}

// WithTimeout sets HTTP client timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Config) { c.Timeout = d }
}

// WithPoolMaxSize sets HTTP client connection pool size.
func WithPoolMaxSize(size int) Option {
	return func(c *Config) { c.PoolMaxSize = size }
}

// WithCloudMode enables or disables Tesla Cloud mode.
func WithCloudMode(cloud bool) Option {
	return func(c *Config) { c.CloudMode = cloud }
}

// WithSiteID sets Tesla energy site ID.
func WithSiteID(id string) Option {
	return func(c *Config) { c.SiteID = id }
}

// WithAuthPath sets directory path for cloud auth files.
func WithAuthPath(path string) Option {
	return func(c *Config) { c.AuthPath = path }
}

// WithAuthMode sets authentication mode ("cookie", "token", or "basic").
func WithAuthMode(mode AuthMode) Option {
	return func(c *Config) { c.AuthMode = mode }
}

// WithCacheFile sets local token cache file path.
func WithCacheFile(path string) Option {
	return func(c *Config) { c.CacheFile = path }
}

// WithFleetAPI enables or disables Tesla FleetAPI mode.
func WithFleetAPI(fleet bool) Option {
	return func(c *Config) { c.FleetAPI = fleet }
}

// WithAutoSelect enables automatic mode selection.
func WithAutoSelect(auto bool) Option {
	return func(c *Config) { c.AutoSelect = auto }
}

// WithRetryModes enables circular fallback across connection modes.
func WithRetryModes(retry bool) Option {
	return func(c *Config) { c.RetryModes = retry }
}

// WithGwPwd sets the Powerwall Gateway TEG password for TEDAPI.
func WithGwPwd(pwd string) Option {
	return func(c *Config) { c.GwPwd = pwd }
}

// WithRSAKeyPath sets private RSA key file path for TEDAPI v1r.
func WithRSAKeyPath(path string) Option {
	return func(c *Config) { c.RSAKeyPath = path }
}

// WithWiFiHost sets fallback WiFi TEDAPI host for v1r mode.
func WithWiFiHost(host string) Option {
	return func(c *Config) { c.WiFiHost = host }
}

// WithTEDAPIApiVersion sets TEDAPI protobuf/query version ("V2024_06" or "V2026_06").
func WithTEDAPIApiVersion(version TEDAPIApiVersion) Option {
	return func(c *Config) { c.TEDAPIApiVersion = version }
}

// WithTEDAPIAuthMode sets TEDAPI auth mode ("basic" or "bearer").
func WithTEDAPIAuthMode(mode AuthMode) Option {
	return func(c *Config) { c.TEDAPIAuthMode = mode }
}

func validateHost(host string) error {
	if host == "" {
		return nil
	}
	hostPart := host
	if strings.Count(hostPart, ":") == 1 {
		parts := strings.Split(hostPart, ":")
		portStr := parts[1]
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > maxTCPPort {
			return &InvalidConfigError{
				Param: "host",
				Message: fmt.Sprintf(
					"invalid port in host '%s': port %d is out of valid TCP range 1-65535",
					host,
					port,
				),
			}
		}
		hostPart = parts[0]
	}
	if !validation.IsValidHost(hostPart) {
		return &InvalidConfigError{
			Param:   "host",
			Message: fmt.Sprintf("invalid powerwall host '%s': must be IP address or valid hostname", host),
		}
	}

	return nil
}

// ValidateConfig validates host format, port, email, and file permissions.
// Matches Python _validate_init_configuration().
func ValidateConfig(c *Config) error {
	if err := validateHost(c.Host); err != nil {
		return err
	}

	switch {
	case c.CloudMode && !c.FleetAPI:
		if c.Email == "" || !validation.IsValidEmail(c.Email) {
			return &InvalidConfigError{
				Param:   "email",
				Message: fmt.Sprintf("valid email required for cloud mode: '%s'", c.Email),
			}
		}
		dir := c.AuthPath
		if dir == "" {
			dir = "."
		}

		return checkDirWritable(dir, "authpath")

	case c.FleetAPI:
		dir := c.AuthPath
		if dir == "" {
			dir = "."
		}

		return checkDirWritable(dir, "authpath")

	default:
		cacheDir := filepath.Dir(c.CacheFile)
		if cacheDir == "" || cacheDir == "." {
			cacheDir = "."
		}

		return checkDirWritable(cacheDir, "cachefile")
	}
}

func checkDirWritable(dirpath, name string) error {
	info, err := os.Stat(dirpath)
	if os.IsNotExist(err) {
		if mkdirErr := os.MkdirAll(dirpath, dirPerms); mkdirErr != nil {
			return &InvalidConfigError{
				Param:   name,
				Message: fmt.Sprintf("unable to create %s directory '%s': %v", name, dirpath, mkdirErr),
			}
		}

		return nil
	}
	if err != nil {
		return &InvalidConfigError{
			Param:   name,
			Message: fmt.Sprintf("cannot access directory '%s': %v", dirpath, err),
		}
	}
	if !info.IsDir() {
		return &InvalidConfigError{
			Param:   name,
			Message: fmt.Sprintf("'%s' must be a directory (%s)", dirpath, name),
		}
	}
	// Check writability by creating a temp file
	tmpFile, err := os.CreateTemp(dirpath, ".pw_check_*")
	if err != nil {
		return &InvalidConfigError{
			Param:   name,
			Message: fmt.Sprintf("directory '%s' is not writable for %s: %v", dirpath, name, err),
		}
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	_ = os.Remove(tmpPath)

	return nil
}
