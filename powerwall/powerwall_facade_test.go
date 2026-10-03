package powerwall_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/powerwall"
	"github.com/blackbirdworks/gopowerwall/powerwall/proto/teslapower"
)

func bogusFixtureDir() string {
	_, thisFile, _, _ := runtime.Caller(0)

	return filepath.Join(filepath.Dir(thisFile), "..", "proxy", "web", "bogus")
}

func readBogusFixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(bogusFixtureDir(), name))
	require.NoError(t, err)

	return data
}

func buildFacadeVitalsProtobuf(t *testing.T) []byte {
	t.Helper()

	pb := &teslapower.DevicesWithVitals{
		Devices: []*teslapower.SiteControllerConnectedDeviceWithVitals{
			{
				Device: &teslapower.SiteControllerConnectedDevice{
					Device: &teslapower.Device{Din: &teslapower.StringValue{Value: "TETHC--123"}},
				},
				Vitals: []*teslapower.DeviceVital{
					vitalFloat("THC_AmbientTemp", 22.5),
				},
			},
			{
				Device: &teslapower.SiteControllerConnectedDevice{
					Device: &teslapower.Device{Din: &teslapower.StringValue{Value: "TEPINV--456"}},
				},
				Vitals: []*teslapower.DeviceVital{
					vitalFloat("PINV_Fout", 60.0),
					vitalFloat("PINV_VSplit1", 120.0),
					vitalFloat("PINV_VSplit2", 120.0),
				},
			},
			{
				Device: &teslapower.SiteControllerConnectedDevice{
					Device: &teslapower.Device{Din: &teslapower.StringValue{Value: "TESYNC--789"}},
				},
				Vitals: []*teslapower.DeviceVital{
					vitalFloat("ISLAND_Frequency", 60.0),
					vitalFloat("METER_Volts", 240.0),
				},
			},
		},
	}

	data, err := proto.Marshal(pb)
	require.NoError(t, err)

	return data
}

func newFacadeTestGateway(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/login/Basic", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "test-auth-cookie", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "UserRecord", Value: "test-user-record", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "mock-token"})
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readBogusFixture(t, "api.status.json"))
	})
	mux.HandleFunc("/api/site_info", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readBogusFixture(t, "api.site_info.json"))
	})
	mux.HandleFunc("/api/site_info/site_name", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readBogusFixture(t, "api.site_info.site_name.json"))
	})
	mux.HandleFunc("/api/system_status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readBogusFixture(t, "api.system_status.json"))
	})
	mux.HandleFunc("/api/system_status/soe", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readBogusFixture(t, "api.system_status.soe.json"))
	})
	mux.HandleFunc("/api/system_status/grid_status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readBogusFixture(t, "api.system_status.grid_status.json"))
	})
	mux.HandleFunc("/api/meters/aggregates", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readBogusFixture(t, "api.meters.aggregates.json"))
	})
	mux.HandleFunc("/api/operation", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "ok"})

			return
		}
		_, _ = w.Write(readBogusFixture(t, "api.operation.json"))
	})
	vitalsBytes := buildFacadeVitalsProtobuf(t)
	mux.HandleFunc("/api/devices/vitals", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(vitalsBytes)
	})

	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

func newConnectedFacadePowerwall(t *testing.T) *powerwall.Powerwall {
	t.Helper()

	srv := newFacadeTestGateway(t)
	host := strings.TrimPrefix(srv.URL, "https://")

	pw, err := powerwall.New(
		t.Context(),
		powerwall.WithHost(host),
		powerwall.WithPassword("testpw"),
		powerwall.WithCloudMode(false),
		powerwall.WithCacheFile(filepath.Join(t.TempDir(), "cache")),
	)
	require.NoError(t, err)
	require.True(t, pw.IsConnected())

	return pw
}

