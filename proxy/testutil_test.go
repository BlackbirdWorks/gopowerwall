package proxy_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/blackbirdworks/gopowerwall/powerwall"
	"github.com/blackbirdworks/gopowerwall/proto/teslapower"
	"github.com/blackbirdworks/gopowerwall/proxy"
)

// readFixture returns the raw contents of a recorded gateway response under
// proxy/web/bogus. Tests prefer these fixtures over hand-written JSON
// literals, per project convention.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("web", "bogus", name))
	require.NoError(t, err)

	return data
}

// vitalsProtoFixture builds a DevicesWithVitals protobuf payload exercising
// every device family the proxy inspects: TEPINV (frequency data), TESYNC
// (island/meter vitals), TETHC (temperature) and PVAC (solar strings).
func vitalsProtoFixture(t *testing.T) []byte {
	t.Helper()

	strVal := func(v string) *teslapower.StringValue { return &teslapower.StringValue{Value: v} }
	floatVital := func(name string, val float64) *teslapower.DeviceVital {
		return &teslapower.DeviceVital{Name: &name, Value: &teslapower.DeviceVital_FloatValue{FloatValue: val}}
	}
	stringVital := func(name, val string) *teslapower.DeviceVital {
		return &teslapower.DeviceVital{Name: &name, Value: &teslapower.DeviceVital_StringValue{StringValue: val}}
	}
	boolVital := func(name string, val bool) *teslapower.DeviceVital {
		return &teslapower.DeviceVital{Name: &name, Value: &teslapower.DeviceVital_BoolValue{BoolValue: val}}
	}
	device := func(
		din string,
		alerts []string,
		vitals ...*teslapower.DeviceVital,
	) *teslapower.SiteControllerConnectedDeviceWithVitals {
		return &teslapower.SiteControllerConnectedDeviceWithVitals{
			Device: &teslapower.SiteControllerConnectedDevice{
				Device: &teslapower.Device{Din: strVal(din)},
			},
			Vitals: vitals,
			Alerts: alerts,
		}
	}

	pb := &teslapower.DevicesWithVitals{
		Devices: []*teslapower.SiteControllerConnectedDeviceWithVitals{
			device("TEPINV--1", nil,
				floatVital("PINV_Fout", 60.05),
				floatVital("PINV_VSplit1", 120.1),
				floatVital("PINV_VSplit2", 120.2),
			),
			device("TESYNC--1", nil,
				floatVital("ISLAND_FreqL1", 60.01),
				floatVital("METER_X_CtA_InstRealPower", 500.0),
			),
			device("TETHC--1", nil,
				floatVital("THC_AmbientTemp", 28.5),
			),
			device("PVAC--1", []string{"StringFault"},
				floatVital("PVAC_PVMeasuredVoltage_A", 245.5),
				floatVital("PVAC_PVCurrent_A", 8.2),
				floatVital("PVAC_PVMeasuredPower_A", 2013.0),
				stringVital("PVAC_PvState_A", "PV_Active"),
			),
			device("PVS--1", nil,
				boolVital("PVS_StringA_Connected", true),
			),
		},
	}

	data, err := proto.Marshal(pb)
	require.NoError(t, err)

	return data
}

func jsonHandler(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

// newFakeGateway starts a TLS server mimicking a Powerwall Gateway's local
// HTTP API, backed by recorded fixtures under proxy/web/bogus. overrides
// replaces or adds handlers for specific paths.
func newFakeGateway(t *testing.T, overrides map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()

	fixtureRoutes := map[string]string{
		"/api/meters/aggregates":         "api.meters.aggregates.json",
		"/api/system_status":             "api.system_status.json",
		"/api/system_status/soe":         "api.system_status.soe.json",
		"/api/system_status/grid_status": "api.system_status.grid_status.json",
		"/api/status":                    "api.status.json",
		"/api/site_info/site_name":       "api.site_info.site_name.json",
		"/api/powerwalls":                "api.powerwalls.json",
		"/api/troubleshooting/problems":  "api.troubleshooting.problems.json",
	}

	handlers := make(map[string]http.HandlerFunc, len(fixtureRoutes)+4)
	for reqPath, fixtureName := range fixtureRoutes {
		handlers[reqPath] = jsonHandler(readFixture(t, fixtureName))
	}

	handlers["/api/login/Basic"] = func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "test-auth-cookie", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "UserRecord", Value: "test-user-record", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "mock-token"})
	}

	handlers["/api/devices/vitals"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(vitalsProtoFixture(t))
	}

	operationFixture := readFixture(t, "api.operation.json")
	handlers["/api/operation"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "ok"})

			return
		}
		_, _ = w.Write(operationFixture)
	}

	handlers["/"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}

	maps.Copy(handlers, overrides)

	mux := http.NewServeMux()
	for reqPath, handler := range handlers {
		mux.HandleFunc(reqPath, handler)
	}

	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

// newLocalPowerwall connects a Powerwall client in local mode to the given
// fake gateway server (as returned by newFakeGateway).
func newLocalPowerwall(t *testing.T, gw *httptest.Server) *powerwall.Powerwall {
	t.Helper()

	host := strings.TrimPrefix(gw.URL, "https://")
	pw, err := powerwall.New(
		t.Context(),
		powerwall.WithHost(host),
		powerwall.WithPassword("testpw"),
		powerwall.WithCloudMode(false),
		powerwall.WithTimeout(2*time.Second),
		// Isolate the auth-session cache file per test: the local backend
		// reads it before ever attempting a network login, so sharing the
		// default "./.powerwall" path across tests (or across runs, since
		// it persists on disk) lets a stale cached session from one test
		// silently satisfy Authenticate() in another.
		powerwall.WithCacheFile(filepath.Join(t.TempDir(), ".powerwall")),
	)
	require.NoError(t, err)

	return pw
}

// newDisconnectedPowerwall returns a Powerwall in local mode pointed at a
// host nothing listens on, so every backend call fails gracefully. Useful
// for exercising the proxy's degraded/no-data response paths.
func newDisconnectedPowerwall(t *testing.T) *powerwall.Powerwall {
	t.Helper()

	pw, err := powerwall.New(
		t.Context(),
		powerwall.WithHost("127.0.0.1:9"),
		powerwall.WithCloudMode(false),
		powerwall.WithTimeout(200*time.Millisecond),
		powerwall.WithCacheFile(filepath.Join(t.TempDir(), ".powerwall")),
	)
	// The connection is deliberately expected to fail here (that's the
	// point of this helper); New still returns a usable, disconnected
	// *Powerwall alongside the wrapped ConnectError.
	require.Error(t, err)
	require.NotNil(t, pw)

	return pw
}

// newProxyServer wraps a proxy.Server for a given Config and Powerwall in an
// httptest.Server, registering cleanup.
func newProxyServer(t *testing.T, cfg proxy.Config, pw *powerwall.Powerwall) *httptest.Server {
	t.Helper()

	srv := proxy.NewServer(t.Context(), cfg, pw)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

// baseTestConfig returns a Config suitable as a starting point for proxy
// tests: control enabled, short cache windows, negative solar preserved.
func baseTestConfig() proxy.Config {
	return proxy.Config{
		ControlSecret:       "test-secret",
		GracefulDegradation: true,
		HealthCheckEnabled:  true,
		CacheExpire:         1,
		CacheTTL:            5,
		NegSolar:            true,
		Style:               "clear.js",
		APIBaseURL:          "/",
	}
}
