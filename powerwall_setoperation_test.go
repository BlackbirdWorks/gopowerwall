package gopowerwall_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall"
	"github.com/blackbirdworks/gopowerwall/pkgs/calc"
)

// operationGateway is a fake local gateway that serves cookie login and
// records every JSON body POSTed to /api/operation. GET /api/operation is
// served from mutable in-memory state so a test can change the gateway's
// live values after seeding the poll cache, proving a forced re-read
// observes the CURRENT value rather than a stale cached one.
type operationGateway struct {
	realMode string
	posts    []map[string]any
	reserve  float64
	getCount int
	mu       sync.Mutex
	failGet  bool
}

// newOperationGateway builds a gateway seeded with the reserve/mode values
// every test in this file starts from.
func newOperationGateway() *operationGateway {
	return &operationGateway{reserve: 20, realMode: "self_consumption"}
}

func (g *operationGateway) setState(reserve float64, realMode string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reserve = reserve
	g.realMode = realMode
}

func (g *operationGateway) setFailGet(fail bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failGet = fail
}

func (g *operationGateway) recordedPosts() []map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()

	out := make([]map[string]any, len(g.posts))
	copy(out, g.posts)

	return out
}

func (g *operationGateway) operationGetCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.getCount
}

func (g *operationGateway) handleGetOperation(w http.ResponseWriter) {
	g.mu.Lock()
	g.getCount++
	fail := g.failGet
	reserve := g.reserve
	mode := g.realMode
	g.mu.Unlock()

	if fail {
		w.WriteHeader(http.StatusInternalServerError)

		return
	}
	w.WriteHeader(http.StatusOK)
	body, _ := json.Marshal(map[string]any{
		"backup_reserve_percent": reserve,
		"real_mode":              mode,
	})
	_, _ = w.Write(body)
}

