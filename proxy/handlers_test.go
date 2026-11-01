package proxy_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/proxy"
)

func TestHandleWebServesEmbeddedIndexWithInjectedScript(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	cfg := baseTestConfig()
	cfg.Style = "clear.js"
	cfg.APIBaseURL = "/"
	ts := newProxyServer(t, cfg, pw)

	resp, err := ts.Client().Get(ts.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// {STYLE} in the template is replaced with the fully-prefixed asset path...
	assert.Contains(t, string(body), `<script src="/viz-static/clear.js" type="text/javascript"></script>`)
	// ...and InjectJS separately appends a second, unprefixed script tag
	// referencing the bare Style value before </body>. This looks like an
	// unintended duplicate/prefix mismatch; pinned here as the actual
	// current contract rather than silently asserted away.
	assert.Contains(t, string(body), `<script type="text/javascript" src="clear.js"></script></body>`)
	assert.Contains(t, string(body), "23.28.2 27626f98") // {VERSION} substitution from api.status.json fixture
}

func TestHandleWebSetsCacheControlBasedOnBrowserCache(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		path          string
		wantCacheCtrl string
		browserCache  int
	}

	for _, tc := range []testCase{
		{
			name:          "js asset with browser cache set gets max-age",
			browserCache:  120,
			path:          "/viz-static/white.js",
			wantCacheCtrl: "max-age=120",
		},
		{
			name:          "html document is never cached",
			browserCache:  120,
			path:          "/index.html",
			wantCacheCtrl: "no-cache, no-store",
		},
		{
			name:          "zero browser cache disables caching",
			browserCache:  0,
			path:          "/viz-static/white.js",
			wantCacheCtrl: "no-cache, no-store",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gw := newFakeGateway(t, nil)
			pw := newLocalPowerwall(t, gw)
			cfg := baseTestConfig()
			cfg.BrowserCache = tc.browserCache
			ts := newProxyServer(t, cfg, pw)

			resp, err := ts.Client().Get(ts.URL + tc.path)
			require.NoError(t, err)
			defer resp.Body.Close()

			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, tc.wantCacheCtrl, resp.Header.Get("Cache-Control"))
		})
	}
}

func TestHandleWebSetsAuthCookies(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	ts := newProxyServer(t, baseTestConfig(), pw)

	resp, err := ts.Client().Get(ts.URL + "/index.html")
	require.NoError(t, err)
	defer resp.Body.Close()

	names := make([]string, 0, len(resp.Cookies()))
	for _, c := range resp.Cookies() {
		names = append(names, c.Name)
	}
	assert.Contains(t, names, "AuthCookie")
	assert.Contains(t, names, "UserRecord")
}

func TestHandleWebSecureCookiesUnderHTTPS(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	cfg := baseTestConfig()
	cfg.HTTPSMode = "yes"
	ts := newProxyServer(t, cfg, pw)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/index.html", nil)
	require.NoError(t, err)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	setCookies := resp.Header.Values("Set-Cookie")
	require.NotEmpty(t, setCookies)
	for _, c := range setCookies {
		assert.Contains(t, c, "SameSite=None;Secure;")
	}
}

func TestHandleWebDiskOverrideTakesPrecedence(t *testing.T) {
	t.Parallel()

	webRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("<html>{VERSION}</html>"), 0o600))

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	srv := proxy.NewServer(t.Context(), baseTestConfig(), pw)
	srv.WebRoot = webRoot
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	resp, err := ts.Client().Get(ts.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "23.28.2 27626f98")
	assert.NotContains(t, string(body), "{VERSION}")
}

