//go:build integration

package integration_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
	"github.com/blackbirdworks/gopowerwall/powerwall"
	"github.com/blackbirdworks/gopowerwall/powerwall/local"
)

// newAuthClient builds a PyPowerwallLocal backend directly (rather than
// through powerwall.New) so tests in this file can control exactly when
// Authenticate happens, and can call Poll with recursive=true to bypass its
// automatic re-login-and-retry - isolating the real, single HTTP round trip
// pwsimulator's cookie enforcement produces. cachefile="" disables the local
// auth-cache file entirely, so every client here starts genuinely
// unauthenticated regardless of test order or the working directory.
func newAuthClient(host, password string) *local.PyPowerwallLocal {
	return local.New(
		host, password, simulatorEmail, simulatorTimezone,
		10*time.Second, 5*time.Second, 5,
		powerwall.AuthModeCookie, "", "",
	)
}

// TestAuthAgainstRealEnforcement proves gopowerwall's cookie-auth path
// against pwsimulator's real request handling, not an httptest fixture that
// only ever returns what the test told it to return. No existing unit test
// in this repo can exercise a live 401/re-login round trip end to end.
//
// Two real-simulator quirks worth knowing (discovered by reading stub.py -
// https://github.com/jasonacox/pypowerwall/blob/main/pwsimulator/stub.py -
// rather than assumed):
//
//  1. /api/login/Basic does NOT validate the submitted credentials at all.
//     Its handler has a literal `# TODO: Add check for right login
//     credentials or send 401` left commented out, and it always returns
//     200 with the same fixed AuthCookie/UserRecord pair. So there is no
//     way to make a real login attempt fail against this simulator - see
//     the first subtest, which documents that rather than asserting
//     something the simulator cannot actually produce.
//  2. Every other endpoint DOES enforce session state for real: a request
//     without a valid cookie is rejected with 401 "Token Expired" - not the
//     403 one might expect from other Tesla gateway documentation. That
//     401 enforcement, and gopowerwall's handling of it, is what the rest
//     of this test proves.
func TestAuthAgainstRealEnforcement(t *testing.T) {
	t.Parallel()

	sim := startSimulator(t)

	t.Run("login succeeds regardless of password, because the simulator does not check it", func(t *testing.T) {
		t.Parallel()

		lb := newAuthClient(sim.HostPort, "definitely-not-the-real-password")
		err := lb.Authenticate(t.Context())
		require.NoError(t, err, "pwsimulator's /api/login/Basic accepts any credentials")

		data, err := lb.Poll(t.Context(), "/api/status", true, false, false)
		require.NoError(t, err)
		assert.Equal(t, "1232100-00-E--TG123456789ABC", lookup.Lookup(data, "din"))
	})

	t.Run("an unauthenticated request is rejected with backend.ErrLogin", func(t *testing.T) {
		t.Parallel()

		lb := newAuthClient(sim.HostPort, simulatorPassword)
		// Deliberately never call Authenticate: lb holds no session cookie.
		// recursive=true stops PyPowerwallLocal.Poll from transparently
		// logging in and retrying on 401, so this observes the real,
		// unauthenticated response the simulator sends back.
		_, err := lb.Poll(t.Context(), "/api/status", true, true, false)
		require.Error(t, err)
		assert.ErrorIs(t, err, powerwall.ErrLogin)
	})

	t.Run("the same unauthenticated request self-heals via automatic re-login", func(t *testing.T) {
		t.Parallel()

		lb := newAuthClient(sim.HostPort, simulatorPassword)
		// recursive=false is the path every public Powerwall method
		// actually takes: on the first 401, PyPowerwallLocal transparently
		// logs in and retries once, so this succeeds despite lb starting
		// with no session at all.
		data, err := lb.Poll(t.Context(), "/api/status", true, false, false)
		require.NoError(t, err)
		assert.Equal(t, "1232100-00-E--TG123456789ABC", lookup.Lookup(data, "din"))
	})

	t.Run("raw HTTP: missing/garbage cookies rejected, a real session cookie accepted", func(t *testing.T) {
		t.Parallel()

		client := httpsClient(10 * time.Second)

		noCookieReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet, sim.baseURL()+"/api/status", nil)
		require.NoError(t, err)
		noCookieResp, err := client.Do(noCookieReq)
		require.NoError(t, err)
		defer func() { _ = noCookieResp.Body.Close() }()
		assert.Equal(t, http.StatusUnauthorized, noCookieResp.StatusCode)

		garbageReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet, sim.baseURL()+"/api/status", nil)
		require.NoError(t, err)
		garbageReq.Header.Set("Cookie", "AuthCookie=garbage; UserRecord=garbage")
		garbageResp, err := client.Do(garbageReq)
		require.NoError(t, err)
		defer func() { _ = garbageResp.Body.Close() }()
		assert.Equal(t, http.StatusUnauthorized, garbageResp.StatusCode)

		loginBody := `{"username":"customer","password":"password","email":"test@example.com",` +
			`"clientInfo":{"timezone":"America/Los_Angeles"}}`
		loginReq, err := http.NewRequestWithContext(
			t.Context(), http.MethodPost, sim.baseURL()+"/api/login/Basic", strings.NewReader(loginBody),
		)
		require.NoError(t, err)
		loginReq.Header.Set("Content-Type", "application/json")
		loginResp, err := client.Do(loginReq)
		require.NoError(t, err)
		defer func() { _ = loginResp.Body.Close() }()
		require.Equal(t, http.StatusOK, loginResp.StatusCode)
		require.NotEmpty(t, loginResp.Cookies(), "login response must set session cookies")

		authedReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet, sim.baseURL()+"/api/status", nil)
		require.NoError(t, err)
		for _, c := range loginResp.Cookies() {
			authedReq.AddCookie(c)
		}
		authedResp, err := client.Do(authedReq)
		require.NoError(t, err)
		defer func() { _ = authedResp.Body.Close() }()
		assert.Equal(t, http.StatusOK, authedResp.StatusCode)
	})
}
