package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/pkgs/version"
	"github.com/blackbirdworks/gopowerwall/powerwall"
)

var (
	ErrNoActionSpecified = errors.New(
		"no action specified. Use --mode, --reserve, --current, --gridcharging, or --gridexport",
	)
	ErrUnableToConnect  = errors.New("unable to connect. Check connection mode and credentials")
	ErrBatteryLevelRead = errors.New("unable to read current battery level from Powerwall")
)

const reserveThreshold80 = 80.0

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
func (c *SetCmd) Run(cmdCtx *Context) error {
	ctx := c.WithLogger(cmdCtx.Context)
	w := cmdCtx.Output()
	if c.Mode == "" && c.Reserve == -1 && !c.Current && c.GridCharging == "" && c.GridExport == "" {
		return ErrNoActionSpecified
	}

	pw, err := c.BuildPowerwall(ctx)
	if err != nil {
		return err
	}

	if !pw.IsConnected() {
		return ErrUnableToConnect
	}

	fmt.Fprintf(w, "gopowerwall [%s] - Set Powerwall settings using %s mode.\n\n", version.Version, pw.Mode())

	if errMode := c.applyMode(ctx, pw, w); errMode != nil {
		return errMode
	}
	c.applyReserve(ctx, pw, w)
	if errCurrent := c.applyCurrent(ctx, pw, w); errCurrent != nil {
		return errCurrent
	}
	if errGrid := c.applyGridCharging(ctx, pw, w); errGrid != nil {
		return errGrid
	}

	return c.applyGridExport(ctx, pw, w)
}

func (c *SetCmd) applyMode(ctx context.Context, pw *powerwall.Powerwall, w io.Writer) error {
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
	fmt.Fprintf(w, "Setting Powerwall Mode to %s\n", m)
	if _, err := pw.SetMode(ctx, m); err != nil {
		logger.Load(ctx).ErrorContext(ctx, "failed to set mode", "error", err)
	}

	return nil
}

func (c *SetCmd) applyReserve(ctx context.Context, pw *powerwall.Powerwall, w io.Writer) {
	if c.Reserve == -1 {
		return
	}
	resVal := c.Reserve
	capped := pw.IsCloud() || pw.IsFleetAPI()
	if resVal > 80 && capped {
		logger.Load(ctx).WarnContext(ctx, "Tesla cloud and FleetAPI limit backup reserve to 80% maximum")
	}
	fmt.Fprintf(w, "Setting Powerwall Reserve to %v\n", resVal)
	if _, err := pw.SetReserve(ctx, resVal); err != nil {
		logger.Load(ctx).ErrorContext(ctx, "failed to set reserve", "error", err)

		return
	}
	if capped {
		if applied, err := pw.GetReserveForced(ctx); err == nil {
			fmt.Fprintf(w, "Powerwall Reserve actually set to %.1f\n", applied)
		}
	}
}

func (c *SetCmd) applyCurrent(ctx context.Context, pw *powerwall.Powerwall, w io.Writer) error {
	if !c.Current {
		return nil
	}
	lvl, err := pw.Level(ctx)
	if err != nil {
		return ErrBatteryLevelRead
	}
	capped := pw.IsCloud() || pw.IsFleetAPI()
	if lvl > reserveThreshold80 && capped {
		logger.Load(ctx).WarnContext(ctx, "Tesla cloud and FleetAPI limit backup reserve to 80% maximum")
	}
	fmt.Fprintf(w, "Setting Powerwall Reserve to Current Charge Level %.1f\n", lvl)
	if _, setErr := pw.SetReserve(ctx, lvl); setErr != nil {
		logger.Load(ctx).ErrorContext(ctx, "failed to set reserve", "error", setErr)

		return nil
	}
	if capped {
		if applied, getErr := pw.GetReserveForced(ctx); getErr == nil {
			fmt.Fprintf(w, "Powerwall Reserve actually set to %.1f\n", applied)
		}
	}

	return nil
}

func (c *SetCmd) applyGridCharging(ctx context.Context, pw *powerwall.Powerwall, w io.Writer) error {
	if c.GridCharging == "" {
		return nil
	}
	gc := strings.ToLower(c.GridCharging)
	if gc != "on" && gc != "off" {
		return fmt.Errorf("invalid Grid Charging Mode [%s] - must be on or off: %w", gc, ErrNoActionSpecified)
	}
	fmt.Fprintf(w, "Setting Grid Charging Mode to %s\n", gc)
	if _, err := pw.SetGridCharging(ctx, gc == "on"); err != nil {
		logger.Load(ctx).ErrorContext(ctx, "failed to set grid charging", "error", err)
	}

	return nil
}

func (c *SetCmd) applyGridExport(ctx context.Context, pw *powerwall.Powerwall, w io.Writer) error {
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
	fmt.Fprintf(w, "Setting Grid Export Mode to %s\n", ge)
	if _, err := pw.SetGridExport(ctx, ge); err != nil {
		logger.Load(ctx).ErrorContext(ctx, "failed to set grid export", "error", err)
	}

	return nil
}
