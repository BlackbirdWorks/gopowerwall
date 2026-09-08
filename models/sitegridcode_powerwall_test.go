package models_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall"
)

// newLocalTestPowerwallForSiteInfo connects a Powerwall in local mode against
// a fake gateway that serves cookie-based login and the given raw
// /api/site_info body. It mirrors the equivalent helper in the root
// package's own tests, kept local to this package to avoid depending on
// root-package test files.
func newLocalTestPowerwallForSiteInfo(t *testing.T, siteInfoBody []byte) *gopowerwall.Powerwall {
	t.Helper()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login/Basic":
			http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "cookie-value"})
			http.SetCookie(w, &http.Cookie{Name: "UserRecord", Value: "user-value"})
			w.WriteHeader(http.StatusOK)
		case "/api/site_info":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(siteInfoBody)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	pw, err := gopowerwall.New(
		t.Context(),
		gopowerwall.WithHost(server.Listener.Addr().String()),
		gopowerwall.WithPassword("password"),
		gopowerwall.WithCloudMode(false),
		gopowerwall.WithCacheFile(filepath.Join(t.TempDir(), "cache")),
	)
	require.NoError(t, err)
	require.True(t, pw.IsConnected())

	return pw
}

// TestPowerwallSiteInfoDecodesRealGatewayGridCode is the regression test for
// the SiteInfo.GridCode bug: models.SiteInfo.GridCode used to be typed
// string, but a real gateway's /api/site_info response (recorded byte for
// byte in proxy/web/bogus/api.site_info.json) nests a full grid-code
// descriptor object under "grid_code". That made json.Unmarshal fail, and
// Powerwall.SiteInfo(ctx) returned an error on every call against a real
// gateway. With GridCode now typed as models.GridCodeInfo, the call
// succeeds and the nested fields decode correctly.
func TestPowerwallSiteInfoDecodesRealGatewayGridCode(t *testing.T) {
	t.Parallel()

	fixture := readFixture(t, "api.site_info.json")
	pw := newLocalTestPowerwallForSiteInfo(t, fixture)

	info, err := pw.SiteInfo(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "Tesla Energy Gateway", info.SiteName)
	assert.Equal(t, "America/Los_Angeles", info.Timezone)
	assert.InDelta(t, 27.0, info.MaxSystemEnergyKWH, 0.001)
	assert.Equal(t, "60Hz_240V_s_UL1741SA:2019_California", info.GridCode.GridCode)
	assert.InDelta(t, 240.0, info.GridCode.GridVoltageSetting, 0.001)
	assert.InDelta(t, 60.0, info.GridCode.GridFreqSetting, 0.001)
	assert.Equal(t, "Split", info.GridCode.GridPhaseSetting)
	assert.Equal(t, "United States", info.GridCode.Country)
	assert.Equal(t, "California", info.GridCode.State)
	assert.Equal(t, "Southern California Edison", info.GridCode.Utility)
}
