package proxy

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config represents all configuration options for the Proxy server.
type Config struct {
	BindAddress           string
	RsaKeyPath            string
	Password              string
	Email                 string
	Host                  string
	Timezone              string
	TedapiAuthMode        string
	TedapiAPIVersion      string
	APIBaseURL            string
	GwPwd                 string
	WifiHost              string
	HTTPSMode             string
	Style                 string
	SiteID                string
	AuthPath              string
	AuthMode              string
	CacheFile             string
	ControlSecret         string
	BrowserCache          int
	CacheTTL              int
	Timeout               int
	CacheExpire           int
	FirmwareCheckInterval int
	TedapiProbeInterval   int
	SiteZeroThreshold     int
	Port                  int
	PoolMaxSize           int
	NetworkErrorRateLimit int
	FailFastMode          bool
	GracefulDegradation   bool
	HealthCheckEnabled    bool
	SuppressNetworkErrors bool
	TedapiRecoveryEnabled bool
	NegSolar              bool
	DebugMode             bool
}

const (
	defaultPort                  = 8675
	defaultCacheExpire           = 5
	defaultTimeout               = 5
	defaultPoolMaxSize           = 15
	defaultNetworkErrorRateLimit = 5
	defaultCacheTTL              = 30
	defaultTedapiProbeInterval   = 30
	defaultFirmwareCheckInterval = 300
	minTedapiProbeInterval       = 5
	minFirmwareCheckInterval     = 30
)

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}

	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}

	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		lower := strings.ToLower(val)

		return lower == "yes" || lower == valTrue || lower == "1"
	}

	return defaultVal
}

// DefaultConfig loads Config from environment variables.
func DefaultConfig() Config {
	authPath := getEnv("PW_AUTH_PATH", "")
	if authPath == "" {
		authPath = getEnv("PW_AUTHPATH", "")
	}
	cf := ".powerwall"
	if authPath != "" {
		cf = authPath + "/.powerwall"
	}

	style := getEnv("PW_STYLE", "clear")
	if !strings.HasSuffix(style, ".js") {
		style += ".js"
	}

	cfg := Config{
		BindAddress:           getEnv("PW_BIND_ADDRESS", ""),
		Port:                  getEnvInt("PW_PORT", defaultPort),
		Password:              getEnv("PW_PASSWORD", ""),
		Email:                 getEnv("PW_EMAIL", "email@example.com"),
		Host:                  getEnv("PW_HOST", ""),
		Timezone:              getEnv("PW_TIMEZONE", "America/Los_Angeles"),
		DebugMode:             getEnvBool("PW_DEBUG", false),
		CacheExpire:           getEnvInt("PW_CACHE_EXPIRE", defaultCacheExpire),
		BrowserCache:          getEnvInt("PW_BROWSER_CACHE", 0),
		Timeout:               getEnvInt("PW_TIMEOUT", defaultTimeout),
		PoolMaxSize:           getEnvInt("PW_POOL_MAXSIZE", defaultPoolMaxSize),
		HTTPSMode:             getEnv("PW_HTTPS", "no"),
		Style:                 style,
		SiteID:                getEnv("PW_SITEID", ""),
		AuthPath:              authPath,
		AuthMode:              getEnv("PW_AUTH_MODE", "cookie"),
		CacheFile:             getEnv("PW_CACHE_FILE", cf),
		ControlSecret:         getEnv("PW_CONTROL_SECRET", ""),
		GwPwd:                 getEnv("PW_GW_PWD", ""),
		RsaKeyPath:            getEnv("PW_RSA_KEY_PATH", ""),
		WifiHost:              getEnv("PW_WIFI_HOST", ""),
		TedapiAPIVersion:      getEnv("PW_TEDAPI_API_VERSION", "V2024_06"),
		TedapiAuthMode:        getEnv("PW_TEDAPI_AUTH_MODE", "basic"),
		NegSolar:              getEnvBool("PW_NEG_SOLAR", true),
		SiteZeroThreshold:     getEnvInt("PW_SITE_ZERO_THRESHOLD", 0),
		APIBaseURL:            getEnv("PROXY_BASE_URL", "/"),
		SuppressNetworkErrors: getEnvBool("PW_SUPPRESS_NETWORK_ERRORS", false),
		NetworkErrorRateLimit: getEnvInt("PW_NETWORK_ERROR_RATE_LIMIT", defaultNetworkErrorRateLimit),
		FailFastMode:          getEnvBool("PW_FAIL_FAST", false),
		GracefulDegradation:   getEnvBool("PW_GRACEFUL_DEGRADATION", true),
		HealthCheckEnabled:    getEnvBool("PW_HEALTH_CHECK", true),
		CacheTTL:              getEnvInt("PW_CACHE_TTL", defaultCacheTTL),
		TedapiRecoveryEnabled: getEnvBool("PW_TEDAPI_RECOVERY", true),
		TedapiProbeInterval:   getEnvInt("PW_TEDAPI_PROBE_INTERVAL", defaultTedapiProbeInterval),
		FirmwareCheckInterval: getEnvInt("PW_FIRMWARE_CHECK_INTERVAL", defaultFirmwareCheckInterval),
	}

	if cfg.TedapiProbeInterval < minTedapiProbeInterval {
		cfg.TedapiProbeInterval = defaultTedapiProbeInterval
	}
	if cfg.FirmwareCheckInterval < minFirmwareCheckInterval {
		cfg.FirmwareCheckInterval = defaultFirmwareCheckInterval
	}

	return cfg
}

func (c *Config) CacheExpireDuration() time.Duration {
	return time.Duration(c.CacheExpire) * time.Second
}

func (c *Config) CacheTTLDuration() time.Duration {
	return time.Duration(c.CacheTTL) * time.Second
}

func (c *Config) TimeoutDuration() time.Duration {
	return time.Duration(c.Timeout) * time.Second
}
