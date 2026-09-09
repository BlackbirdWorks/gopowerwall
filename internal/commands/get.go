package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

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

// orNil converts a (value, error) pair from a gopowerwall client accessor
// into an any that is nil on error, so printText/printCSV/printJSON below -
// unchanged since before the client's API redesign - keep rendering
// "unavailable" fields as "N/A" via formatMetricValue's existing nil check
// rather than needing their own per-field error handling.
func orNil[T any](v T, err error) any {
	if err != nil {
		return nil
	}

	return v
}

func collectMetrics(ctx context.Context, pw *powerwall.Powerwall) map[string]any {
	gridStatus, gridStatusErr := pw.GridStatusString(ctx)
	if gridStatusErr != nil {
		gridStatus = "Unknown"
	}
	timeRemaining, timeRemainingErr := pw.GetTimeRemaining(ctx)

	return map[string]any{
		"site":             orNil(pw.SiteName(ctx)),
		"site_id":          orNil(pw.SiteName(ctx)),
		"din":              orNil(pw.Din(ctx)),
		"firmware":         orNil(pw.Version(ctx)),
		"mode":             orNil(pw.GetMode(ctx)),
		"reserve":          orNil(pw.GetReserve(ctx)),
		"soc":              orNil(pw.LevelScaled(ctx)),
		"grid_status":      gridStatus,
		"grid":             orNil(pw.Grid(ctx)),
		"home":             orNil(pw.Home(ctx)),
		"battery":          orNil(pw.Battery(ctx)),
		"solar":            orNil(pw.Solar(ctx)),
		"grid_charging":    orNil(pw.GetGridCharging(ctx)),
		"grid_export_mode": orNil(pw.GetGridExport(ctx)),
		"time_remaining":   orNil(timeRemaining.Hours(), timeRemainingErr),
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
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
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
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
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
