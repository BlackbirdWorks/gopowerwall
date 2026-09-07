package gopowerwall_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blackbirdworks/gopowerwall/scan"
)

func TestScanHosts(t *testing.T) {
	// Subnet /30 has 4 IPs: network, 2 usable hosts, broadcast
	hosts, err := scan.Hosts("192.168.1.0/30")
	if err != nil {
		t.Fatalf("scan.Hosts error: %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("Expected 2 usable hosts for /30, got %d: %v", len(hosts), hosts)
	}
	if hosts[0] != "192.168.1.1" || hosts[1] != "192.168.1.2" {
		t.Errorf("Unexpected hosts: %v", hosts)
	}
}

func TestScanMockPowerwall(t *testing.T) {
	// Mock Powerwall Gateway HTTP server
	mockGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"din":             "1538000-45-C--TEST123456",
				"version":         "24.36.2",
				"up_time_seconds": "24h00m00s",
				"teg_type":        "Powerwall",
			})
		case "/tedapi/din":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":403,"message":"User does not have adequate access rights"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockGateway.Close()

	ctx := scan.NewContext(1*time.Second, false, false, nil)
	client := mockGateway.Client()

	addr := strings.TrimPrefix(mockGateway.URL, "http://")
	dev, _ := scan.ScanIP(context.Background(), addr, ctx, client)
	_ = dev
}
