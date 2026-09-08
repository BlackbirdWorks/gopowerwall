package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPort covers port's PW_PORT environment lookup and its fallback to
// defaultPort. Not parallel: it uses t.Setenv, which the testing package
// forbids combining with t.Parallel.
func TestPort(t *testing.T) {
	type testCase struct {
		env  string
		name string
		want int
	}

	for _, tc := range []testCase{
		{name: "unset falls back to the default", env: "", want: defaultPort},
		{name: "valid value is parsed", env: "9999", want: 9999},
		{name: "non-numeric value falls back to the default", env: "not-a-port", want: defaultPort},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("PW_PORT", tc.env)
			}

			assert.Equal(t, tc.want, port())
		})
	}
}

// TestRun drives run's actual HTTP probe against a local httptest server,
// covering the healthy, unhealthy-status and unreachable-server branches.
// Not parallel: it uses t.Setenv to point run at each case's server instead
// of the proxy's real default port.
func TestRun(t *testing.T) {
	type testCase struct {
		handler http.HandlerFunc
		name    string
		want    int
		closed  bool
	}

	for _, tc := range []testCase{
		{
			name:    "2xx response is healthy",
			handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
			want:    0,
		},
		{
			name:    "5xx response is unhealthy",
			handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
			want:    1,
		},
		{
			name:   "unreachable server is unhealthy",
			closed: true,
			want:   1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var portStr string

			if tc.closed {
				portStr = closedPort(t)
			} else {
				srv := httptest.NewServer(tc.handler)
				t.Cleanup(srv.Close)
				portStr = serverPort(t, srv)
			}

			t.Setenv("PW_PORT", portStr)

			assert.Equal(t, tc.want, run())
		})
	}
}

// serverPort extracts the numeric port httptest bound srv to.
func serverPort(t *testing.T, srv *httptest.Server) string {
	t.Helper()

	_, p, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	require.NoError(t, err)

	return p
}

// closedPort returns the string form of a port that was briefly listened on
// and then released, so a connection to it is refused rather than accepted.
func closedPort(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	return strconv.Itoa(port)
}
