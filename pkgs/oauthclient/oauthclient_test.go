package oauthclient_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/blackbirdworks/gopowerwall/pkgs/oauthclient"
)

const testTimeout = 5 * time.Second

func TestTokenFromFields(t *testing.T) {
	t.Parallel()

	type testCase struct {
		wantErrIs  error
		fields     map[string]any
		name       string
		wantAccess string
		wantExpiry bool
	}

	cases := []testCase{
		{
			name:       "access and refresh token, no expiry",
			fields:     map[string]any{"access_token": "at-1", "refresh_token": "rt-1"},
			wantAccess: "at-1",
		},
		{
			name: "absolute expiry",
			fields: map[string]any{
				"access_token": "at-1", "refresh_token": "rt-1",
				"expires_at": float64(time.Now().Add(time.Hour).Unix()),
			},
			wantAccess: "at-1",
			wantExpiry: true,
		},
		{
			name: "relative expiry",
			fields: map[string]any{
				"access_token": "at-1", "refresh_token": "rt-1",
				"expires_in": float64(3600),
			},
			wantAccess: "at-1",
			wantExpiry: true,
		},
		{
			name:   "empty access token still valid for bootstrap",
			fields: map[string]any{"access_token": "", "refresh_token": "rt-1"},
		},
		{
			name:      "missing refresh token",
			fields:    map[string]any{"access_token": "at-1"},
			wantErrIs: oauthclient.ErrMissingRefreshToken,
		},
		{
			name:      "empty fields",
			fields:    map[string]any{},
			wantErrIs: oauthclient.ErrMissingRefreshToken,
		},
		{
			name: "nested sso map from pypowerwall auth format",
			fields: map[string]any{
				"url": "https://auth.tesla.com/",
				"sso": map[string]any{
					"access_token":  "sso-at",
					"refresh_token": "sso-rt",
					"token_type":    "Bearer",
					"expires_in":    int64(3600),
				},
			},
			wantAccess: "sso-at",
			wantExpiry: true,
		},
		{
			name: "nested token map from fleetapi config format",
			fields: map[string]any{
				"client_id": "cid",
				"token": map[string]any{
					"access_token":  "tok-at",
					"refresh_token": "tok-rt",
					"expires_at":    int(1800000000),
				},
			},
			wantAccess: "tok-at",
			wantExpiry: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tok, err := oauthclient.TokenFromFields(tc.fields)
			if tc.wantErrIs != nil {
				require.ErrorIs(t, err, tc.wantErrIs)

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantAccess, tok.AccessToken)
			assert.Equal(t, tc.wantExpiry, !tok.Expiry.IsZero())
		})
	}
}

