// Package oauthclient builds HTTP clients that transparently attach and
// refresh Tesla OAuth2 bearer tokens, and notifies callers whenever the
// access token changes so it can be persisted to disk. Tesla access tokens
// expire after a few hours, so a long-running process (such as the proxy
// running 24/7 in cloud mode) must refresh them automatically rather than
// pinning a token read once at startup.
package oauthclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
)

// ErrMissingRefreshToken indicates an auth file has no refresh_token, so an
// expired access token could never be renewed automatically.
var ErrMissingRefreshToken = errors.New("missing refresh_token in auth file")

// Config configures an OAuth2-backed HTTP client for a Tesla token endpoint.
type Config struct {
	// Token is the current (possibly stale) token loaded from disk.
	Token *oauth2.Token
	// OnRefresh, when non-nil, is called with the new token whenever the
	// access token changes, so the caller can persist it. It is called at
	// most once per actual refresh, even when multiple requests race to
	// refresh concurrently. A returned error is logged, not propagated: a
	// transient failure to persist a refreshed token must not block the API
	// call that triggered the refresh.
	OnRefresh func(*oauth2.Token) error
	// ClientID is the OAuth2 client id Tesla expects for the refresh_token
	// grant (e.g. "ownerapi" for Owner API tokens, or a Fleet API
	// application's registered client id).
	ClientID string
	// TokenURL is Tesla's OAuth2 token endpoint.
	TokenURL string
	// Timeout bounds every HTTP call made by the returned client, including
	// token refresh requests.
	Timeout time.Duration
}

// New returns an *http.Client that attaches a bearer token from cfg.Token to
// every request, transparently refreshing it via the OAuth2 refresh_token
// grant once it expires, and invoking cfg.OnRefresh whenever the access
// token changes.
func New(ctx context.Context, cfg Config) *http.Client {
	// oauth2.Config.TokenSource only ever uses a context-supplied client for
	// token acquisition - never for requests made through the client this
	// function returns - so this is the only place the configured timeout
	// reaches token refresh requests.
	base := &http.Client{Timeout: cfg.Timeout}
	tokenCtx := context.WithValue(ctx, oauth2.HTTPClient, base)

	oauthCfg := &oauth2.Config{
		ClientID: cfg.ClientID,
		Endpoint: oauth2.Endpoint{TokenURL: cfg.TokenURL},
	}

	src := oauthCfg.TokenSource(tokenCtx, cfg.Token)
	if cfg.OnRefresh != nil {
		src = &notifyingSource{
			ctx:       ctx,
			base:      src,
			onRefresh: cfg.OnRefresh,
			last:      cfg.Token.AccessToken,
		}
	}

	return &http.Client{
		Timeout:   cfg.Timeout,
		Transport: &oauth2.Transport{Source: src},
	}
}

// notifyingSource wraps an oauth2.TokenSource and calls onRefresh whenever
// the access token changes, so callers can persist a refreshed token. ctx is
// captured at construction time because oauth2.TokenSource.Token takes none,
// and is used only to carry the logger for a failed-persist warning - never
// for cancellation.
type notifyingSource struct {
	ctx       context.Context
	base      oauth2.TokenSource
	onRefresh func(*oauth2.Token) error
	last      string
	mu        sync.Mutex // guards last
}

func (n *notifyingSource) Token() (*oauth2.Token, error) {
	tok, err := n.base.Token()
	if err != nil {
		return nil, fmt.Errorf("refresh oauth2 token: %w", err)
	}

	n.mu.Lock()
	changed := tok.AccessToken != n.last
	if changed {
		n.last = tok.AccessToken
	}
	n.mu.Unlock()

	if changed {
		if persistErr := n.onRefresh(tok); persistErr != nil {
			logger.Load(n.ctx).ErrorContext(n.ctx, "failed to persist refreshed oauth2 token", "error", persistErr)
		}
	}

	return tok, nil
}

// TokenFromFields builds an oauth2.Token from a generic auth-file field map
// (as decoded from JSON into map[string]any), reading "access_token",
// "refresh_token", "token_type", and either "expires_at" (absolute Unix
// seconds) or "expires_in" (seconds from now). It returns
// [ErrMissingRefreshToken] when no refresh token is present, since a token
// that can never be renewed defeats the purpose of a long-running cloud
// connection.
func TokenFromFields(fields map[string]any) (*oauth2.Token, error) {
	refresh, _ := fields["refresh_token"].(string)
	if refresh == "" {
		return nil, ErrMissingRefreshToken
	}

	access, _ := fields["access_token"].(string)
	tokenType, _ := fields["token_type"].(string)

	tok := &oauth2.Token{
		AccessToken:  access,
		RefreshToken: refresh,
		TokenType:    tokenType,
	}

	switch {
	case setAbsoluteExpiry(tok, fields["expires_at"]):
	case setRelativeExpiry(tok, fields["expires_in"]):
	}

	return tok, nil
}

func setAbsoluteExpiry(tok *oauth2.Token, raw any) bool {
	expiresAt, ok := raw.(float64)
	if !ok || expiresAt <= 0 {
		return false
	}
	tok.Expiry = time.Unix(int64(expiresAt), 0)

	return true
}

func setRelativeExpiry(tok *oauth2.Token, raw any) bool {
	expiresIn, ok := raw.(float64)
	if !ok || expiresIn <= 0 {
		return false
	}
	tok.Expiry = time.Now().Add(time.Duration(expiresIn) * time.Second)

	return true
}

// MergeToken writes tok's fields into fields in place (creating a new map
// when fields is nil) and returns it for convenient chaining. Fields
// unrelated to the token, such as a cached site ID, are left untouched.
func MergeToken(fields map[string]any, tok *oauth2.Token) map[string]any {
	if fields == nil {
		fields = make(map[string]any)
	}
	fields["access_token"] = tok.AccessToken
	if tok.RefreshToken != "" {
		fields["refresh_token"] = tok.RefreshToken
	}
	if tok.TokenType != "" {
		fields["token_type"] = tok.TokenType
	}
	if !tok.Expiry.IsZero() {
		fields["expires_at"] = tok.Expiry.Unix()
	}

	return fields
}
