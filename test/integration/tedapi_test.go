//go:build integration

package integration_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/backend/tedapi"
	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
	"github.com/blackbirdworks/gopowerwall/powerwall"
)

// TestTEDAPIProtocolCompatibility talks TEDAPI protobuf directly to
// pwsimulator via backend/tedapi.Client - bypassing the Powerwall facade -
// to answer the question this suite most needed a real emulator for: does
// gopowerwall's vendored proto/tedapi definitions actually round-trip
// against a real gateway's protobuf wire format, not just against
// hand-built fixtures?
//
// They do. pwsimulator's stub.py vendors Tesla's own tedapi_pb2.py and
// switches on exactly the fields backend/tedapi.go populates
// (message.config.send.file for config reads, message.payload.send for the
// GraphQL-style status query), returning the same tedapi_config/
// tedapi_status JSON either way. That confirms TEDAPI mode is protocol-
// compatible with a real Powerwall 3 gateway. See
// TestTEDAPIModeDispatchFixed below for the Powerwall facade's own
// pure-TEDAPI dispatch, exercised end to end.
func TestTEDAPIProtocolCompatibility(t *testing.T) {
	t.Parallel()

	sim := startSimulator(t)

	client := tedapi.NewClient(
		sim.HostPort, "password",
		10*time.Second, 5*time.Second, 5,
		powerwall.TEDAPIVersion2024_06, powerwall.AuthModeBasic,
	)

	require.True(t, client.Connect(t.Context()), "TEDAPI config round trip should succeed against the simulator")

	cfg := client.GetConfig(t.Context(), false)
	require.NotNil(t, cfg)
	assert.Equal(t, "1232100-00-E--TG123456789ABC", cfg["vin"])

	siteInfo, ok := cfg["site_info"].(map[string]any)
	require.True(t, ok, "config.json's site_info should decode as a nested object")
	assert.Equal(t, "Tesla Energy Gateway", siteInfo["site_name"])

	status := client.GetStatus(t.Context(), false)
	require.NotNil(t, status, "GraphQL-style status query should succeed against the simulator")

	control, ok := status["control"].(map[string]any)
	require.True(t, ok)
	islanding, ok := control["islanding"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, islanding["gridOK"])
}

// TestTEDAPIModeDispatchFixed is the regression test for a real dispatch
// defect this integration suite originally surfaced in powerwall.go:
// gopowerwall.New with only WithGwPwd set (no WithPassword) picks the "pure
// TEDAPI" branch of connectLocal, which builds a fully working tedapi.Client
// (proven reachable and protocol-compatible by
// TestTEDAPIProtocolCompatibility above) and sets tedapiMode/tedapiFlag.
//
// It used to never assign p.mode away from the ModeLocal value New() sets
// before Connect runs, so every mode-dispatching read method (Poll, Power,
// Vitals, GetTimeRemaining, ...) took the "case ModeLocal: if p.local !=
// nil" branch - and p.local is nil on this path, since no local HTTP
// backend is ever constructed for pure TEDAPI/v1r. A Powerwall configured
// for pure TEDAPI mode (or v1r) reported itself connected and
// IsTEDAPI()==true, yet silently returned nil/zero-value/empty results for
// every read.
//
// connectLocal now assigns p.mode = ModeTEDAPI (or ModeV1r for the RSA-key
// variant) on these branches, so the mode-dispatching read methods take
// their `case ModeTEDAPI, ModeV1r:` arms instead of the dead ModeLocal one.
// This test locks in that fix against a real gateway emulator.
func TestTEDAPIModeDispatchFixed(t *testing.T) {
	t.Parallel()

	sim := startSimulator(t)

	pw, err := powerwall.New(
		t.Context(),
		powerwall.WithHost(sim.HostPort),
		powerwall.WithGwPwd("password"),
		powerwall.WithCloudMode(false),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pw.Close(t.Context()) })

	require.True(t, pw.IsConnected(), "connectLocal's pure-TEDAPI branch reports success")
	require.True(t, pw.IsTEDAPI())

	assert.Equal(t, powerwall.ModeTEDAPI, pw.Mode())
	assert.False(t, pw.IsLocal(), "no local HTTP backend was built on the pure-TEDAPI path")

	data := pw.Poll(t.Context(), "/api/status")
	require.NotNil(t, data, "pollInternal's ModeTEDAPI case should now dispatch to the tedapi backend")
	assert.Equal(t, "1232100-00-E--TG123456789ABC", lookup.Lookup(data, "din"))

	vitals, err := pw.Vitals(t.Context())
	require.NoError(t, err)
	// TEDAPI's Vitals always synthesizes at least a TESLA--None entry
	// carrying the gateway DIN (see backend/tedapi.PyPowerwallTEDAPI.Vitals),
	// so a real dispatch should never come back empty.
	require.NotEmpty(t, vitals.Devices)
	tesla, ok := vitals.Devices["TESLA--None"]
	require.True(t, ok)
	assert.Equal(t, "STSTSM--1232100-00-E--TG123456789ABC", tesla["componentParentDin"])
}
