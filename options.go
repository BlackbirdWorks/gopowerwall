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

// Config holds all configuration parameters for a [Powerwall] instance.
// Build one with [DefaultConfig] and a chain of [Option] functions (or let
// [New] do that for you) rather than constructing it directly - several
// fields interact (see [ValidateConfig] and connectLocal's mode-selection
// logic in powerwall.go), and DefaultConfig's zero values matter for that
// logic to behave as documented.
type Config struct {
	// TEDAPIAuthMode selects HTTP Basic ([AuthModeBasic], the default) or
	// bearer-token ([AuthModeBearer]) authentication for TEDAPI requests.
	// Set via [WithTEDAPIAuthMode].
	TEDAPIAuthMode AuthMode
	// Password is the local gateway's short customer password (the last
	// five characters of the full gateway WiFi password), used to
	// authenticate ModeLocal connections. Set via [WithPassword].
	Password string
	// Email identifies the account for cloud/FleetAPI mode and is recorded
	// in the local backend's cache file; it need not be a real address for
	// local-only use. Set via [WithEmail].
	Email string
	// Timezone is the gateway's configured timezone, used only for
	// formatting timestamps in returned data. Set via [WithTimezone].
	Timezone string
	// SiteID is the Tesla energy site ID used to select among multiple
	// sites on a cloud/FleetAPI account. Set via [WithSiteID].
	SiteID string
	// AuthPath is the directory holding cloud/FleetAPI token files
	// (.pypowerwall.auth, .pypowerwall.fleetapi) and consulted by
	// AutoSelect. Set via [WithAuthPath].
	AuthPath string
	// CacheFile is the path to the local backend's session cache file,
	// storing the auth cookie/token so repeated connects need not
	// re-authenticate. Set via [WithCacheFile].
	CacheFile string
	// GwPwd is the full gateway WiFi password, required for TEDAPI
	// (ModeTEDAPI/hybrid) connections. Set via [WithGwPwd].
	GwPwd string
	// WiFiHost is a fallback TEDAPI host used by v1r mode when the primary
	// LAN host is unreachable. Set via [WithWiFiHost].
	WiFiHost string
	// RSAKeyPath is the path to the RSA private key registered with the
	// gateway, required for ModeV1r connections. Set via [WithRSAKeyPath].
	RSAKeyPath string
	// Host is the gateway's IP address or hostname, optionally with a port
	// (":443" is assumed if omitted). Leaving it empty forces cloud mode
	// (see New). Set via [WithHost].
	Host string
	// TEDAPIApiVersion selects which TEDAPI protobuf/query definitions to
	// use. Set via [WithTEDAPIApiVersion]; see [CoerceTEDAPIApiVersion] for
	// parsing one from a string.
	TEDAPIApiVersion TEDAPIApiVersion
	// AuthMode selects cookie-based ([AuthModeCookie], the default) or
	// token-based ([AuthModeToken]) authentication for the local backend.
	// Set via [WithAuthMode].
	AuthMode AuthMode
	// PWCacheExpire is how long [Powerwall.Poll] results are cached before
	// a repeat call re-fetches from the backend. Set via
	// [WithPWCacheExpire]; bypass it per call with [WithForce].
	PWCacheExpire time.Duration
	// Timeout is the HTTP client timeout applied to requests against the
	// active backend. Set via [WithTimeout].
	Timeout time.Duration
	// PoolMaxSize is the maximum size of the backend's HTTP connection
	// pool. Set via [WithPoolMaxSize].
	PoolMaxSize int
	// CloudMode selects the Tesla Owner API (ModeCloud) instead of a local
	// gateway connection. Set via [WithCloudMode]; New also forces this to
	// true whenever Host is empty.
	CloudMode bool
	// FleetAPI selects the official Tesla Fleet API (ModeFleetAPI) instead
	// of the Owner API, when CloudMode is also true. Set via
	// [WithFleetAPI].
	FleetAPI bool
	// AutoSelect enables New's automatic backend selection: local if Host
	// is set, otherwise whichever of FleetAPI/cloud has an existing token
	// file under AuthPath. Set via [WithAutoSelect].
	AutoSelect bool
	// RetryModes enables [Powerwall.Connect]'s circular fallback (local ->
	// FleetAPI -> cloud -> local) with a delay before the final attempt,
	// instead of failing after the first exhausted mode. Set via
	// [WithRetryModes].
	RetryModes bool
}

// Option is a functional option for configuring a [Powerwall] via [New], in
// the style of [DefaultConfig] plus a chain of With* functions
// (WithHost, WithPassword, and so on) declared in this file.
type Option func(*Config)

// DefaultConfig returns the default [Config]: cloud mode disabled, cookie
// auth for the local backend, HTTP Basic auth for TEDAPI, the 2024-06 TEDAPI
// query set, a 5-second cache and timeout, and no host/credentials set.
// [New] starts from this and applies any [Option] values on top of it.
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

// WithHost sets [Config.Host], the gateway's IP or hostname. Leaving this
// unset (the default) forces [New] into cloud mode.
func WithHost(host string) Option {
	return func(c *Config) { c.Host = host }
}

