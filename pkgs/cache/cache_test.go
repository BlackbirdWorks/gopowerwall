package cache_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/pkgs/cache"
)

func TestResponseCacheGetSet(t *testing.T) {
	t.Parallel()

	type testCase struct {
		wantVal   any
		setup     func(c *cache.ResponseCache)
		name      string
		key       string
		customTTL []time.Duration
		wantFound bool
		wantNeg   bool
	}

	for _, tc := range []testCase{
		{
			name: "cache hit on stored value",
			setup: func(c *cache.ResponseCache) {
				c.Set("/api/test", "value1")
			},
			key:       "/api/test",
			wantVal:   "value1",
			wantFound: true,
		},
		{
			name:      "cache miss on missing key",
			setup:     func(_ *cache.ResponseCache) {},
			key:       "/api/missing",
			wantFound: false,
		},
		{
			name: "negative cache hit",
			setup: func(c *cache.ResponseCache) {
				c.SetNegative("/api/404", 500*time.Millisecond)
			},
			key:       "/api/404",
			wantFound: true,
			wantNeg:   true,
		},
		{
			name: "custom TTL younger than entry still hits",
			setup: func(c *cache.ResponseCache) {
				c.Set("/api/fresh", "fresh-val")
			},
			key:       "/api/fresh",
			customTTL: []time.Duration{time.Minute},
			wantVal:   "fresh-val",
			wantFound: true,
		},
		{
			name: "custom TTL shorter than entry age misses",
			setup: func(c *cache.ResponseCache) {
				c.Set("/api/stale", "stale-val")
				time.Sleep(10 * time.Millisecond)
			},
			key:       "/api/stale",
			customTTL: []time.Duration{time.Millisecond},
			wantFound: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := cache.NewResponseCache(time.Second)
			t.Cleanup(c.Close)
			tc.setup(c)

			val, found, isNeg := c.Get(tc.key, tc.customTTL...)
			assert.Equal(t, tc.wantFound, found)
			assert.Equal(t, tc.wantNeg, isNeg)
			assert.Equal(t, tc.wantVal, val)
		})
	}
}

func TestResponseCacheInvalidate(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		setup      func(c *cache.ResponseCache)
		invalidate string
		checkKeys  []string
	}

	for _, tc := range []testCase{
		{
			name: "invalidating a mapped write endpoint clears every mapped read key",
			setup: func(c *cache.ResponseCache) {
				c.Set("/api/operation", "op-val")
				c.Set("SITE_CONFIG", "cfg-val")
			},
			invalidate: "/api/operation",
			checkKeys:  []string{"/api/operation", "SITE_CONFIG"},
		},
		{
			name: "invalidating an unmapped key deletes only that key",
			setup: func(c *cache.ResponseCache) {
				c.Set("/api/other", "other-val")
			},
			invalidate: "/api/other",
			checkKeys:  []string{"/api/other"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := cache.NewResponseCache(time.Second)
			t.Cleanup(c.Close)
			tc.setup(c)

			c.Invalidate(tc.invalidate)

			for _, key := range tc.checkKeys {
				_, found, _ := c.Get(key)
				assert.Falsef(t, found, "expected key %q to be invalidated", key)
			}
		})
	}
}

func TestResponseCacheClear(t *testing.T) {
	t.Parallel()

	c := cache.NewResponseCache(time.Second)
	t.Cleanup(c.Close)

	c.Set("/api/a", "a-val")
	c.Set("/api/b", "b-val")
	c.Clear()

	_, foundA, _ := c.Get("/api/a")
	_, foundB, _ := c.Get("/api/b")
	assert.False(t, foundA)
	assert.False(t, foundB)
}

func TestResponseCacheCooldown(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name     string
		cooldown time.Duration
		wait     time.Duration
		want     bool
	}

	for _, tc := range []testCase{
		{name: "immediately within cooldown", cooldown: 100 * time.Millisecond, wait: 0, want: true},
		{name: "past cooldown expiry", cooldown: 10 * time.Millisecond, wait: 30 * time.Millisecond, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := cache.NewResponseCache(time.Second)
			t.Cleanup(c.Close)

			c.SetCooldown(tc.cooldown)
			time.Sleep(tc.wait)
			assert.Equal(t, tc.want, c.InCooldown())
		})
	}
}

