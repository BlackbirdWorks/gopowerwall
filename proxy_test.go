package gopowerwall_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/blackbirdworks/gopowerwall"
	"github.com/blackbirdworks/gopowerwall/proxy"
)

func TestProxyServerRoutes(t *testing.T) {
	// Create disconnected powerwall for testing stubs & graceful degradation
	pw, _ := gopowerwall.New(
		t.Context(),
		gopowerwall.WithHost("127.0.0.1:9"),
		gopowerwall.WithCloudMode(false),
	)

	cfg := proxy.Config{
		ControlSecret:       "test-secret",
		GracefulDegradation: true,
		HealthCheckEnabled:  true,
		CacheExpire:         5,
		CacheTTL:            30,
		NegSolar:            true,
		Style:               "clear.js",
	}
	server := proxy.NewServer(t.Context(), cfg, pw)
	ts := httptest.NewServer(server)
	defer ts.Close()

	client := ts.Client()

	// 1. Test /stats
	resp, err := client.Get(ts.URL + "/stats")
	if err != nil {
		t.Fatalf("GET /stats error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /stats status = %d, want 200", resp.StatusCode)
	}
	var stats map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Errorf("Failed to decode /stats: %v", err)
	}
	if _, ok := stats["uptime"]; !ok {
		t.Errorf("Missing uptime in /stats")
	}

	// 2. Test /health and /health/reset
	respH, err := client.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health error: %v", err)
	}
	defer respH.Body.Close()
	if respH.StatusCode != http.StatusOK {
		t.Errorf("GET /health status = %d, want 200", respH.StatusCode)
	}

	respReset, err := client.Get(ts.URL + "/health/reset")
	if err != nil {
		t.Fatalf("GET /health/reset error: %v", err)
	}
	defer respReset.Body.Close()
	if respReset.StatusCode != http.StatusOK {
		t.Errorf("GET /health/reset status = %d, want 200", respReset.StatusCode)
	}

	// 3. Test /version
	respV, err := client.Get(ts.URL + "/version")
	if err != nil {
		t.Fatalf("GET /version error: %v", err)
	}
	defer respV.Body.Close()
	var ver map[string]any
	_ = json.NewDecoder(respV.Body).Decode(&ver)
	if ver["version"] == nil {
		t.Errorf("Expected version in response")
	}

	// 4. Test /api/troubleshooting/problems
	respProb, err := client.Get(ts.URL + "/api/troubleshooting/problems")
	if err != nil {
		t.Fatalf("GET problems error: %v", err)
	}
	defer respProb.Body.Close()
	bodyProb, _ := io.ReadAll(respProb.Body)
	if !strings.Contains(string(bodyProb), `"problems"`) {
		t.Errorf("Unexpected problems body: %s", string(bodyProb))
	}

	// 5. Test Static HTML & InjectJS
	respRoot, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET / error: %v", err)
	}
	defer respRoot.Body.Close()
	bodyRoot, _ := io.ReadAll(respRoot.Body)
	if !strings.Contains(string(bodyRoot), `<script type="text/javascript" src="`) {
		t.Errorf("Expected injected script in root index.html: %s", string(bodyRoot))
	}
}

func TestProxyControlSecurity(t *testing.T) {
	pw, _ := gopowerwall.New(
		t.Context(),
		gopowerwall.WithHost("127.0.0.1:9"),
		gopowerwall.WithCloudMode(false),
	)
	cfg := proxy.Config{
		ControlSecret: "correct-token-123",
	}
	server := proxy.NewServer(t.Context(), cfg, pw)
	ts := httptest.NewServer(server)
	defer ts.Close()

	client := ts.Client()

	// 1. Post without token -> 401 Unauthorized
	dataBad := url.Values{"value": {"20"}, "token": {"wrong"}}
	respBad, err := client.PostForm(ts.URL+"/control/reserve", dataBad)
	if err != nil {
		t.Fatalf("POST /control/reserve error: %v", err)
	}
	defer respBad.Body.Close()
	if respBad.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST with wrong token status = %d, want 401", respBad.StatusCode)
	}

	// 2. Post with correct token but empty value -> queries current reserve (200 OK)
	dataRead := url.Values{"token": {"correct-token-123"}}
	respRead, err := client.PostForm(ts.URL+"/control/reserve", dataRead)
	if err != nil {
		t.Fatalf("POST /control/reserve read error: %v", err)
	}
	defer respRead.Body.Close()
	if respRead.StatusCode != http.StatusOK {
		t.Errorf("POST with correct token query status = %d, want 200", respRead.StatusCode)
	}
}
