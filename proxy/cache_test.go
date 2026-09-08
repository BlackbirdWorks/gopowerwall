package proxy_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/proxy"
)

func TestPerformanceCache(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		setKey    string
		setValue  string
		getKey    string
		wantValue string
		ttl       time.Duration
		sleep     time.Duration
		wantOK    bool
	}

	for _, tc := range []testCase{
		{
			name:      "hit before expiry",
			ttl:       time.Minute,
			setKey:    "/aggregates",
			setValue:  `{"a":1}`,
			getKey:    "/aggregates",
			wantValue: `{"a":1}`,
			wantOK:    true,
		},
		{
			name:   "miss on unknown key",
			ttl:    time.Minute,
			setKey: "/aggregates",
			getKey: "/other",
			wantOK: false,
		},
		{
			name:   "miss after expiry",
			ttl:    10 * time.Millisecond,
			setKey: "/aggregates",
			getKey: "/aggregates",
			sleep:  30 * time.Millisecond,
			wantOK: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cache := proxy.NewPerformanceCache(tc.ttl)
			cache.Set(tc.setKey, tc.setValue)
			if tc.sleep > 0 {
				time.Sleep(tc.sleep)
			}

			got, ok := cache.Get(tc.getKey)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantValue, got)
			}
		})
	}
}

func TestPerformanceCacheClearAndSize(t *testing.T) {
	t.Parallel()

	cache := proxy.NewPerformanceCache(time.Minute)
	cache.Set("/a", "1")
	cache.Set("/b", "2")
	require.Equal(t, 2, cache.Size())

	cache.Clear()
	assert.Equal(t, 0, cache.Size())
	_, ok := cache.Get("/a")
	assert.False(t, ok)
}

func TestDegradationCache(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name        string
		lookupKey   string
		ttl         time.Duration
		sleep       time.Duration
		wantFound   bool
		wantExpired bool
	}

	for _, tc := range []testCase{
		{name: "found and fresh", ttl: time.Minute, lookupKey: "/soe", wantFound: true, wantExpired: false},
		{name: "not found", ttl: time.Minute, lookupKey: "/missing", wantFound: false, wantExpired: true},
		{
			name:        "found but expired",
			ttl:         10 * time.Millisecond,
			sleep:       30 * time.Millisecond,
			lookupKey:   "/soe",
			wantFound:   true,
			wantExpired: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cache := proxy.NewDegradationCache(tc.ttl)
			cache.Set("/soe", `{"percentage":50}`)
			if tc.sleep > 0 {
				time.Sleep(tc.sleep)
			}

			val, found, expired := cache.Get(tc.lookupKey)
			assert.Equal(t, tc.wantFound, found)
			assert.Equal(t, tc.wantExpired, expired)
			if tc.wantFound && !tc.wantExpired {
				assert.Equal(t, `{"percentage":50}`, val)
			}
		})
	}
}

func TestDegradationCacheClear(t *testing.T) {
	t.Parallel()

	cache := proxy.NewDegradationCache(time.Minute)
	cache.Set("/a", "1")
	cache.Set("/b", "2")

	removed := cache.Clear()
	assert.Equal(t, 2, removed)
	_, found, _ := cache.Get("/a")
	assert.False(t, found)
}

func TestDegradationCacheSnapshot(t *testing.T) {
	t.Parallel()

	cache := proxy.NewDegradationCache(time.Hour)
	cache.Set("/aggregates", "value")

	size, snap := cache.Snapshot()
	require.Equal(t, 1, size)
	require.Contains(t, snap, "/aggregates")
	entry := snap["/aggregates"]
	assert.False(t, entry["is_expired"].(bool))
	assert.GreaterOrEqual(t, entry["age_seconds"].(float64), 0.0)
}

