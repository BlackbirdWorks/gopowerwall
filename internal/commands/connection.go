package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/powerwall"
)

var (
	ErrV1rMissingGwPwd  = errors.New("-v1r requires -gw_pwd <gateway_password>")
	ErrV1rMissingHost   = errors.New("-v1r requires -host <gateway_ip>")
	ErrTedapiMissingGw  = errors.New("-tedapi requires -gw_pwd <gateway_password>")
	ErrLocalMissingHost = errors.New("-local requires -host <gateway_ip>")
)

// ConnectionFlags holds shared connection flags for commands interacting with a Powerwall.
type ConnectionFlags struct {
	Host string `env:"PW_HOST" help:"IP address of Powerwall Gateway [local/tedapi/v1r]" name:"host"`

	Password string `env:"PW_PASSWORD" help:"Customer password = last 5 characters of gateway password" name:"password"`

	GwPwd string `env:"PW_GW_PWD" help:"Gateway password [required for -tedapi and -v1r]" name:"gw_pwd"`

	RsaKeyPath string `env:"PW_RSA_KEY_PATH" help:"RSA private key PEM path [v1r]" name:"rsa_key_path"`

	AuthPath string `env:"PW_AUTH_PATH" help:"Auth path" name:"authpath"`

	workDir string

	Local    bool `help:"Connect via local Powerwall Gateway (requires -host)"              name:"local"`
	Cloud    bool `help:"Connect via Tesla Cloud (requires prior 'setup')"                  name:"cloud"`
	FleetAPI bool `help:"Connect via Tesla Fleet API (requires prior 'setup -fleetapi')"    name:"fleetapi"`
	TEDAPI   bool `help:"Connect via TEDAPI (requires -gw_pwd)"                             name:"tedapi"`
	V1r      bool `help:"Connect via v1r LAN TEDAPI (requires -gw_pwd and RSA private key)" name:"v1r"`

	Debug bool `env:"PW_DEBUG" help:"Enable debug output" name:"debug"`
}

func (c *ConnectionFlags) resolveRSAKey() string {
	if !c.V1r || c.RsaKeyPath != "" {
		return c.RsaKeyPath
	}
	defaultKey := "tedapi_rsa_private.pem"
	if c.AuthPath != "" {
		cand := filepath.Join(c.AuthPath, defaultKey)
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	dir := c.workDir
	if dir == "" {
		dir = "."
	}
	if _, err := os.Stat(filepath.Join(dir, defaultKey)); err == nil {
		return defaultKey
	}

	return ""
}

// WithLogger returns a context carrying a logger configured from the command's
// debug flag, so that every layer below logs at the requested verbosity.
func (c *ConnectionFlags) WithLogger(ctx context.Context) context.Context {
	return logger.Into(ctx, logger.New(os.Stderr, logger.LevelFor(c.Debug)))
}

// BuildPowerwall constructs a Powerwall client based on flags.
//
// A non-nil error here always means the flags themselves were invalid -
// a bad mode combination (see resolveModeOptions) or a [powerwall.Config]
// validation failure. A failed *connection* attempt is deliberately not
// surfaced as an error: [powerwall.New] wraps that case in a
// [powerwall.ConnectError], but every command built on BuildPowerwall
// already reports "not connected" itself, with its own message, after
// checking [powerwall.Powerwall.IsConnected] - see GetCmd.Run and
// SetCmd.Run. Propagating New's ConnectError here as well would just
// duplicate that reporting with a second, differently-worded error, so
// BuildPowerwall discards it and returns the constructed (but possibly
// disconnected) *Powerwall with a nil error instead, preserving the
// existing "build, then check IsConnected" flow.
func (c *ConnectionFlags) BuildPowerwall(ctx context.Context) (*powerwall.Powerwall, error) {
	var opts []powerwall.Option

	if c.AuthPath != "" {
		// The proxy server (see proxy.DefaultConfig) already relocates its
		// session-cache file under AuthPath when one is configured; the CLI
		// commands built through BuildPowerwall did not, so -authpath had no
		// effect on where a local-mode connection wrote its session cache -
		// it always fell back to the process's working directory. Mirror the
		// proxy's behaviour so -authpath consistently controls both.
		opts = append(opts,
			powerwall.WithAuthPath(c.AuthPath),
			powerwall.WithCacheFile(filepath.Join(c.AuthPath, ".powerwall")),
		)
	}
	if c.Host != "" {
		opts = append(opts, powerwall.WithHost(c.Host))
	}
	if c.Password != "" {
		opts = append(opts, powerwall.WithPassword(c.Password))
	}
	if c.GwPwd != "" {
		opts = append(opts, powerwall.WithGwPwd(c.GwPwd))
	}
	if rsaKey := c.resolveRSAKey(); rsaKey != "" {
		opts = append(opts, powerwall.WithRSAKeyPath(rsaKey))
	}

	modeOpts, err := c.resolveModeOptions()
	if err != nil {
		return nil, err
	}
	opts = append(opts, modeOpts...)

	pw, err := powerwall.New(ctx, opts...)
	if _, ok := errors.AsType[*powerwall.ConnectError](err); ok {
		return pw, nil
	}

	return pw, err
}

func (c *ConnectionFlags) resolveModeOptions() ([]powerwall.Option, error) {
	switch {
	case c.V1r:
		if c.GwPwd == "" {
			return nil, ErrV1rMissingGwPwd
		}
		if c.Host == "" {
			return nil, ErrV1rMissingHost
		}

		return nil, nil
	case c.TEDAPI:
		if c.GwPwd == "" {
			return nil, ErrTedapiMissingGw
		}
		if c.Host == "" {
			return []powerwall.Option{powerwall.WithHost("192.168.91.1")}, nil
		}

		return nil, nil
	case c.Local:
		if c.Host == "" {
			return nil, ErrLocalMissingHost
		}

		return []powerwall.Option{powerwall.WithCloudMode(false)}, nil
	case c.Cloud:
		return []powerwall.Option{powerwall.WithCloudMode(true), powerwall.WithFleetAPI(false)}, nil
	case c.FleetAPI:
		return []powerwall.Option{powerwall.WithCloudMode(true), powerwall.WithFleetAPI(true)}, nil
	default:
		return []powerwall.Option{powerwall.WithAutoSelect(true)}, nil
	}
}
