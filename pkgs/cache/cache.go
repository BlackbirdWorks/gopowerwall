package cache

import (
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

const (
	secondsPerMinute   = 60
	rateLimitMapMaxLen = 200
)

// WriteOpReadOpCacheMap defines which write API calls invalidate which read cache keys.
//
//nolint:gochecknoglobals // Mapping between endpoints is constant across package.
var WriteOpReadOpCacheMap = map[string][]string{
	"/api/operation": {"/api/operation", "SITE_CONFIG"},
}

// Entry holds cached value and metadata.
type Entry struct {
	Timestamp  time.Time
	Value      any
	IsNegative bool
}

// rawKeyPrefix namespaces the raw (undecoded) representation of an endpoint
// so it never collides with a parsed/decoded representation cached under the
// bare endpoint key. The NUL byte cannot appear in an HTTP path or in the
// literal cache keys backends use (e.g. "SITE_CONFIG"), so it cannot collide
// with a real key.
const rawKeyPrefix = "raw\x00"

// RawKey derives the cache key that stores the raw (undecoded) representation
// of key, keeping it distinct from any parsed representation cached under the
// bare key. A backend that caches both a raw and a parsed reading of the same
// endpoint must use RawKey(api) for the raw one and api itself for the
// parsed one - never the same key for both - otherwise a type assertion on
// the cached value can fail depending on which representation was cached
// most recently. See backend/local for the caller-side handling this
// requires for negative caching to stay consistent across both keyspaces.
func RawKey(key string) string {
	return rawKeyPrefix + key
}

// ResponseCache wraps ttlcache.Cache for backend HTTP and TEDAPI response caching with negative TTL and cooldown.
type ResponseCache struct {
	cache        *ttlcache.Cache[string, *Entry]
	cooldownTime time.Time
	defaultTTL   time.Duration
	mu           sync.RWMutex
}

// NewResponseCache creates a new ResponseCache backed by ttlcache.
func NewResponseCache(defaultTTL time.Duration) *ResponseCache {
	c := ttlcache.New[string, *Entry](
		ttlcache.WithTTL[string, *Entry](defaultTTL),
		ttlcache.WithDisableTouchOnHit[string, *Entry](),
	)
	go c.Start()

	return &ResponseCache{
		cache:      c,
		defaultTTL: defaultTTL,
	}
}

// Close stops the underlying cache cleaner goroutine.
func (c *ResponseCache) Close() {
	if c.cache != nil {
		c.cache.Stop()
	}
}

// Get retrieves a cached item if not expired.
// Returns (value, found, isNegative).
func (c *ResponseCache) Get(key string, customTTL ...time.Duration) (any, bool, bool) {
	item := c.cache.Get(key)
	if item == nil || item.IsExpired() {
		return nil, false, false
	}
	entry := item.Value()
	if entry == nil {
		return nil, false, false
	}

	if len(customTTL) > 0 && customTTL[0] > 0 {
		if time.Since(entry.Timestamp) > customTTL[0] {
			return nil, false, false
		}
	}

	return entry.Value, true, entry.IsNegative
}

// Set stores a successful value in the cache.
func (c *ResponseCache) Set(key string, value any) {
	c.cache.Set(key, &Entry{
		Timestamp:  time.Now(),
		Value:      value,
		IsNegative: false,
	}, c.defaultTTL)
}

// SetNegative stores a negative cache entry (e.g. 404 or persistent error).
func (c *ResponseCache) SetNegative(key string, ttl time.Duration) {
	c.cache.Set(key, &Entry{
		Timestamp:  time.Now(),
		Value:      nil,
		IsNegative: true,
	}, ttl)
}

// Invalidate clears cache entries mapped to a write endpoint. Both the
// parsed keyspace (the bare key) and the raw keyspace ([RawKey] of it) are
// cleared for every affected key, so a write leaves no stale representation
// behind regardless of which one a subsequent read asks for.
func (c *ResponseCache) Invalidate(api string) {
	keys, ok := WriteOpReadOpCacheMap[api]
	if !ok {
		keys = []string{api}
	}
	for _, k := range keys {
		c.cache.Delete(k)
		c.cache.Delete(RawKey(k))
	}
}

// Clear clears all cache entries.
func (c *ResponseCache) Clear() {
	c.cache.DeleteAll()
}

// SetCooldown sets a cooldown until the given time.
func (c *ResponseCache) SetCooldown(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cooldownTime = time.Now().Add(d)
}

// InCooldown returns true if the cache is in a rate limit cooldown period.
func (c *ResponseCache) InCooldown() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return time.Now().Before(c.cooldownTime)
}