func TestPerformanceCache(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		setup     func(pc *cache.PerformanceCache)
		key       string
		wantVal   string
		wantFound bool
	}

	for _, tc := range []testCase{
		{
			name: "hit on valid key",
			setup: func(pc *cache.PerformanceCache) {
				pc.Set("/aggregates", `{"site": 100}`)
			},
			key:       "/aggregates",
			wantVal:   `{"site": 100}`,
			wantFound: true,
		},
		{
			name:      "miss on absent key",
			setup:     func(_ *cache.PerformanceCache) {},
			key:       "/absent",
			wantFound: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pc := cache.NewPerformanceCache(time.Second)
			t.Cleanup(pc.Close)
			tc.setup(pc)

			val, found := pc.Get(tc.key)
			assert.Equal(t, tc.wantFound, found)
			assert.Equal(t, tc.wantVal, val)
		})
	}
}

func TestPerformanceCacheSizeAndClear(t *testing.T) {
	t.Parallel()

	pc := cache.NewPerformanceCache(time.Second)
	t.Cleanup(pc.Close)

	pc.Set("/one", "1")
	pc.Set("/two", "2")
	require.Equal(t, 2, pc.Size())

	pc.Clear()
	assert.Equal(t, 0, pc.Size())
}

func TestDegradationCacheGetSet(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name        string
		setup       func(dc *cache.DegradationCache)
		key         string
		wantVal     string
		wantExists  bool
		wantExpired bool
	}

	for _, tc := range []testCase{
		{
			name: "fresh data in degradation cache",
			setup: func(dc *cache.DegradationCache) {
				dc.Set("/vitals", "good-data")
			},
			key:        "/vitals",
			wantVal:    "good-data",
			wantExists: true,
		},
		{
			name:        "missing key reports expired",
			setup:       func(_ *cache.DegradationCache) {},
			key:         "/missing",
			wantExpired: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dc := cache.NewDegradationCache(time.Second)
			tc.setup(dc)

			val, exists, expired := dc.Get(tc.key)
			assert.Equal(t, tc.wantExists, exists)
			assert.Equal(t, tc.wantExpired, expired)
			assert.Equal(t, tc.wantVal, val)
		})
	}
}

func TestDegradationCacheAges(t *testing.T) {
	t.Parallel()

	dc := cache.NewDegradationCache(10 * time.Millisecond)
	dc.Set("/vitals", "good-data")
	time.Sleep(30 * time.Millisecond)

	val, exists, expired := dc.Get("/vitals")
	assert.True(t, exists)
	assert.True(t, expired)
	assert.Equal(t, "good-data", val)
}

func TestDegradationCacheSnapshotAndClear(t *testing.T) {
	t.Parallel()

	dc := cache.NewDegradationCache(time.Minute)
	dc.Set("/a", "a-val")
	dc.Set("/b", "b-val")

	count, snapshot := dc.Snapshot()
	require.Equal(t, 2, count)
	require.Len(t, snapshot, 2)
	for _, entry := range snapshot {
		assert.Contains(t, entry, "age_seconds")
		assert.Contains(t, entry, "is_expired")
	}

	cleared := dc.Clear()
	assert.Equal(t, 2, cleared)

	countAfter, _ := dc.Snapshot()
	assert.Equal(t, 0, countAfter)
}

func TestRateLimiterShouldLog(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name         string
		want         []bool
		maxPerMinute int
		calls        int
	}

	for _, tc := range []testCase{
		{
			name:         "unlimited when max is zero",
			maxPerMinute: 0,
			calls:        3,
			want:         []bool{true, true, true},
		},
		{
			name:         "unlimited when max is negative",
			maxPerMinute: -1,
			calls:        2,
			want:         []bool{true, true},
		},
		{
			name:         "allows up to the limit then blocks",
			maxPerMinute: 2,
			calls:        3,
			want:         []bool{true, true, false},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rl := cache.NewRateLimiter()
			got := make([]bool, 0, tc.calls)
			for range tc.calls {
				got = append(got, rl.ShouldLog("some.func", tc.maxPerMinute))
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRateLimiterResetsBucketMapWhenLarge(t *testing.T) {
	t.Parallel()

	rl := cache.NewRateLimiter()
	for i := range 205 {
		rl.ShouldLog(fmt.Sprintf("func-%d", i), 1000)
	}

	// The bucket map should have been reset internally without panicking, and
	// a brand new key should still be tracked correctly from a count of one.
	assert.True(t, rl.ShouldLog("fresh-func", 1))
	assert.False(t, rl.ShouldLog("fresh-func", 1))
}
