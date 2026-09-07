package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbirdworks/gopowerwall/backend"
	"github.com/blackbirdworks/gopowerwall/client"
	"github.com/blackbirdworks/gopowerwall/models"
)

var errMockFailure = errors.New("mock reauth failure")

type sampleData struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func TestClientConfig(t *testing.T) {
	t.Parallel()

	c := client.New(client.Options{
		BaseURL:     "http://example.com/",
		Timeout:     0,
		CacheTTL:    50 * time.Millisecond,
		MaxIdleConn: 0,
		Insecure:    true,
		AuthMode:    models.AuthModeToken,
	})
	defer func() {
		_ = c.Close()
	}()

	if c.BaseURL() != "http://example.com" {
		t.Errorf("unexpected base URL: %s", c.BaseURL())
	}

	c.SetBaseURL("http://new-example.com/")
	if c.BaseURL() != "http://new-example.com" {
		t.Errorf("unexpected updated base URL: %s", c.BaseURL())
	}

	c.SetAuthToken("test-token-123")
	if c.AuthToken() != "test-token-123" {
		t.Errorf("unexpected auth token: %s", c.AuthToken())
	}

	c.SetBasicAuth("user", "pass")

	cookie := &http.Cookie{Name: "session", Value: "123"}
	c.SetCookies([]*http.Cookie{cookie})
	cookies := c.Cookies()
	if len(cookies) != 1 || cookies[0].Value != "123" {
		t.Errorf("unexpected cookies: %v", cookies)
	}

	c.SetDefaultHeader("X-Custom", "Val")

	if c.InCooldown() {
		t.Error("expected not in cooldown initially")
	}

	c.SetCooldown(100 * time.Millisecond)
	if !c.InCooldown() {
		t.Error("expected in cooldown")
	}
	time.Sleep(110 * time.Millisecond)
	if c.InCooldown() {
		t.Error("expected cooldown expired")
	}
}

func TestClientGetRaw(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/raw":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("raw-bytes-content"))
		case "/notfound":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	c := client.New(client.Options{
		BaseURL:  server.URL,
		Timeout:  2 * time.Second,
		CacheTTL: 100 * time.Millisecond,
	})
	defer func() {
		_ = c.Close()
	}()

	ctx := context.Background()

	// 1. Success fetch
	data, err := client.GetRaw(ctx, c, "/raw")
	if err != nil || string(data) != "raw-bytes-content" {
		t.Fatalf("unexpected GetRaw result: data=%s err=%v", string(data), err)
	}

	// 2. Cache hit
	dataCached, errCached := client.GetRaw(ctx, c, "/raw")
	if errCached != nil || string(dataCached) != "raw-bytes-content" {
		t.Fatalf("unexpected cached GetRaw result: %v", errCached)
	}

	// 3. Force refresh
	dataForced, errForced := client.GetRaw(ctx, c, "/raw", client.WithForceRefresh())
	if errForced != nil || string(dataForced) != "raw-bytes-content" {
		t.Fatalf("unexpected forced GetRaw result: %v", errForced)
	}

	// 4. Not found with negative cache
	_, err404 := client.GetRaw(ctx, c, "/notfound", client.WithTTL(50*time.Millisecond))
	if !errors.Is(err404, backend.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err404)
	}
	// Negative cache hit
	_, errNeg := client.GetRaw(ctx, c, "/notfound")
	if !errors.Is(errNeg, backend.ErrNotFound) {
		t.Fatalf("expected ErrNotFound from negative cache, got %v", errNeg)
	}

	// Invalidate key and clear cache
	c.InvalidateKey("/raw")
	c.ClearCache()
}

func TestClientPostRaw(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)

			return
		}
		if r.Header.Get("Content-Type") != "application/octet-stream" {
			w.WriteHeader(http.StatusBadRequest)

			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("posted"))
	}))
	defer server.Close()

	c := client.New(client.Options{
		BaseURL: server.URL,
		Timeout: 2 * time.Second,
	})
	defer func() {
		_ = c.Close()
	}()

	ctx := context.Background()
	res, err := client.PostRaw(ctx, c, "/upload", []byte("data"), "application/octet-stream")
	if err != nil || string(res) != "posted" {
		t.Fatalf("unexpected PostRaw result: %s, %v", string(res), err)
	}
}

