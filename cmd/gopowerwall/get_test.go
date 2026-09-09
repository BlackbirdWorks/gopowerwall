package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetCmdConnected drives GetCmd.Run against a fake local gateway for
// every output format, exercising collectMetrics and the
// printJSON/printCSV/printText helpers.
func TestGetCmdConnected(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		format string
	}

	for _, tc := range []testCase{
		{name: "text format", format: "text"},
		{name: "json format", format: "json"},
		{name: "csv format", format: "csv"},
		{name: "unrecognised format falls back to text", format: "xml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gw := newFakeGateway(t)
			cmd := GetCmd{
				Format:          tc.format,
				ConnectionFlags: connectionFlags(t, gw),
			}

			err := cmd.Run(&Context{Context: t.Context()})
			require.NoError(t, err)
		})
	}
}

// TestGetCmdTextOutputDereferencesPointerMetrics is the regression test for
// a bug found while adding this coverage: collectMetrics' values are
// pointers (din, mode, reserve, site, site_id, soc), and get.go used to
// format them with a bare fmt.Sprintf("%v", v). For a non-nil pointer to a
// plain type that prints the pointer's address (e.g. "0xc0000a4010")
// instead of the value, so "gopowerwall get" was unusable in its default
// text format, and in --format=csv, whenever those fields were populated.
func TestGetCmdTextOutputDereferencesPointerMetrics(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t)
	cmd := GetCmd{
		Format:          "text",
		ConnectionFlags: connectionFlags(t, gw),
	}

	var buf bytes.Buffer
	err := cmd.Run(&Context{Context: t.Context(), Out: &buf})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Tesla Energy Gateway")
	assert.Contains(t, out, "1232100-00-E--TG1234567890G1")
	assert.Contains(t, out, "self_consumption")
	assert.NotContains(t, out, "0x")
}

// TestGetCmdRequiresConnection covers GetCmd's disconnected error path; it
// never authenticates, so it is safe to run in parallel.
func TestGetCmdRequiresConnection(t *testing.T) {
	t.Parallel()

	cmd := GetCmd{
		Local: true,
		Host:  "127.0.0.1:9",
	}

	err := cmd.Run(&Context{Context: t.Context()})
	assert.ErrorIs(t, err, ErrUnableToConnectGet)
}
