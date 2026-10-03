package powerwall

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/powerwall/cloud"
	"github.com/blackbirdworks/gopowerwall/powerwall/fleetapi"
)

func TestValidateHost(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		host      string
		wantError bool
	}

	for _, tc := range []testCase{
		{name: "empty host is valid for cloud mode", host: "", wantError: false},
		{name: "plain IPv4 address", host: "192.168.1.100", wantError: false},
		{name: "IPv4 with valid port", host: "192.168.1.100:8443", wantError: false},
		{name: "valid hostname", host: "powerwall.local", wantError: false},
		{name: "valid hostname with port", host: "powerwall.local:443", wantError: false},
		{name: "non-numeric port", host: "192.168.1.100:abc", wantError: true},
		{name: "port zero out of range", host: "192.168.1.100:0", wantError: true},
		{name: "port above 65535 out of range", host: "192.168.1.100:70000", wantError: true},
		{name: "invalid hostname with spaces", host: "invalid host name", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateHost(tc.host)
			if tc.wantError {
				require.Error(t, err)
				var cfgErr *models.InvalidConfigError
				require.ErrorAs(t, err, &cfgErr)
				assert.Equal(t, "host", cfgErr.Param)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCheckDirWritable(t *testing.T) {
	t.Parallel()

	type testCase struct {
		setup     func(t *testing.T) (dirpath, name string)
		name      string
		wantError bool
	}

	for _, tc := range []testCase{
		{
			name: "existing writable directory",
			setup: func(t *testing.T) (string, string) {
				t.Helper()

				return t.TempDir(), "testdir"
			},
			wantError: false,
		},
		{
			name: "non-existent directory gets created",
			setup: func(t *testing.T) (string, string) {
				t.Helper()

				return filepath.Join(t.TempDir(), "sub", "dir"), "newdir"
			},
			wantError: false,
		},
		{
			name: "path is a regular file, not a directory",
			setup: func(t *testing.T) (string, string) {
				t.Helper()
				filePath := filepath.Join(t.TempDir(), "file.txt")
				require.NoError(t, os.WriteFile(filePath, []byte("data"), 0o600))

				return filePath, "filedir"
			},
			wantError: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dirpath, name := tc.setup(t)
			err := checkDirWritable(dirpath, name)
			if tc.wantError {
				require.Error(t, err)
				var cfgErr *models.InvalidConfigError
				require.ErrorAs(t, err, &cfgErr)
				assert.Equal(t, name, cfgErr.Param)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestDecodeToJSON(t *testing.T) {
	t.Parallel()

	type testCase struct {
		input     any
		name      string
		want      string
		wantError bool
	}

	for _, tc := range []testCase{
		{
			name:      "string passes through directly as bytes",
			input:     `{"key":"val"}`,
			want:      `{"key":"val"}`,
			wantError: false,
		},
		{
			name:      "map gets JSON marshaled",
			input:     map[string]int{"a": 1},
			want:      `{"a":1}`,
			wantError: false,
		},
		{
			name:      "channel cannot be marshaled",
			input:     make(chan int),
			wantError: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := decodeToJSON(tc.input)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, string(got))
			}
		})
	}
}

func TestFloatFromAny(t *testing.T) {
	t.Parallel()

	type testCase struct {
		input     any
		name      string
		want      float64
		wantError bool
	}

	for _, tc := range []testCase{
		{name: "float64 value", input: float64(42.5), want: 42.5, wantError: false},
		{name: "int value", input: int(10), want: 10.0, wantError: false},
		{name: "float32 unsupported", input: float32(12.25), want: 0, wantError: true},
		{name: "int64 unsupported", input: int64(99), want: 0, wantError: true},
		{name: "string unsupported", input: "55.5", want: 0, wantError: true},
		{name: "nil input", input: nil, want: 0, wantError: true},
		{name: "bool input", input: true, want: 0, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := floatFromAny(tc.input)
			if tc.wantError {
				require.ErrorIs(t, err, ErrFieldMissing)
			} else {
				require.NoError(t, err)
				assert.InDelta(t, tc.want, got, 0.0001)
			}
		})
	}
}

func TestIntOrZero(t *testing.T) {
	t.Parallel()

	type testCase struct {
		input any
		name  string
		want  int
	}

	for _, tc := range []testCase{
		{name: "int value", input: int(7), want: 7},
		{name: "float64 value", input: float64(15.9), want: 15},
		{name: "bool true", input: true, want: 1},
		{name: "bool false", input: false, want: 0},
		{name: "string non-numeric fallback", input: "abc", want: 0},
		{name: "nil value fallback", input: nil, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := intOrZero(map[string]any{"val": tc.input}, "val")
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestLookupFloatPtr(t *testing.T) {
	t.Parallel()

	type testCase struct {
		m       map[string]any
		name    string
		key     string
		wantVal float64
		wantNil bool
	}

	for _, tc := range []testCase{
		{
			name:    "key exists with float value",
			m:       map[string]any{"power": 1200.5},
			key:     "power",
			wantVal: 1200.5,
			wantNil: false,
		},
		{
			name:    "key is missing",
			m:       map[string]any{"other": 10.0},
			key:     "power",
			wantNil: true,
		},
		{
			name:    "key exists with non-numeric value",
			m:       map[string]any{"power": "invalid"},
			key:     "power",
			wantNil: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := lookupFloatPtr(tc.m, tc.key)
			if tc.wantNil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.InDelta(t, tc.wantVal, *got, 0.001)
			}
		})
	}
}

func TestCollectGridStatusAlert(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name           string
		status         string
		wantAlert      string
		servicesActive bool
	}

	for _, tc := range []testCase{
		{name: "grid services active alert", servicesActive: true, wantAlert: "GridServicesActive"},
		{name: "transition status alert", status: "SystemTransitionToGrid", wantAlert: "SystemTransitionToGrid"},
		{name: "islanded status alert", status: "SystemIslandedActive", wantAlert: "SystemIslandedActive"},
		{name: "empty status produces no alert", status: "", wantAlert: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			alertSet := make(map[string]struct{})
			var statusMap map[string]any
			if tc.status != "" || tc.servicesActive {
				statusMap = map[string]any{
					"grid_status":          tc.status,
					"grid_services_active": tc.servicesActive,
				}
			}
			collectGridStatusAlert(statusMap, alertSet)
			if tc.wantAlert == "" {
				assert.Empty(t, alertSet)
			} else {
				assert.Contains(t, alertSet, tc.wantAlert)
			}
		})
	}
}

func TestCollectDeviceAlerts(t *testing.T) {
	t.Parallel()

	type testCase struct {
		devices    map[string]map[string]any
		name       string
		wantAlerts []string
	}

	for _, tc := range []testCase{
		{
			name: "string slice alerts from protobuf local backend",
			devices: map[string]map[string]any{
				"dev1": {"alerts": []string{"AlertA", "AlertB"}},
			},
			wantAlerts: []string{"AlertA", "AlertB"},
		},
		{
			name: "any slice alerts from json cloud backend with string and non-string values",
			devices: map[string]map[string]any{
				"dev2": {"alerts": []any{"AlertC", 404}},
			},
			wantAlerts: []string{"AlertC", "404"},
		},
		{
			name: "device with no alerts key or empty",
			devices: map[string]map[string]any{
				"dev3": {"other": "value"},
			},
			wantAlerts: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			alertSet := make(map[string]struct{})
			collectDeviceAlerts(tc.devices, alertSet)
			for _, want := range tc.wantAlerts {
				assert.Contains(t, alertSet, want)
			}
			assert.Len(t, alertSet, len(tc.wantAlerts))
		})
	}
}

func TestAutoSelectMode(t *testing.T) {
	t.Parallel()

	type testCase struct {
		setup    func(t *testing.T) *Config
		name     string
		wantMode ConnectionMode
	}

	for _, tc := range []testCase{
		{
			name: "host set selects ModeLocal",
			setup: func(t *testing.T) *Config {
				t.Helper()

				return &Config{Host: "192.168.1.100"}
			},
			wantMode: ModeLocal,
		},
		{
			name: "fleetapi config file in authpath selects ModeFleetAPI",
			setup: func(t *testing.T) *Config {
				t.Helper()
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, fleetapi.ConfigFile), []byte("{}"), 0o600))

				return &Config{AuthPath: dir}
			},
			wantMode: ModeFleetAPI,
		},
		{
			name: "cloud auth file in authpath selects ModeCloud",
			setup: func(t *testing.T) *Config {
				t.Helper()
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, cloud.AuthFile), []byte("{}"), 0o600))

				return &Config{AuthPath: dir}
			},
			wantMode: ModeCloud,
		},
		{
			name: "empty config leaves mode unchanged",
			setup: func(t *testing.T) *Config {
				t.Helper()

				return &Config{AuthPath: t.TempDir()}
			},
			wantMode: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pw := &Powerwall{}
			cfg := tc.setup(t)
			pw.autoSelectMode(t.Context(), cfg)
			assert.Equal(t, tc.wantMode, pw.mode)
		})
	}
}