func TestClientJSONOperations(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/data":
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(sampleData{Name: "tesla", Value: 42})

				return
			}
			if r.Method == http.MethodPost {
				var req sampleData
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					w.WriteHeader(http.StatusBadRequest)

					return
				}
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(sampleData{Name: req.Name + "-reply", Value: req.Value * 2})

				return
			}
		case "/api/empty":
			w.WriteHeader(http.StatusOK)

			return
		case "/api/badjson":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{invalid-json"))

			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := client.New(client.Options{
		BaseURL:  server.URL,
		Timeout:  2 * time.Second,
		CacheTTL: 100 * time.Millisecond,
	})
	defer func() {
		_ = c.Close()
	}()

	ctx := context.Background()

	// GetJSON success
	item, err := client.GetJSON[sampleData](ctx, c, "/api/data", client.WithHeader("X-Test", "1"))
	if err != nil || item.Name != "tesla" || item.Value != 42 {
		t.Fatalf("unexpected GetJSON result: %+v, %v", item, err)
	}

	// GetJSON cache hit
	cachedItem, cachedErr := client.GetJSON[sampleData](ctx, c, "/api/data")
	if cachedErr != nil || cachedItem.Name != "tesla" || cachedItem.Value != 42 {
		t.Fatalf("unexpected cached GetJSON result: %+v, %v", cachedItem, cachedErr)
	}

	// PostJSON success
	postRes, postErr := client.PostJSON[sampleData](ctx, c, "/api/data", sampleData{Name: "in", Value: 10})
	if postErr != nil || postRes.Name != "in-reply" || postRes.Value != 20 {
		t.Fatalf("unexpected PostJSON result: %+v, %v", postRes, postErr)
	}

	// PostJSON empty response
	emptyRes, emptyErr := client.PostJSON[sampleData](ctx, c, "/api/empty", sampleData{})
	if emptyErr != nil || emptyRes.Name != "" || emptyRes.Value != 0 {
		t.Fatalf("unexpected empty PostJSON result: %+v, %v", emptyRes, emptyErr)
	}

	// Bad JSON
	_, badErr := client.GetJSON[sampleData](ctx, c, "/api/badjson")
	if badErr == nil {
		t.Fatal("expected error on bad JSON unmarshal")
	}
}

func TestClientAuthModesAndReauth(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		cookie, _ := r.Cookie("session_token")

		if r.URL.Path == "/public" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("public-ok"))

			return
		}

		if r.URL.Path == "/protected" {
			count := attempts.Add(1)
			if count == 1 {
				w.WriteHeader(http.StatusUnauthorized)

				return
			}
			if auth == "Bearer renewed-token" || (cookie != nil && cookie.Value == "renewed-cookie") {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("auth-ok"))

				return
			}
			w.WriteHeader(http.StatusForbidden)

			return
		}

		if r.URL.Path == "/rate-limit" {
			w.WriteHeader(http.StatusTooManyRequests)

			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := client.New(client.Options{
		BaseURL:  server.URL,
		Timeout:  2 * time.Second,
		AuthMode: models.AuthModeBearer,
	})
	defer func() {
		_ = c.Close()
	}()

	c.SetAuthToken("initial-token")
	c.SetReauthFunc(func(_ context.Context, cl *client.Client) error {
		cl.SetAuthToken("renewed-token")

		return nil
	})

	ctx := context.Background()

	// WithSkipAuth for public endpoint
	pub, err := client.GetRaw(ctx, c, "/public", client.WithSkipAuth())
	if err != nil || string(pub) != "public-ok" {
		t.Fatalf("unexpected WithSkipAuth response: %s, %v", string(pub), err)
	}

	// Protected endpoint triggers reauth and retry
	res, err := client.GetRaw(ctx, c, "/protected")
	if err != nil || string(res) != "auth-ok" {
		t.Fatalf("unexpected protected response: %s, %v", string(res), err)
	}

	// Rate limit triggering cooldown
	_, rlErr := client.GetRaw(ctx, c, "/rate-limit")
	if !errors.Is(rlErr, backend.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", rlErr)
	}

	// Subsequent call in cooldown immediately rejected when not served from cache
	_, cdErr := client.GetRaw(ctx, c, "/public", client.WithForceRefresh())
	if !errors.Is(cdErr, backend.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited during cooldown, got %v", cdErr)
	}
}

func TestClientErrorConditions(t *testing.T) {
	t.Parallel()

	c := client.New(client.Options{
		BaseURL: "http://127.0.0.1:9", // Unreachable port
		Timeout: 50 * time.Millisecond,
	})
	defer func() {
		_ = c.Close()
	}()

	// Network failure
	_, err := client.GetRaw(context.Background(), c, "/fail")
	if err == nil {
		t.Fatal("expected network error")
	}

	// Context canceled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, ctxErr := client.GetRaw(ctx, c, "/fail")
	if ctxErr == nil || !errors.Is(ctxErr, backend.ErrTimeout) {
		t.Fatalf("expected ErrTimeout wrapping context cancel, got %v", ctxErr)
	}
}

