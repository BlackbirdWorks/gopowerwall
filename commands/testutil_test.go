package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/commands"
)

// fixtureDir returns the absolute path to proxy/web/bogus, resolved from
// this file's own location so it stays correct even after a test calls
// t.Chdir (several commands tests do, to isolate the local backend's
// session-cache file).
func fixtureDir() string {
	_, thisFile, _, _ := runtime.Caller(0)

	return filepath.Join(filepath.Dir(thisFile), "..", "proxy", "web", "bogus")
}

// readFixture returns the raw contents of a recorded gateway response under
// proxy/web/bogus. Tests prefer these fixtures over hand-written JSON
// literals, per project convention.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(fixtureDir(), name))
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
// HTTP API, backed by recorded fixtures under proxy/web/bogus, so GetCmd and
// SetCmd can be driven against a connected Powerwall without a real device.
func newFakeGateway(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/login/Basic", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "test-auth-cookie", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "UserRecord", Value: "test-user-record", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "mock-token"})
	})
	mux.Handle("/api/site_info/site_name", jsonHandler(readFixture(t, "api.site_info.site_name.json")))
	mux.Handle("/api/status", jsonHandler(readFixture(t, "api.status.json")))
	mux.Handle("/api/system_status/soe", jsonHandler(readFixture(t, "api.system_status.soe.json")))
	mux.Handle("/api/system_status/grid_status", jsonHandler(readFixture(t, "api.system_status.grid_status.json")))
	mux.Handle("/api/meters/aggregates", jsonHandler(readFixture(t, "api.meters.aggregates.json")))
	mux.HandleFunc("/api/operation", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "ok"})

			return
		}
		_, _ = w.Write(readFixture(t, "api.operation.json"))
	})

	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

// connectionFlags returns ConnectionFlags wired to connect in local mode
// against the given fake gateway. AuthPath is set to a fresh t.TempDir() so
// BuildPowerwall relocates its session-cache file there (see
// ConnectionFlags.BuildPowerwall): without it, local mode falls back to
// "./.powerwall" relative to the process's working directory, which every
// caller of this helper would then share and race over. Isolating it this
// way, instead of the previous t.Chdir(t.TempDir()) approach, keeps callers
// free to run in parallel - the testing package forbids combining t.Chdir
// with t.Parallel since both act on process-wide state.
func connectionFlags(t *testing.T, gw *httptest.Server) commands.ConnectionFlags {
	t.Helper()

	return commands.ConnectionFlags{
		Local:    true,
		Host:     strings.TrimPrefix(gw.URL, "https://"),
		Password: "testpw",
		AuthPath: t.TempDir(),
	}
}
