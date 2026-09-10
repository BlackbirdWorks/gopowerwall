package powerwall_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/blackbirdworks/gopowerwall/powerwall"
	"github.com/blackbirdworks/gopowerwall/powerwall/proto/teslapower"
)

// TestPowerwallDisconnectedDegradation verifies that every facade method on a
// Powerwall constructed against an unreachable host degrades gracefully:
// no panics, and nil or zero-value stub results instead of errors bubbling up.
func TestPowerwallDisconnectedDegradation(t *testing.T) {
	t.Parallel()

	pw, err := powerwall.New(
		t.Context(),
		powerwall.WithHost("127.0.0.1:9"), // non-routable port: nothing listens here
		powerwall.WithPassword("test"),
		powerwall.WithCloudMode(false),
	)
	var connectErr *powerwall.ConnectError
	require.ErrorAs(t, err, &connectErr)
	require.NotNil(t, pw)
	require.False(t, pw.IsConnected())

	type testCase struct {
		run  func(t *testing.T, pw *powerwall.Powerwall)
		name string
	}

	for _, tc := range []testCase{
		{
			name: "Poll returns nil",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.Poll(t.Context(), "/api/status"))
			},
		},
		{
			name: "Level returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.Level(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "Power returns zero summary",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				p := pw.Power(t.Context())
				assert.Zero(t, p.Site)
				assert.Zero(t, p.Battery)
			},
		},
		{
			name: "SiteReading returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.SiteReading(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "SolarReading returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.SolarReading(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "BatteryReading returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.BatteryReading(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "LoadReading returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.LoadReading(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "GridReading returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.GridReading(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "HomeReading returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.HomeReading(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "Vitals returns no devices",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				vit, vitErr := pw.Vitals(t.Context())
				if vitErr == nil {
					assert.Empty(t, vit.Devices)
				}
			},
		},
		{
			name: "Strings returns no strings",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				assert.Empty(t, pw.Strings(t.Context()).Strings)
			},
		},
		{
			name: "Din returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.Din(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "Uptime returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.Uptime(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "SiteName returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.SiteName(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "GetTimeRemaining returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.GetTimeRemaining(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "GetReserve returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.GetReserve(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "GetMode returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.GetMode(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "GetGridCharging returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.GetGridCharging(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "GetGridExport returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.GetGridExport(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "GetTEDAPIStatus returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.GetTEDAPIStatus(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "GetTEDAPIComponents returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.GetTEDAPIComponents(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "GetTEDAPIBattery returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.GetTEDAPIBattery(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "GetTEDAPIDeviceController returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.GetTEDAPIDeviceController(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "GetCloudBattery returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.GetCloudBattery(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "GetCloudPower returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.GetCloudPower(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "GetCloudConfig returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.GetCloudConfig(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "GetFleetAPIInfo returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.GetFleetAPIInfo(t.Context())
				assert.Error(t, callErr)
			},
		},
		{
			name: "GetFleetAPIStatus returns an error",
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()
				_, callErr := pw.GetFleetAPIStatus(t.Context())
				assert.Error(t, callErr)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t, pw)
		})
	}
}

const stringVitalFieldsPerLabel = 4

// solarStringTestLabels are the four labels stringLabelVitals/pvsLabelVitals
// populate; only A-D are populated deliberately, so tests can assert E/F are
// absent (see hasStringLabel in powerwall.go) rather than fabricated as
// all-zero entries.
var solarStringTestLabels = [...]string{"A", "B", "C", "D"} //nolint:gochecknoglobals // Read-only test fixture table.

// vitalFloat builds a single named float DeviceVital.
func vitalFloat(name string, value float64) *teslapower.DeviceVital {
	return &teslapower.DeviceVital{Name: new(name), Value: &teslapower.DeviceVital_FloatValue{FloatValue: value}}
}

// vitalString builds a single named string DeviceVital.
func vitalString(name, value string) *teslapower.DeviceVital {
	return &teslapower.DeviceVital{Name: new(name), Value: &teslapower.DeviceVital_StringValue{StringValue: value}}
}

// vitalBool builds a single named bool DeviceVital.
func vitalBool(name string, value bool) *teslapower.DeviceVital {
	return &teslapower.DeviceVital{Name: new(name), Value: &teslapower.DeviceVital_BoolValue{BoolValue: value}}
}

// stringLabelVitals builds the real gateway vitals field names Strings reads
// off a PVAC device - PVAC_PVMeasuredVoltage_<label>, PVAC_PVCurrent_<label>,
// PVAC_PVMeasuredPower_<label>, and PVAC_PvState_<label> - with distinct
// values per label so a bug that reads the wrong field, or the wrong label
// range, is caught. Only labels A-D are populated (E/F are deliberately
// absent, see [TestStringsKeysByDeviceToAvoidCollisions]). Label B's state
// is "PV_Standby" (not containing "Pv_Active") so the Connected-derivation
// test data covers both outcomes; C uses "PV_Active_Parallel" to exercise
// the substring match.
func stringLabelVitals(base float64) []*teslapower.DeviceVital {
	states := map[string]string{
		"A": "PV_Active",
		"B": "PV_Standby",
		"C": "PV_Active_Parallel",
		"D": "PV_Active",
	}
	vitals := make([]*teslapower.DeviceVital, 0, len(solarStringTestLabels)*stringVitalFieldsPerLabel)
	for i, label := range solarStringTestLabels {
		v := base + float64(i)*10
		vitals = append(vitals,
			vitalFloat("PVAC_PVMeasuredVoltage_"+label, v),
			vitalFloat("PVAC_PVCurrent_"+label, v+1),
			vitalFloat("PVAC_PVMeasuredPower_"+label, v+2),
			vitalString("PVAC_PvState_"+label, states[label]),
		)
	}

	return vitals
}

// pvsLabelVitals builds the sibling PVS device's PVS_String<label>_Connected
// fields Strings merges in by device-name suffix. Connected is false only
// for B, matching stringLabelVitals' non-"Pv_Active" state for B, and true
// for the wrongly-"Standby"-adjacent C to prove Connected is read verbatim
// from this field rather than re-derived from state.
func pvsLabelVitals() []*teslapower.DeviceVital {
	connected := map[string]bool{"A": true, "B": false, "C": true, "D": true}
	vitals := make([]*teslapower.DeviceVital, 0, len(solarStringTestLabels))
	for _, label := range solarStringTestLabels {
		vitals = append(vitals, vitalBool("PVS_String"+label+"_Connected", connected[label]))
	}

	return vitals
}

// buildMultiPVACVitalsProtobuf encodes two distinct PVAC devices (each with
// its own sibling PVS device), each reporting its own A-D string data, plus
// a device-level alert on the first one. It backs the regression tests for
// Strings' device-collision bug, its real-field-name/Connected-derivation
// bug, and Alerts' []string type-assertion bug.
func buildMultiPVACVitalsProtobuf(t *testing.T) []byte {
	t.Helper()

	pb := &teslapower.DevicesWithVitals{
		Devices: []*teslapower.SiteControllerConnectedDeviceWithVitals{
			{
				Device: &teslapower.SiteControllerConnectedDevice{
					Device: &teslapower.Device{Din: &teslapower.StringValue{Value: "PVAC--1"}},
				},
				Vitals: stringLabelVitals(100),
				Alerts: []string{"PVACAlertOne"},
			},
			{
				Device: &teslapower.SiteControllerConnectedDevice{
					Device: &teslapower.Device{Din: &teslapower.StringValue{Value: "PVS--1"}},
				},
				Vitals: pvsLabelVitals(),
			},
			{
				Device: &teslapower.SiteControllerConnectedDevice{
					Device: &teslapower.Device{Din: &teslapower.StringValue{Value: "PVAC--2"}},
				},
				Vitals: stringLabelVitals(1100),
			},
			{
				Device: &teslapower.SiteControllerConnectedDevice{
					Device: &teslapower.Device{Din: &teslapower.StringValue{Value: "PVS--2"}},
				},
				Vitals: pvsLabelVitals(),
			},
		},
	}

	data, err := proto.Marshal(pb)
	require.NoError(t, err)

	return data
}

// newLocalTestPowerwall connects a Powerwall in local mode against a fake
// gateway that serves cookie-based login and the given raw /api/devices/vitals
// protobuf payload.
func newLocalTestPowerwall(t *testing.T, vitalsBody []byte) *powerwall.Powerwall {
	t.Helper()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login/Basic":
			http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "cookie-value"})
			http.SetCookie(w, &http.Cookie{Name: "UserRecord", Value: "user-value"})
			w.WriteHeader(http.StatusOK)
		case "/api/devices/vitals":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(vitalsBody)
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
	require.True(t, pw.IsConnected())

	return pw
}

// TestStringsKeysByDeviceToAvoidCollisions is the regression test for three
// Strings bugs, all failing before their respective fixes:
//
//  1. Ranging over the label slice used the loop index (0-3) as both the
//     lookup suffix and the map key, so real gateway fields
//     (PVAC_VsolarA..PVAC_VsolarD) were never found, and a second PVAC
//     device silently overwrote the first at the same "0".."3" keys. With
//     the fix, distinct PVAC devices each keep their own A-D entries.
//  2. Strings read invented field names (PVAC_Vsolar<label> etc.) that no
//     gateway - local firmware or TEDAPI - ever produces, so every reading
//     came back zero. With the fix, it reads the real field names
//     (PVAC_PVMeasuredVoltage_<label>, PVAC_PVCurrent_<label>,
//     PVAC_PVMeasuredPower_<label>, PVAC_PvState_<label>).
//  3. Connected was hardcoded true unconditionally. With the fix, it is
//     read from the sibling PVS device's PVS_String<label>_Connected field
//     - verbatim, not re-derived from state (see stringLabelVitals' B/C
//     data, where state and Connected deliberately disagree with a naive
//     "Pv_Active substring" re-derivation).
func TestStringsKeysByDeviceToAvoidCollisions(t *testing.T) {
	t.Parallel()

	pw := newLocalTestPowerwall(t, buildMultiPVACVitalsProtobuf(t))
	result := pw.Strings(t.Context())

	type testCase struct {
		name          string
		key           string
		wantState     string
		wantVolts     float64
		wantCurrent   float64
		wantPower     float64
		wantConnected bool
	}

	for _, tc := range []testCase{
		{
			name: "device one string A", key: "PVAC--1_A",
			wantVolts: 100, wantCurrent: 101, wantPower: 102, wantState: "PV_Active", wantConnected: true,
		},
		{
			name: "device one string B is not connected", key: "PVAC--1_B",
			wantVolts: 110, wantCurrent: 111, wantPower: 112, wantState: "PV_Standby", wantConnected: false,
		},
		{
			name: "device one string C reads Connected verbatim, not re-derived from state", key: "PVAC--1_C",
			wantVolts: 120, wantCurrent: 121, wantPower: 122, wantState: "PV_Active_Parallel", wantConnected: true,
		},
		{
			name: "device one string D", key: "PVAC--1_D",
			wantVolts: 130, wantCurrent: 131, wantPower: 132, wantState: "PV_Active", wantConnected: true,
		},
		{
			name: "device two string A does not collide with device one", key: "PVAC--2_A",
			wantVolts: 1100, wantCurrent: 1101, wantPower: 1102, wantState: "PV_Active", wantConnected: true,
		},
		{
			name: "device two string D", key: "PVAC--2_D",
			wantVolts: 1130, wantCurrent: 1131, wantPower: 1132, wantState: "PV_Active", wantConnected: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			metric, ok := result.Strings[tc.key]
			require.True(t, ok, "expected key %q in Strings map, got %v", tc.key, result.Strings)
			assert.InDelta(t, tc.wantVolts, metric.Voltage, 0.001)
			assert.InDelta(t, tc.wantCurrent, metric.Current, 0.001)
			assert.InDelta(t, tc.wantPower, metric.Power, 0.001)
			assert.Equal(t, tc.wantState, metric.State)
			assert.Equal(t, tc.wantConnected, metric.Connected)
		})
	}

	assert.Len(t, result.Strings, 8, "expected four labels for each of the two PVAC devices with no collisions")

	assert.NotContains(t, result.Strings, "PVAC--1_E", "label E is absent from vitals and must not be fabricated")
	assert.NotContains(t, result.Strings, "PVAC--1_F", "label F is absent from vitals and must not be fabricated")
}

