package main

import (
	"context"
	"fmt"
	"os"

	"github.com/alecthomas/kong"

	"github.com/blackbirdworks/gopowerwall/commands"
	"github.com/blackbirdworks/gopowerwall/pkgs/version"
)

// CLI represents root command-line grammar for gopowerwall.
type CLI struct {
	Version    commands.VersionCmd    `cmd:"" help:"Print version information"`
	Register   commands.RegisterCmd   `cmd:"" help:"Register RSA key with Powerwall via Tesla Owner API or Fleet API"`
	AuthToken  commands.AuthTokenCmd  `cmd:"" help:"Get Tesla Cloud refresh token via local browser login"            name:"authtoken"`
	Get        commands.GetCmd        `cmd:"" help:"Get Powerwall settings and power levels"`
	Tedapi     commands.TedapiCmd     `cmd:"" help:"Test TEDAPI connection to Powerwall Gateway"`
	Setup      commands.SetupCmd      `cmd:"" help:"Setup Tesla Cloud, Fleet API, or v1r LAN TEDAPI access"`
	CloudCheck commands.CloudCheckCmd `cmd:"" help:"Diagnose cloud auth environment"                                  name:"cloudcheck"`
	Proxy      commands.ProxyCmd      `cmd:"" help:"Run Powerwall HTTP proxy server"`
	Set        commands.SetCmd        `cmd:"" help:"Set Powerwall operating mode and reserve level"`
	Scan       commands.ScanCmd       `cmd:"" help:"Scan local network for Powerwall gateway"`
}

// Run parses command line and executes selected command.
func Run() {
	var cli CLI
	kctx := kong.Parse(
		&cli,
		kong.Name("github.com/blackbirdworks/gopowerwall"),
		kong.Description(fmt.Sprintf("gopowerwall [%s] - Tesla Powerwall Gateway in Go", version.Version)),
		kong.UsageOnError(),
	)

	err := kctx.Run(&commands.Context{Context: context.Background()})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
