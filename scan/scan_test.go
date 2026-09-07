package scan_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/scan"
)

func TestScanContextFormatting(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	cColor := scan.NewContext(100*time.Millisecond, true, true, buf)

	if cColor.Bold() == "" || cColor.SubBold() == "" || cColor.Normal() == "" || cColor.Dim() == "" ||
		cColor.Alert() == "" {
		t.Error("expected color escape codes when color and interactive are true")
	}

	cNoColor := scan.NewContext(100*time.Millisecond, true, false, nil)
	if cNoColor.Bold() != "" || cNoColor.SubBold() != "" || cNoColor.Normal() != "" || cNoColor.Dim() != "" ||
		cNoColor.Alert() != "" {
		t.Error("expected empty formatting strings when non-interactive")
	}
}

func TestHosts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cidr      string
		wantLen   int
		expectErr bool
	}{
		{"valid-24", "192.168.1.0/24", 254, false},
		{"valid-30", "192.168.1.0/30", 2, false},
		{"valid-31", "192.168.1.0/31", 2, false},
		{"invalid", "not-a-cidr", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hosts, err := scan.Hosts(tt.cidr)
			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error parsing invalid CIDR")
				}

				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(hosts) != tt.wantLen {
				t.Fatalf("Hosts(%s) length = %d, want %d", tt.cidr, len(hosts), tt.wantLen)
			}
		})
	}
}

func TestCheckConnection(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	_, portStr, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	ctx := context.Background()

	// 1. Success
	if !scan.CheckConnection(ctx, "127.0.0.1", 1*time.Second, port) {
		t.Error("expected connection to succeed")
	}

	// 2. Failure (closed port)
	if scan.CheckConnection(ctx, "127.0.0.1", 50*time.Millisecond, 1) {
		t.Error("expected connection to fail on closed port")
	}
}

func TestScanIPAndEndpoints(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"din":             "123-DIN",
				"version":         "24.36.2",
				"up_time_seconds": "3600",
				"teg_type":        "teg",
			})
		case "/tedapi/din":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("User does not have adequate access rights"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	host, portStr, _ := net.SplitHostPort(server.Listener.Addr().String())
	_ = host
	_ = portStr

	ctx := context.Background()
	buf := &bytes.Buffer{}
	sCtx := scan.NewContext(500*time.Millisecond, false, true, buf)

	// Scan normal powerwall status
	dev, msg := scan.IP(ctx, server.Listener.Addr().String(), sCtx, server.Client())
	if dev == nil || dev.DIN != "123-DIN" || dev.Version != "24.36.2" {
		t.Fatalf("unexpected discovered device: %+v, msg=%s", dev, msg)
	}
}

func TestScanFullFlow(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	opts := models.ScanOptions{
		CIDR:        "invalid-network",
		TimeoutSec:  0.1,
		Interactive: true,
		Color:       true,
		MaxHosts:    500, // Should be clamped to 256
	}

	_, err := scan.Scan(context.Background(), opts, buf)
	if err == nil {
		t.Fatal("expected error on invalid CIDR in Scan")
	}
}

func TestScanPW3Detection(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			w.WriteHeader(http.StatusNotFound)
		case "/tedapi/din":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("User does not have adequate access rights"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	buf := &bytes.Buffer{}
	sCtx := scan.NewContext(500*time.Millisecond, false, true, buf)

	dev, msg := scan.IP(ctx, server.Listener.Addr().String(), sCtx, server.Client())
	if dev == nil || dev.DIN != "Powerwall-3" {
		t.Fatalf("expected PW3 detection, got %+v (msg: %s)", dev, msg)
	}
}

func TestScanStatusEdgeCases(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"din":"","version":""}`))
	}))
	defer server.Close()

	ctx := context.Background()
	buf := &bytes.Buffer{}
	sCtx := scan.NewContext(500*time.Millisecond, false, true, buf)

	dev, _ := scan.IP(ctx, server.Listener.Addr().String(), sCtx, server.Client())
	if dev != nil {
		t.Fatalf("expected nil for empty din/version, got %+v", dev)
	}
}

func TestScanGetMyIP(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Might succeed if outbound network is available, or fail in sandbox. Both cases are handled gracefully.
	_, _ = scan.GetMyIP(ctx)
}

func TestScanExecutionFlow(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	opts := models.ScanOptions{
		CIDR:        "127.0.0.1/32",
		TimeoutSec:  0.05,
		Interactive: true,
		Color:       false,
		MaxHosts:    -1, // should default to 30
	}

	results, err := scan.Scan(context.Background(), opts, buf)
	if err != nil {
		t.Fatalf("unexpected error scanning 127.0.0.1/32: %v", err)
	}
	_ = results
}
