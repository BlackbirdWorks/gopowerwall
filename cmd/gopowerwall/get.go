package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
	"github.com/blackbirdworks/gopowerwall/pkgs/version"
	"github.com/blackbirdworks/gopowerwall/powerwall"
)

var ErrUnableToConnectGet = errors.New(
	"unable to connect. Set -host and -password or configure FleetAPI or Cloud access",
)

// GetCmd retrieves Powerwall metrics and settings.
type GetCmd struct {
	Format string `default:"text" help:"Output format: text, json, csv" name:"format"`
	ConnectionFlags
}

// Run executes the get command.
func (c *GetCmd) Run(cmdCtx *Context) error {
	ctx := c.WithLogger(cmdCtx.Context)
	w := cmdCtx.Output()
	pw, err := c.BuildPowerwall(ctx)
	if err != nil {
		return err
	}

	if !pw.IsConnected() {
		return ErrUnableToConnectGet
	}

	if c.Format == "text" {
		fmt.Fprintf(
			w,
			"gopowerwall [%s] - Get Powerwall settings using %s mode.\n\n",
			version.Version,
			pw.Mode(),
		)
	}

	out := collectMetrics(ctx, pw)

	switch c.Format {
	case "json":
		return printJSON(w, out)
	case "csv":
		return printCSV(w, out)
	default:
		return printText(w, out)
	}
}

func collectMetrics(ctx context.Context, pw *powerwall.Powerwall) map[string]any {
	var (
		wg             sync.WaitGroup
		siteName       any
		din            any
		firmware       any
		mode           any
		reserve        any
		soc            any
		gridStatus     = "Unknown"
		grid           any
		home           any
		battery        any
		solar          any
		gridCharging   any
		gridExportMode any
		timeRemaining  any
	)

	wg.Go(func() { siteName = lookup.OrNil(pw.SiteName(ctx)) })
	wg.Go(func() { din = lookup.OrNil(pw.Din(ctx)) })
	wg.Go(func() { firmware = lookup.OrNil(pw.Version(ctx)) })
	wg.Go(func() { mode = lookup.OrNil(pw.GetMode(ctx)) })
	wg.Go(func() { reserve = lookup.OrNil(pw.GetReserve(ctx)) })
	wg.Go(func() { soc = lookup.OrNil(pw.LevelScaled(ctx)) })
	wg.Go(func() {
		gs, err := pw.GridStatusString(ctx)
		if err == nil {
			gridStatus = gs
		}
	})
	wg.Go(func() { grid = lookup.OrNil(pw.Grid(ctx)) })
	wg.Go(func() { home = lookup.OrNil(pw.Home(ctx)) })
	wg.Go(func() { battery = lookup.OrNil(pw.Battery(ctx)) })
	wg.Go(func() { solar = lookup.OrNil(pw.Solar(ctx)) })
	wg.Go(func() { gridCharging = lookup.OrNil(pw.GetGridCharging(ctx)) })
	wg.Go(func() { gridExportMode = lookup.OrNil(pw.GetGridExport(ctx)) })
	wg.Go(func() {
		tr, err := pw.GetTimeRemaining(ctx)
		timeRemaining = lookup.OrNil(tr.Hours(), err)
	})
	wg.Wait()

	return map[string]any{
		"site":             siteName,
		"site_id":          siteName,
		"din":              din,
		"firmware":         firmware,
		"mode":             mode,
		"reserve":          reserve,
		"soc":              soc,
		"grid_status":      gridStatus,
		"grid":             grid,
		"home":             home,
		"battery":          battery,
		"solar":            solar,
		"grid_charging":    gridCharging,
		"grid_export_mode": gridExportMode,
		"time_remaining":   timeRemaining,
	}
}

func printJSON(w io.Writer, out map[string]any) error {
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(b))

	return nil
}

func printCSV(w io.Writer, out map[string]any) error {
	keys := slices.Sorted(maps.Keys(out))
	fmt.Fprintln(w, strings.Join(keys, ","))
	vals := make([]string, 0, len(keys))
	for _, k := range keys {
		vals = append(vals, formatMetricValue(out[k]))
	}
	fmt.Fprintln(w, strings.Join(vals, ","))

	return nil
}

func printText(w io.Writer, out map[string]any) error {
	labels := map[string]string{
		"site_id": "Site ID",
		"din":     "DIN",
		"soc":     "Battery Level",
	}
	keys := slices.Sorted(maps.Keys(out))
	for _, item := range keys {
		name := labels[item]
		if name == "" {
			name = strings.ReplaceAll(item, "_", " ")
		}
		fmt.Fprintf(w, "  %-18s%s\n", name, formatMetricValue(out[item]))
	}
	fmt.Fprintln(w)

	return nil
}

// formatMetricValue renders a collectMetrics value for the human-readable
// and CSV output formats. Metrics are typically nil-able pointers (so a
// caller can tell "unavailable" apart from a legitimate zero value). A bare
// fmt.Sprintf("%v", v) mishandles both cases here: a *string/*float64/etc.
// boxed in the map's `any` value is a typed nil, and a typed nil pointer
// compares unequal to a literal nil interface, so the old "v == nil" guard
// never actually fired; and printing a non-nil pointer to a plain type such
// as *string with %v prints its memory address rather than the pointed-to
// value. Both bugs meant `gopowerwall get` printed either "<nil>" for every
// missing field or a raw pointer address (e.g. "0xc0000a4010") for din,
// mode, reserve, site, site_id and soc whenever they were populated, in
// both the default text output and --format=csv. Dereferencing one level
// via reflection fixes both.
func formatMetricValue(v any) string {
	if v == nil {
		return "N/A"
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "N/A"
		}

		return fmt.Sprintf("%v", rv.Elem().Interface())
	}

	return fmt.Sprintf("%v", v)
}