func TestPowerwallOptions(t *testing.T) {
	t.Parallel()

	type testCase struct {
		verify func(t *testing.T, cfg *powerwall.Config)
		opt    powerwall.Option
		name   string
	}

	for _, tc := range []testCase{
		{
			name: "WithSiteID sets site ID",
			opt:  powerwall.WithSiteID("12345"),
			verify: func(t *testing.T, cfg *powerwall.Config) {
				t.Helper()
				assert.Equal(t, "12345", cfg.SiteID)
			},
		},
		{
			name: "WithAuthMode sets auth mode",
			opt:  powerwall.WithAuthMode(powerwall.AuthModeCookie),
			verify: func(t *testing.T, cfg *powerwall.Config) {
				t.Helper()
				assert.Equal(t, powerwall.AuthModeCookie, cfg.AuthMode)
			},
		},
		{
			name: "WithAutoSelect sets auto select",
			opt:  powerwall.WithAutoSelect(true),
			verify: func(t *testing.T, cfg *powerwall.Config) {
				t.Helper()
				assert.True(t, cfg.AutoSelect)
			},
		},
		{
			name: "WithRetryModes sets retry modes",
			opt:  powerwall.WithRetryModes(true),
			verify: func(t *testing.T, cfg *powerwall.Config) {
				t.Helper()
				assert.True(t, cfg.RetryModes)
			},
		},
		{
			name: "WithWiFiHost sets wifi host",
			opt:  powerwall.WithWiFiHost("192.168.91.1"),
			verify: func(t *testing.T, cfg *powerwall.Config) {
				t.Helper()
				assert.Equal(t, "192.168.91.1", cfg.WiFiHost)
			},
		},
		{
			name: "WithTEDAPIApiVersion sets api version",
			opt:  powerwall.WithTEDAPIApiVersion(powerwall.TEDAPIVersion2026_06),
			verify: func(t *testing.T, cfg *powerwall.Config) {
				t.Helper()
				assert.Equal(t, powerwall.TEDAPIVersion2026_06, cfg.TEDAPIApiVersion)
			},
		},
		{
			name: "WithTEDAPIAuthMode sets tedapi auth mode",
			opt:  powerwall.WithTEDAPIAuthMode(powerwall.AuthModeToken),
			verify: func(t *testing.T, cfg *powerwall.Config) {
				t.Helper()
				assert.Equal(t, powerwall.AuthModeToken, cfg.TEDAPIAuthMode)
			},
		},
		{
			name: "WithFleetAPI sets fleetapi mode",
			opt:  powerwall.WithFleetAPI(true),
			verify: func(t *testing.T, cfg *powerwall.Config) {
				t.Helper()
				assert.True(t, cfg.FleetAPI)
			},
		},
		{
			name: "WithEmail sets email",
			opt:  powerwall.WithEmail("user@example.com"),
			verify: func(t *testing.T, cfg *powerwall.Config) {
				t.Helper()
				assert.Equal(t, "user@example.com", cfg.Email)
			},
		},
		{
			name: "WithTimezone sets timezone",
			opt:  powerwall.WithTimezone("America/New_York"),
			verify: func(t *testing.T, cfg *powerwall.Config) {
				t.Helper()
				assert.Equal(t, "America/New_York", cfg.Timezone)
			},
		},
		{
			name: "WithPWCacheExpire sets cache expire duration",
			opt:  powerwall.WithPWCacheExpire(10 * time.Second),
			verify: func(t *testing.T, cfg *powerwall.Config) {
				t.Helper()
				assert.Equal(t, 10*time.Second, cfg.PWCacheExpire)
			},
		},
		{
			name: "WithTimeout sets timeout duration",
			opt:  powerwall.WithTimeout(15 * time.Second),
			verify: func(t *testing.T, cfg *powerwall.Config) {
				t.Helper()
				assert.Equal(t, 15*time.Second, cfg.Timeout)
			},
		},
		{
			name: "WithPoolMaxSize sets max pool size",
			opt:  powerwall.WithPoolMaxSize(50),
			verify: func(t *testing.T, cfg *powerwall.Config) {
				t.Helper()
				assert.Equal(t, 50, cfg.PoolMaxSize)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := &powerwall.Config{}
			tc.opt(cfg)
			tc.verify(t, cfg)
		})
	}
}

