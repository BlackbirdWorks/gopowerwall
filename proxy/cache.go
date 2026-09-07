package proxy

import (
	"sync"
	"time"
)

// CachedItem stores a cached string value with timestamp.
type CachedItem struct {
	Timestamp time.Time
	Value     string
}

// PerformanceCache handles short-lived in-memory caching for endpoints.
type PerformanceCache struct {
	items map[string]CachedItem
	ttl   time.Duration
	mu    sync.RWMutex
}

// NewPerformanceCache initializes PerformanceCache.
func NewPerformanceCache(ttl time.Duration) *PerformanceCache {
	return &PerformanceCache{
		items: make(map[string]CachedItem),
		ttl:   ttl,
	}
}

// Get returns the value if present and not expired.
func (c *PerformanceCache) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, ok := c.items[key]
	if !ok {
		return "", false
	}
	if time.Since(item.Timestamp) > c.ttl {
		return "", false
	}

	return item.Value, true
}

// Set stores a value with the current timestamp.
func (c *PerformanceCache) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = CachedItem{
		Value:     value,
		Timestamp: time.Now(),
	}
}

// Clear flushes all entries.
func (c *PerformanceCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]CachedItem)
}

// Size returns entry count.
func (c *PerformanceCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.items)
}

// DegradationCache stores last known good responses for fallback when offline.
type DegradationCache struct {
	items map[string]CachedItem
	ttl   time.Duration
	mu    sync.RWMutex
}

// NewDegradationCache initializes DegradationCache.
func NewDegradationCache(ttl time.Duration) *DegradationCache {
	return &DegradationCache{
		items: make(map[string]CachedItem),
		ttl:   ttl,
	}
}

const (
	percentMultiplier = 100.0
	secondsPerMinute  = 60
	maxRateBuckets    = 200
)

// Get returns the cached data along with expired status.
func (c *DegradationCache) Get(key string) (string, bool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, ok := c.items[key]
	if !ok {
		return "", false, true
	}
	age := time.Since(item.Timestamp)

	return item.Value, true, age >= c.ttl
}

// Set saves the good response.
func (c *DegradationCache) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = CachedItem{
		Value:     value,
		Timestamp: time.Now(),
	}
}

// Clear clears all responses.
func (c *DegradationCache) Clear() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	cnt := len(c.items)
	c.items = make(map[string]CachedItem)

	return cnt
}

// Snapshot returns summary for /health and /stats.
func (c *DegradationCache) Snapshot() (int, map[string]map[string]any) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	res := make(map[string]map[string]any)
	now := time.Now()
	for k, v := range c.items {
		age := now.Sub(v.Timestamp).Seconds()
		res[k] = map[string]any{
			"age_seconds": age,
			"is_expired":  age >= c.ttl.Seconds(),
		}
	}

	return len(c.items), res
}

// ConnectionHealth tracks failures and degradation state.
type ConnectionHealth struct {
	LastSuccessTime     time.Time
	ConsecutiveFailures int
	TotalFailures       int
	TotalSuccesses      int
	mu                  sync.RWMutex
	IsDegraded          bool
}

func NewConnectionHealth() *ConnectionHealth {
	return &ConnectionHealth{
		LastSuccessTime: time.Now(),
	}
}

func (h *ConnectionHealth) RecordSuccess() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ConsecutiveFailures = 0
	h.TotalSuccesses++
	h.IsDegraded = false
	h.LastSuccessTime = time.Now()
}

func (h *ConnectionHealth) RecordFailure(threshold int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ConsecutiveFailures++
	h.TotalFailures++
	if threshold > 0 && h.ConsecutiveFailures >= threshold {
		h.IsDegraded = true
	}
}

func (h *ConnectionHealth) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ConsecutiveFailures = 0
	h.TotalFailures = 0
	h.TotalSuccesses = 0
	h.IsDegraded = false
	h.LastSuccessTime = time.Now()
}

func (h *ConnectionHealth) Snapshot() map[string]any {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return map[string]any{
		"consecutive_failures":     h.ConsecutiveFailures,
		"total_failures":           h.TotalFailures,
		"total_successes":          h.TotalSuccesses,
		"is_degraded":              h.IsDegraded,
		"last_success_time":        float64(h.LastSuccessTime.Unix()),
		"last_success_age_seconds": time.Since(h.LastSuccessTime).Seconds(),
	}
}

// EndpointStat tracks metrics for an individual endpoint.
type EndpointStat struct {
	LastSuccessTime time.Time
	LastFailureTime time.Time
	TotalCalls      int
	SuccessfulCalls int
	FailedCalls     int
}

// EndpointStatsTracker tracks stats across endpoints.
type EndpointStatsTracker struct {
	stats map[string]*EndpointStat
	mu    sync.RWMutex
}

func NewEndpointStatsTracker() *EndpointStatsTracker {
	return &EndpointStatsTracker{
		stats: make(map[string]*EndpointStat),
	}
}

func (t *EndpointStatsTracker) Record(endpoint string, success bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.stats[endpoint]
	if !ok {
		st = &EndpointStat{}
		t.stats[endpoint] = st
	}
	st.TotalCalls++
	now := time.Now()
	if success {
		st.SuccessfulCalls++
		st.LastSuccessTime = now
	} else {
		st.FailedCalls++
		st.LastFailureTime = now
	}
}

func (t *EndpointStatsTracker) Reset() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	cnt := len(t.stats)
	t.stats = make(map[string]*EndpointStat)

	return cnt
}

func (t *EndpointStatsTracker) Snapshot() map[string]any {
	t.mu.RLock()
	defer t.mu.RUnlock()
	res := make(map[string]any)
	now := time.Now()
	for ep, st := range t.stats {
		rate := 0.0
		if st.TotalCalls > 0 {
			rate = float64(st.SuccessfulCalls) / float64(st.TotalCalls) * percentMultiplier
		}
		item := map[string]any{
			"total_calls":          st.TotalCalls,
			"successful_calls":     st.SuccessfulCalls,
			"failed_calls":         st.FailedCalls,
			"success_rate_percent": rate,
		}
		if !st.LastSuccessTime.IsZero() {
			item["last_success_age_seconds"] = now.Sub(st.LastSuccessTime).Seconds()
		}
		if !st.LastFailureTime.IsZero() {
			item["last_failure_age_seconds"] = now.Sub(st.LastFailureTime).Seconds()
		}
		res[ep] = item
	}

	return res
}

// RateLimiter limits logs per function per minute.
type RateLimiter struct {
	counts map[string]int
	mu     sync.Mutex
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{counts: make(map[string]int)}
}

func (r *RateLimiter) ShouldLog(funcName string, maxPerMinute int) bool {
	if maxPerMinute <= 0 {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	minBucket := time.Now().Unix() / secondsPerMinute
	// Periodically cleanup
	if len(r.counts) > maxRateBuckets {
		r.counts = make(map[string]int)
	}
	bucketKey := funcName + "_" + time.Unix(minBucket*secondsPerMinute, 0).Format("1504")
	r.counts[bucketKey]++

	return r.counts[bucketKey] <= maxPerMinute
}
