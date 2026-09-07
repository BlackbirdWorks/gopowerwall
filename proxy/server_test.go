package proxy_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/blackbirdworks/gopowerwall"
	"github.com/blackbirdworks/gopowerwall/proxy"
)

func createTestServer(t *testing.T, controlSecret string) *httptest.Server {
	t.Helper()

	mockPWServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/login/Basic":
			http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "test-auth-cookie", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "UserRecord", Value: "test-user-record", Path: "/"})
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "mock-token"})
		case "/api/operation":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "ok"})
		case "/api/devices/vitals", "/vitals":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"devices": map[string]any{
					"TEPINV1": map[string]any{"PINV_Fout": 60.0},
				},
			})
		case "/api/powerwalls":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"powerwalls": []map[string]any{
					{"PackagePartNumber": "1234", "PackageSerialNumber": "TG123", "Temperature": 23.5},
				},
			})
		case "/api/meters/aggregates":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"site":    map[string]any{"instant_power": 120.0},
				"solar":   map[string]any{"instant_power": 5500.0},
				"battery": map[string]any{"instant_power": -1500.0},
				"load":    map[string]any{"instant_power": 3880.0},
			})
		case "/api/system_status/soe":
			_ = json.NewEncoder(w).Encode(map[string]any{"percentage": 94.5})
		case "/api/system_status/grid_status":
			_ = json.NewEncoder(w).Encode(map[string]any{"grid_status": "SystemGridConnected"})
		case "/api/system_status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"nominal_full_pack_energy": 27000,
				"nominal_energy_remaining": 25000,
				"battery_blocks": []map[string]any{
					{
						"PackagePartNumber":        "1234567-00-A",
						"PackageSerialNumber":      "TG123456",
						"f_out":                    60.0,
						"v_out":                    240.0,
						"p_out":                    1500.0,
						"q_out":                    0.0,
						"i_out":                    6.25,
						"nominal_energy_remaining": 12500,
						"nominal_full_pack_energy": 13500,
					},
				},
			})
		case "/api/site_info/site_name":
			_ = json.NewEncoder(w).Encode(map[string]any{"site_name": "Test Residence"})
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"din":      "12345-TEST",
				"version":  "24.36.2",
				"git_hash": "abcdef",
			})
		default:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		}
	}))
	t.Cleanup(mockPWServer.Close)

	pwHost := strings.TrimPrefix(mockPWServer.URL, "https://")
	pw, err := gopowerwall.New(
		t.Context(),
		gopowerwall.WithHost(pwHost),
		gopowerwall.WithPassword("testpw"),
		gopowerwall.WithCloudMode(false),
		gopowerwall.WithTimeout(1*time.Second),
	)
	if err != nil {
		t.Fatalf("Failed to initialize powerwall client: %v", err)
	}

	cfg := proxy.DefaultConfig()
	cfg.ControlSecret = controlSecret
	cfg.Host = pwHost
	cfg.CacheExpire = 1
	cfg.CacheTTL = 5

	srv := proxy.NewServer(t.Context(), cfg, pw)
	httpServer := httptest.NewServer(srv)
	t.Cleanup(httpServer.Close)

	return httpServer
}

