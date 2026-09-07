package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/gopowerwall/backend"
	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/cache"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
)

const (
	defaultCooldownDuration = 5 * time.Minute
	defaultRequestTimeout   = 10 * time.Second
	defaultMaxIdleConns     = 10
	contentTypeJSON         = "application/json"
	headerAuthorization     = "Authorization"
	headerContentType       = "Content-Type"
	headerUserAgent         = "User-Agent"
	defaultUserAgent        = "github.com/blackbirdworks/gopowerwall/1.0"
)

// ReauthFunc is invoked when an unauthorized response is received to refresh tokens or session.
type ReauthFunc func(ctx context.Context, c *Client) error

// RequestConfig represents execution options for a request.
type RequestConfig struct {
	Headers      map[string]string
	TTL          time.Duration
	ForceRefresh bool
	SkipAuth     bool
}

// RequestOption customizes request execution.
type RequestOption func(*RequestConfig)

// WithTTL sets a custom cache TTL for the request.
func WithTTL(d time.Duration) RequestOption {
	return func(cfg *RequestConfig) {
		cfg.TTL = d
	}
}

// WithForceRefresh bypasses the cache.
func WithForceRefresh() RequestOption {
	return func(cfg *RequestConfig) {
		cfg.ForceRefresh = true
	}
}

// WithHeader adds a header to the request.
func WithHeader(k, v string) RequestOption {
	return func(cfg *RequestConfig) {
		if cfg.Headers == nil {
			cfg.Headers = make(map[string]string)
		}
		cfg.Headers[k] = v
	}
}

// WithSkipAuth bypasses authentication headers/cookies for public endpoints.
func WithSkipAuth() RequestOption {
	return func(cfg *RequestConfig) {
		cfg.SkipAuth = true
	}
}

// Options configures a new Client instance.
type Options struct {
	BaseURL     string
	AuthMode    models.AuthMode
	Timeout     time.Duration
	CacheTTL    time.Duration
	MaxIdleConn int
	Insecure    bool
}

// Client provides a generic, authenticated HTTP client with caching, cooldown, and auto-reauth.
type Client struct {
	cooldownUntil time.Time
	cache         *cache.ResponseCache
	httpClient    *http.Client
	reauthFunc    ReauthFunc
	headers       map[string]string
	baseURL       string
	token         string
	username      string
	password      string
	authMode      models.AuthMode
	cookies       []*http.Cookie
	mu            sync.RWMutex
}

// New creates and initializes a generic Client.
func New(opts Options) *Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}

	maxIdle := opts.MaxIdleConn
	if maxIdle <= 0 {
		maxIdle = defaultMaxIdleConns
	}

	//nolint:gosec // Powerwall uses self-signed certs
	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: opts.Insecure},
		MaxIdleConns:        maxIdle,
		MaxIdleConnsPerHost: maxIdle,
		DisableKeepAlives:   maxIdle == 0,
	}

	return &Client{
		baseURL:    strings.TrimRight(opts.BaseURL, "/"),
		httpClient: &http.Client{Transport: transport, Timeout: timeout},
		cache:      cache.NewResponseCache(opts.CacheTTL),
		authMode:   opts.AuthMode,
		headers:    make(map[string]string),
	}
}

// SetBaseURL updates the base URL.
func (c *Client) SetBaseURL(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.baseURL = strings.TrimRight(url, "/")
}

// BaseURL returns the configured base URL.
func (c *Client) BaseURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.baseURL
}

// SetAuthToken updates the bearer authentication token.
func (c *Client) SetAuthToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = token
	c.authMode = models.AuthModeBearer
}

// AuthToken returns the active bearer token.
func (c *Client) AuthToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.token
}

// SetBasicAuth configures basic username/password credentials.
func (c *Client) SetBasicAuth(username, password string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.username = username
	c.password = password
	c.authMode = models.AuthModeBasic
}

// SetCookies sets the session cookies.
func (c *Client) SetCookies(cookies []*http.Cookie) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cookies = make([]*http.Cookie, len(cookies))
	copy(c.cookies, cookies)
	c.authMode = models.AuthModeCookie
}