func TestValidateConfigTable(t *testing.T) {
	t.Parallel()

	type testCase struct {
		cfg       func(t *testing.T) *powerwall.Config
		name      string
		wantParam string
		wantError bool
	}

	for _, tc := range []testCase{
		{
			name: "valid local mode config",
			cfg: func(t *testing.T) *powerwall.Config {
				t.Helper()

				return &powerwall.Config{
					Host:      "192.168.1.100",
					CacheFile: filepath.Join(t.TempDir(), "cache"),
				}
			},
			wantError: false,
		},
		{
			name: "valid cloud mode config with writable auth path",
			cfg: func(t *testing.T) *powerwall.Config {
				t.Helper()

				return &powerwall.Config{
					CloudMode: true,
					Email:     "user@example.com",
					AuthPath:  t.TempDir(),
				}
			},
			wantError: false,
		},
		{
			name: "valid fleetapi config with writable auth path",
			cfg: func(t *testing.T) *powerwall.Config {
				t.Helper()

				return &powerwall.Config{
					CloudMode: true,
					FleetAPI:  true,
					AuthPath:  t.TempDir(),
				}
			},
			wantError: false,
		},
		{
			name: "cloud mode missing email",
			cfg: func(t *testing.T) *powerwall.Config {
				t.Helper()

				return &powerwall.Config{
					CloudMode: true,
					Email:     "",
					AuthPath:  t.TempDir(),
				}
			},
			wantError: true,
			wantParam: "email",
		},
		{
			name: "cloud mode invalid email format",
			cfg: func(t *testing.T) *powerwall.Config {
				t.Helper()

				return &powerwall.Config{
					CloudMode: true,
					Email:     "not-an-email",
					AuthPath:  t.TempDir(),
				}
			},
			wantError: true,
			wantParam: "email",
		},
		{
			name: "invalid host address",
			cfg: func(t *testing.T) *powerwall.Config {
				t.Helper()

				return &powerwall.Config{
					Host:      "invalid host with space",
					CacheFile: filepath.Join(t.TempDir(), "cache"),
				}
			},
			wantError: true,
			wantParam: "host",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := tc.cfg(t)
			err := powerwall.ValidateConfig(cfg)
			if tc.wantError {
				require.Error(t, err)
				var cfgErr *models.InvalidConfigError
				require.ErrorAs(t, err, &cfgErr)
				if tc.wantParam != "" {
					assert.Equal(t, tc.wantParam, cfgErr.Param)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCoerceTEDAPIApiVersion(t *testing.T) {
	t.Parallel()

	type testCase struct {
		input string
		name  string
		want  powerwall.TEDAPIApiVersion
	}

	for _, tc := range []testCase{
		{name: "exact 2024_06", input: "2024_06", want: powerwall.TEDAPIVersion2024_06},
		{name: "exact 2026_06", input: "2026_06", want: powerwall.TEDAPIVersion2026_06},
		{name: "short V2026", input: "V2026", want: powerwall.TEDAPIVersion2026_06},
		{name: "unrecognised falls back to 2024_06", input: "unknown", want: powerwall.TEDAPIVersion2024_06},
		{name: "empty falls back to 2024_06", input: "", want: powerwall.TEDAPIVersion2024_06},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, powerwall.CoerceTEDAPIApiVersion(tc.input))
		})
	}
}

func TestPowerwallPowerSensors(t *testing.T) {
	t.Parallel()

	pw := newConnectedFacadePowerwall(t)

	type testCase struct {
		run  func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall)
		name string
	}

	for _, tc := range []testCase{
		{
			name: "Site returns scalar instant power",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				val, err := pw.Site(ctx)
				require.NoError(t, err)
				assert.NotZero(t, val)
			},
		},
		{
			name: "Solar returns scalar instant power",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				val, err := pw.Solar(ctx)
				require.NoError(t, err)
				assert.NotZero(t, val)
			},
		},
		{
			name: "Battery returns scalar instant power",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				val, err := pw.Battery(ctx)
				require.NoError(t, err)
				assert.NotZero(t, val)
			},
		},
		{
			name: "Load returns scalar instant power",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				val, err := pw.Load(ctx)
				require.NoError(t, err)
				assert.NotZero(t, val)
			},
		},
		{
			name: "Grid is alias of Site",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				grid, err := pw.Grid(ctx)
				require.NoError(t, err)
				site, _ := pw.Site(ctx)
				assert.InDelta(t, site, grid, 0.001)
			},
		},
		{
			name: "Home is alias of Load",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				home, err := pw.Home(ctx)
				require.NoError(t, err)
				load, _ := pw.Load(ctx)
				assert.InDelta(t, load, home, 0.001)
			},
		},
		{
			name: "SiteReading returns full MeterReading",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				reading, err := pw.SiteReading(ctx)
				require.NoError(t, err)
				assert.NotZero(t, reading.InstantPower)
			},
		},
		{
			name: "SolarReading returns full MeterReading",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				reading, err := pw.SolarReading(ctx)
				require.NoError(t, err)
				assert.NotZero(t, reading.InstantPower)
			},
		},
		{
			name: "BatteryReading returns full MeterReading",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				reading, err := pw.BatteryReading(ctx)
				require.NoError(t, err)
				assert.NotZero(t, reading.InstantPower)
			},
		},
		{
			name: "LoadReading returns full MeterReading",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				reading, err := pw.LoadReading(ctx)
				require.NoError(t, err)
				assert.NotZero(t, reading.InstantPower)
			},
		},
		{
			name: "GridReading is alias of SiteReading",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				grid, err := pw.GridReading(ctx)
				require.NoError(t, err)
				site, _ := pw.SiteReading(ctx)
				assert.InDelta(t, site.InstantPower, grid.InstantPower, 0.001)
			},
		},
		{
			name: "HomeReading is alias of LoadReading",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				home, err := pw.HomeReading(ctx)
				require.NoError(t, err)
				load, _ := pw.LoadReading(ctx)
				assert.InDelta(t, load.InstantPower, home.InstantPower, 0.001)
			},
		},
		{
			name: "Level returns battery state of charge",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				level, err := pw.Level(ctx)
				require.NoError(t, err)
				assert.Positive(t, level)
			},
		},
		{
			name: "LevelScaled returns scaled level within 0-100",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				scaled, err := pw.LevelScaled(ctx)
				require.NoError(t, err)
				assert.GreaterOrEqual(t, scaled, 0.0)
				assert.LessOrEqual(t, scaled, 100.0)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t, t.Context(), pw)
		})
	}
}

