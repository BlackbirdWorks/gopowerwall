package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/blackbirdworks/gopowerwall"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
)

var (
	ErrV1rMissingGwPwd  = errors.New("-v1r requires -gw_pwd <gateway_password>")
	ErrV1rMissingHost   = errors.New("-v1r requires -host <gateway_ip>")
	ErrTedapiMissingGw  = errors.New("-tedapi requires -gw_pwd <gateway_password>")
	ErrLocalMissingHost = errors.New("-local requires -host <gateway_ip>")
)

// ConnectionFlags holds shared connection flags for commands interacting with a Powerwall.
type ConnectionFlags struct {
	Host       string `help:"IP address of Powerwall Gateway [local/tedapi/v1r]"                name:"host"`
	Password   string `help:"Customer password = last 5 characters of gateway password"         name:"password"`
	GwPwd      string `help:"Gateway password [required for -tedapi and -v1r]"                  name:"gw_pwd"`
	RsaKeyPath string `help:"RSA private key PEM path [v1r; default: ./tedapi_rsa_private.pem]" name:"rsa_key_path"`
	AuthPath   string `help:"Auth path"                                                         name:"authpath"     env:"PW_AUTH_PATH"` //nolint:lll // config struct tags are intentionally verbose
	Local      bool   `help:"Connect via local Powerwall Gateway (requires -host)"              name:"local"`
	Cloud      bool   `help:"Connect via Tesla Cloud (requires prior 'setup')"                  name:"cloud"`
	FleetAPI   bool   `help:"Connect via Tesla Fleet API (requires prior 'setup -fleetapi')"    name:"fleetapi"`
	TEDAPI     bool   `help:"Connect via TEDAPI (requires -gw_pwd)"                             name:"tedapi"`
	V1r        bool   `help:"Connect via v1r LAN TEDAPI (requires -gw_pwd and RSA private key)" name:"v1r"`
	Debug      bool   `help:"Enable debug output"                                               name:"debug"`
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
	if _, err := os.Stat(defaultKey); err == nil {
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
func (c *ConnectionFlags) BuildPowerwall(ctx context.Context) (*gopowerwall.Powerwall, error) {
	var opts []gopowerwall.Option

	if c.AuthPath != "" {
		opts = append(opts, gopowerwall.WithAuthPath(c.AuthPath))
	}
	if c.Host != "" {
		opts = append(opts, gopowerwall.WithHost(c.Host))
	}
	if c.Password != "" {
		opts = append(opts, gopowerwall.WithPassword(c.Password))
	}
	if c.GwPwd != "" {
		opts = append(opts, gopowerwall.WithGwPwd(c.GwPwd))
	}
	if rsaKey := c.resolveRSAKey(); rsaKey != "" {
		opts = append(opts, gopowerwall.WithRSAKeyPath(rsaKey))
	}

	modeOpts, err := c.resolveModeOptions()
	if err != nil {
		return nil, err
	}
	opts = append(opts, modeOpts...)

	return gopowerwall.New(ctx, opts...)
}

func (c *ConnectionFlags) resolveModeOptions() ([]gopowerwall.Option, error) {
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
			return []gopowerwall.Option{gopowerwall.WithHost("192.168.91.1")}, nil
		}

		return nil, nil
	case c.Local:
		if c.Host == "" {
			return nil, ErrLocalMissingHost
		}

		return []gopowerwall.Option{gopowerwall.WithCloudMode(false)}, nil
	case c.Cloud:
		return []gopowerwall.Option{gopowerwall.WithCloudMode(true), gopowerwall.WithFleetAPI(false)}, nil
	case c.FleetAPI:
		return []gopowerwall.Option{gopowerwall.WithCloudMode(true), gopowerwall.WithFleetAPI(true)}, nil
	default:
		return []gopowerwall.Option{gopowerwall.WithAutoSelect(true)}, nil
	}
}