// TestAlertsIncludesDeviceStringSliceAlerts is the regression test for the
// Alerts bug: the local backend stores device alerts as []string (the
// protobuf accessor's native type), but Alerts only type-asserted []any, so
// every device-level alert was silently dropped.
func TestAlertsIncludesDeviceStringSliceAlerts(t *testing.T) {
	t.Parallel()

	pw := newLocalTestPowerwall(t, buildMultiPVACVitalsProtobuf(t))
	alerts := pw.Alerts(t.Context()).Alerts

	type testCase struct {
		name string
		want string
	}

	for _, tc := range []testCase{
		{name: "device []string alert surfaces in the alert list", want: "PVACAlertOne"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Contains(t, alerts, tc.want)
		})
	}
}

// writeTestRSAKey generates a throwaway RSA private key and PEM-encodes it to
// a file under t.TempDir(), for exercising connectLocal's v1r branch without
// a reachable gateway - tedapi.NewTEDAPIv1r only ever parses this file
// locally, it never dials the network.
func writeTestRSAKey(t *testing.T) string {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	path := filepath.Join(t.TempDir(), "tedapi.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))

	return path
}

// TestPureTEDAPIAndV1rModesReportTheirOwnConnectionMode is the regression
// test for a dispatch bug in connectLocal: constructing a Powerwall for pure
// TEDAPI (gateway password, no customer password) or pure v1r (RSA key, no
// customer password) built a fully working tedapi backend and set
// tedapiMode/tedapiFlag, but never assigned p.mode away from the ModeLocal
// value New() sets by default. Every mode-dispatching read method (Poll,
// Power, Vitals, GetTimeRemaining, ...) switches on p.mode, so with p.mode
// stuck at ModeLocal and p.local nil (no local HTTP backend exists on these
// paths), every read silently fell through to a nil/zero-value result while
// IsConnected/IsTEDAPI still reported success.
//
// This does not need a reachable gateway: tedapi.NewClient and
// tedapi.NewTEDAPIv1r only construct in-memory client state, so connectLocal
// succeeds (and, with the fix, sets p.mode) purely from local configuration.
func TestPureTEDAPIAndV1rModesReportTheirOwnConnectionMode(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		wantMode   powerwall.ConnectionMode
		useV1rKey  bool
		wantV1r    bool
		wantTEDAPI bool
	}

	for _, tc := range []testCase{
		{
			name:       "pure TEDAPI (gateway password, no customer password)",
			wantMode:   powerwall.ModeTEDAPI,
			wantTEDAPI: true,
		},
		{
			name:       "pure v1r (RSA key, no customer password)",
			useV1rKey:  true,
			wantMode:   powerwall.ModeV1r,
			wantV1r:    true,
			wantTEDAPI: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := []powerwall.Option{
				powerwall.WithHost("127.0.0.1:9"), // non-routable: reads are expected to fail over the wire
				powerwall.WithCloudMode(false),
				powerwall.WithCacheFile(filepath.Join(t.TempDir(), ".powerwall")),
				powerwall.WithGwPwd("gatewaypassword"),
			}
			if tc.useV1rKey {
				opts = append(opts, powerwall.WithRSAKeyPath(writeTestRSAKey(t)))
			}

			pw, err := powerwall.New(t.Context(), opts...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = pw.Close(t.Context()) })

			require.True(t, pw.IsConnected(), "connectLocal's pure TEDAPI/v1r branch should report success")
			assert.Equal(t, tc.wantMode, pw.Mode())
			assert.True(t, pw.IsTEDAPI())
			assert.False(t, pw.IsLocal(), "pure TEDAPI/v1r has no local HTTP backend, so IsLocal must be false")

			if tc.wantV1r {
				assert.Equal(t, powerwall.TEDAPIV1r, pw.TEDAPIMode())
			} else {
				assert.Equal(t, powerwall.TEDAPIFull, pw.TEDAPIMode())
			}
		})
	}
}

