package proxy_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestControlSecurity preserves the coverage from the legacy root
// proxy_test.go: an unauthenticated control POST must be rejected, and a
// correctly authenticated read (no value) must succeed.
func TestControlSecurity(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	cfg := baseTestConfig()
	cfg.ControlSecret = "correct-token-123"
	ts := newProxyServer(t, cfg, pw)
	client := ts.Client()

	badResp, err := client.PostForm(ts.URL+"/control/reserve", url.Values{"value": {"20"}, "token": {"wrong"}})
	require.NoError(t, err)
	defer badResp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, badResp.StatusCode)

	okResp, err := client.PostForm(ts.URL+"/control/reserve", url.Values{"token": {"correct-token-123"}})
	require.NoError(t, err)
	defer okResp.Body.Close()
	assert.Equal(t, http.StatusOK, okResp.StatusCode)
}

func TestControlDisabledWithoutSecretConfigured(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	cfg := baseTestConfig()
	cfg.ControlSecret = ""
	ts := newProxyServer(t, cfg, pw)

	resp, err := ts.Client().PostForm(ts.URL+"/control/reserve", url.Values{"token": {"anything"}})
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "control commands disabled")
}

func TestControlRejectsOversizedBody(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	ts := newProxyServer(t, baseTestConfig(), pw)

	padding := strings.Repeat("a", 4200)
	form := url.Values{"token": {"test-secret"}, "value": {"1"}, "padding": {padding}}

	resp, err := ts.Client().PostForm(ts.URL+"/control/reserve", form)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestControlRejectsMalformedBody(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	ts := newProxyServer(t, baseTestConfig(), pw)

	req, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, ts.URL+"/control/reserve", strings.NewReader("%zz"),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestControlNonControlPathRejected(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	cfg := baseTestConfig()
	ts := newProxyServer(t, cfg, pw)

	resp, err := ts.Client().PostForm(ts.URL+"/other/path", url.Values{"token": {"test-secret"}})
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "Invalid Request")
}

func TestControlInvalidAction(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	ts := newProxyServer(t, baseTestConfig(), pw)

	resp, err := ts.Client().PostForm(ts.URL+"/control/not-a-real-action", url.Values{"token": {"test-secret"}})
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "Invalid Command Action")
}

