package scan_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/scan"
)

const (
	shortTimeout  = 100 * time.Millisecond
	serverTimeout = 500 * time.Millisecond
)

func TestNewContextFormatting(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name        string
		color       bool
		interactive bool
		wantEscapes bool
	}

	for _, tc := range []testCase{
		{name: "color and interactive produce escape codes", color: true, interactive: true, wantEscapes: true},
		{name: "non-interactive forces color off", color: true, interactive: false, wantEscapes: false},
		{name: "interactive without color has no escapes", color: false, interactive: true, wantEscapes: false},
		{name: "neither color nor interactive", color: false, interactive: false, wantEscapes: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			buf := &bytes.Buffer{}
			sCtx := scan.NewContext(shortTimeout, tc.color, tc.interactive, buf)

			codes := []string{sCtx.Bold(), sCtx.SubBold(), sCtx.Normal(), sCtx.Dim(), sCtx.Alert()}
			for _, code := range codes {
				if tc.wantEscapes {
					assert.NotEmpty(t, code)
				} else {
					assert.Empty(t, code)
				}
			}
		})
	}
}

func TestNewContextDefaultsOutputToStdout(t *testing.T) {
	t.Parallel()

	sCtx := scan.NewContext(shortTimeout, false, true, nil)

	assert.Equal(t, os.Stdout, sCtx.Output)
}

func TestHosts(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		cidr      string
		wantHosts []string
		wantLen   int
		wantErr   bool
	}

	for _, tc := range []testCase{
		{name: "slash-24 returns 254 usable hosts", cidr: "192.168.1.0/24", wantLen: 254},
		{
			name:      "slash-30 excludes network and broadcast",
			cidr:      "192.168.1.0/30",
			wantLen:   2,
			wantHosts: []string{"192.168.1.1", "192.168.1.2"},
		},
		{name: "slash-31 treats both addresses as usable", cidr: "192.168.1.0/31", wantLen: 2},
		{name: "slash-32 returns the single host", cidr: "192.168.1.5/32", wantLen: 1},
		{name: "invalid cidr returns an error", cidr: "not-a-cidr", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hosts, err := scan.Hosts(tc.cidr)
			if tc.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
			assert.Len(t, hosts, tc.wantLen)
			if tc.wantHosts != nil {
				assert.Equal(t, tc.wantHosts, hosts)
			}
		})
	}
}

func TestCheckConnection(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = listener.Close()
	})

	_, portStr, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	openPort, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	closedListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, closedPortStr, err := net.SplitHostPort(closedListener.Addr().String())
	require.NoError(t, err)
	closedPort, err := strconv.Atoi(closedPortStr)
	require.NoError(t, err)
	require.NoError(t, closedListener.Close())

	type testCase struct {
		name    string
		addr    string
		port    int
		timeout time.Duration
		want    bool
	}

	for _, tc := range []testCase{
		{name: "open port succeeds", addr: "127.0.0.1", port: openPort, timeout: time.Second, want: true},
		{name: "closed port fails", addr: "127.0.0.1", port: closedPort, timeout: shortTimeout, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := scan.CheckConnection(t.Context(), tc.addr, tc.timeout, tc.port)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIP(t *testing.T) {
	t.Parallel()

	type testCase struct {
		handler     http.HandlerFunc
		wantDevice  func(t *testing.T, dev *models.DiscoveredDevice)
		name        string
		wantNilDev  bool
		unreachable bool
	}

	for _, tc := range []testCase{
		{
			name: "status endpoint reports a powerwall",
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/status":
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"din":             "1538000-45-C--TEST123456",
						"version":         "24.36.2",
						"up_time_seconds": "3600",
						"teg_type":        "Powerwall",
					})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			},
			wantDevice: func(t *testing.T, dev *models.DiscoveredDevice) {
				t.Helper()
				require.NotNil(t, dev)
				assert.Equal(t, "1538000-45-C--TEST123456", dev.DIN)
				assert.Equal(t, "24.36.2", dev.Version)
				assert.Equal(t, "Powerwall", dev.DeviceType)
				assert.True(t, dev.IsPowerwall)
			},
		},
		{
			name: "tedapi din falls back to powerwall 3 detection",
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/status":
					w.WriteHeader(http.StatusNotFound)
				case "/tedapi/din":
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"message":"User does not have adequate access rights"}`))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			},
			wantDevice: func(t *testing.T, dev *models.DiscoveredDevice) {
				t.Helper()
				require.NotNil(t, dev)
				assert.Equal(t, "Powerwall-3", dev.DIN)
				assert.True(t, dev.IsPowerwall)
			},
		},
		{
			name: "empty din and version yields no device",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"din":"","version":""}`))
			},
			wantNilDev: true,
		},
		{
			name:        "unreachable address returns nil without an http call",
			unreachable: true,
			wantNilDev:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			buf := &bytes.Buffer{}

			if tc.unreachable {
				closedListener, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				addr := closedListener.Addr().String()
				require.NoError(t, closedListener.Close())

				sCtx := scan.NewContext(10*time.Millisecond, false, true, buf)
				dev, msg := scan.IP(t.Context(), addr, sCtx, http.DefaultClient)
				assert.Nil(t, dev)
				assert.Empty(t, msg)

				return
			}

			server := httptest.NewTLSServer(tc.handler)
			t.Cleanup(server.Close)

			sCtx := scan.NewContext(serverTimeout, false, true, buf)
			dev, msg := scan.IP(t.Context(), server.Listener.Addr().String(), sCtx, server.Client())

			if tc.wantNilDev {
				assert.Nil(t, dev)

				return
			}
			tc.wantDevice(t, dev)
			assert.NotEmpty(t, msg)
		})
	}
}