func (g *operationGateway) handlePostOperation(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(data, &body)

	g.mu.Lock()
	g.posts = append(g.posts, body)
	g.mu.Unlock()

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (g *operationGateway) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/login/Basic":
			http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "cookie-value"})
			http.SetCookie(w, &http.Cookie{Name: "UserRecord", Value: "user-value"})
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"din":"test-din"}`))
		case r.URL.Path == "/api/operation" && r.Method == http.MethodGet:
			g.handleGetOperation(w)
		case r.URL.Path == "/api/operation" && r.Method == http.MethodPost:
			g.handlePostOperation(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// newOperationTestPowerwall connects a Powerwall in local mode against a fake
// gateway driven by the given operationGateway.
func newOperationTestPowerwall(t *testing.T, gw *operationGateway) *gopowerwall.Powerwall {
	t.Helper()

	server := httptest.NewTLSServer(gw.handler())
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
	require.True(t, pw.IsLocal())

	return pw
}

// TestSetOperationLocalModeBackfillsRealMode is the regression test for the
// live-hardware clobber bug: the local gateway's /api/operation endpoint is a
// full overwrite, so a bare SetReserve call (which only supplies
// backup_reserve_percent) must not omit real_mode from the POST body -
// omitting it would reset the operating mode on a real Powerwall. The test
// also proves the back-filled value is fetched fresh rather than reused from
// the poll cache: it seeds the cache with one mode via a plain read, then
// changes the gateway's live mode before calling SetReserve, and requires
// the POST to carry the NEW value.
//
// Before the fix, this test fails: SetOperation sent only
// {"backup_reserve_percent":30} with no "real_mode" key at all.
func TestSetOperationLocalModeBackfillsRealMode(t *testing.T) {
	t.Parallel()

	gw := newOperationGateway()
	pw := newOperationTestPowerwall(t, gw)

	_, err := pw.Operation(t.Context())
	require.NoError(t, err)
	gw.setState(20, "backup")

	_, err = pw.SetReserve(t.Context(), 30)
	require.NoError(t, err)

	posts := gw.recordedPosts()
	require.Len(t, posts, 1)
	assert.InDelta(t, 30.0, posts[0]["backup_reserve_percent"], 0.001)
	assert.Equal(t, "backup", posts[0]["real_mode"],
		"real_mode must be back-filled with the CURRENT gateway value, not a stale cached one")
}

// TestSetOperationLocalModeBackfillsReserve is the symmetric regression test:
// a bare SetMode call must not omit backup_reserve_percent, which would reset
// the reserve level on a real Powerwall.
func TestSetOperationLocalModeBackfillsReserve(t *testing.T) {
	t.Parallel()

	gw := newOperationGateway()
	pw := newOperationTestPowerwall(t, gw)

	_, err := pw.Operation(t.Context())
	require.NoError(t, err)
	gw.setState(45, "self_consumption")

	_, err = pw.SetMode(t.Context(), "backup")
	require.NoError(t, err)

	posts := gw.recordedPosts()
	require.Len(t, posts, 1)
	assert.Equal(t, "backup", posts[0]["real_mode"])
	assert.InDelta(t, 45.0, posts[0]["backup_reserve_percent"], 0.001,
		"backup_reserve_percent must be back-filled with the CURRENT gateway value, not a stale cached one")
}

// TestSetOperationBothFieldsSuppliedSkipsBackfillRead verifies that supplying
// both level and mode sends both keys without performing any extra read of
// the current state - there is nothing to back-fill.
func TestSetOperationBothFieldsSuppliedSkipsBackfillRead(t *testing.T) {
	t.Parallel()

	gw := newOperationGateway()
	pw := newOperationTestPowerwall(t, gw)

	before := gw.operationGetCount()
	level := 55.0
	mode := "autonomous"
	_, err := pw.SetOperation(t.Context(), &level, &mode)
	require.NoError(t, err)

	assert.Equal(t, before, gw.operationGetCount(), "supplying both fields must not trigger a backfill read")

	posts := gw.recordedPosts()
	require.Len(t, posts, 1)
	assert.InDelta(t, 55.0, posts[0]["backup_reserve_percent"], 0.001)
	assert.Equal(t, "autonomous", posts[0]["real_mode"])
}

// TestSetOperationLocalModeBackfillReadFailureRefusesPartialWrite verifies
// that when the local-mode backfill read fails, SetOperation refuses the
// write outright - returning the ErrOperationBackfillFailed sentinel and
// performing NO POST - rather than falling back to the dangerous partial
// payload.
func TestSetOperationLocalModeBackfillReadFailureRefusesPartialWrite(t *testing.T) {
	t.Parallel()

	gw := newOperationGateway()
	pw := newOperationTestPowerwall(t, gw)

	gw.setFailGet(true)

	_, err := pw.SetReserve(t.Context(), 40)
	require.ErrorIs(t, err, gopowerwall.ErrOperationBackfillFailed)
	assert.Empty(t, gw.recordedPosts(), "a failed backfill read must not fall back to a partial write")
}

// TestGetReserveForcedBypassesCacheAndScales verifies the helper the CLI uses
// to confirm what a cloud/FleetAPI reserve write actually applied: it must
// read past the poll cache (a stale value here would defeat the whole point
// of confirming a write that Tesla may have capped) and return the scaled,
// user-facing percentage, matching pypowerwall's get_reserve(scale=True,
// force=True).
func TestGetReserveForcedBypassesCacheAndScales(t *testing.T) {
	t.Parallel()

	gw := newOperationGateway()
	pw := newOperationTestPowerwall(t, gw)

	_, err := pw.Operation(t.Context())
	require.NoError(t, err)
	gw.setState(50, "self_consumption")

	got, err := pw.GetReserveForced(t.Context())
	require.NoError(t, err)
	assert.InDelta(t, calc.ScaleBatteryLevel(50), got, 0.001)
}

// TestSetOperationNonLocalModeSkipsBackfill guards the critical subtlety of
// this fix: cloud, FleetAPI and TEDAPI apply BACKUP_RESERVE and
// OPERATION_MODE as two independent asynchronous commands, so they must
// receive a partial payload unconditionally - back-filling there would race
// the other write. Neither backend below is actually connected (no auth
// file), so a bare SetReserve call fails at the final Post step with
// ErrSetOperationFailed. If the local-mode-only guard were ever weakened to
// apply unconditionally, SetOperation would instead try to read back the
// current operation state first, which also fails against a disconnected
// backend - but with ErrOperationBackfillFailed instead. Asserting the
// former (and explicitly not the latter) pins the guard down so a future
// change cannot silently broaden the back-fill to every connection mode.
func TestSetOperationNonLocalModeSkipsBackfill(t *testing.T) {
	t.Parallel()

	type testCase struct {
		connect func(t *testing.T) *gopowerwall.Powerwall
		name    string
	}

	for _, tc := range []testCase{
		{
			name: "cloud mode",
			connect: func(t *testing.T) *gopowerwall.Powerwall {
				t.Helper()
				pw, err := gopowerwall.New(
					t.Context(),
					gopowerwall.WithCloudMode(true),
					gopowerwall.WithAuthPath(t.TempDir()),
				)
				// No auth file exists in this fresh temp dir, so the
				// connection attempt itself fails; New still returns a
				// usable, disconnected *Powerwall alongside the wrapped
				// ConnectError.
				require.Error(t, err)
				require.False(t, pw.IsLocal())

				return pw
			},
		},
		{
			name: "fleetapi mode",
			connect: func(t *testing.T) *gopowerwall.Powerwall {
				t.Helper()
				pw, err := gopowerwall.New(
					t.Context(),
					gopowerwall.WithFleetAPI(true),
					gopowerwall.WithAuthPath(t.TempDir()),
				)
				require.Error(t, err)
				require.False(t, pw.IsLocal())

				return pw
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pw := tc.connect(t)

			_, err := pw.SetReserve(t.Context(), 30)
			require.ErrorIs(t, err, gopowerwall.ErrSetOperationFailed)
			assert.NotErrorIs(t, err, gopowerwall.ErrOperationBackfillFailed)
		})
	}
}
