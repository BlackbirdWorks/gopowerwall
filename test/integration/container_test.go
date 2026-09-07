//go:build integration

// Package integration_test runs gopowerwall against pwsimulator, the official
// pypowerwall Powerwall Gateway emulator
// (https://github.com/jasonacox/pypowerwall/tree/main/pwsimulator), started
// as a real container via testcontainers-go. Unlike the httptest fixtures
// used by the rest of this repository's unit tests, this proves gopowerwall
// speaks the same wire protocol a live gateway does.
//
// Requires a container runtime (Docker or a compatible alternative). Tests
// skip cleanly via testcontainers.SkipIfProviderIsNotHealthy when none is
// available, so `make test` (which never passes -tags=integration) is
// unaffected and a Docker-less workstation is not blocked.
package integration_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// pwSimulatorImage pins the pwsimulator image to the exact tag AND digest
// whose stub.py this suite was written and verified against:
// https://github.com/jasonacox/pypowerwall/blob/main/pwsimulator/stub.py
// (version 0.2.5, as reported by the simulator's own version_tuple). Pinning
// by digest as well as tag means "latest" drifting upstream can never
// silently change what this suite runs against - a bump here must be a
// deliberate, reviewed decision.
const pwSimulatorImage = "jasonacox/pwsimulator:0.2.5" +
	"@sha256:b70831888951759b741c9abf76c7753f4c41c2f68783908390d6bf12a9442c18"

// simulatorPort is the single port pwsimulator's stub.py binds
// (server_address = ('0.0.0.0', 443)), serving HTTPS with a self-signed cert.
const simulatorPort = "443/tcp"

const simulatorStartupTimeout = 60 * time.Second

// simulatorLogin is the account pwsimulator's own test.sh authenticates
// with. The simulator does not actually validate credentials (see
// TestAuthAgainstRealEnforcement's documentation of that), but gopowerwall's
// local backend still requires *some* password/email to attempt login.
const (
	simulatorPassword = "password"
	simulatorEmail    = "test@example.com"
	simulatorTimezone = "America/Los_Angeles"
)

// simulator describes a running pwsimulator container.
type simulator struct {
	// HostPort is host:port suitable for gopowerwall.WithHost, pointing at
	// the container's mapped HTTPS port.
	HostPort string
}

// insecureTLSConfig trusts pwsimulator's self-signed certificate, exactly as
// gopowerwall's local and TEDAPI backends do by design when talking to a
// real gateway (see backend/local/local.go and backend/tedapi/tedapi.go).
func insecureTLSConfig() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true}
}

// httpsClient returns a client that trusts the simulator's self-signed cert,
// for tests that talk to it directly (scenario control, raw auth probing)
// rather than through the gopowerwall client.
func httpsClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: insecureTLSConfig(),
		},
	}
}

// startSimulator launches a fresh pwsimulator container and returns its
// connection details, tearing the container down when the test completes.
//
// Every caller gets its own container instance rather than sharing one
// package-level container: several tests (scenario_test.go) deliberately
// mutate the simulator's global in-memory state via its /test/* control
// endpoints, and giving every top-level test its own container is simpler
// and safer than reasoning about which read-only tests are safe to
// interleave with which mutations. The cost is a few extra container starts
// per run, which is cheap next to the value of not sharing mutable state.
func startSimulator(t *testing.T) simulator {
	t.Helper()

	// Skips cleanly (rather than failing) when no container runtime is
	// reachable, e.g. a developer workstation with Docker Desktop stopped.
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := t.Context()

	// /test/scenario is answered unconditionally (no auth required) and
	// with a fixed 200, making it a clean readiness probe distinct from the
	// behavior under test: it is control-plane, not gateway-API, surface.
	readiness := wait.ForHTTP("/test/scenario").
		WithPort(simulatorPort).
		WithTLS(true, insecureTLSConfig()).
		WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
		WithStartupTimeout(simulatorStartupTimeout)

	ctr, err := testcontainers.Run(ctx, pwSimulatorImage,
		testcontainers.WithExposedPorts(simulatorPort),
		testcontainers.WithWaitStrategy(readiness),
	)
	if err != nil {
		// A container runtime that reports healthy but then fails to
		// actually run anything (e.g. mid-CI Docker hiccup) still shouldn't
		// fail the whole suite outright.
		t.Skipf("could not start pwsimulator container, skipping: %v", err)
	}
	t.Cleanup(func() {
		tctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if termErr := ctr.Terminate(tctx); termErr != nil {
			t.Logf("failed to terminate pwsimulator container: %v", termErr)
		}
	})

	host, err := ctr.Host(ctx)
	require.NoError(t, err, "resolve pwsimulator container host")
	mapped, err := ctr.MappedPort(ctx, simulatorPort)
	require.NoError(t, err, "resolve pwsimulator mapped port")

	return simulator{HostPort: fmt.Sprintf("%s:%s", host, mapped.Port())}
}

// baseURL returns the simulator's HTTPS origin, for tests that talk to it
// directly rather than through the gopowerwall client.
func (s simulator) baseURL() string {
	return "https://" + s.HostPort
}

// triggerControl calls one of the simulator's /test/* scenario-control
// endpoints. These endpoints require no authentication (see stub.py's
// do_test_endpoint) and mutate the simulator's shared, package-level state,
// which is why scenario_test.go's subtests that call this must run serially.
func (s simulator) triggerControl(t *testing.T, path string) {
	t.Helper()

	client := httpsClient(10 * time.Second)
	resp, err := client.Get(s.baseURL() + path)
	require.NoError(t, err, "call simulator control endpoint %s", path)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "simulator control endpoint %s", path)
}