// WithPassword sets [Config.Password], the local gateway's short customer
// password used to authenticate ModeLocal connections.
func WithPassword(password string) Option {
	return func(c *Config) { c.Password = password }
}

// WithEmail sets [Config.Email], the account identifier used by cloud and
// FleetAPI mode.
func WithEmail(email string) Option {
	return func(c *Config) { c.Email = email }
}

// WithTimezone sets [Config.Timezone], used only for formatting timestamps
// in returned data.
func WithTimezone(tz string) Option {
	return func(c *Config) { c.Timezone = tz }
}

// WithPWCacheExpire sets [Config.PWCacheExpire], how long [Powerwall.Poll]
// results are cached before a repeat call re-fetches from the backend.
func WithPWCacheExpire(d time.Duration) Option {
	return func(c *Config) { c.PWCacheExpire = d }
}

// WithTimeout sets [Config.Timeout], the HTTP client timeout applied to
// requests against the active backend.
func WithTimeout(d time.Duration) Option {
	return func(c *Config) { c.Timeout = d }
}

// WithPoolMaxSize sets [Config.PoolMaxSize], the backend's maximum HTTP
// connection pool size.
func WithPoolMaxSize(size int) Option {
	return func(c *Config) { c.PoolMaxSize = size }
}

// WithCloudMode sets [Config.CloudMode], selecting the Tesla Owner API
// (ModeCloud) instead of a local gateway connection when true.
func WithCloudMode(cloud bool) Option {
	return func(c *Config) { c.CloudMode = cloud }
}

// WithSiteID sets [Config.SiteID], the Tesla energy site ID used to select
// among multiple sites on a cloud/FleetAPI account.
func WithSiteID(id string) Option {
	return func(c *Config) { c.SiteID = id }
}

// WithAuthPath sets [Config.AuthPath], the directory holding cloud/FleetAPI
// token files and consulted by [WithAutoSelect].
func WithAuthPath(path string) Option {
	return func(c *Config) { c.AuthPath = path }
}

// WithAuthMode sets [Config.AuthMode] ([AuthModeCookie] or [AuthModeToken])
// for the local backend.
func WithAuthMode(mode AuthMode) Option {
	return func(c *Config) { c.AuthMode = mode }
}

// WithCacheFile sets [Config.CacheFile], the path to the local backend's
// session cache file.
func WithCacheFile(path string) Option {
	return func(c *Config) { c.CacheFile = path }
}

// WithFleetAPI sets [Config.FleetAPI], selecting the official Tesla Fleet
// API (ModeFleetAPI) instead of the Owner API when true and CloudMode is
// also set.
func WithFleetAPI(fleet bool) Option {
	return func(c *Config) { c.FleetAPI = fleet }
}

// WithAutoSelect sets [Config.AutoSelect], enabling [New]'s automatic
// backend selection between local, FleetAPI, and cloud mode.
func WithAutoSelect(auto bool) Option {
	return func(c *Config) { c.AutoSelect = auto }
}

// WithRetryModes sets [Config.RetryModes], enabling [Powerwall.Connect]'s
// circular fallback (local -> FleetAPI -> cloud -> local) across connection
// modes instead of failing after the first exhausted mode.
func WithRetryModes(retry bool) Option {
	return func(c *Config) { c.RetryModes = retry }
}

// WithGwPwd sets [Config.GwPwd], the full gateway WiFi password required
// for TEDAPI connections.
func WithGwPwd(pwd string) Option {
	return func(c *Config) { c.GwPwd = pwd }
}

// WithRSAKeyPath sets [Config.RSAKeyPath], the path to the RSA private key
// registered with the gateway, required for ModeV1r connections.
func WithRSAKeyPath(path string) Option {
	return func(c *Config) { c.RSAKeyPath = path }
}

// WithWiFiHost sets [Config.WiFiHost], a fallback TEDAPI host used by v1r
// mode when the primary LAN host is unreachable.
func WithWiFiHost(host string) Option {
	return func(c *Config) { c.WiFiHost = host }
}

// WithTEDAPIApiVersion sets [Config.TEDAPIApiVersion] ([TEDAPIVersion2024_06]
// or [TEDAPIVersion2026_06]).
func WithTEDAPIApiVersion(version TEDAPIApiVersion) Option {
	return func(c *Config) { c.TEDAPIApiVersion = version }
}

// WithTEDAPIAuthMode sets [Config.TEDAPIAuthMode] ([AuthModeBasic] or
// [AuthModeBearer]) for TEDAPI requests.
func WithTEDAPIAuthMode(mode AuthMode) Option {
	return func(c *Config) { c.TEDAPIAuthMode = mode }
}

// validateHost reports whether host is a syntactically valid, optionally
// ported gateway address ("" is treated as valid: cloud mode has no host).
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

// ValidateConfig validates c's host format and port, its email (in cloud
// mode), and the writability of its cache/auth directory, returning an
// [InvalidConfigError] on the first problem found. [New] calls this itself
// before connecting, so most callers never need to call it directly; it is
// exported for callers that want to validate a [Config] before passing it
// through a chain of [Option] values. Matches pypowerwall's
// _validate_init_configuration().
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