func TestMergeToken(t *testing.T) {
	t.Parallel()

	type testCase struct {
		fields map[string]any
		tok    *oauth2.Token
		want   map[string]any
		name   string
	}

	expiry := time.Unix(1700000000, 0)

	cases := []testCase{
		{
			name:   "nil fields creates a new map",
			fields: nil,
			tok:    &oauth2.Token{AccessToken: "at", RefreshToken: "rt"},
			want:   map[string]any{"access_token": "at", "refresh_token": "rt"},
		},
		{
			name:   "preserves unrelated fields",
			fields: map[string]any{"site_id": "site-1", "access_token": "old"},
			tok:    &oauth2.Token{AccessToken: "new", RefreshToken: "rt"},
			want:   map[string]any{"site_id": "site-1", "access_token": "new", "refresh_token": "rt"},
		},
		{
			name:   "includes expiry when set",
			fields: map[string]any{},
			tok:    &oauth2.Token{AccessToken: "at", RefreshToken: "rt", Expiry: expiry},
			want:   map[string]any{"access_token": "at", "refresh_token": "rt", "expires_at": expiry.Unix()},
		},
		{
			name:   "empty refresh token is not overwritten with empty string",
			fields: map[string]any{"refresh_token": "keep-me"},
			tok:    &oauth2.Token{AccessToken: "at"},
			want:   map[string]any{"access_token": "at", "refresh_token": "keep-me"},
		},
		{
			name: "updates nested sso map in place",
			fields: map[string]any{
				"url": "https://auth.tesla.com/",
				"sso": map[string]any{
					"access_token":  "old-at",
					"refresh_token": "old-rt",
				},
			},
			tok: &oauth2.Token{AccessToken: "new-at", RefreshToken: "new-rt", Expiry: expiry},
			want: map[string]any{
				"url":           "https://auth.tesla.com/",
				"access_token":  "new-at",
				"refresh_token": "new-rt",
				"expires_at":    expiry.Unix(),
				"sso": map[string]any{
					"access_token":  "new-at",
					"refresh_token": "new-rt",
					"expires_at":    expiry.Unix(),
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := oauthclient.MergeToken(tc.fields, tc.tok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// tokenEndpoint returns an httptest server standing in for Tesla's OAuth2
// token endpoint, along with a counter of how many refresh requests it
// received.
func tokenEndpoint(t *testing.T, accessToken string) (*httptest.Server, *atomic.Int64) {
	t.Helper()

	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.NoError(t, r.ParseForm())
		assert.Equal(t, "refresh_token", r.PostForm.Get("grant_type"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  accessToken,
			"refresh_token": r.PostForm.Get("refresh_token"),
			"token_type":    "Bearer",
			"expires_in":    28800,
		})
	}))
	t.Cleanup(srv.Close)

	return srv, &calls
}

func TestNewRefreshesExpiredTokenExactlyOnce(t *testing.T) {
	t.Parallel()

	srv, calls := tokenEndpoint(t, "at-refreshed")

	apiCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls++
		assert.Equal(t, "Bearer at-refreshed", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(api.Close)

	var refreshed []*oauth2.Token
	client := oauthclient.New(t.Context(), oauthclient.Config{
		ClientID: "ownerapi",
		TokenURL: srv.URL,
		Timeout:  testTimeout,
		Token: &oauth2.Token{
			AccessToken:  "", // empty access token forces an immediate refresh
			RefreshToken: "rt-1",
		},
		OnRefresh: func(tok *oauth2.Token) error {
			refreshed = append(refreshed, tok)

			return nil
		},
	})

	for range 3 {
		resp, err := client.Get(api.URL)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}

	assert.Equal(t, int64(1), calls.Load(), "the token endpoint should be hit exactly once")
	assert.Equal(t, 3, apiCalls)
	require.Len(t, refreshed, 1, "OnRefresh should fire exactly once")
	assert.Equal(t, "at-refreshed", refreshed[0].AccessToken)
}

func TestNewDoesNotRefreshAValidToken(t *testing.T) {
	t.Parallel()

	srv, calls := tokenEndpoint(t, "should-not-be-used")

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer at-valid", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(api.Close)

	var onRefreshCalls int
	client := oauthclient.New(t.Context(), oauthclient.Config{
		ClientID: "ownerapi",
		TokenURL: srv.URL,
		Timeout:  testTimeout,
		Token: &oauth2.Token{
			AccessToken:  "at-valid",
			RefreshToken: "rt-1",
			Expiry:       time.Now().Add(time.Hour),
		},
		OnRefresh: func(*oauth2.Token) error {
			onRefreshCalls++

			return nil
		},
	})

	resp, err := client.Get(api.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, int64(0), calls.Load(), "a valid token must not trigger a refresh")
	assert.Equal(t, 0, onRefreshCalls)
}

func TestNewOnRefreshFailureDoesNotFailTheRequest(t *testing.T) {
	t.Parallel()

	srv, _ := tokenEndpoint(t, "at-refreshed")

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(api.Close)

	client := oauthclient.New(t.Context(), oauthclient.Config{
		ClientID: "ownerapi",
		TokenURL: srv.URL,
		Timeout:  testTimeout,
		Token: &oauth2.Token{
			RefreshToken: "rt-1",
		},
		OnRefresh: func(*oauth2.Token) error {
			return assert.AnError
		},
	})

	resp, err := client.Get(api.URL)
	require.NoError(t, err, "a persistence failure must not fail the API call that triggered the refresh")
	require.NoError(t, resp.Body.Close())
}

func TestNewWithoutOnRefresh(t *testing.T) {
	t.Parallel()

	srv, calls := tokenEndpoint(t, "at-refreshed")

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer at-refreshed", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(api.Close)

	client := oauthclient.New(t.Context(), oauthclient.Config{
		ClientID: "ownerapi",
		TokenURL: srv.URL,
		Timeout:  testTimeout,
		Token:    &oauth2.Token{RefreshToken: "rt-1"},
	})

	resp, err := client.Get(api.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, int64(1), calls.Load())
}