// Cookies returns a copy of the stored session cookies.
func (c *Client) Cookies() []*http.Cookie {
	c.mu.RLock()
	defer c.mu.RUnlock()

	res := make([]*http.Cookie, len(c.cookies))
	copy(res, c.cookies)

	return res
}

// SetReauthFunc configures the re-authentication handler.
func (c *Client) SetReauthFunc(fn ReauthFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reauthFunc = fn
}

// SetDefaultHeader configures a persistent default header.
func (c *Client) SetDefaultHeader(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.headers[key] = value
}

// InCooldown reports whether the client is currently in rate-limit cooldown.
func (c *Client) InCooldown() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return time.Now().Before(c.cooldownUntil)
}

// SetCooldown manually sets a cooldown duration.
func (c *Client) SetCooldown(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cooldownUntil = time.Now().Add(d)
}

// ClearCache clears the client response cache.
func (c *Client) ClearCache() {
	c.cache.Clear()
}

// InvalidateKey evicts a single path from cache.
func (c *Client) InvalidateKey(path string) {
	c.cache.Invalidate(path)
}

// Close releases client resources and stops the cache cleanup worker.
func (c *Client) Close() error {
	c.cache.Close()

	return nil
}

// prepareRequest constructs an *http.Request with headers, cookies, and context.
func (c *Client) prepareRequest(
	ctx context.Context,
	method, fullURL string,
	body io.Reader,
	cfg *RequestConfig,
) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set(headerUserAgent, defaultUserAgent)

	c.mu.RLock()
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	if !cfg.SkipAuth {
		switch c.authMode {
		case models.AuthModeBearer, models.AuthModeToken:
			if c.token != "" {
				req.Header.Set(headerAuthorization, "Bearer "+c.token)
			}
		case models.AuthModeBasic:
			if c.username != "" || c.password != "" {
				req.SetBasicAuth(c.username, c.password)
			}
		case models.AuthModeCookie:
			for _, cookie := range c.cookies {
				req.AddCookie(cookie)
			}
		}
	}
	c.mu.RUnlock()

	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	return req, nil
}

// tryReauth invokes the reauth callback on 401 Unauthorized and retries the request once.
func (c *Client) tryReauth(
	ctx context.Context,
	method, endpoint string,
	body []byte,
	cfg *RequestConfig,
) *http.Response {
	c.mu.RLock()
	reauth := c.reauthFunc
	c.mu.RUnlock()

	if reauth == nil || cfg.SkipAuth {
		return nil
	}

	logger.LogDebug("Received 401 Unauthorized - attempting reauth")
	if err := reauth(ctx, c); err != nil {
		return nil
	}

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	reqRetry, retryErr := c.prepareRequest(ctx, method, endpoint, bodyReader, cfg)
	if retryErr != nil {
		return nil
	}

	respRetry, doErr := c.httpClient.Do(reqRetry)
	if doErr != nil {
		return nil
	}

	return respRetry
}

// handleStatus processes HTTP response codes and extracts body or error.
func (c *Client) handleStatus(resp *http.Response, endpoint string) ([]byte, error) {
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted:
		respBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("read response body: %w", readErr)
		}

		return respBytes, nil

	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", backend.ErrNotFound, endpoint)

	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		c.SetCooldown(defaultCooldownDuration)
		logger.LogWarn("Received HTTP %d from %s - entered cooldown", resp.StatusCode, endpoint)

		return nil, fmt.Errorf("%w: HTTP %d", backend.ErrRateLimited, resp.StatusCode)

	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("%w: HTTP %d", backend.ErrLogin, resp.StatusCode)

	default:
		return nil, fmt.Errorf("%w: HTTP %d from %s", backend.ErrUnexpectedStatus, resp.StatusCode, endpoint)
	}
}

