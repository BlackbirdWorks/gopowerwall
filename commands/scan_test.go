package commands_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/commands"
	"github.com/blackbirdworks/gopowerwall/models"
)

// TestScanCmdJSON drives ScanCmd.Run against a single, almost-certainly-idle
// loopback host with a very short per-host timeout, so it completes quickly
// without depending on any real Powerwall gateway being reachable. It only
// exercises the JSON output path (no "Scanner" banner, colour, or
// interactive progress output).
func TestScanCmdJSON(t *testing.T) {
	t.Parallel()

	cmd := commands.ScanCmd{
		Network: "127.0.0.1/32",
		Hosts:   1,
		Timeout: 0.05,
		JSON:    true,
		Nocolor: true,
	}

	var buf bytes.Buffer
	err := cmd.Run(&commands.Context{Context: t.Context(), Out: &buf})
	require.NoError(t, err)

	var results []models.DiscoveredDevice
	require.NoError(t, json.Unmarshal(buf.Bytes(), &results))
}

// TestScanCmdTextBanner covers ScanCmd.Run's non-JSON path, which prints a
// banner before scanning. Network (not IP) is used to keep the scan to a
// single /32 host: resolveCIDR appends a /24 to a bare IP with no mask,
// which would otherwise expand this into a 256-host scan.
func TestScanCmdTextBanner(t *testing.T) {
	t.Parallel()

	cmd := commands.ScanCmd{
		Network: "127.0.0.1/32",
		Hosts:   1,
		Timeout: 0.05,
		Nocolor: true,
	}

	var buf bytes.Buffer
	err := cmd.Run(&commands.Context{Context: t.Context(), Out: &buf})
	require.NoError(t, err)
	require.Contains(t, buf.String(), "Scanner")
}