// TestControlDispatch exercises every control/<action> POST endpoint with a
// live (fake) local gateway backing SetOperation/Post calls, table-driven
// across the get/set/valid/invalid permutations described in control.go.
func TestControlDispatch(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	ts := newProxyServer(t, baseTestConfig(), pw)
	client := ts.Client()

	type testCase struct {
		form       url.Values
		name       string
		path       string
		wantSubs   []string
		wantStatus int
	}

	for _, tc := range []testCase{
		{
			name:       "get reserve",
			path:       "/control/reserve",
			form:       url.Values{"token": {"test-secret"}},
			wantStatus: http.StatusOK,
			wantSubs:   []string{"reserve"},
		},
		{
			name:       "set reserve",
			path:       "/control/reserve",
			form:       url.Values{"token": {"test-secret"}, "value": {"25"}},
			wantStatus: http.StatusOK,
			wantSubs:   []string{"backup_reserve_percent"},
		},
		{
			name:       "set reserve with mode",
			path:       "/control/reserve",
			form:       url.Values{"token": {"test-secret"}, "value": {"30"}, "mode": {"backup"}},
			wantStatus: http.StatusOK,
			wantSubs:   []string{"backup_reserve_percent", "real_mode"},
		},
		{
			name:       "set reserve invalid value",
			path:       "/control/reserve",
			form:       url.Values{"token": {"test-secret"}, "value": {"not-a-number"}},
			wantStatus: http.StatusBadRequest,
			wantSubs:   []string{"error"},
		},
		{
			name:       "set reserve invalid mode",
			path:       "/control/reserve",
			form:       url.Values{"token": {"test-secret"}, "value": {"30"}, "mode": {"not-a-mode"}},
			wantStatus: http.StatusBadRequest,
			wantSubs:   []string{"error"},
		},
		{
			name:       "get mode",
			path:       "/control/mode",
			form:       url.Values{"token": {"test-secret"}},
			wantStatus: http.StatusOK,
			wantSubs:   []string{"mode"},
		},
		{
			name:       "set mode",
			path:       "/control/mode",
			form:       url.Values{"token": {"test-secret"}, "value": {"self_consumption"}},
			wantStatus: http.StatusOK,
			wantSubs:   []string{"real_mode"},
		},
		{
			name: "set mode with level",
			path: "/control/mode",
			form: url.Values{
				"token": {"test-secret"}, "value": {"self_consumption"}, "level": {"40"},
			},
			wantStatus: http.StatusOK,
			wantSubs:   []string{"real_mode", "backup_reserve_percent"},
		},
		{
			name:       "set mode invalid value",
			path:       "/control/mode",
			form:       url.Values{"token": {"test-secret"}, "value": {"not-a-mode"}},
			wantStatus: http.StatusBadRequest,
			wantSubs:   []string{"error"},
		},
		{
			name: "set mode invalid level",
			path: "/control/mode",
			form: url.Values{
				"token": {"test-secret"}, "value": {"self_consumption"}, "level": {"nope"},
			},
			wantStatus: http.StatusBadRequest,
			wantSubs:   []string{"error"},
		},
		{
			name:       "get grid charging",
			path:       "/control/grid_charging",
			form:       url.Values{"token": {"test-secret"}},
			wantStatus: http.StatusOK,
			wantSubs:   []string{"grid_charging"},
		},
		{
			name:       "set grid charging unsupported in local mode",
			path:       "/control/grid_charging",
			form:       url.Values{"token": {"test-secret"}, "value": {"true"}},
			wantStatus: http.StatusBadRequest,
			wantSubs:   []string{"Failed to set grid_charging"},
		},
		{
			name:       "set grid charging invalid value",
			path:       "/control/grid_charging",
			form:       url.Values{"token": {"test-secret"}, "value": {"maybe"}},
			wantStatus: http.StatusBadRequest,
			wantSubs:   []string{"error"},
		},
		{
			name:       "get grid export",
			path:       "/control/grid_export",
			form:       url.Values{"token": {"test-secret"}},
			wantStatus: http.StatusOK,
			wantSubs:   []string{"grid_export"},
		},
		{
			name:       "set grid export unsupported in local mode",
			path:       "/control/grid_export",
			form:       url.Values{"token": {"test-secret"}, "value": {"battery_ok"}},
			wantStatus: http.StatusBadRequest,
			wantSubs:   []string{"Failed to set grid_export"},
		},
		{
			name:       "set grid export invalid value",
			path:       "/control/grid_export",
			form:       url.Values{"token": {"test-secret"}, "value": {"whatever"}},
			wantStatus: http.StatusBadRequest,
			wantSubs:   []string{"error"},
		},
		{
			name:       "max backup requires tedapi",
			path:       "/control/max_backup",
			form:       url.Values{"token": {"test-secret"}},
			wantStatus: http.StatusBadRequest,
			wantSubs:   []string{"max_backup requires v1r LAN transport"},
		},
		{
			name:       "max backup set requires tedapi",
			path:       "/control/max_backup",
			form:       url.Values{"token": {"test-secret"}, "value": {"600"}},
			wantStatus: http.StatusBadRequest,
			wantSubs:   []string{"max_backup requires v1r LAN transport"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resp, err := client.PostForm(ts.URL+tc.path, tc.form)
			require.NoError(t, err)
			defer resp.Body.Close()

			body, readErr := io.ReadAll(resp.Body)
			require.NoError(t, readErr)

			assert.Equal(t, tc.wantStatus, resp.StatusCode, "body: %s", body)
			for _, sub := range tc.wantSubs {
				assert.Contains(t, string(body), sub)
			}
		})
	}
}

func TestControlReserveSetResponseIsValidOperationJSON(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	ts := newProxyServer(t, baseTestConfig(), pw)

	resp, err := ts.Client().PostForm(
		ts.URL+"/control/reserve", url.Values{"token": {"test-secret"}, "value": {"42"}},
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	var op map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&op))
	assert.InEpsilon(t, 42.0, op["backup_reserve_percent"], 0)
}