// TestSOEAndGridStatusResponseSurviveEarlierParsedPoll is the regression
// test for the local-mode response cache conflating raw and parsed reads of
// the same endpoint under one cache key. SOE and GridStatusResponse both
// fetch their endpoint via PollRaw, which requires a []byte back from the
// cache; before the fix, an earlier *parsed* Poll of the same endpoint left
// a map[string]any cached under the bare endpoint key, so PollRaw's
// val.([]byte) type assertion failed, PollRaw returned nil, and SOE /
// GridStatusResponse both reported ErrNotFound even though the gateway
// served the data correctly. That is exactly the failure the CI integration
// suite caught against the real pwsimulator emulator.
func TestSOEAndGridStatusResponseSurviveEarlierParsedPoll(t *testing.T) {
	t.Parallel()

	type testCase struct {
		run  func(t *testing.T, pw *powerwall.Powerwall)
		name string
		api  string
		body string
	}

	for _, tc := range []testCase{
		{
			name: "SOE succeeds after an earlier parsed Poll of the same endpoint",
			api:  "/api/system_status/soe",
			body: `{"percentage": 20.109166592431226}`,
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()

				soe, err := pw.SOE(t.Context())
				require.NoError(t, err)
				assert.InDelta(t, 20.109166592431226, soe.Percentage, 0.0000001)
			},
		},
		{
			name: "GridStatusResponse succeeds after an earlier parsed Poll of the same endpoint",
			api:  "/api/system_status/grid_status",
			body: `{"grid_status":"SystemGridConnected","grid_services_active":false}`,
			run: func(t *testing.T, pw *powerwall.Powerwall) {
				t.Helper()

				status, err := pw.GridStatusResponse(t.Context())
				require.NoError(t, err)
				assert.Equal(t, "SystemGridConnected", status.GridStatus)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/login/Basic":
					http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "cookie-value"})
					http.SetCookie(w, &http.Cookie{Name: "UserRecord", Value: "user-value"})
					w.WriteHeader(http.StatusOK)
				case tc.api:
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(tc.body))
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
			require.True(t, pw.IsConnected())

			// Poll the endpoint parsed first. This is what left a
			// map[string]any cached under the bare endpoint key before the
			// fix, so the raw poll SOE/GridStatusResponse rely on could no
			// longer find its []byte value there.
			parsed := pw.Poll(t.Context(), tc.api)
			require.NotNil(t, parsed)
			_, ok := parsed.(map[string]any)
			require.True(t, ok)

			tc.run(t, pw)
		})
	}
}