func TestHandleWebUnknownPathWithoutStaticOrGatewayIs404(t *testing.T) {
	t.Parallel()

	pw := newDisconnectedPowerwall(t)
	ts := newProxyServer(t, baseTestConfig(), pw)

	resp, err := ts.Client().Get(ts.URL + "/no-such-asset.xyz")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandleStatsFieldsAndConfig(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	cfg := baseTestConfig()
	cfg.Port = 1234
	cfg.NegSolar = false
	ts := newProxyServer(t, cfg, pw)

	resp, err := ts.Client().Get(ts.URL + "/stats")
	require.NoError(t, err)
	defer resp.Body.Close()

	var stats map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stats))

	assert.Contains(t, stats["pypowerwall"], "Proxy t111")
	assert.Contains(t, stats, "uptime")
	assert.Contains(t, stats, "ts")
	assert.Contains(t, stats, "mem")
	assert.Equal(t, "Tesla Energy Gateway", stats["site_name"])
	assert.Equal(t, false, stats["cloudmode"])

	subConfig, ok := stats["config"].(map[string]any)
	require.True(t, ok)
	assert.InEpsilon(t, 1234.0, subConfig["PW_PORT"], 0)
	assert.Equal(t, false, subConfig["PW_NEG_SOLAR"])
}

func TestHandleStatsOmitsHealthWhenDisabled(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	cfg := baseTestConfig()
	cfg.HealthCheckEnabled = false
	ts := newProxyServer(t, cfg, pw)

	resp, err := ts.Client().Get(ts.URL + "/stats")
	require.NoError(t, err)
	defer resp.Body.Close()

	var stats map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stats))
	assert.NotContains(t, stats, "connection_health")
}

func TestHandleStatsConfigMasking(t *testing.T) {
	t.Parallel()

	cases := []struct {
		modifyConfig func(c *proxy.Config)
		check        func(t *testing.T, stats map[string]any)
		name         string
	}{
		{
			name: "unconfigured secrets remain empty or nil",
			modifyConfig: func(c *proxy.Config) {
				c.Password = ""
				c.ControlSecret = ""
				c.GwPwd = ""
				c.Email = ""
				c.SiteID = ""
				c.AuthPath = ""
				c.CacheFile = ""
				c.RsaKeyPath = ""
				c.HTTPSCert = ""
				c.HTTPSKey = ""
				c.Failover = true
			},
			check: func(t *testing.T, stats map[string]any) {
				t.Helper()
				config, ok := stats["config"].(map[string]any)
				require.True(t, ok)
				assert.Nil(t, config["PW_PASSWORD"])
				assert.Nil(t, config["PW_CONTROL_SECRET"])
				assert.Nil(t, config["PW_GW_PWD"])
				assert.Nil(t, config["PW_HTTPS_CERT"])
				assert.Nil(t, config["PW_HTTPS_KEY"])
				assert.Empty(t, config["PW_EMAIL"])
				assert.Empty(t, config["PW_SITEID"])
				assert.Empty(t, config["PW_AUTH_PATH"])
				assert.Empty(t, config["PW_CACHE_FILE"])
				assert.Empty(t, config["PW_RSA_KEY_PATH"])
				assert.Nil(t, stats["siteid"])
				assert.Empty(t, stats["cf"])
				assert.Equal(t, true, config["PW_FAILOVER"])
				assert.Nil(t, config["PW_FLEETAPI_HTTP"])
			},
		},
		{
			name: "configured secrets are masked as 10 asterisks",
			modifyConfig: func(c *proxy.Config) {
				c.Password = "secret-pass"
				c.ControlSecret = "secret-control"
				c.GwPwd = "secret-gw"
				c.HTTPSCert = "/etc/ssl/proxy.crt"
				c.HTTPSKey = "/etc/ssl/proxy.key"
				c.Email = "secret@example.com"
				c.SiteID = "12345"
				c.AuthPath = "/var/secrets"
				c.CacheFile = "/var/secrets/.powerwall"
				c.RsaKeyPath = "/var/secrets/key.pem"
				c.Failover = false
				c.FleetAPIHTTP = "1.1"
			},
			check: func(t *testing.T, stats map[string]any) {
				t.Helper()
				config, ok := stats["config"].(map[string]any)
				require.True(t, ok)
				const masked = "**********"
				assert.Equal(t, masked, config["PW_PASSWORD"])
				assert.Equal(t, masked, config["PW_CONTROL_SECRET"])
				assert.Equal(t, masked, config["PW_GW_PWD"])
				assert.Equal(t, masked, config["PW_HTTPS_CERT"])
				assert.Equal(t, masked, config["PW_HTTPS_KEY"])
				assert.Equal(t, masked, config["PW_EMAIL"])
				assert.Equal(t, masked, config["PW_SITEID"])
				assert.Equal(t, masked, config["PW_AUTH_PATH"])
				assert.Equal(t, masked, config["PW_CACHE_FILE"])
				assert.Equal(t, masked, config["PW_RSA_KEY_PATH"])
				assert.Equal(t, masked, stats["siteid"])
				assert.Equal(t, masked, stats["cf"])
				assert.Equal(t, false, config["PW_FAILOVER"])
				assert.Equal(t, "1.1", config["PW_FLEETAPI_HTTP"])
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gw := newFakeGateway(t, nil)
			pw := newLocalPowerwall(t, gw)
			cfg := baseTestConfig()
			tc.modifyConfig(&cfg)
			ts := newProxyServer(t, cfg, pw)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/stats", nil)
			require.NoError(t, err)
			resp, err := ts.Client().Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			var stats map[string]any
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&stats))
			tc.check(t, stats)
		})
	}
}