func TestProxyGetRoutes(t *testing.T) {
	t.Parallel()

	ts := createTestServer(t, "secret123")
	client := ts.Client()

	tests := []struct {
		name       string
		path       string
		wantSub    string
		wantStatus int
	}{
		{name: "aggregates", path: "/aggregates", wantStatus: http.StatusOK, wantSub: "instant_power"},
		{name: "api aggregates", path: "/api/meters/aggregates", wantStatus: http.StatusOK, wantSub: "instant_power"},
		{name: "soe", path: "/soe", wantStatus: http.StatusOK, wantSub: "percentage"},
		{name: "api soe", path: "/api/system_status/soe", wantStatus: http.StatusOK, wantSub: "percentage"},
		{
			name:       "grid status",
			path:       "/api/system_status/grid_status",
			wantStatus: http.StatusOK,
			wantSub:    "grid_status",
		},
		{name: "csv v1", path: "/csv", wantStatus: http.StatusOK, wantSub: ","},
		{
			name:       "csv v1 headers",
			path:       "/csv?headers=true",
			wantStatus: http.StatusOK,
			wantSub:    "Grid,Home,Solar,Battery",
		},
		{name: "csv v2", path: "/csv/v2", wantStatus: http.StatusOK, wantSub: ","},
		{
			name:       "csv v2 headers",
			path:       "/csv/v2?headers=true",
			wantStatus: http.StatusOK,
			wantSub:    "GridStatus,Reserve",
		},
		{name: "json composite", path: "/json", wantStatus: http.StatusOK, wantSub: "grid"},
		{name: "freq", path: "/freq", wantStatus: http.StatusOK, wantSub: "PW1_"},
		{name: "pod", path: "/pod", wantStatus: http.StatusOK, wantSub: "nominal_full_pack_energy"},
		{name: "version", path: "/version", wantStatus: http.StatusOK, wantSub: "version"},
		{name: "help", path: "/help", wantStatus: http.StatusOK, wantSub: "Documentation & API Reference"},
		{name: "stats", path: "/stats", wantStatus: http.StatusOK, wantSub: "pypowerwall"},
		{name: "stats clear", path: "/stats/clear", wantStatus: http.StatusOK, wantSub: "pypowerwall"},
		{name: "health", path: "/health", wantStatus: http.StatusOK, wantSub: "proxy_stats"},
		{name: "health reset", path: "/health/reset", wantStatus: http.StatusOK, wantSub: "reset_complete"},
		{
			name:       "troubleshooting",
			path:       "/api/troubleshooting/problems",
			wantStatus: http.StatusOK,
			wantSub:    "problems",
		},
		{name: "pw level", path: "/pw/level", wantStatus: http.StatusOK, wantSub: "level"},
		{name: "pw power", path: "/pw/power", wantStatus: http.StatusOK, wantSub: "site"},
		{name: "pw site", path: "/pw/site", wantStatus: http.StatusOK, wantSub: "120"},
		{name: "pw solar", path: "/pw/solar", wantStatus: http.StatusOK, wantSub: "5500"},
		{name: "pw battery", path: "/pw/battery", wantStatus: http.StatusOK, wantSub: "-1500"},
		{name: "pw load", path: "/pw/load", wantStatus: http.StatusOK, wantSub: "3880"},
		{name: "pw grid", path: "/pw/grid", wantStatus: http.StatusOK, wantSub: "120"},
		{name: "pw home", path: "/pw/home", wantStatus: http.StatusOK, wantSub: "3880"},
		{name: "pw aggregates", path: "/pw/aggregates", wantStatus: http.StatusOK, wantSub: "instant_power"},
		{name: "pw uptime", path: "/pw/uptime", wantStatus: http.StatusOK, wantSub: "uptime"},
		{name: "pw version", path: "/pw/version", wantStatus: http.StatusOK, wantSub: "version"},
		{name: "pw site name", path: "/pw/site_name", wantStatus: http.StatusOK, wantSub: "site_name"},
		{name: "pw is connected", path: "/pw/is_connected", wantStatus: http.StatusOK, wantSub: "is_connected"},
		{
			name:       "disabled endpoint",
			path:       "/api/customer/registration",
			wantStatus: http.StatusNotFound,
			wantSub:    "Disabled",
		},
		{
			name:       "path traversal attack",
			path:       "/../../../etc/passwd",
			wantStatus: http.StatusBadRequest,
			wantSub:    "Invalid Path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, err := client.Get(ts.URL + tt.path)
			if err != nil {
				t.Fatalf("GET %s failed: %v", tt.path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("GET %s status = %d, want %d", tt.path, resp.StatusCode, tt.wantStatus)
			}
			body, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(body), tt.wantSub) {
				t.Errorf("GET %s body = %s, want substring %q", tt.path, string(body), tt.wantSub)
			}
		})
	}
}

