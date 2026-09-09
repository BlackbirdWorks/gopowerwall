package proxy

import (
	"sync"
	"time"
)

const percentMultiplier = 100.0

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