func TestPowerwallStatusAndViews(t *testing.T) {
	t.Parallel()

	pw := newConnectedFacadePowerwall(t)

	type testCase struct {
		run  func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall)
		name string
	}

	for _, tc := range []testCase{
		{
			name: "Status returns GatewayStatus",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				st, err := pw.Status(ctx)
				require.NoError(t, err)
				assert.NotEmpty(t, st.DIN)
			},
		},
		{
			name: "Version returns firmware string",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				v, err := pw.Version(ctx)
				require.NoError(t, err)
				assert.NotEmpty(t, v)
			},
		},
		{
			name: "VersionNumeric parses version number",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				vn, err := pw.VersionNumeric(ctx)
				require.NoError(t, err)
				assert.Positive(t, vn)
			},
		},
		{
			name: "Uptime returns duration",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				up, err := pw.Uptime(ctx)
				require.NoError(t, err)
				assert.GreaterOrEqual(t, up, time.Duration(0))
			},
		},
		{
			name: "Din returns DIN string",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				din, err := pw.Din(ctx)
				require.NoError(t, err)
				assert.NotEmpty(t, din)
			},
		},
		{
			name: "SystemStatus returns decoded system status",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				sys, err := pw.SystemStatus(ctx)
				require.NoError(t, err)
				assert.Positive(t, sys.NominalFullPackEnergy)
			},
		},
		{
			name: "SiteInfo returns site info",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				info, err := pw.SiteInfo(ctx)
				require.NoError(t, err)
				assert.NotEmpty(t, info.SiteName)
			},
		},
		{
			name: "GridStatusString reports Connected",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				str, err := pw.GridStatusString(ctx)
				require.NoError(t, err)
				assert.Equal(t, "Connected", str)
			},
		},
		{
			name: "GridStatusNumeric reports 1",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				num, err := pw.GridStatusNumeric(ctx)
				require.NoError(t, err)
				assert.Equal(t, 1, num)
			},
		},
		{
			name: "GridStatusResponse returns status struct",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				gs, err := pw.GridStatusResponse(ctx)
				require.NoError(t, err)
				assert.Equal(t, "SystemGridConnected", gs.GridStatus)
			},
		},
		{
			name: "Aggregates with SiteZeroThreshold option",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				agg, err := pw.Aggregates(ctx,
					powerwall.WithSiteZeroThreshold(100),
					powerwall.WithNegativeSolarCorrection(true),
				)
				require.NoError(t, err)
				assert.Zero(t, agg.Site.InstantPower)
				assert.InDelta(t, 1840.0, agg.Solar.InstantPower, 0.001)
			},
		},
		{
			name: "Snapshot returns composite view",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				snap := pw.Snapshot(ctx,
					powerwall.WithSiteZeroThreshold(50),
					powerwall.WithNegativeSolarCorrection(true),
				)
				assert.True(t, snap.GridConnected)
				assert.Positive(t, snap.BatteryLevel)
				assert.Positive(t, snap.FullPackEnergy)
			},
		},
		{
			name: "SiteName returns site name string",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				name, err := pw.SiteName(ctx)
				require.NoError(t, err)
				assert.NotEmpty(t, name)
			},
		},
		{
			name: "FrequencyView returns view struct",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				fv := pw.FrequencyView(ctx)
				assert.NotEmpty(t, fv.Inverters)
				assert.InDelta(t, 60.0, fv.Inverters[0].Fout, 0.001)
				assert.InDelta(t, 60.0, fv.SyncMeterFields["ISLAND_Frequency"], 0.001)
			},
		},
		{
			name: "Temps returns PowerwallTemps struct",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				temps := pw.Temps(ctx)
				require.NotEmpty(t, temps.Temps)
				assert.InDelta(t, 22.5, temps.Temps["TETHC--123"], 0.001)
			},
		},
		{
			name: "BatteryBlocks returns map of blocks",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				blocks := pw.BatteryBlocks(ctx)
				assert.NotEmpty(t, blocks)
			},
		},
		{
			name: "PollJSON queries endpoint and formats as json string",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				rawJSON := pw.PollJSON(ctx, "/api/status", powerwall.WithForce(true), powerwall.WithRaw(true))
				assert.NotEmpty(t, rawJSON)
				assert.Contains(t, rawJSON, "din")
			},
		},
		{
			name: "Mode flags report local mode and poller is available",
			run: func(t *testing.T, _ context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				assert.False(t, pw.IsCloud())
				assert.False(t, pw.IsFleetAPI())
				assert.NotNil(t, pw.Poller())
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t, t.Context(), pw)
		})
	}
}