func TestHandleIndexEmailRemoval(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		email     string
		wantEmail string
	}{
		{
			name:      "default email is not leaked into index html",
			email:     "secret-email@example.com",
			wantEmail: "secret-email@example.com",
		},
		{
			name:      "empty email is not leaked into index html",
			email:     "",
			wantEmail: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gw := newFakeGateway(t, nil)
			pw := newLocalPowerwall(t, gw)
			cfg := baseTestConfig()
			cfg.Email = tc.email
			ts := newProxyServer(t, cfg, pw)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/", nil)
			require.NoError(t, err)
			resp, err := ts.Client().Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			bodyStr := string(body)

			assert.NotContains(t, bodyStr, "{EMAIL}")
			if tc.wantEmail != "" {
				assert.NotContains(t, bodyStr, tc.wantEmail)
			}
			assert.Contains(t, bodyStr, `"username": ""`)
		})
	}
}

func TestHandleHealthFieldsIncludeDegradedCacheSnapshot(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	cfg := baseTestConfig()
	ts := newProxyServer(t, cfg, pw)

	// Prime the degraded cache with one entry.
	_, err := ts.Client().Get(ts.URL + "/soe")
	require.NoError(t, err)

	resp, err := ts.Client().Get(ts.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	var health map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&health))

	assert.Contains(t, health, "pypowerwall")
	assert.Contains(t, health, "connection_health")
	cached, ok := health["cached_data"].(map[string]any)
	require.True(t, ok)
	assert.GreaterOrEqual(t, cached["cache_size"], 1.0)
	assert.Contains(t, health, "endpoint_statistics")
}

func TestHandleHelpBody(t *testing.T) {
	t.Parallel()

	pw := newDisconnectedPowerwall(t)
	ts := newProxyServer(t, baseTestConfig(), pw)

	resp, err := ts.Client().Get(ts.URL + "/help")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "<h1>pyPowerwall")
	assert.Contains(t, string(body), "Documentation & API Reference")
}

func TestHandlePWFacingUnknownReturnsInvalidRequest(t *testing.T) {
	t.Parallel()

	pw := newDisconnectedPowerwall(t)
	ts := newProxyServer(t, baseTestConfig(), pw)

	resp, err := ts.Client().Get(ts.URL + "/pw/not-a-real-sensor")
	require.NoError(t, err)
	defer resp.Body.Close()

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "Invalid Request", body["error"])
}
