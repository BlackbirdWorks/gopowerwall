package gopowerwall_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/blackbirdworks/gopowerwall"
	"github.com/blackbirdworks/gopowerwall/proto/teslapower"
)

// TestPowerwallDisconnectedDegradation verifies that every facade method on a
// Powerwall constructed against an unreachable host degrades gracefully:
// no panics, and nil or zero-value stub results instead of errors bubbling up.
func TestPowerwallDisconnectedDegradation(t *testing.T) {
	t.Parallel()

	pw, err := gopowerwall.New(
		t.Context(),
		gopowerwall.WithHost("127.0.0.1:9"), // non-routable port: nothing listens here
		gopowerwall.WithPassword("test"),
		gopowerwall.WithCloudMode(false),
	)
	require.NoError(t, err)
	require.False(t, pw.IsConnected())

	type testCase struct {
		run  func(t *testing.T, pw *gopowerwall.Powerwall)
		name string
	}

	for _, tc := range []testCase{
		{
			name: "Poll returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.Poll(t.Context(), "/api/status"))
			},
		},
		{
			name: "Level returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.Level(t.Context()))
			},
		},
		{
			name: "Power returns zero summary",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				p := pw.Power(t.Context())
				assert.Zero(t, p.Site)
				assert.Zero(t, p.Battery)
			},
		},
		{
			name: "Site verbose returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.Site(t.Context(), true))
			},
		},
		{
			name: "Solar verbose returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.Solar(t.Context(), true))
			},
		},
		{
			name: "Battery verbose returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.Battery(t.Context(), true))
			},
		},
		{
			name: "Load verbose returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.Load(t.Context(), true))
			},
		},
		{
			name: "Grid verbose returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.Grid(t.Context(), true))
			},
		},
		{
			name: "Home verbose returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.Home(t.Context(), true))
			},
		},
		{
			name: "Vitals returns no devices",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				vit, vitErr := pw.Vitals(t.Context())
				if vitErr == nil {
					assert.Empty(t, vit.Devices)
				}
			},
		},
		{
			name: "Strings returns no strings",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Empty(t, pw.Strings(t.Context()).Strings)
			},
		},
		{
			name: "Din returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.Din(t.Context()))
			},
		},
		{
			name: "Uptime returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.Uptime(t.Context()))
			},
		},
		{
			name: "SiteName returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.SiteName(t.Context()))
			},
		},
		{
			name: "GetTimeRemaining returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.GetTimeRemaining(t.Context()))
			},
		},
		{
			name: "GetReserve returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.GetReserve(t.Context()))
			},
		},
		{
			name: "GetMode returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.GetMode(t.Context()))
			},
		},
		{
			name: "GetGridCharging returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.GetGridCharging(t.Context()))
			},
		},
		{
			name: "GetGridExport returns nil",
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
				t.Helper()
				assert.Nil(t, pw.GetGridExport(t.Context()))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t, pw)
		})
	}
}

const stringVitalFieldsPerLabel = 3

// vitalFloat builds a single named float DeviceVital.
func vitalFloat(name string, value float64) *teslapower.DeviceVital {
	return &teslapower.DeviceVital{Name: new(name), Value: &teslapower.DeviceVital_FloatValue{FloatValue: value}}
}

// stringLabelVitals builds the PVAC_Vsolar/Isolar/Psolar<label> vitals fields
// that Strings reads, with distinct values per label so a bug that reads the
// wrong field (e.g. "PVAC_Vsolar0" instead of "PVAC_VsolarA") is caught.
func stringLabelVitals(base float64) []*teslapower.DeviceVital {
	labels := [...]string{"A", "B", "C", "D"}
	vitals := make([]*teslapower.DeviceVital, 0, len(labels)*stringVitalFieldsPerLabel)
	for i, label := range labels {
		v := base + float64(i)*10
		vitals = append(vitals,
			vitalFloat("PVAC_Vsolar"+label, v),
			vitalFloat("PVAC_Isolar"+label, v+1),
			vitalFloat("PVAC_Psolar"+label, v+2),
		)
	}

	return vitals
}

// buildMultiPVACVitalsProtobuf encodes two distinct PVAC devices, each
// reporting its own A-D string data, plus a device-level alert on the first
// one. It backs the regression tests for Strings' device-collision bug and
// Alerts' []string type-assertion bug.
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
					Device: &teslapower.Device{Din: &teslapower.StringValue{Value: "PVAC--2"}},
				},
				Vitals: stringLabelVitals(1100),
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
func newLocalTestPowerwall(t *testing.T, vitalsBody []byte) *gopowerwall.Powerwall {
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

	pw, err := gopowerwall.New(
		t.Context(),
		gopowerwall.WithHost(server.Listener.Addr().String()),
		gopowerwall.WithPassword("password"),
		gopowerwall.WithCloudMode(false),
		gopowerwall.WithCacheFile(filepath.Join(t.TempDir(), "cache")),
	)
	require.NoError(t, err)
	require.True(t, pw.IsConnected())

	return pw
}

// TestStringsKeysByDeviceToAvoidCollisions is the regression test for the
// Strings bug: ranging over the label slice used the loop index (0-3) as both
// the lookup suffix and the map key, so real gateway fields
// (PVAC_VsolarA..PVAC_VsolarD) were never found, and a second PVAC device
// silently overwrote the first at the same "0".."3" keys. With the fix,
// distinct PVAC devices each keep their own A-D entries.
func TestStringsKeysByDeviceToAvoidCollisions(t *testing.T) {
	t.Parallel()

	pw := newLocalTestPowerwall(t, buildMultiPVACVitalsProtobuf(t))
	result := pw.Strings(t.Context())

	type testCase struct {
		name      string
		key       string
		wantVolts float64
	}

	for _, tc := range []testCase{
		{name: "device one string A", key: "PVAC--1_A", wantVolts: 100},
		{name: "device one string D", key: "PVAC--1_D", wantVolts: 130},
		{name: "device two string A does not collide with device one", key: "PVAC--2_A", wantVolts: 1100},
		{name: "device two string D", key: "PVAC--2_D", wantVolts: 1130},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			metric, ok := result.Strings[tc.key]
			require.True(t, ok, "expected key %q in Strings map, got %v", tc.key, result.Strings)
			assert.InDelta(t, tc.wantVolts, metric.Voltage, 0.001)
			assert.True(t, metric.Connected)
		})
	}

	assert.Len(t, result.Strings, 8, "expected four labels for each of the two PVAC devices with no collisions")
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