func TestProxyControlRoutes(t *testing.T) {
	t.Parallel()

	secret := "mysecret999"
	ts := createTestServer(t, secret)
	client := ts.Client()

	tests := []struct {
		form       url.Values
		name       string
		path       string
		wantSub    string
		wantStatus int
	}{
		{
			name:       "invalid post path",
			path:       "/other/path",
			form:       url.Values{"token": {secret}},
			wantStatus: http.StatusBadRequest,
			wantSub:    "Invalid Request",
		},
		{
			name:       "unauthorized wrong token",
			path:       "/control/reserve",
			form:       url.Values{"token": {"wrongtoken"}, "value": {"20"}},
			wantStatus: http.StatusUnauthorized,
			wantSub:    "unauthorized",
		},
		{
			name:       "control get reserve value",
			path:       "/control/reserve",
			form:       url.Values{"token": {secret}},
			wantStatus: http.StatusOK,
			wantSub:    "reserve",
		},
		{
			name:       "control set reserve value",
			path:       "/control/reserve",
			form:       url.Values{"token": {secret}, "value": {"25"}},
			wantStatus: http.StatusOK,
			wantSub:    "",
		},
		{
			name:       "control set reserve invalid value",
			path:       "/control/reserve",
			form:       url.Values{"token": {secret}, "value": {"notanumber"}},
			wantStatus: http.StatusBadRequest,
			wantSub:    "error",
		},
		{
			name:       "control get mode",
			path:       "/control/mode",
			form:       url.Values{"token": {secret}},
			wantStatus: http.StatusOK,
			wantSub:    "mode",
		},
		{
			name:       "control set mode valid",
			path:       "/control/mode",
			form:       url.Values{"token": {secret}, "value": {"self_consumption"}},
			wantStatus: http.StatusOK,
			wantSub:    "",
		},
		{
			name:       "control set mode invalid",
			path:       "/control/mode",
			form:       url.Values{"token": {secret}, "value": {"invalid_mode"}},
			wantStatus: http.StatusBadRequest,
			wantSub:    "error",
		},
		{
			name:       "control get grid charging",
			path:       "/control/grid_charging",
			form:       url.Values{"token": {secret}},
			wantStatus: http.StatusOK,
			wantSub:    "grid_charging",
		},
		{
			name:       "control set grid charging unsupported in local",
			path:       "/control/grid_charging",
			form:       url.Values{"token": {secret}, "value": {"true"}},
			wantStatus: http.StatusBadRequest,
			wantSub:    "Failed to set grid_charging",
		},
		{
			name:       "control set grid charging invalid",
			path:       "/control/grid_charging",
			form:       url.Values{"token": {secret}, "value": {"maybe"}},
			wantStatus: http.StatusBadRequest,
			wantSub:    "error",
		},
		{
			name:       "control get grid export",
			path:       "/control/grid_export",
			form:       url.Values{"token": {secret}},
			wantStatus: http.StatusOK,
			wantSub:    "grid_export",
		},
		{
			name:       "control set grid export unsupported in local",
			path:       "/control/grid_export",
			form:       url.Values{"token": {secret}, "value": {"battery_ok"}},
			wantStatus: http.StatusBadRequest,
			wantSub:    "Failed to set grid_export",
		},
		{
			name:       "control set grid export invalid",
			path:       "/control/grid_export",
			form:       url.Values{"token": {secret}, "value": {"all_the_power"}},
			wantStatus: http.StatusBadRequest,
			wantSub:    "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, err := client.PostForm(ts.URL+tt.path, tt.form)
			if err != nil {
				t.Fatalf("POST %s failed: %v", tt.path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("POST %s status = %d, want %d", tt.path, resp.StatusCode, tt.wantStatus)
			}
			body, _ := io.ReadAll(resp.Body)
			if tt.wantSub != "" && !strings.Contains(string(body), tt.wantSub) {
				t.Errorf("POST %s body = %s, want substring %q", tt.path, string(body), tt.wantSub)
			}
		})
	}
}

func TestProxyServerLifecycle(t *testing.T) {
	t.Parallel()

	cfg := proxy.DefaultConfig()
	cfg.Port = 19876
	cfg.BindAddress = "127.0.0.1"

	srv := proxy.NewServer(t.Context(), cfg, nil)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		errCh <- srv.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Start() returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Server failed to shutdown within timeout")
	}
}