// TestPODViewIncludesTEPODVitalsAugmentation is the regression test for the
// /pod route's missing vitals-augmentation pass: before this change,
// PODView never called Vitals at all (confirmed by grep -
// docs/parity-matrix.md's /pod row), so a TEPOD device's data - including
// nom_energy_to_be_charged, a field gopowerwall never emitted under any
// connection mode - never reached the view. This mirrors pypowerwall's own
// second, independent /pod loop (server.py:2196-2244, "Augment with Vitals
// Data").
func TestPODViewIncludesTEPODVitalsAugmentation(t *testing.T) {
	t.Parallel()

	pb := &teslapower.DevicesWithVitals{
		Devices: []*teslapower.SiteControllerConnectedDeviceWithVitals{
			{
				Device: &teslapower.SiteControllerConnectedDevice{
					Device: &teslapower.Device{Din: &teslapower.StringValue{Value: "TEPOD--1234--5678"}},
				},
				Vitals: []*teslapower.DeviceVital{
					vitalBool("POD_ActiveHeating", true),
					vitalBool("POD_ChargeComplete", false),
					vitalBool("POD_ChargeRequest", true),
					vitalBool("POD_DischargeComplete", false),
					vitalBool("POD_PermanentlyFaulted", false),
					vitalBool("POD_PersistentlyFaulted", false),
					vitalBool("POD_enable_line", true),
					vitalFloat("POD_available_charge_power", 3300.0),
					vitalFloat("POD_available_dischg_power", 3200.0),
					vitalFloat("POD_nom_energy_remaining", 9000.0),
					vitalFloat("POD_nom_energy_to_be_charged", 4500.0),
					vitalFloat("POD_nom_full_pack_energy", 13500.0),
				},
			},
		},
	}
	data, err := proto.Marshal(pb)
	require.NoError(t, err)

	pw := newLocalTestPowerwall(t, data)
	view := pw.PODView(t.Context())

	require.Len(t, view.TEPODEntries, 1)
	entry := view.TEPODEntries[0]
	assert.Equal(t, "TEPOD--1234--5678", entry.Device)
	assert.Equal(t, 1, entry.ActiveHeating)
	assert.Equal(t, 0, entry.ChargeComplete)
	assert.Equal(t, 1, entry.ChargeRequest)
	assert.Equal(t, 0, entry.DischargeComplete)
	assert.Equal(t, 0, entry.PermanentlyFaulted)
	assert.Equal(t, 0, entry.PersistentlyFaulted)
	assert.Equal(t, 1, entry.EnableLine)
	require.NotNil(t, entry.AvailableChargePower)
	assert.InDelta(t, 3300.0, *entry.AvailableChargePower, 0.001)
	require.NotNil(t, entry.AvailableDischargePower)
	assert.InDelta(t, 3200.0, *entry.AvailableDischargePower, 0.001)
	require.NotNil(t, entry.NomEnergyRemaining)
	assert.InDelta(t, 9000.0, *entry.NomEnergyRemaining, 0.001)
	require.NotNil(t, entry.NomEnergyToBeCharged)
	assert.InDelta(t, 4500.0, *entry.NomEnergyToBeCharged, 0.001)
	require.NotNil(t, entry.NomFullPackEnergy)
	assert.InDelta(t, 13500.0, *entry.NomFullPackEnergy, 0.001)
}

// TestGetFanSpeedsEmptyOutsideTEDAPIMode is the regression test proving
// GetFanSpeeds - previously nonexistent, since no /fans route existed at
// all (docs/parity-matrix.md's /fans row) - mirrors pypowerwall's own
// `pw.tedapi` falsy gate (server.py:2483-2500): a local-mode connection
// with no TEDAPI client attached returns an empty, non-nil map rather than
// erroring or panicking.
func TestGetFanSpeedsEmptyOutsideTEDAPIMode(t *testing.T) {
	t.Parallel()

	pw := newLocalTestPowerwall(t, buildMultiPVACVitalsProtobuf(t))

	speeds := pw.GetFanSpeeds(t.Context())
	assert.NotNil(t, speeds)
	assert.Empty(t, speeds)
}