func TestClientAuthCookieAndBasic(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if ok && user == "tesla_user" && pass == "secret" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("basic-auth-ok"))

			return
		}

		c, err := r.Cookie("session_cookie")
		if err == nil && c.Value == "cookie-123" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("cookie-auth-ok"))

			return
		}

		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	// 1. Basic Auth test
	cBasic := client.New(client.Options{
		BaseURL:  server.URL,
		AuthMode: models.AuthModeBasic,
	})
	defer func() { _ = cBasic.Close() }()
	cBasic.SetBasicAuth("tesla_user", "secret")

	resBasic, errBasic := client.GetRaw(context.Background(), cBasic, "/test")
	if errBasic != nil || string(resBasic) != "basic-auth-ok" {
		t.Fatalf("unexpected basic auth response: %s, %v", string(resBasic), errBasic)
	}

	// 2. Cookie Auth test
	cCookie := client.New(client.Options{
		BaseURL:  server.URL,
		AuthMode: models.AuthModeCookie,
	})
	defer func() { _ = cCookie.Close() }()
	cCookie.SetCookies([]*http.Cookie{{Name: "session_cookie", Value: "cookie-123"}})

	resCookie, errCookie := client.GetRaw(context.Background(), cCookie, "/test")
	if errCookie != nil || string(resCookie) != "cookie-auth-ok" {
		t.Fatalf("unexpected cookie auth response: %s, %v", string(resCookie), errCookie)
	}
}

func TestClientJSONNegativeCacheAndErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/notfound-json" {
			w.WriteHeader(http.StatusNotFound)

			return
		}
		if r.URL.Path == "/err-post" {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := client.New(client.Options{
		BaseURL: server.URL,
	})
	defer func() { _ = c.Close() }()

	ctx := context.Background()

	// 1. GetJSON 404
	_, err404 := client.GetJSON[sampleData](ctx, c, "/notfound-json")
	if !errors.Is(err404, backend.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err404)
	}

	// 2. GetJSON negative cache hit
	_, errNeg := client.GetJSON[sampleData](ctx, c, "/notfound-json")
	if !errors.Is(errNeg, backend.ErrNotFound) {
		t.Fatalf("expected ErrNotFound from negative cache, got %v", errNeg)
	}

	// 3. PostJSON failure
	_, errPost := client.PostJSON[sampleData](ctx, c, "/err-post", sampleData{})
	if errPost == nil {
		t.Fatal("expected error on 500 POST")
	}

	// 4. PostRaw failure
	_, errPostRaw := client.PostRaw(ctx, c, "/err-post", []byte("bad"), "text/plain")
	if errPostRaw == nil {
		t.Fatal("expected error on 500 PostRaw")
	}
}

func TestClientAdditionalStatusBranches(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/503":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/403":
			w.WriteHeader(http.StatusForbidden)
		case "/502":
			w.WriteHeader(http.StatusBadGateway)
		case "/fail-reauth":
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	c := client.New(client.Options{
		BaseURL: server.URL,
	})
	defer func() { _ = c.Close() }()

	ctx := context.Background()

	// 503 Service Unavailable
	_, err503 := client.GetRaw(ctx, c, "/503")
	if !errors.Is(err503, backend.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited on 503, got %v", err503)
	}

	// Reset cooldown
	c.SetCooldown(-1 * time.Second)

	// 403 Forbidden
	_, err403 := client.GetRaw(ctx, c, "/403", client.WithForceRefresh())
	if !errors.Is(err403, backend.ErrLogin) {
		t.Fatalf("expected ErrLogin on 403, got %v", err403)
	}

	// 502 Bad Gateway (unexpected status)
	_, err502 := client.GetRaw(ctx, c, "/502", client.WithForceRefresh())
	if !errors.Is(err502, backend.ErrUnexpectedStatus) {
		t.Fatalf("expected ErrUnexpectedStatus on 502, got %v", err502)
	}

	// 401 with failing reauth callback
	c.SetReauthFunc(func(_ context.Context, _ *client.Client) error {
		return errMockFailure
	})
	_, errReauthFail := client.GetRaw(ctx, c, "/fail-reauth", client.WithForceRefresh())
	if !errors.Is(errReauthFail, backend.ErrLogin) {
		t.Fatalf("expected ErrLogin when reauth fails, got %v", errReauthFail)
	}
}
