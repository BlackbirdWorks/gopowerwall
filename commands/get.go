package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/blackbirdworks/gopowerwall"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/pkgs/version"
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
func (c *GetCmd) Run() error {
	if c.Debug {
		logger.SetDebug(true)
	}

	pw, err := c.BuildPowerwall()
	if err != nil {
		return err
	}

	if !pw.IsConnected() {
		return ErrUnableToConnectGet
	}

	if c.Format == "text" {
		fmt.Fprintf(
			os.Stdout,
			"gopowerwall [%s] - Get Powerwall settings using %s mode.\n\n",
			version.Version,
			pw.Mode(),
		)
	}

	out := collectMetrics(pw)

	switch c.Format {
	case "json":
		return printJSON(out)
	case "csv":
		return printCSV(out)
	default:
		return printText(out)
	}
}

func collectMetrics(pw *gopowerwall.Powerwall) map[string]any {
	return map[string]any{
		"site":             pw.SiteName(),
		"site_id":          pw.SiteName(),
		"din":              pw.Din(),
		"firmware":         pw.Version(),
		"mode":             pw.GetMode(),
		"reserve":          pw.GetReserve(false),
		"soc":              pw.Level(true),
		"grid_status":      pw.GridStatus(gopowerwall.GridStatusString),
		"grid":             pw.Grid(),
		"home":             pw.Home(),
		"battery":          pw.Battery(),
		"solar":            pw.Solar(),
		"grid_charging":    pw.GetGridCharging(),
		"grid_export_mode": pw.GetGridExport(),
		"time_remaining":   pw.GetTimeRemaining(),
	}
}

func printJSON(out map[string]any) error {
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, string(b))

	return nil
}

func printCSV(out map[string]any) error {
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintln(os.Stdout, strings.Join(keys, ","))
	vals := make([]string, 0, len(keys))
	for _, k := range keys {
		v := out[k]
		if v == nil {
			vals = append(vals, "N/A")
		} else {
			vals = append(vals, fmt.Sprintf("%v", v))
		}
	}
	fmt.Fprintln(os.Stdout, strings.Join(vals, ","))

	return nil
}

func printText(out map[string]any) error {
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
		val := out[item]
		if val == nil {
			fmt.Fprintf(os.Stdout, "  %-18s%s\n", name, "N/A")
		} else {
			fmt.Fprintf(os.Stdout, "  %-18s%v\n", name, val)
		}
	}
	fmt.Fprintln(os.Stdout)

	return nil
}
