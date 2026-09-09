package main

import (
	"context"
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	"github.com/joho/godotenv"

	"github.com/blackbirdworks/gopowerwall/pkgs/version"
)

// CLI represents root command-line grammar for gopowerwall.
type CLI struct {
	Version    VersionCmd    `cmd:"" help:"Print version information"`
	Register   RegisterCmd   `cmd:"" help:"Register RSA key via Owner or Fleet API"`
	AuthToken  AuthTokenCmd  `cmd:"" help:"Get a Tesla Cloud refresh token"                name:"authtoken"`
	Get        GetCmd        `cmd:"" help:"Get Powerwall settings and power levels"`
	Tedapi     TedapiCmd     `cmd:"" help:"Test TEDAPI connection to Powerwall Gateway"`
	Setup      SetupCmd      `cmd:"" help:"Setup Tesla Cloud, Fleet API or v1r access"`
	CloudCheck CloudCheckCmd `cmd:"" help:"Diagnose cloud auth environment"                name:"cloudcheck"`
	Proxy      ProxyCmd      `cmd:"" help:"Run Powerwall HTTP proxy server"`
	Set        SetCmd        `cmd:"" help:"Set Powerwall operating mode and reserve level"`
	Scan       ScanCmd       `cmd:"" help:"Scan local network for Powerwall gateway"`
}

// newParser builds the kong parser for cli's grammar: every subcommand,
// its flags, and the root name/description. It is split out of Run so
// tests can exercise the grammar itself (kong.New's own validation of the
// struct tags above, plus Parse against arbitrary argv) without going
// through Run's os.Args/os.Exit-driven control flow.
func newParser(cli *CLI) (*kong.Kong, error) {
	return kong.New(
		cli,
		kong.Name("github.com/blackbirdworks/gopowerwall"),
		kong.Description(fmt.Sprintf("gopowerwall [%s] - Tesla Powerwall Gateway in Go", version.Version)),
		kong.UsageOnError(),
	)
}

// Run parses command line and executes selected command.
func Run() {
	// Load .env before parsing flags or reading configuration, so gopowerwall
	// can be configured either by real environment variables or a local
	// .env file. Real environment variables always win: godotenv.Load never
	// overwrites a variable that is already set. A missing .env file is a
	// silent no-op; only a malformed one is reported.
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warning: failed to load .env: %v\n", err)
	}

	var cli CLI
	parser, err := newParser(&cli)
	if err != nil {
		panic(err)
	}
	kctx, err := parser.Parse(os.Args[1:])
	parser.FatalIfErrorf(err)

	if runErr := kctx.Run(&Context{Context: context.Background()}); runErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", runErr)
		os.Exit(1)
	}
}
