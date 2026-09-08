package local_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/blackbirdworks/gopowerwall/backend"
	"github.com/blackbirdworks/gopowerwall/backend/local"
	"github.com/blackbirdworks/gopowerwall/backend/tedapi"
	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/proto/teslapower"
)

const (
	fixturesDir     = "../../proxy/web/bogus"
	testTimeout     = 5 * time.Second
	testCacheTTL    = time.Minute
	unreachableHost = "127.0.0.1:1"
	testPassword    = "password"
	testEmail       = "customer@example.com"
	testTimezone    = "America/Los_Angeles"
	testPoolSize    = 2
	singleCall      = 1
	doubleCall      = 2
)

// newTLSServer starts a self-signed TLS test server and registers its cleanup.
func newTLSServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)

	return srv
}

// hostOf returns the host:port of a test server, suitable for local.New.
func hostOf(srv *httptest.Server) string {
	return srv.Listener.Addr().String()
}

// newBackend builds a PyPowerwallLocal pointed at host with the given auth mode and cache file.
func newBackend(host string, authMode models.AuthMode, cachefile string) *local.PyPowerwallLocal {
	return local.New(
		host, testPassword, testEmail, testTimezone,
		testTimeout, testCacheTTL, testPoolSize, authMode, cachefile, "",
	)
}

// readFixture loads a recorded gateway response from proxy/web/bogus.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(fixturesDir, name))
	require.NoError(t, err)

	return data
}

// writeFile writes content to a new file under the test's temp directory and returns its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

func cookieLoginHandler(t *testing.T) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/login/Basic" {
			w.WriteHeader(http.StatusNotFound)

			return
		}
		http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "cookie-value"})
		http.SetCookie(w, &http.Cookie{Name: "UserRecord", Value: "user-value"})
		w.WriteHeader(http.StatusOK)
	}
}

