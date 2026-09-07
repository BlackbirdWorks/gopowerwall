package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/blackbirdworks/gopowerwall"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/pkgs/version"
)

var (
	ErrNoActionSpecified = errors.New(
		"no action specified. Use --mode, --reserve, --current, --gridcharging, or --gridexport",
	)
	ErrUnableToConnect  = errors.New("unable to connect. Check connection mode and credentials")
	ErrBatteryLevelRead = errors.New("unable to read current battery level from Powerwall")
)

// SetCmd configures Powerwall operating mode and reserve levels.
type SetCmd struct {
	Mode         string `help:"Operating mode: self_consumption, backup, or autonomous" name:"mode"`
	GridCharging string `help:"Grid Charging Mode: on or off"                           name:"gridcharging"`
	GridExport   string `help:"Grid Export Mode: battery_ok, pv_only, or never"         name:"gridexport"`
	ConnectionFlags
	Reserve float64 `help:"Set Battery Reserve Level [Default=20]"                  name:"reserve"      default:"-1"`
	Current bool    `help:"Set Battery Reserve Level to Current Charge"             name:"current"`
}

// Run executes the set command.
func (c *SetCmd) Run() error {
	if c.Debug {
		logger.SetDebug(true)
	}

	if c.Mode == "" && c.Reserve == -1 && !c.Current && c.GridCharging == "" && c.GridExport == "" {
		return ErrNoActionSpecified
	}

	pw, err := c.BuildPowerwall()
	if err != nil {
		return err
	}

	if !pw.IsConnected() {
		return ErrUnableToConnect
	}

	fmt.Fprintf(os.Stdout, "gopowerwall [%s] - Set Powerwall settings using %s mode.\n\n", version.Version, pw.Mode())

	if errMode := c.applyMode(pw); errMode != nil {
		return errMode
	}
	c.applyReserve(pw)
	if errCurrent := c.applyCurrent(pw); errCurrent != nil {
		return errCurrent
	}
	if errGrid := c.applyGridCharging(pw); errGrid != nil {
		return errGrid
	}

	return c.applyGridExport(pw)
}

func (c *SetCmd) applyMode(pw *gopowerwall.Powerwall) error {
	if c.Mode == "" {
		return nil
	}
	m := strings.ToLower(c.Mode)
	if m != "self_consumption" && m != "backup" && m != "autonomous" {
		return fmt.Errorf(
			"invalid Mode [%s] - must be self_consumption, backup, or autonomous: %w",
			m,
			ErrNoActionSpecified,
		)
	}
	fmt.Fprintf(os.Stdout, "Setting Powerwall Mode to %s\n", m)
	if _, err := pw.SetMode(m); err != nil {
		logger.LogError("Failed to set mode: %v", err)
	}

	return nil
}

func (c *SetCmd) applyReserve(pw *gopowerwall.Powerwall) {
	if c.Reserve == -1 {
		return
	}
	resVal := c.Reserve
	if resVal > 80 && (pw.IsCloud() || pw.IsFleetAPI()) {
		logger.LogWarn("Tesla cloud/FleetAPI limits backup reserve to 80%% max.")
	}
	fmt.Fprintf(os.Stdout, "Setting Powerwall Reserve to %v\n", resVal)
	if _, err := pw.SetReserve(resVal); err != nil {
		logger.LogError("Failed to set reserve: %v", err)
	}
}

func (c *SetCmd) applyCurrent(pw *gopowerwall.Powerwall) error {
	if !c.Current {
		return nil
	}
	lvl := pw.Level()
	if lvl == nil {
		return ErrBatteryLevelRead
	}
	fmt.Fprintf(os.Stdout, "Setting Powerwall Reserve to Current Charge Level %.1f\n", *lvl)
	if _, err := pw.SetReserve(*lvl); err != nil {
		logger.LogError("Failed to set reserve: %v", err)
	}

	return nil
}

func (c *SetCmd) applyGridCharging(pw *gopowerwall.Powerwall) error {
	if c.GridCharging == "" {
		return nil
	}
	gc := strings.ToLower(c.GridCharging)
	if gc != "on" && gc != "off" {
		return fmt.Errorf("invalid Grid Charging Mode [%s] - must be on or off: %w", gc, ErrNoActionSpecified)
	}
	fmt.Fprintf(os.Stdout, "Setting Grid Charging Mode to %s\n", gc)
	if _, err := pw.SetGridCharging(gc == "on"); err != nil {
		logger.LogError("Failed to set grid charging: %v", err)
	}

	return nil
}

func (c *SetCmd) applyGridExport(pw *gopowerwall.Powerwall) error {
	if c.GridExport == "" {
		return nil
	}
	ge := strings.ToLower(c.GridExport)
	if ge != "battery_ok" && ge != "pv_only" && ge != "never" {
		return fmt.Errorf(
			"invalid Grid Export Mode [%s] - must be battery_ok, pv_only, or never: %w",
			ge,
			ErrNoActionSpecified,
		)
	}
	fmt.Fprintf(os.Stdout, "Setting Grid Export Mode to %s\n", ge)
	if _, err := pw.SetGridExport(ge); err != nil {
		logger.LogError("Failed to set grid export: %v", err)
	}

	return nil
}
