package gopowerwall_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/blackbirdworks/gopowerwall"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/proxy"
)

// newExampleGateway starts a fake local gateway TLS server serving just
// enough of the local backend's HTTP API - cookie login, battery
// state-of-energy, and meter aggregates - for the examples below to run
// without a real Powerwall on the network. It mirrors the pattern the
// package's own tests use to build a fake gateway (see
// newLocalTestPowerwall in powerwall_test.go and newFakeGateway in
// proxy/testutil_test.go): an httptest.NewTLSServer, since the local
// backend always dials with InsecureSkipVerify (the gateway's own
// certificate is self-signed).
func newExampleGateway() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/login/Basic", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "cookie-value"})
		http.SetCookie(w, &http.Cookie{Name: "UserRecord", Value: "user-value"})
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/system_status/soe", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"percentage": 42}`)
	})
	mux.HandleFunc("/api/meters/aggregates", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"site":    {"instant_power": 400},
			"solar":   {"instant_power": 900},
			"battery": {"instant_power": -300},
			"load":    {"instant_power": 1000}
		}`)
	})

	return httptest.NewTLSServer(mux)
}

// newExampleCacheFile returns a throwaway path for [gopowerwall.WithCacheFile],
// so the examples below never write their session cache into the module's
// own source directory.
func newExampleCacheFile() string {
	dir, err := os.MkdirTemp("", "gopowerwall-example-cache")
	if err != nil {
		panic(err)
	}

	return filepath.Join(dir, "cache")
}

// connectExampleGateway starts a fake gateway and connects a [gopowerwall.Powerwall]
// to it in local mode, for examples that only need a connected instance
// without caring about the gateway server itself. Callers must defer both
// the returned server's Close and pw.Close.
func connectExampleGateway(ctx context.Context) (*httptest.Server, *gopowerwall.Powerwall) {
	srv := newExampleGateway()

	pw, err := gopowerwall.New(ctx,
		gopowerwall.WithHost(srv.Listener.Addr().String()),
		gopowerwall.WithPassword("password"),
		gopowerwall.WithCloudMode(false),
		gopowerwall.WithCacheFile(newExampleCacheFile()),
	)
	if err != nil {
		panic(err)
	}

	return srv, pw
}

// ExampleNew connects to a Powerwall gateway in local mode and checks that
// the connection actually succeeded. [gopowerwall.New] returns a non-nil
// *Powerwall even when the initial connection attempt fails (wrapping that
// failure as a [gopowerwall.ConnectError] instead), so
// [gopowerwall.Powerwall.IsConnected] remains the check that matters here.
func ExampleNew() {
	srv := newExampleGateway()
	defer srv.Close()

	ctx := context.Background()

	pw, err := gopowerwall.New(ctx,
		gopowerwall.WithHost(srv.Listener.Addr().String()),
		gopowerwall.WithPassword("password"), // last 5 characters of the gateway password
		gopowerwall.WithCloudMode(false),
		gopowerwall.WithCacheFile(newExampleCacheFile()),
	)
	if err != nil {
		panic(err)
	}
	defer pw.Close(ctx)

	fmt.Println("connected:", pw.IsConnected())
	fmt.Println("mode:", pw.Mode())
	// Output:
	// connected: true
	// mode: local
}

// ExamplePowerwall_Power reads instant power for the site, solar, battery,
// and load channels as a typed models.PowerSummary rather than an any or a
// map.
func ExamplePowerwall_Power() {
	ctx := context.Background()

	srv, pw := connectExampleGateway(ctx)
	defer srv.Close()
	defer pw.Close(ctx)

	power := pw.Power(ctx)
	fmt.Printf("site=%.0fW solar=%.0fW battery=%.0fW load=%.0fW\n",
		power.Site, power.Solar, power.Battery, power.Load)
	// Output:
	// site=400W solar=900W battery=-300W load=1000W
}

// ExamplePowerwall_Level reads the battery's state-of-charge percentage,
// both raw and rescaled, via the (T, error) idiom every accessor in this
// package now follows: a non-nil error means the value could not be
// retrieved, and it must be checked before the value is used.
func ExamplePowerwall_Level() {
	ctx := context.Background()

	srv, pw := connectExampleGateway(ctx)
	defer srv.Close()
	defer pw.Close(ctx)

	level, err := pw.Level(ctx)
	if err != nil {
		fmt.Println("battery level unavailable:", err)

		return
	}
	fmt.Printf("battery level: %.0f%%\n", level)

	scaled, err := pw.LevelScaled(ctx)
	if err != nil {
		fmt.Println("scaled battery level unavailable:", err)

		return
	}
	fmt.Printf("scaled battery level: %.2f%%\n", scaled)
	// Output:
	// battery level: 42%
	// scaled battery level: 38.95%
}

// Example_proxy shows the HTTP proxy consuming an already-connected
// [gopowerwall.Powerwall] instead of building its own - passing pw to
// [proxy.NewServer] instead of nil skips that constructor's implicit
// New/Connect call entirely. proxy.Server implements http.Handler, so it
// can be exercised directly with httptest, or handed to an *http.Server for
// a real listener.
func Example_proxy() {
	// The proxy logs a debug-level warning when its own fallback probe of the
	// underlying gateway misses a route this example's fake gateway does not
	// implement; a discard logger keeps that expected noise out of this
	// example's output.
	ctx := logger.Into(context.Background(), logger.New(io.Discard, slog.LevelError+1))

	srv, pw := connectExampleGateway(ctx)
	defer srv.Close()
	defer pw.Close(ctx)

	proxySrv := proxy.NewServer(ctx, proxy.Config{}, pw)

	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	proxySrv.ServeHTTP(rec, req)

	fmt.Println("status:", rec.Code)
	fmt.Println("mode:", pw.Mode())
	// Output:
	// status: 200
	// mode: local
}

// Example_scan discovers Powerwall gateways on a network range. A real scan
// targets a LAN CIDR such as "192.168.1.0/24"; this example instead scans
// only the loopback address with a short timeout so it runs quickly and
// without touching the network, and so finds no gateways.
func Example_scan() {
	ctx := context.Background()

	results, err := gopowerwall.Scan(ctx, gopowerwall.ScanOptions{
		CIDR:       "127.0.0.1/32",
		TimeoutSec: 0.05,
	}, io.Discard)
	if err != nil {
		panic(err)
	}

	fmt.Println("gateways found:", len(results))
	// Output:
	// gateways found: 0
}