// execute executes the HTTP request, managing cooldown, reauth, and errors.
func (c *Client) execute(ctx context.Context, method, path string, body []byte, cfg *RequestConfig) ([]byte, error) {
	if c.InCooldown() {
		return nil, backend.ErrRateLimited
	}

	endpoint := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		c.mu.RLock()
		base := c.baseURL
		c.mu.RUnlock()
		endpoint = base + "/" + strings.TrimLeft(path, "/")
	}

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	req, err := c.prepareRequest(ctx, method, endpoint, bodyReader, cfg)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: %w", backend.ErrTimeout, err)
		}

		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized {
		if retryResp := c.tryReauth(ctx, method, endpoint, body, cfg); retryResp != nil {
			defer func() {
				_ = retryResp.Body.Close()
			}()
			resp = retryResp
		}
	}

	return c.handleStatus(resp, endpoint)
}

// GetRaw performs an authenticated GET request returning raw response bytes.
func GetRaw(ctx context.Context, c *Client, path string, opts ...RequestOption) ([]byte, error) {
	cfg := &RequestConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	if !cfg.ForceRefresh {
		val, found, isNeg := c.cache.Get(path, cfg.TTL)
		if found && isNeg {
			return nil, backend.ErrNotFound
		}
		if found && !isNeg {
			if b, ok := val.([]byte); ok {
				return b, nil
			}
		}
	}

	data, err := c.execute(ctx, http.MethodGet, path, nil, cfg)
	if err != nil {
		if errors.Is(err, backend.ErrNotFound) {
			c.cache.SetNegative(path, cfg.TTL)
		}

		return nil, err
	}

	c.cache.Set(path, data)

	return data, nil
}

// PostRaw performs an authenticated POST request returning raw response bytes.
func PostRaw(
	ctx context.Context,
	c *Client,
	path string,
	payload []byte,
	contentType string,
	opts ...RequestOption,
) ([]byte, error) {
	cfg := &RequestConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	if contentType != "" {
		if cfg.Headers == nil {
			cfg.Headers = make(map[string]string)
		}
		cfg.Headers[headerContentType] = contentType
	}

	data, err := c.execute(ctx, http.MethodPost, path, payload, cfg)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// GetJSON performs an authenticated GET request and unmarshals the JSON response into type T.
func GetJSON[T any](ctx context.Context, c *Client, path string, opts ...RequestOption) (T, error) {
	var zero T

	cfg := &RequestConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	if !cfg.ForceRefresh {
		val, found, isNeg := c.cache.Get(path, cfg.TTL)
		if found && isNeg {
			return zero, backend.ErrNotFound
		}
		if found && !isNeg {
			if typed, ok := val.(T); ok {
				return typed, nil
			}
		}
	}

	raw, err := c.execute(ctx, http.MethodGet, path, nil, cfg)
	if err != nil {
		if errors.Is(err, backend.ErrNotFound) {
			c.cache.SetNegative(path, cfg.TTL)
		}

		return zero, err
	}

	var result T
	if jsonErr := json.Unmarshal(raw, &result); jsonErr != nil {
		return zero, fmt.Errorf("unmarshal JSON: %w", jsonErr)
	}

	c.cache.Set(path, result)

	return result, nil
}

// PostJSON performs an authenticated POST request with a JSON payload and unmarshals response into T.
func PostJSON[T any](ctx context.Context, c *Client, path string, payload any, opts ...RequestOption) (T, error) {
	var zero T

	body, err := json.Marshal(payload)
	if err != nil {
		return zero, fmt.Errorf("marshal JSON payload: %w", err)
	}

	cfg := &RequestConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.Headers == nil {
		cfg.Headers = make(map[string]string)
	}
	cfg.Headers[headerContentType] = contentTypeJSON

	raw, postErr := c.execute(ctx, http.MethodPost, path, body, cfg)
	if postErr != nil {
		return zero, postErr
	}

	var result T
	if len(raw) == 0 {
		return result, nil
	}

	if jsonErr := json.Unmarshal(raw, &result); jsonErr != nil {
		return zero, fmt.Errorf("unmarshal JSON response: %w", jsonErr)
	}

	return result, nil
}