func TestPowerwallRawAndErrorHandling(t *testing.T) {
	t.Parallel()

	pw := newConnectedFacadePowerwall(t)

	type testCase struct {
		run  func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall)
		name string
	}

	for _, tc := range []testCase{
		{
			name: "GetFileStoreConfig returns not found on mock gateway",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.GetFileStoreConfig(ctx)
				assert.Error(t, err)
			},
		},
		{
			name: "GetTEDAPIStatus in local mode returns error",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.GetTEDAPIStatus(ctx)
				assert.Error(t, err)
			},
		},
		{
			name: "GetTEDAPIComponents in local mode returns error",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.GetTEDAPIComponents(ctx)
				assert.Error(t, err)
			},
		},
		{
			name: "GetTEDAPIBattery in local mode returns error",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.GetTEDAPIBattery(ctx)
				assert.Error(t, err)
			},
		},
		{
			name: "GetTEDAPIDeviceController in local mode returns error",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.GetTEDAPIDeviceController(ctx)
				assert.Error(t, err)
			},
		},
		{
			name: "GetCloudBattery in local mode returns error",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.GetCloudBattery(ctx)
				assert.Error(t, err)
			},
		},
		{
			name: "GetCloudPower in local mode returns error",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.GetCloudPower(ctx)
				assert.Error(t, err)
			},
		},
		{
			name: "GetCloudConfig in local mode returns error",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.GetCloudConfig(ctx)
				assert.Error(t, err)
			},
		},
		{
			name: "GetFleetAPIInfo in local mode returns error",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.GetFleetAPIInfo(ctx)
				assert.Error(t, err)
			},
		},
		{
			name: "GetFleetAPIStatus in local mode returns error",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.GetFleetAPIStatus(ctx)
				assert.Error(t, err)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t, t.Context(), pw)
		})
	}
}

