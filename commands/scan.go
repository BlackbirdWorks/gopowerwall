package commands

import (
	"encoding/json"
	"fmt"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/version"
	"github.com/blackbirdworks/gopowerwall/scan"
)

// ScanCmd scans local network for Powerwall gateways.
type ScanCmd struct {
	Network string  `arg:"" help:"IPv4 CIDR network to scan (e.g. 192.168.1.0/24)" optional:""`
	IP      string  `       help:"IP address within network to scan"                           name:"ip"`
	Timeout float64 `       help:"Seconds per host"                                            name:"timeout" default:"1.0"` //nolint:lll // config struct tags are intentionally verbose
	Hosts   int     `       help:"Max hosts [1-256]"                                           name:"hosts"   default:"30"`
	JSON    bool    `       help:"Output discovered gateways as JSON"                          name:"json"`
	Nocolor bool    `       help:"Disable color text output"                                   name:"nocolor"`
}

// Run executes the scan command.
func (c *ScanCmd) Run(cmdCtx *Context) error {
	w := cmdCtx.Output()
	if !c.JSON {
		fmt.Fprintf(w, "gopowerwall [%s] - Scanner\n\n", version.Version)
	}

	target := c.Network
	if target == "" {
		target = c.IP
	}

	results, err := scan.Scan(cmdCtx.Context, models.ScanOptions{
		CIDR:        target,
		MaxHosts:    c.Hosts,
		TimeoutSec:  c.Timeout,
		Color:       !c.Nocolor,
		Interactive: !c.JSON,
		JSONOutput:  c.JSON,
	}, w)
	if err != nil {
		return err
	}

	if c.JSON {
		b, mErr := json.MarshalIndent(results, "", "  ")
		if mErr != nil {
			return mErr
		}
		fmt.Fprintln(w, string(b))
	}

	return nil
}