func TestConnectionHealth(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		threshold     int
		failures      int
		recordSuccess bool
		wantDegraded  bool
	}

	for _, tc := range []testCase{
		{name: "no failures stays healthy", threshold: 3, failures: 0, wantDegraded: false},
		{name: "failures below threshold stays healthy", threshold: 3, failures: 2, wantDegraded: false},
		{name: "failures at threshold degrades", threshold: 3, failures: 3, wantDegraded: true},
		{name: "zero threshold never degrades", threshold: 0, failures: 10, wantDegraded: false},
		{
			name:          "success after degradation recovers",
			threshold:     2,
			failures:      2,
			recordSuccess: true,
			wantDegraded:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			health := proxy.NewConnectionHealth()
			for range tc.failures {
				health.RecordFailure(tc.threshold)
			}
			if tc.recordSuccess {
				health.RecordSuccess()
			}

			assert.Equal(t, tc.wantDegraded, health.IsDegraded)
			assert.Equal(t, tc.failures, health.TotalFailures)
		})
	}
}

func TestConnectionHealthReset(t *testing.T) {
	t.Parallel()

	health := proxy.NewConnectionHealth()
	health.RecordFailure(1)
	require.True(t, health.IsDegraded)

	health.Reset()
	assert.False(t, health.IsDegraded)
	assert.Equal(t, 0, health.ConsecutiveFailures)
	assert.Equal(t, 0, health.TotalFailures)
	assert.Equal(t, 0, health.TotalSuccesses)
}

func TestConnectionHealthSnapshot(t *testing.T) {
	t.Parallel()

	health := proxy.NewConnectionHealth()
	health.RecordSuccess()
	health.RecordFailure(5)

	snap := health.Snapshot()
	assert.Equal(t, 1, snap["consecutive_failures"])
	assert.Equal(t, 1, snap["total_failures"])
	assert.Equal(t, 1, snap["total_successes"])
	assert.Equal(t, false, snap["is_degraded"])
	assert.Contains(t, snap, "last_success_time")
	assert.Contains(t, snap, "last_success_age_seconds")
}

func TestEndpointStatsTracker(t *testing.T) {
	t.Parallel()

	tracker := proxy.NewEndpointStatsTracker()
	tracker.Record("/aggregates", true)
	tracker.Record("/aggregates", true)
	tracker.Record("/aggregates", false)
	tracker.Record("/soe", true)

	snap := tracker.Snapshot()
	require.Contains(t, snap, "/aggregates")
	agg, ok := snap["/aggregates"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 3, agg["total_calls"])
	assert.Equal(t, 2, agg["successful_calls"])
	assert.Equal(t, 1, agg["failed_calls"])
	assert.InDelta(t, 66.66, agg["success_rate_percent"].(float64), 0.1)
	assert.Contains(t, agg, "last_success_age_seconds")
	assert.Contains(t, agg, "last_failure_age_seconds")

	require.Contains(t, snap, "/soe")
}

func TestEndpointStatsTrackerReset(t *testing.T) {
	t.Parallel()

	tracker := proxy.NewEndpointStatsTracker()
	tracker.Record("/a", true)
	tracker.Record("/b", true)

	cleared := tracker.Reset()
	assert.Equal(t, 2, cleared)
	assert.Empty(t, tracker.Snapshot())
}

func TestRateLimiterShouldLog(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name         string
		maxPerMinute int
		calls        int
		wantAllowed  int
	}

	for _, tc := range []testCase{
		{name: "unlimited when zero", maxPerMinute: 0, calls: 5, wantAllowed: 5},
		{name: "unlimited when negative", maxPerMinute: -1, calls: 3, wantAllowed: 3},
		{name: "caps at limit", maxPerMinute: 2, calls: 5, wantAllowed: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			limiter := proxy.NewRateLimiter()
			allowed := 0
			for range tc.calls {
				if limiter.ShouldLog("myFunc", tc.maxPerMinute) {
					allowed++
				}
			}
			assert.Equal(t, tc.wantAllowed, allowed)
		})
	}
}

func TestRateLimiterSeparateBuckets(t *testing.T) {
	t.Parallel()

	limiter := proxy.NewRateLimiter()
	assert.True(t, limiter.ShouldLog("funcA", 1))
	assert.False(t, limiter.ShouldLog("funcA", 1))
	assert.True(t, limiter.ShouldLog("funcB", 1))
}
