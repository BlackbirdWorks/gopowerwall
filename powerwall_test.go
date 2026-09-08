package gopowerwall_test

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
		wantMode   gopowerwall.ConnectionMode
		useV1rKey  bool
		wantV1r    bool
		wantTEDAPI bool
	}

	for _, tc := range []testCase{
		{
			name:       "pure TEDAPI (gateway password, no customer password)",
			wantMode:   gopowerwall.ModeTEDAPI,
			wantTEDAPI: true,
		},
		{
			name:       "pure v1r (RSA key, no customer password)",
			useV1rKey:  true,
			wantMode:   gopowerwall.ModeV1r,
			wantV1r:    true,
			wantTEDAPI: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := []gopowerwall.Option{
				gopowerwall.WithHost("127.0.0.1:9"), // non-routable: reads are expected to fail over the wire
				gopowerwall.WithCloudMode(false),
				gopowerwall.WithCacheFile(filepath.Join(t.TempDir(), ".powerwall")),
				gopowerwall.WithGwPwd("gatewaypassword"),
			}
			if tc.useV1rKey {
				opts = append(opts, gopowerwall.WithRSAKeyPath(writeTestRSAKey(t)))
			}

			pw, err := gopowerwall.New(t.Context(), opts...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = pw.Close(t.Context()) })

			require.True(t, pw.IsConnected(), "connectLocal's pure TEDAPI/v1r branch should report success")
			assert.Equal(t, tc.wantMode, pw.Mode())
			assert.True(t, pw.IsTEDAPI())
			assert.False(t, pw.IsLocal(), "pure TEDAPI/v1r has no local HTTP backend, so IsLocal must be false")

			if tc.wantV1r {
				assert.Equal(t, gopowerwall.TEDAPIV1r, pw.TEDAPIMode())
			} else {
				assert.Equal(t, gopowerwall.TEDAPIFull, pw.TEDAPIMode())
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
		run  func(t *testing.T, pw *gopowerwall.Powerwall)
		name string
		api  string
		body string
	}

	for _, tc := range []testCase{
		{
			name: "SOE succeeds after an earlier parsed Poll of the same endpoint",
			api:  "/api/system_status/soe",
			body: `{"percentage": 20.109166592431226}`,
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
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
			run: func(t *testing.T, pw *gopowerwall.Powerwall) {
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

			pw, err := gopowerwall.New(
				t.Context(),
				gopowerwall.WithHost(server.Listener.Addr().String()),
				gopowerwall.WithPassword("password"),
				gopowerwall.WithCloudMode(false),
				gopowerwall.WithCacheFile(filepath.Join(t.TempDir(), "cache")),
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