func TestApplyAggregateCorrections(t *testing.T) {
	t.Parallel()

	type testCase struct {
		agg       *models.MetersAggregates
		name      string
		cfg       aggregatesConfig
		wantSite  float64
		wantSolar float64
		wantLoad  float64
	}

	for _, tc := range []testCase{
		{
			name: "site power within zero threshold is zeroed",
			cfg:  aggregatesConfig{siteZeroThreshold: 30},
			agg: &models.MetersAggregates{
				Site: models.MeterReading{InstantPower: 25},
			},
			wantSite: 0,
		},
		{
			name: "site power outside threshold is unchanged",
			cfg:  aggregatesConfig{siteZeroThreshold: 30},
			agg: &models.MetersAggregates{
				Site: models.MeterReading{InstantPower: 50},
			},
			wantSite: 50,
		},
		{
			name: "negative solar moved into load when correctNegativeSolar is true",
			cfg:  aggregatesConfig{correctNegativeSolar: true},
			agg: &models.MetersAggregates{
				Solar: models.MeterReading{InstantPower: -20},
				Load:  models.MeterReading{InstantPower: 100},
			},
			wantSolar: 0,
			wantLoad:  120,
		},
		{
			name: "negative solar unchanged when correctNegativeSolar is false",
			cfg:  aggregatesConfig{correctNegativeSolar: false},
			agg: &models.MetersAggregates{
				Solar: models.MeterReading{InstantPower: -20},
				Load:  models.MeterReading{InstantPower: 100},
			},
			wantSolar: -20,
			wantLoad:  100,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			agg := *tc.agg
			applyAggregateCorrections(tc.cfg, &agg)
			assert.InDelta(t, tc.wantSite, agg.Site.InstantPower, 0.001)
			assert.InDelta(t, tc.wantSolar, agg.Solar.InstantPower, 0.001)
			assert.InDelta(t, tc.wantLoad, agg.Load.InstantPower, 0.001)
		})
	}
}

func TestSetClientLocked(t *testing.T) {
	t.Parallel()

	type testCase struct {
		client     Client
		name       string
		wantClient bool
		wantPoller bool
	}

	for _, tc := range []testCase{
		{
			name:       "nil client clears client and poller",
			client:     nil,
			wantClient: false,
			wantPoller: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pw := &Powerwall{}
			pw.setClientLocked(tc.client)
			if tc.wantClient {
				assert.NotNil(t, pw.client)
			} else {
				assert.Nil(t, pw.client)
			}
			if tc.wantPoller {
				assert.NotNil(t, pw.poller)
			} else {
				assert.Nil(t, pw.poller)
			}
		})
	}
}