// PerformanceCache handles short-lived in-memory route caching backed by ttlcache.
type PerformanceCache struct {
	cache *ttlcache.Cache[string, string]
	ttl   time.Duration
}

// NewPerformanceCache initializes PerformanceCache using ttlcache.
func NewPerformanceCache(ttl time.Duration) *PerformanceCache {
	c := ttlcache.New[string, string](
		ttlcache.WithTTL[string, string](ttl),
		ttlcache.WithDisableTouchOnHit[string, string](),
	)
	go c.Start()

	return &PerformanceCache{
		cache: c,
		ttl:   ttl,
	}
}

// Close stops the background cleaner.
func (c *PerformanceCache) Close() {
	if c.cache != nil {
		c.cache.Stop()
	}
}

// Get returns the value if present and not expired.
func (c *PerformanceCache) Get(key string) (string, bool) {
	item := c.cache.Get(key)
	if item == nil || item.IsExpired() {
		return "", false
	}

	return item.Value(), true
}

// Set stores a value with the configured TTL.
func (c *PerformanceCache) Set(key, value string) {
	c.cache.Set(key, value, c.ttl)
}

// Clear flushes all entries.
func (c *PerformanceCache) Clear() {
	c.cache.DeleteAll()
}

// Size returns entry count.
func (c *PerformanceCache) Size() int {
	return c.cache.Len()
}

// DegradationEntry stores timestamp and content for fallback cache.
type DegradationEntry struct {
	Timestamp time.Time
	Value     string
}

// DegradationCache stores last known good responses for metrics continuity when offline.
type DegradationCache struct {
	cache *ttlcache.Cache[string, DegradationEntry]
	ttl   time.Duration
}

// NewDegradationCache initializes DegradationCache. Items persist for fallback until overwritten.
func NewDegradationCache(ttl time.Duration) *DegradationCache {
	c := ttlcache.New[string, DegradationEntry](
		ttlcache.WithTTL[string, DegradationEntry](ttlcache.NoTTL),
		ttlcache.WithDisableTouchOnHit[string, DegradationEntry](),
	)

	return &DegradationCache{
		cache: c,
		ttl:   ttl,
	}
}

// Get returns the cached data along with expired status.
func (c *DegradationCache) Get(key string) (string, bool, bool) {
	item := c.cache.Get(key)
	if item == nil {
		return "", false, true
	}
	entry := item.Value()
	age := time.Since(entry.Timestamp)

	return entry.Value, true, age >= c.ttl
}

// Set saves the good response.
func (c *DegradationCache) Set(key, value string) {
	c.cache.Set(key, DegradationEntry{
		Timestamp: time.Now(),
		Value:     value,
	}, ttlcache.NoTTL)
}

// Clear clears all responses and returns count.
func (c *DegradationCache) Clear() int {
	cnt := c.cache.Len()
	c.cache.DeleteAll()

	return cnt
}

// Snapshot returns summary for /health and /stats.
func (c *DegradationCache) Snapshot() (int, map[string]map[string]any) {
	res := make(map[string]map[string]any)
	now := time.Now()
	items := c.cache.Items()
	for k, item := range items {
		entry := item.Value()
		age := now.Sub(entry.Timestamp).Seconds()
		res[k] = map[string]any{
			"age_seconds": age,
			"is_expired":  age >= c.ttl.Seconds(),
		}
	}

	return len(items), res
}

// RateLimiter limits logs per function per minute.
type RateLimiter struct {
	counts map[string]int
	mu     sync.Mutex
}

// NewRateLimiter initializes RateLimiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{counts: make(map[string]int)}
}

// ShouldLog checks if logging is within rate limits.
func (r *RateLimiter) ShouldLog(funcName string, maxPerMinute int) bool {
	if maxPerMinute <= 0 {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	minBucket := time.Now().Unix() / secondsPerMinute
	if len(r.counts) > rateLimitMapMaxLen {
		r.counts = make(map[string]int)
	}
	bucketKey := funcName + "_" + time.Unix(minBucket*secondsPerMinute, 0).Format("1504")
	r.counts[bucketKey]++

	return r.counts[bucketKey] <= maxPerMinute
}
