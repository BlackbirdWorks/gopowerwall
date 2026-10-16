package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/models"
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

func TestFlatten(t *testing.T) {
	t.Parallel()

	type testCase struct {
		input map[string]any
		want  map[string]any
		name  string
	}

	for _, tc := range []testCase{
		{
			name:  "empty map",
			input: map[string]any{},
			want:  map[string]any{},
		},
		{
			name: "flat map is unchanged",
			input: map[string]any{
				"site": "Home",
				"soc":  85.5,
			},
			want: map[string]any{
				"site": "Home",
				"soc":  85.5,
			},
		},
		{
			name: "single nested level produces dotted keys",
			input: map[string]any{
				"site": "Home",
				"temps": map[string]any{
					"TEPOD--1": 24.5,
					"TEPOD--2": 26.0,
				},
			},
			want: map[string]any{
				"site":           "Home",
				"temps.TEPOD--1": 24.5,
				"temps.TEPOD--2": 26.0,
			},
		},
		{
			name: "multiple nested levels",
			input: map[string]any{
				"fans": map[string]any{
					"PVAC--1": map[string]any{
						"PVAC_Fan_Speed_Actual_RPM": 1200.0,
					},
				},
			},
			want: map[string]any{
				"fans.PVAC--1.PVAC_Fan_Speed_Actual_RPM": 1200.0,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := flatten(tc.input, "")
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFanSpeedEntryToMap(t *testing.T) {
	t.Parallel()

	type testCase struct {
		entry models.FanSpeedEntry
		want  map[string]any
		name  string
	}

	actualRPM := 1200.0
	targetRPM := 1250.0
	fanSpeedA := 1395.0
	fanDutyA := 19.1

	for _, tc := range []testCase{
		{
			name:  "empty entry",
			entry: models.FanSpeedEntry{},
			want:  map[string]any{},
		},
		{
			name: "PVAC fan speeds",
			entry: models.FanSpeedEntry{
				ActualRPM: &actualRPM,
				TargetRPM: &targetRPM,
			},
			want: map[string]any{
				"PVAC_Fan_Speed_Actual_RPM": &actualRPM,
				"PVAC_Fan_Speed_Target_RPM": &targetRPM,
			},
		},
		{
			name: "PW3 fan speeds",
			entry: models.FanSpeedEntry{
				FanSpeedA: &fanSpeedA,
				FanDutyA:  &fanDutyA,
			},
			want: map[string]any{
				"PCH_FanDuty_A":  &fanDutyA,
				"PCH_FanDuty_B":  (*float64)(nil),
				"PCH_FanSpeed_A": &fanSpeedA,
				"PCH_FanSpeed_B": (*float64)(nil),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := fanSpeedEntryToMap(tc.entry)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrintCSVWithTempsAndFans(t *testing.T) {
	t.Parallel()

	type testCase struct {
		out         map[string]any
		name        string
		wantHeaders []string
		wantVals    []string
	}

	tempVal := 25.5
	rpmVal := 1200.0

	for _, tc := range []testCase{
		{
			name: "csv without temps and fans",
			out: map[string]any{
				"site": "Home",
				"soc":  85.0,
			},
			wantHeaders: []string{"site,soc"},
			wantVals:    []string{"Home,85"},
		},
		{
			name: "csv with flattened temps and fans",
			out: map[string]any{
				"site": "Home",
				"temps": map[string]any{
					"TEPOD--1": &tempVal,
				},
				"fans": map[string]any{
					"PVAC--1": map[string]any{
						"PVAC_Fan_Speed_Actual_RPM": &rpmVal,
					},
				},
			},
			wantHeaders: []string{"fans.PVAC--1.PVAC_Fan_Speed_Actual_RPM,site,temps.TEPOD--1"},
			wantVals:    []string{"1200,Home,25.5"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			err := printCSV(&buf, tc.out)
			require.NoError(t, err)

			res := buf.String()
			for _, h := range tc.wantHeaders {
				assert.Contains(t, res, h)
			}
			for _, v := range tc.wantVals {
				assert.Contains(t, res, v)
			}
		})
	}
}

func TestPrintTextWithTempsAndFans(t *testing.T) {
	t.Parallel()

	type testCase struct {
		out      map[string]any
		name     string
		contains []string
	}

	tempVal := 25.5
	rpmVal := 1200.0

	for _, tc := range []testCase{
		{
			name: "text with temps and fans",
			out: map[string]any{
				"site": "Home",
				"temps": map[string]any{
					"TEPOD--1": &tempVal,
				},
				"fans": map[string]any{
					"PVAC--1": map[string]any{
						"PVAC_Fan_Speed_Actual_RPM": &rpmVal,
					},
				},
			},
			contains: []string{
				"Temperatures",
				"TEPOD--1",
				"25.5",
				"Fans",
				"PVAC--1",
				"PVAC_Fan_Speed_Actual_RPM=1200",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			err := printText(&buf, tc.out)
			require.NoError(t, err)

			res := buf.String()
			for _, exp := range tc.contains {
				assert.Contains(t, res, exp)
			}
		})
	}
}