func tokenLoginHandler(t *testing.T) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/login/Basic" {
			w.WriteHeader(http.StatusNotFound)

			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"tok-123"}`))
	}
}

func TestAuthenticate(t *testing.T) {
	t.Parallel()

	type testCase struct {
		wantErrIs    error
		handler      http.HandlerFunc
		name         string
		cacheContent string
		authMode     models.AuthMode
		noServer     bool
		wantErr      bool
	}

	cases := []testCase{
		{
			name:         "loads cached token from disk without contacting server",
			cacheContent: `{"Authorization":"Bearer cached-token"}`,
			authMode:     models.AuthModeToken,
			noServer:     true,
		},
		{
			name:         "loads cached cookies from disk without contacting server",
			cacheContent: `{"AuthCookie":"c1","UserRecord":"u1"}`,
			authMode:     models.AuthModeCookie,
			noServer:     true,
		},
		{
			name:         "invalid auth mode falls back to cookie mode",
			cacheContent: `{"AuthCookie":"c1","UserRecord":"u1"}`,
			authMode:     models.AuthMode("bogus"),
			noServer:     true,
		},
		{
			name:     "no cache file logs in over cookie mode",
			authMode: models.AuthModeCookie,
			handler:  cookieLoginHandler(t),
		},
		{
			name:     "no cache file logs in over token mode",
			authMode: models.AuthModeToken,
			handler:  tokenLoginHandler(t),
		},
		{
			name:         "malformed cache file falls back to login",
			cacheContent: `{not-json`,
			authMode:     models.AuthModeCookie,
			handler:      cookieLoginHandler(t),
		},
		{
			name:     "invalid credentials returns ErrLogin",
			authMode: models.AuthModeCookie,
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
			wantErrIs: backend.ErrLogin,
			wantErr:   true,
		},
		{
			name:     "forbidden returns ErrLogin",
			authMode: models.AuthModeCookie,
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
			},
			wantErrIs: backend.ErrLogin,
			wantErr:   true,
		},
		{
			name:     "unexpected status returns ErrLogin",
			authMode: models.AuthModeCookie,
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantErrIs: backend.ErrLogin,
			wantErr:   true,
		},
		{
			name:     "200 response missing cookies returns ErrLogin",
			authMode: models.AuthModeCookie,
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			wantErrIs: backend.ErrLogin,
			wantErr:   true,
		},
		{
			name:     "unreachable host returns a network error",
			authMode: models.AuthModeCookie,
			noServer: true,
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			var cachefile string
			if tc.cacheContent != "" {
				cachefile = writeFile(t, dir, "auth.json", tc.cacheContent)
			} else {
				cachefile = filepath.Join(dir, "auth.json")
			}

			host := unreachableHost
			if tc.handler != nil {
				host = hostOf(newTLSServer(t, tc.handler))
			}

			b := newBackend(host, tc.authMode, cachefile)
			err := b.Authenticate(t.Context())

			switch {
			case tc.wantErrIs != nil:
				require.ErrorIs(t, err, tc.wantErrIs)
			case tc.wantErr:
				require.Error(t, err)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestAuthenticateWritesNewCacheFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cachefile := filepath.Join(dir, "auth.json")
	host := hostOf(newTLSServer(t, cookieLoginHandler(t)))

	b := newBackend(host, models.AuthModeCookie, cachefile)
	require.NoError(t, b.Authenticate(t.Context()))

	data, err := os.ReadFile(cachefile)
	require.NoError(t, err)
	assert.Contains(t, string(data), "AuthCookie")
}

func TestClose(t *testing.T) {
	t.Parallel()

	var logoutCalls atomic.Int32
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login/Basic":
			http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "c1"})
			http.SetCookie(w, &http.Cookie{Name: "UserRecord", Value: "u1"})
			w.WriteHeader(http.StatusOK)
		case "/api/logout":
			logoutCalls.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
	host := hostOf(newTLSServer(t, handler))
	b := newBackend(host, models.AuthModeCookie, "")
	require.NoError(t, b.Authenticate(t.Context()))

	require.NoError(t, b.Close(t.Context()))
	assert.Equal(t, int32(singleCall), logoutCalls.Load())
}

func TestCloseCancelledContext(t *testing.T) {
	t.Parallel()

	b := newBackend(unreachableHost, models.AuthModeCookie, "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// Close swallows the logout request error and always returns nil.
	assert.NoError(t, b.Close(ctx))
}

func TestPollSuccess(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name     string
		api      string
		fixture  string
		checkKey string
	}

	cases := []testCase{
		{name: "status", api: "/api/status", fixture: "api.status.json", checkKey: "din"},
		{
			name: "meters aggregates", api: "/api/meters/aggregates",
			fixture: "api.meters.aggregates.json", checkKey: "site",
		},
		{
			name: "system status", api: "/api/system_status",
			fixture: "api.system_status.json", checkKey: "command_source",
		},
		{name: "soe", api: "/api/system_status/soe", fixture: "api.system_status.soe.json", checkKey: "percentage"},
		{name: "operation", api: "/api/operation", fixture: "api.operation.json", checkKey: "real_mode"},
		{name: "site info", api: "/api/site_info", fixture: "api.site_info.json", checkKey: "site_name"},
		{name: "sitemaster", api: "/api/sitemaster", fixture: "api.sitemaster.json", checkKey: "status"},
		{name: "customer", api: "/api/customer", fixture: "api.customer.json", checkKey: "registered"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := readFixture(t, tc.fixture)
			var calls atomic.Int32
			handler := func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, tc.api, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}
			host := hostOf(newTLSServer(t, handler))
			b := newBackend(host, models.AuthModeCookie, "")

			res, err := b.Poll(t.Context(), tc.api, false, false, false)
			require.NoError(t, err)
			m, ok := res.(map[string]any)
			require.True(t, ok)
			assert.Contains(t, m, tc.checkKey)

			// Second call should be served from cache: no additional request.
			res2, err2 := b.Poll(t.Context(), tc.api, false, false, false)
			require.NoError(t, err2)
			assert.Equal(t, res, res2)
			assert.Equal(t, int32(singleCall), calls.Load())

			// force=true bypasses the cache.
			_, err3 := b.Poll(t.Context(), tc.api, true, false, false)
			require.NoError(t, err3)
			assert.Equal(t, int32(doubleCall), calls.Load())
		})
	}
}

func TestPollHTTPStatusHandling(t *testing.T) {
	t.Parallel()

	type testCase struct {
		wantErrIs error
		name      string
		status    int
	}

	cases := []testCase{
		{name: "not found", status: http.StatusNotFound, wantErrIs: backend.ErrNotFound},
		{name: "too many requests", status: http.StatusTooManyRequests, wantErrIs: backend.ErrRateLimited},
		{name: "service unavailable", status: http.StatusServiceUnavailable, wantErrIs: backend.ErrRateLimited},
		{name: "bad request", status: http.StatusBadRequest, wantErrIs: backend.ErrUnexpectedStatus},
		{name: "internal server error", status: http.StatusInternalServerError, wantErrIs: backend.ErrUnexpectedStatus},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}
			host := hostOf(newTLSServer(t, handler))
			b := newBackend(host, models.AuthModeCookie, "")

			_, err := b.Poll(t.Context(), "/api/status", false, false, false)
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.wantErrIs)
		})
	}
}

func TestPollNegativeCacheAndCooldown(t *testing.T) {
	t.Parallel()

	t.Run("404 sets negative cache that short-circuits later polls", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32
		handler := func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusNotFound)
		}
		host := hostOf(newTLSServer(t, handler))
		b := newBackend(host, models.AuthModeCookie, "")

		_, err := b.Poll(t.Context(), "/api/status", false, false, false)
		require.ErrorIs(t, err, backend.ErrNotFound)

		_, err = b.Poll(t.Context(), "/api/status", false, false, false)
		require.ErrorIs(t, err, backend.ErrNotFound)
		assert.Equal(t, int32(singleCall), calls.Load(), "second poll should be served from the negative cache")
	})

	t.Run("429 activates cooldown blocking other endpoints", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32
		handler := func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.URL.Path == "/api/status" {
				w.WriteHeader(http.StatusTooManyRequests)

				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
		host := hostOf(newTLSServer(t, handler))
		b := newBackend(host, models.AuthModeCookie, "")

		_, err := b.Poll(t.Context(), "/api/status", false, false, false)
		require.ErrorIs(t, err, backend.ErrRateLimited)

		_, err = b.Poll(t.Context(), "/api/operation", false, false, false)
		require.ErrorIs(t, err, backend.ErrRateLimited)
		assert.Equal(t, int32(singleCall), calls.Load(), "cooldown should block the second, different endpoint")
	})

	t.Run("503 sets both cooldown and negative cache for the same endpoint", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		host := hostOf(newTLSServer(t, handler))
		b := newBackend(host, models.AuthModeCookie, "")

		_, err := b.Poll(t.Context(), "/api/status", false, false, false)
		require.ErrorIs(t, err, backend.ErrRateLimited)

		// The negative cache entry for the same API is consulted before the
		// cooldown check, so a repeat poll of the same endpoint reports
		// ErrNotFound rather than ErrRateLimited.
		_, err = b.Poll(t.Context(), "/api/status", false, false, false)
		require.ErrorIs(t, err, backend.ErrNotFound)

		// A different endpoint has no negative cache entry, so it falls
		// through to the cooldown check.
		_, err = b.Poll(t.Context(), "/api/operation", false, false, false)
		require.ErrorIs(t, err, backend.ErrRateLimited)
	})
}

func TestPollLoginRetry(t *testing.T) {
	t.Parallel()

	t.Run("re-login succeeds and the poll is retried", func(t *testing.T) {
		t.Parallel()

		var statusCalls atomic.Int32
		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/login/Basic":
				http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "c2"})
				http.SetCookie(w, &http.Cookie{Name: "UserRecord", Value: "u2"})
				w.WriteHeader(http.StatusOK)
			case "/api/status":
				n := statusCalls.Add(1)
				if n == singleCall {
					w.WriteHeader(http.StatusUnauthorized)

					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"din":"abc"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}
		host := hostOf(newTLSServer(t, handler))
		b := newBackend(host, models.AuthModeCookie, "")

		res, err := b.Poll(t.Context(), "/api/status", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "abc", m["din"])
		assert.Equal(t, int32(doubleCall), statusCalls.Load())
	})

	t.Run("re-login fails and ErrLogin is returned", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/login/Basic":
				w.WriteHeader(http.StatusUnauthorized)
			case "/api/status":
				w.WriteHeader(http.StatusForbidden)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}
		host := hostOf(newTLSServer(t, handler))
		b := newBackend(host, models.AuthModeCookie, "")

		_, err := b.Poll(t.Context(), "/api/status", false, false, false)
		require.ErrorIs(t, err, backend.ErrLogin)
	})
}

func TestPollRawAndNonJSON(t *testing.T) {
	t.Parallel()

	t.Run("raw mode returns the response body verbatim", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte{0x01, 0x02, 0x03})
		}
		host := hostOf(newTLSServer(t, handler))
		b := newBackend(host, models.AuthModeCookie, "")

		res, err := b.Poll(t.Context(), "/api/custom", false, false, true)
		require.NoError(t, err)
		data, ok := res.([]byte)
		require.True(t, ok)
		assert.Equal(t, []byte{0x01, 0x02, 0x03}, data)
	})

	t.Run("non-JSON body falls back to a raw string", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("plain text response"))
		}
		host := hostOf(newTLSServer(t, handler))
		b := newBackend(host, models.AuthModeCookie, "")

		res, err := b.Poll(t.Context(), "/api/custom", false, false, false)
		require.NoError(t, err)
		s, ok := res.(string)
		require.True(t, ok)
		assert.Equal(t, "plain text response", s)
	})
}

// TestPollRawAndParsedCacheIsolation is the regression test for the cache
// conflating raw and parsed reads of the same endpoint under one key. A
// parsed poll cached a map[string]any under the bare endpoint key; a raw
// poll of the same endpoint then read that same key back and tried to
// assert it to []byte, which failed and silently returned nil. This
// exercises both orderings against a single *PyPowerwallLocal so a fresh
// per-test cache cannot mask the bug the way the existing unit tests did.
func TestPollRawAndParsedCacheIsolation(t *testing.T) {
	t.Parallel()

	type testCase struct {
		run  func(t *testing.T, b *local.PyPowerwallLocal)
		name string
	}

	body := []byte(`{"percentage":42}`)

	for _, tc := range []testCase{
		{
			name: "a parsed poll followed by a raw poll still returns raw bytes",
			run: func(t *testing.T, b *local.PyPowerwallLocal) {
				t.Helper()

				parsed, err := b.Poll(t.Context(), "/api/system_status/soe", false, false, false)
				require.NoError(t, err)
				_, ok := parsed.(map[string]any)
				require.True(t, ok, "parsed poll should decode a map")

				raw, err := b.Poll(t.Context(), "/api/system_status/soe", false, false, true)
				require.NoError(t, err)
				data, ok := raw.([]byte)
				require.True(t, ok, "raw poll after a parsed poll must return bytes, not the cached parsed value")
				assert.Equal(t, body, data)
			},
		},
		{
			name: "a raw poll followed by a parsed poll still returns a decoded map",
			run: func(t *testing.T, b *local.PyPowerwallLocal) {
				t.Helper()

				raw, err := b.Poll(t.Context(), "/api/system_status/soe", false, false, true)
				require.NoError(t, err)
				_, ok := raw.([]byte)
				require.True(t, ok, "raw poll should return bytes")

				parsed, err := b.Poll(t.Context(), "/api/system_status/soe", false, false, false)
				require.NoError(t, err)
				m, ok := parsed.(map[string]any)
				require.True(t, ok, "parsed poll after a raw poll must return a decoded map, not the cached raw bytes")
				assert.InDelta(t, float64(42), m["percentage"], 0)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}
			host := hostOf(newTLSServer(t, handler))
			b := newBackend(host, models.AuthModeCookie, "")

			tc.run(t, b)
		})
	}
}

// TestPollNegativeCacheSharedAcrossRepresentations verifies that a negative
// result (404) is a property of the endpoint, not of the representation
// requested: recording it while polling raw must suppress a later parsed
// poll of the same endpoint, and vice versa, even though the two
// representations are cached under distinct keys.
func TestPollNegativeCacheSharedAcrossRepresentations(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		firstRaw  bool
		secondRaw bool
	}

	for _, tc := range []testCase{
		{name: "a 404 on a raw poll suppresses a later parsed poll", firstRaw: true, secondRaw: false},
		{name: "a 404 on a parsed poll suppresses a later raw poll", firstRaw: false, secondRaw: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32
			handler := func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusNotFound)
			}
			host := hostOf(newTLSServer(t, handler))
			b := newBackend(host, models.AuthModeCookie, "")

			_, err := b.Poll(t.Context(), "/api/custom", false, false, tc.firstRaw)
			require.ErrorIs(t, err, backend.ErrNotFound)

			_, err = b.Poll(t.Context(), "/api/custom", false, false, tc.secondRaw)
			require.ErrorIs(t, err, backend.ErrNotFound)
			assert.Equal(t, int32(singleCall), calls.Load(),
				"a negative result recorded for one representation must suppress a poll of the other")
		})
	}
}

func TestPollVitalsAPIDisabling(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	handler := func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}
	host := hostOf(newTLSServer(t, handler))
	b := newBackend(host, models.AuthModeCookie, "")

	_, err := b.Vitals(t.Context())
	require.Error(t, err)

	_, err = b.Vitals(t.Context())
	require.Error(t, err)
	assert.Equal(t, int32(singleCall), calls.Load(), "vitals should stay disabled after the first 404")
}

func TestPollContextCancellation(t *testing.T) {
	t.Parallel()

	host := hostOf(newTLSServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	b := newBackend(host, models.AuthModeCookie, "")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := b.Poll(ctx, "/api/status", false, false, false)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestPost(t *testing.T) {
	t.Parallel()

	type testCase struct {
		payload   any
		wantEqual any
		handler   http.HandlerFunc
		name      string
		api       string
		wantErr   bool
	}

	cases := []testCase{
		{
			name: "JSON response is decoded",
			api:  "/api/operation",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"success"}`))
			},
			payload:   map[string]any{"real_mode": "self_consumption"},
			wantEqual: map[string]any{"status": "success"},
		},
		{
			name: "non-JSON response falls back to string",
			api:  "/api/operation",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("ok"))
			},
			wantEqual: "ok",
		},
		{
			name: "nil payload sends no body",
			api:  "/api/operation",
			handler: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.NoBody, r.Body)
				_, _ = w.Write([]byte(`{}`))
			},
			payload:   nil,
			wantEqual: map[string]any{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			host := hostOf(newTLSServer(t, tc.handler))
			b := newBackend(host, models.AuthModeCookie, "")

			res, err := b.Post(t.Context(), tc.api, tc.payload, "", false, false)
			if tc.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantEqual, res)
		})
	}
}