func TestGetMyIP(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	ip, err := scan.GetMyIP(ctx)
	if err != nil {
		assert.Empty(t, ip)
	} else {
		assert.NotEmpty(t, net.ParseIP(ip))
	}
}

func TestScan(t *testing.T) {
	t.Parallel()

	type testCase struct {
		checkOutput func(t *testing.T, out string)
		name        string
		opts        models.ScanOptions
		cancelCtx   bool
		wantErr     bool
	}

	for _, tc := range []testCase{
		{
			name: "invalid cidr returns an error",
			opts: models.ScanOptions{
				CIDR:        "invalid-network",
				TimeoutSec:  0.05,
				Interactive: true,
				Color:       true,
				MaxHosts:    500, // clamped to 256
			},
			wantErr: true,
		},
		{
			name: "single host cidr completes and reports progress",
			opts: models.ScanOptions{
				CIDR:        "127.0.0.1/32",
				TimeoutSec:  0.05,
				Interactive: true,
				Color:       false,
				MaxHosts:    -1, // defaults to 30
			},
			checkOutput: func(t *testing.T, out string) {
				t.Helper()
				assert.Contains(t, out, "gopowerwall Network Scanner")
				assert.Contains(t, out, "Running Scan on")
				assert.Contains(t, out, "Discovered")
			},
		},
		{
			// Interactive is left false here: with a cancelled context every one of the
			// /24's host goroutines returns almost instantly, so many of them would race
			// on the shared, non-thread-safe bytes.Buffer if progress lines were printed
			// concurrently (see the reported data race in Scan's per-host Fprintf calls).
			name: "cancelled context returns fast with no results",
			opts: models.ScanOptions{
				CIDR:        "10.255.255.0/24",
				TimeoutSec:  5,
				Interactive: false,
				Color:       false,
			},
			cancelCtx: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tc.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			buf := &bytes.Buffer{}
			start := time.Now()
			results, err := scan.Scan(ctx, tc.opts, buf)
			elapsed := time.Since(start)

			if tc.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)

			if tc.cancelCtx {
				assert.Empty(t, results)
				assert.Less(t, elapsed, 10*time.Second)

				return
			}

			if tc.checkOutput != nil {
				tc.checkOutput(t, buf.String())
			}
			_ = results
		})
	}
}

// TestScanInteractiveOutputIsRaceFree fans a scan out across many hosts with
// Interactive progress reporting enabled, so every per-host goroutine writes
// to the shared bytes.Buffer output. Run under `go test -race`, this must
// pass without the race detector reporting a concurrent read/write on the
// buffer (see the reported data race in Scan's per-host Fprintf calls).
func TestScanInteractiveOutputIsRaceFree(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		cidr string
	}

	for _, tc := range []testCase{
		{name: "loopback slash-24 fans out many concurrent unreachable hosts", cidr: "127.0.0.0/24"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			buf := &bytes.Buffer{}
			opts := models.ScanOptions{
				CIDR:        tc.cidr,
				TimeoutSec:  0.02,
				Interactive: true,
				Color:       false,
				MaxHosts:    64,
			}

			results, err := scan.Scan(t.Context(), opts, buf)

			require.NoError(t, err)
			assert.Empty(t, results)
			assert.NotEmpty(t, buf.String())
		})
	}
}
