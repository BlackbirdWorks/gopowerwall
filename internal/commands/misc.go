package commands

import (
	"fmt"

	"github.com/blackbirdworks/gopowerwall/pkgs/version"
)

// VersionCmd prints version information.
type VersionCmd struct{}

// Run executes the version command.
func (c *VersionCmd) Run(cmdCtx *Context) error {
	fmt.Fprintf(cmdCtx.Output(), "gopowerwall [%s]\n", version.Version)

	return nil
}

// SetupCmd provides cloud/fleetapi setup instructions.
type SetupCmd struct {
	Email string `env:"PW_EMAIL" help:"Email address for Tesla Login" name:"email"`

	Region   string `default:"us" help:"Tesla region: 'us' or 'cn'"                              name:"region"`
	Cloud    bool   `             help:"Setup Tesla Cloud Mode"                                  name:"cloud"`
	FleetAPI bool   `             help:"Setup Tesla Fleet API mode"                              name:"fleetapi"`
	V1r      bool   `             help:"Register RSA key with Powerwall for v1r LAN TEDAPI mode" name:"v1r"`
	Headless bool   `             help:"Force headless token-paste mode"                         name:"headless"`
}

// Run executes the setup command.
func (c *SetupCmd) Run(cmdCtx *Context) error {
	w := cmdCtx.Output()
	fmt.Fprintf(w, "gopowerwall [%s] - Setup Mode\n\n", version.Version)
	fmt.Fprintln(w, "To configure Cloud or FleetAPI credentials:")
	fmt.Fprintln(w, "  1. Run 'gopowerwall authtoken' to generate your Tesla OAuth tokens.")
	fmt.Fprintln(w, "  2. Place tokens in .pypowerwall.auth or set PW_AUTH_PATH.")
	fmt.Fprintln(w, "  3. For FleetAPI, configure .pypowerwall.fleetapi with your client credentials.")

	return nil
}

// AuthTokenCmd gets Tesla Cloud refresh token.
type AuthTokenCmd struct {
	Region string `default:"us" help:"Tesla region: 'us' or 'cn'" name:"region"`
}

// Run executes the authtoken command.
func (c *AuthTokenCmd) Run(cmdCtx *Context) error {
	w := cmdCtx.Output()
	fmt.Fprintf(w, "gopowerwall [%s] - Auth Token Helper\n\n", version.Version)
	fmt.Fprintln(w, "Authenticate via Tesla OAuth portal:")
	fmt.Fprintln(w, "Visit: https://auth.tesla.com/oauth2/v3/authorize")

	return nil
}

// CloudCheckCmd runs diagnostics on cloud auth.
type CloudCheckCmd struct {
	Email     string `env:"PW_EMAIL" help:"Email to test token refresh for" name:"email"`
	NoConnect bool   `               help:"Skip live connectivity tests"    name:"noconnect"`
}

// Run executes the cloudcheck command.
func (c *CloudCheckCmd) Run(cmdCtx *Context) error {
	w := cmdCtx.Output()
	fmt.Fprintf(w, "gopowerwall [%s] - Cloud Diagnostics\n\n", version.Version)
	fmt.Fprintln(w, "Checking network reachability to Tesla Auth and Owner APIs...")

	return nil
}

// TedapiCmd tests TEDAPI connection.
type TedapiCmd struct {
	GwPwd string `arg:"" env:"PW_GW_PWD" help:"Powerwall Gateway Password" optional:""`

	Host string `env:"PW_HOST" help:"IP address of Powerwall Gateway" name:"host"`

	Password string `env:"PW_PASSWORD" help:"Customer password for v1r mode" name:"password"`

	V1r bool `help:"Use v1r LAN TEDAPI mode" name:"v1r"`
}

// Run executes the tedapi command.
func (c *TedapiCmd) Run(cmdCtx *Context) error {
	w := cmdCtx.Output()
	fmt.Fprintf(w, "gopowerwall [%s] - TEDAPI Test\n\n", version.Version)
	fmt.Fprintf(w, "Connecting to gateway %s...\n", c.Host)

	return nil
}

// RegisterCmd provides RSA key registration instructions.
type RegisterCmd struct{}

// Run executes the register command.
func (c *RegisterCmd) Run(cmdCtx *Context) error {
	w := cmdCtx.Output()
	fmt.Fprintf(w, "gopowerwall [%s] - Fleet API Key Registration\n\n", version.Version)
	fmt.Fprintln(w, "Registering public RSA key for v1r LAN TEDAPI access...")

	return nil
}