func TestPostLoginRetry(t *testing.T) {
	t.Parallel()

	var opCalls atomic.Int32
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login/Basic":
			http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "c1"})
			http.SetCookie(w, &http.Cookie{Name: "UserRecord", Value: "u1"})
			w.WriteHeader(http.StatusOK)
		case "/api/operation":
			n := opCalls.Add(1)
			if n == singleCall {
				w.WriteHeader(http.StatusForbidden)

				return
			}
			_, _ = w.Write([]byte(`{"status":"success"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
	host := hostOf(newTLSServer(t, handler))
	b := newBackend(host, models.AuthModeCookie, "")

	res, err := b.Post(t.Context(), "/api/operation", map[string]any{"real_mode": "backup"}, "", false, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"status": "success"}, res)
	assert.Equal(t, int32(doubleCall), opCalls.Load())
}

func TestPostCacheInvalidation(t *testing.T) {
	t.Parallel()

	var opCalls atomic.Int32
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			opCalls.Add(1)
			_, _ = w.Write([]byte(`{"real_mode":"self_consumption"}`))
		case http.MethodPost:
			_, _ = w.Write([]byte(`{"status":"success"}`))
		}
	}
	host := hostOf(newTLSServer(t, handler))
	b := newBackend(host, models.AuthModeCookie, "")

	_, err := b.Poll(t.Context(), "/api/operation", false, false, false)
	require.NoError(t, err)
	_, err = b.Poll(t.Context(), "/api/operation", false, false, false)
	require.NoError(t, err)
	assert.Equal(t, int32(singleCall), opCalls.Load())

	_, err = b.Post(t.Context(), "/api/operation", map[string]any{"real_mode": "backup"}, "", false, false)
	require.NoError(t, err)

	_, err = b.Poll(t.Context(), "/api/operation", false, false, false)
	require.NoError(t, err)
	assert.Equal(t, int32(doubleCall), opCalls.Load(), "Post should invalidate the cached GET result")
}

func TestPostContextCancellation(t *testing.T) {
	t.Parallel()

	host := hostOf(newTLSServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	b := newBackend(host, models.AuthModeCookie, "")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := b.Post(ctx, "/api/operation", nil, "", false, false)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// buildVitalsProtobuf constructs a minimal DevicesWithVitals payload for decode testing.
func buildVitalsProtobuf(t *testing.T) []byte {
	t.Helper()

	pb := &teslapower.DevicesWithVitals{
		Devices: []*teslapower.SiteControllerConnectedDeviceWithVitals{
			{
				Device: &teslapower.SiteControllerConnectedDevice{
					Device: &teslapower.Device{
						Din:                &teslapower.StringValue{Value: "STSTSM--1232100-00-E--TG1234567890G1"},
						PartNumber:         &teslapower.StringValue{Value: "1232100-00-E"},
						SerialNumber:       &teslapower.StringValue{Value: "TG1234567890G1"},
						ComponentParentDin: &teslapower.StringValue{Value: "STSTSM--PARENT"},
					},
				},
				Vitals: []*teslapower.DeviceVital{
					{Name: new("STSTSM-Alerts"), Value: &teslapower.DeviceVital_BoolValue{BoolValue: true}},
					{Name: new("STSTSM-Temp"), Value: &teslapower.DeviceVital_FloatValue{FloatValue: 42.5}},
				},
				Alerts: []string{"SystemConnectedToGrid"},
			},
			{
				// A second vitals record for the same DIN, carrying device
				// attributes, exercises the devMap-merge branch and every
				// DeviceAttributes oneof variant.
				Device: &teslapower.SiteControllerConnectedDevice{
					Device: &teslapower.Device{
						Din: &teslapower.StringValue{Value: "STSTSM--1232100-00-E--TG1234567890G1"},
						DeviceAttributes: &teslapower.DeviceAttributes{
							DeviceAttributes: &teslapower.DeviceAttributes_TeslaEnergyEcuAttributes{
								TeslaEnergyEcuAttributes: &teslapower.TeslaEnergyEcuAttributes{EcuType: 3},
							},
						},
					},
				},
				Vitals: []*teslapower.DeviceVital{
					{Name: new("STSTSM-Name"), Value: &teslapower.DeviceVital_StringValue{StringValue: "gateway"}},
					{Name: new("STSTSM-Count"), Value: &teslapower.DeviceVital_IntValue{IntValue: 7}},
					{Name: new(""), Value: &teslapower.DeviceVital_IntValue{IntValue: 99}},
				},
			},
			{
				Device: &teslapower.SiteControllerConnectedDevice{
					Device: &teslapower.Device{
						Din: &teslapower.StringValue{Value: "PVAC--1"},
						DeviceAttributes: &teslapower.DeviceAttributes{
							DeviceAttributes: &teslapower.DeviceAttributes_GeneratorAttributes{
								GeneratorAttributes: &teslapower.GeneratorAttributes{
									NameplateRealPowerW:      1000,
									NameplateApparentPowerVa: 1200,
								},
							},
						},
					},
				},
			},
			{
				Device: &teslapower.SiteControllerConnectedDevice{
					Device: &teslapower.Device{
						Din: &teslapower.StringValue{Value: "PVAC--2"},
						DeviceAttributes: &teslapower.DeviceAttributes{
							DeviceAttributes: &teslapower.DeviceAttributes_PvInverterAttributes{
								PvInverterAttributes: &teslapower.PVInverterAttributes{NameplateRealPowerW: 500},
							},
						},
					},
				},
			},
			{
				Device: &teslapower.SiteControllerConnectedDevice{
					Device: &teslapower.Device{
						Din: &teslapower.StringValue{Value: "METER--1"},
						DeviceAttributes: &teslapower.DeviceAttributes{
							DeviceAttributes: &teslapower.DeviceAttributes_MeterAttributes{
								MeterAttributes: &teslapower.MeterAttributes{MeterLocation: []uint32{1, 2}},
							},
						},
					},
				},
			},
			{
				// A device with no DIN is skipped entirely.
				Device: &teslapower.SiteControllerConnectedDevice{
					Device: &teslapower.Device{},
				},
			},
		},
	}
	data, err := proto.Marshal(pb)
	require.NoError(t, err)

	return data
}

func TestVitalsProtobufDecoding(t *testing.T) {
	t.Parallel()

	t.Run("decodes a well-formed protobuf payload", func(t *testing.T) {
		t.Parallel()

		body := buildVitalsProtobuf(t)
		handler := func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/devices/vitals", r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		}
		host := hostOf(newTLSServer(t, handler))
		b := newBackend(host, models.AuthModeCookie, "")

		out, err := b.Vitals(t.Context())
		require.NoError(t, err)
		require.Contains(t, out, "STSTSM--1232100-00-E--TG1234567890G1")
		dev, ok := out["STSTSM--1232100-00-E--TG1234567890G1"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "1232100-00-E", dev["partNumber"])
		assert.Equal(t, "TG1234567890G1", dev["serialNumber"])
		assert.Equal(t, true, dev["STSTSM-Alerts"])
		assert.InDelta(t, 42.5, dev["STSTSM-Temp"], 0.001)
		assert.Equal(t, []string{"SystemConnectedToGrid"}, dev["alerts"])
		assert.Equal(t, "gateway", dev["STSTSM-Name"])
		assert.Equal(t, int64(7), dev["STSTSM-Count"])
		ecu, ok := dev["teslaEnergyEcuAttributes"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, int64(3), ecu["ecuType"])

		pvac1, ok := out["PVAC--1"].(map[string]any)
		require.True(t, ok)
		gen, ok := pvac1["generatorAttributes"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, uint64(1000), gen["nameplateRealPowerW"])

		pvac2, ok := out["PVAC--2"].(map[string]any)
		require.True(t, ok)
		inv, ok := pvac2["pvInverterAttributes"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, uint64(500), inv["nameplateRealPowerW"])

		meter, ok := out["METER--1"].(map[string]any)
		require.True(t, ok)
		meterAttrs, ok := meter["meterAttributes"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, []int64{1, 2}, meterAttrs["meterLocation"])
	})

	t.Run("malformed protobuf payload returns an error", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte{0xFF, 0xFF, 0xFF})
		}
		host := hostOf(newTLSServer(t, handler))
		b := newBackend(host, models.AuthModeCookie, "")

		_, err := b.Vitals(t.Context())
		require.Error(t, err)
	})

	t.Run("404 on vitals endpoint surfaces ErrNotFound", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}
		host := hostOf(newTLSServer(t, handler))
		b := newBackend(host, models.AuthModeCookie, "")

		_, err := b.Vitals(t.Context())
		require.ErrorIs(t, err, backend.ErrNotFound)
	})
}

func TestVitalsHybridDelegation(t *testing.T) {
	t.Parallel()

	// When a TEDAPI client is attached, Vitals must delegate to it instead of
	// polling /api/devices/vitals over the local gateway HTTP API.
	tedapiClient := tedapi.NewClient(
		unreachableHost, "gwpwd", testTimeout, testCacheTTL, testPoolSize,
		models.TEDAPIVersion2024_06, models.AuthModeCookie,
	)
	tedapiBackend := tedapi.NewBackend(tedapiClient, nil)

	b := newBackend(unreachableHost, models.AuthModeCookie, "")
	b.SetTEDAPIClient(tedapiBackend, true)

	assert.True(t, b.HasTEDAPI())
	assert.True(t, b.IsPW3())

	out, err := b.Vitals(t.Context())
	require.NoError(t, err)
	assert.Contains(t, out, "TESLA--None")
	assert.Contains(t, out, "TESYNC--None--None")
}

func TestHasTEDAPIAndIsPW3Defaults(t *testing.T) {
	t.Parallel()

	b := newBackend(unreachableHost, models.AuthModeCookie, "")
	assert.False(t, b.HasTEDAPI())
	assert.False(t, b.IsPW3())
}

func TestGetTimeRemaining(t *testing.T) {
	t.Parallel()

	type testCase struct {
		wantErrIs   error
		name        string
		systemBody  string
		aggBody     string
		wantHours   float64
		wantSuccess bool
	}

	cases := []testCase{
		{
			name:        "computes hours remaining from nominal energy and load",
			systemBody:  `{"nominal_energy_remaining":1000}`,
			aggBody:     `{"load":{"instant_power":500}}`,
			wantSuccess: true,
			wantHours:   2,
		},
		{
			name:       "missing nominal_energy_remaining returns ErrNotFound",
			systemBody: `{"other_field":1}`,
			aggBody:    `{"load":{"instant_power":500}}`,
			wantErrIs:  backend.ErrNotFound,
		},
		{
			name:       "zero load returns ErrNotFound",
			systemBody: `{"nominal_energy_remaining":1000}`,
			aggBody:    `{"load":{"instant_power":0}}`,
			wantErrIs:  backend.ErrNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/system_status":
					_, _ = w.Write([]byte(tc.systemBody))
				case "/api/meters/aggregates":
					_, _ = w.Write([]byte(tc.aggBody))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}
			host := hostOf(newTLSServer(t, handler))
			b := newBackend(host, models.AuthModeCookie, "")

			hours, err := b.GetTimeRemaining(t.Context())
			if tc.wantErrIs != nil {
				require.ErrorIs(t, err, tc.wantErrIs)

				return
			}
			require.NoError(t, err)
			require.NotNil(t, hours)
			assert.InDelta(t, tc.wantHours, *hours, 0.001)
		})
	}
}

func TestGetTimeRemainingPollError(t *testing.T) {
	t.Parallel()

	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}
	host := hostOf(newTLSServer(t, handler))
	b := newBackend(host, models.AuthModeCookie, "")

	_, err := b.GetTimeRemaining(t.Context())
	require.ErrorIs(t, err, backend.ErrNotFound)
}

func TestPower(t *testing.T) {
	t.Parallel()

	body := readFixture(t, "api.meters.aggregates.json")
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/meters/aggregates", r.URL.Path)
		_, _ = w.Write(body)
	}
	host := hostOf(newTLSServer(t, handler))
	b := newBackend(host, models.AuthModeCookie, "")

	p, err := b.Power(t.Context())
	require.NoError(t, err)
	assert.InDelta(t, 27.0, p["site"], 0.001)
	assert.InDelta(t, -990.0, p["battery"], 0.001)
	assert.InDelta(t, 866.25, p["load"], 0.001)
}

func TestPowerPollError(t *testing.T) {
	t.Parallel()

	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}
	host := hostOf(newTLSServer(t, handler))
	b := newBackend(host, models.AuthModeCookie, "")

	p, err := b.Power(t.Context())
	require.NoError(t, err)
	assert.Equal(t, map[string]float64{"site": 0, "solar": 0, "battery": 0, "load": 0}, p)
}

func TestFetchPower(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		sensor  string
		verbose bool
	}

	cases := []testCase{
		{name: "verbose returns raw sensor payload", sensor: "site", verbose: true},
		{name: "non-verbose returns instant power float", sensor: "site", verbose: false},
	}

	body := readFixture(t, "api.meters.aggregates.json")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(body)
			}
			host := hostOf(newTLSServer(t, handler))
			b := newBackend(host, models.AuthModeCookie, "")

			res, err := b.FetchPower(t.Context(), tc.sensor, tc.verbose)
			require.NoError(t, err)
			if tc.verbose {
				m, ok := res.(map[string]any)
				require.True(t, ok)
				assert.Contains(t, m, "instant_power")

				return
			}
			f, ok := res.(float64)
			require.True(t, ok)
			assert.InDelta(t, 27.0, f, 0.001)
		})
	}
}

// TestLoadAuthCacheDataTypeMismatch verifies token-mode loading rejects a
// header value that doesn't split into exactly two "Bearer <token>" parts.
func TestLoadAuthCacheDataTypeMismatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cachefile := writeFile(t, dir, "auth.json", `{"Authorization":"MalformedNoSpaceValue"}`)
	host := hostOf(newTLSServer(t, tokenLoginHandler(t)))

	b := newBackend(host, models.AuthModeToken, cachefile)
	require.NoError(t, b.Authenticate(t.Context()))
}

// TestTokenModeAppliesBearerHeader proves a freshly logged-in token-mode
// backend sends the bearer token on subsequent requests.
func TestTokenModeAppliesBearerHeader(t *testing.T) {
	t.Parallel()

	var gotAuth string
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login/Basic":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"tok-abc"}`))
		case "/api/status":
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
	host := hostOf(newTLSServer(t, handler))
	b := newBackend(host, models.AuthModeToken, "")
	require.NoError(t, b.Authenticate(t.Context()))

	_, err := b.Poll(t.Context(), "/api/status", false, false, false)
	require.NoError(t, err)
	assert.Equal(t, "Bearer tok-abc", gotAuth)
}

// TestSaveAuthCacheWriteFailureIsNonFatal proves that Authenticate still
// succeeds when the cache file cannot be written (e.g. its parent directory
// does not exist), since caching the session is a best-effort optimization.
func TestSaveAuthCacheWriteFailureIsNonFatal(t *testing.T) {
	t.Parallel()

	cachefile := filepath.Join(t.TempDir(), "missing-parent", "auth.json")
	host := hostOf(newTLSServer(t, cookieLoginHandler(t)))

	b := newBackend(host, models.AuthModeCookie, cachefile)
	require.NoError(t, b.Authenticate(t.Context()))
}