func TestPowerwallOperations(t *testing.T) {
	t.Parallel()

	pw := newConnectedFacadePowerwall(t)

	type testCase struct {
		run  func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall)
		name string
	}

	for _, tc := range []testCase{
		{
			name: "GetReserveScaled returns scaled reserve",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				val, err := pw.GetReserveScaled(ctx)
				require.NoError(t, err)
				assert.GreaterOrEqual(t, val, 0.0)
			},
		},
		{
			name: "GetReserveForced returns forced scaled reserve",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				val, err := pw.GetReserveForced(ctx)
				require.NoError(t, err)
				assert.GreaterOrEqual(t, val, 0.0)
			},
		},
		{
			name: "GetMode returns real mode string",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				mode, err := pw.GetMode(ctx)
				require.NoError(t, err)
				assert.NotEmpty(t, mode)
			},
		},
		{
			name: "SetGridCharging unsupported in local mode",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.SetGridCharging(ctx, true)
				require.ErrorIs(t, err, powerwall.ErrUnsupported)
			},
		},
		{
			name: "GetGridCharging unsupported in local mode",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.GetGridCharging(ctx)
				require.ErrorIs(t, err, powerwall.ErrUnsupported)
			},
		},
		{
			name: "SetGridExport unsupported in local mode",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.SetGridExport(ctx, "battery_ok")
				require.ErrorIs(t, err, powerwall.ErrUnsupported)
			},
		},
		{
			name: "GetGridExport unsupported in local mode",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.GetGridExport(ctx)
				require.ErrorIs(t, err, powerwall.ErrUnsupported)
			},
		},
		{
			name: "ScheduleMaxBackup unsupported in local mode",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.ScheduleMaxBackup(ctx, 3600)
				require.ErrorIs(t, err, powerwall.ErrUnsupported)
			},
		},
		{
			name: "CancelMaxBackup unsupported in local mode",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.CancelMaxBackup(ctx)
				require.ErrorIs(t, err, powerwall.ErrUnsupported)
			},
		},
		{
			name: "GetBackupEvents unsupported in local mode",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.GetBackupEvents(ctx)
				require.ErrorIs(t, err, powerwall.ErrUnsupported)
			},
		},
		{
			name: "GoOffGrid unconfirmed returns ErrOffGridConfirm",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.GoOffGrid(ctx, false)
				require.ErrorIs(t, err, powerwall.ErrOffGridConfirm)
			},
		},
		{
			name: "GoOffGrid confirmed unsupported in local mode",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.GoOffGrid(ctx, true)
				require.ErrorIs(t, err, powerwall.ErrUnsupported)
			},
		},
		{
			name: "ReconnectGrid unsupported in local mode",
			run: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.ReconnectGrid(ctx)
				require.ErrorIs(t, err, powerwall.ErrUnsupported)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t, t.Context(), pw)
		})
	}
}

func TestStatusEdgeCases(t *testing.T) {
	t.Parallel()

	type testCase struct {
		verify     func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall)
		name       string
		statusJSON string
	}

	for _, tc := range []testCase{
		{
			name:       "empty version returns ErrFieldMissing",
			statusJSON: `{"version":"","up_time_seconds":"10s","din":"test"}`,
			verify: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.Version(ctx)
				require.ErrorIs(t, err, powerwall.ErrFieldMissing)
				_, numErr := pw.VersionNumeric(ctx)
				require.ErrorIs(t, numErr, powerwall.ErrFieldMissing)
			},
		},
		{
			name:       "empty uptime returns ErrFieldMissing",
			statusJSON: `{"version":"1.0.0","up_time_seconds":"","din":"test"}`,
			verify: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.Uptime(ctx)
				require.ErrorIs(t, err, powerwall.ErrFieldMissing)
			},
		},
		{
			name:       "unparseable uptime returns duration parse error",
			statusJSON: `{"version":"1.0.0","up_time_seconds":"not-a-duration","din":"test"}`,
			verify: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.Uptime(ctx)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "parse uptime")
			},
		},
		{
			name:       "empty din returns ErrFieldMissing",
			statusJSON: `{"version":"1.0.0","up_time_seconds":"10s","din":""}`,
			verify: func(t *testing.T, ctx context.Context, pw *powerwall.Powerwall) {
				t.Helper()
				_, err := pw.Din(ctx)
				require.ErrorIs(t, err, powerwall.ErrFieldMissing)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/login/Basic":
					http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "c"})
					http.SetCookie(w, &http.Cookie{Name: "UserRecord", Value: "u"})
					w.WriteHeader(http.StatusOK)
				case "/api/status":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(tc.statusJSON))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)

			pw, err := powerwall.New(
				t.Context(),
				powerwall.WithHost(server.Listener.Addr().String()),
				powerwall.WithPassword("password"),
				powerwall.WithCloudMode(false),
				powerwall.WithCacheFile(filepath.Join(t.TempDir(), "cache")),
			)
			require.NoError(t, err)
			tc.verify(t, t.Context(), pw)
		})
	}
}
